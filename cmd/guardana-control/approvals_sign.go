package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/approvals"
	"github.com/guardana/control/internal/approvals/inbox"
	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/operators"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policykey"
)

// signedDecision is the signed answer's word for state.
func signedDecision(state controlv1.ApprovalState) string {
	if state == controlv1.ApprovalState_APPROVAL_STATE_APPROVED {
		return approvals.DecisionApprove
	}
	return approvals.DecisionReject
}

// signAnswer signs one answer with the operator key at ans.key and files it
// in ans.inbox. Every member of the answer but the decision, the reason and
// the time is read from the plane's record of the hold in dir, which this
// command only reads: nothing the operator types names what is signed. A key
// file another account may read or write is refused, and an answer file of
// the same name is never replaced: the answer takes the next fresh label of
// its key and decision instead, and says which name was taken.
func signAnswer(command, decision string, ans answer, dir, id string, stdout, stderr io.Writer) int {
	key, err := policykey.ReadPrivate(ans.key)
	if err != nil {
		return fail(stderr, command, fmt.Errorf("--key: %w", err))
	}
	defer clear(key)
	a, raw, planeAccount, err := signedFromRecord(dir, id, decision, ans.reason, key, time.Now())
	if err != nil {
		return failHinted(stderr, command, err)
	}
	if planeAccount {
		writeLine(stderr, brand.CLI+": "+command+": this account owns the approvals directory, which is the plane's account: "+
			"an answer it signs separates nothing, since the plane's account can then sign as this operator; sign from an operator's own account")
	}
	name, taken, err := fileInInbox(ans.inbox, a, raw, publicHalf(key))
	if err != nil {
		return failHinted(stderr, command, fmt.Errorf("--inbox %s: %w", oneLine(ans.inbox), err))
	}
	if taken != "" {
		writeLine(stderr, brand.CLI+": "+command+": "+oneLine(taken)+" is held by a file that is not this answer ("+
			oneLine(ownerText(filepath.Join(ans.inbox, taken)))+"); the answer is filed as "+oneLine(name)+" instead")
	}
	lines := "approval " + oneLine(a.ApprovalID) + ": " + a.Decision + " signed by key " + a.KeyID + "\n" +
		"  action digest  " + a.ActionDigest + "\n" +
		"  tenant         " + oneLine(a.Tenant) + "\n" +
		"  project        " + oneLine(a.Project) + "\n" +
		"  filed          " + oneLine(filepath.Join(ans.inbox, name)) + "\n"
	if _, err := io.WriteString(stdout, lines); err != nil {
		return fail(stderr, command, fmt.Errorf("writing to standard output: %w; the answer was filed", err))
	}
	return exitOK
}

// signedFromRecord reads the plane's record of approvalID from dir and signs
// the answer to it at now. planeAccount reports whether this process owns
// dir, which makes it the plane's account.
func signedFromRecord(dir, approvalID, decision, reason string, key ed25519.PrivateKey, now time.Time) (a approvals.Answer, raw []byte, planeAccount bool, err error) {
	r, err := approvals.OpenReader(dir)
	if err != nil {
		return approvals.Answer{}, nil, false, err
	}
	planeAccount = r.PlaneAccount()
	rec, err := r.Held(context.Background(), approvalID)
	if err = errors.Join(err, r.Close()); err != nil {
		return approvals.Answer{}, nil, false, err
	}
	if a, err = approvals.AnswerFor(rec, decision, reason, now, policykey.KeyID(publicHalf(key))); err != nil {
		return approvals.Answer{}, nil, false, err
	}
	raw, err = signedAnswerFile(a, key)
	return a, raw, planeAccount, err
}

// signedAnswerFile signs a with key under the operator answer payload type
// and returns the answer file's bytes.
func signedAnswerFile(a approvals.Answer, key ed25519.PrivateKey) ([]byte, error) {
	body, err := approvals.MarshalAnswer(a)
	if err != nil {
		return nil, err
	}
	sig, err := bundle.SignBytesAs(operators.AnswerPayloadType, body, key)
	if err != nil {
		return nil, err
	}
	return inbox.MarshalAnswerFile(policy.StatementEnvelope{
		PayloadType: operators.AnswerPayloadType, Payload: body,
		Signatures: []policy.StatementSignature{{KeyID: a.KeyID, Signature: sig}},
	})
}

// ownedHere is ownedByCaller; a test planting another account's file stands
// in for that account through it.
var ownedHere = ownedByCaller

// fileInInbox files raw, the answer a signed by pub, in the inbox at dir
// under the first label of a's that no file holds, and returns that name and
// the first name, where it was taken. Any account that may write the inbox can
// take a name, and can hard-link any file this account owns there, so a file
// is passed over unless this account owns it and it is a's answer filed
// earlier, which refuses the answer as filed. Once every label is taken the
// answer is refused, naming the account that owns the first.
func fileInInbox(dir string, a approvals.Answer, raw []byte, pub ed25519.PublicKey) (name, taken string, err error) {
	in, err := inbox.OpenInbox(dir)
	if err != nil {
		return "", "", err
	}
	defer func() { err = errors.Join(err, in.Close()) }()
	for _, label := range inbox.AnswerLabels(a) {
		next, nameErr := inbox.AnswerFileName(a.ApprovalID, label)
		if nameErr != nil {
			return "", "", nameErr
		}
		fileErr := in.File(next, raw)
		switch {
		case fileErr == nil:
			return next, taken, nil
		case !errors.Is(fileErr, approvals.ErrAnswerFiled):
			return "", "", fileErr
		case ownedHere(filepath.Join(dir, next)) && filedEarlier(in, next, a, pub):
			return "", "", fmt.Errorf("%w: %s is this account's own, filed earlier", approvals.ErrAnswerFiled, next)
		case taken == "":
			taken = next
		}
	}
	return "", "", fmt.Errorf("%w: every name this answer may take is held, and %s is %s", approvals.ErrAnswerFiled, taken, ownerText(filepath.Join(dir, taken)))
}

// filedEarlier reports whether the file at name in in is a's answer filed
// earlier: one envelope of the answer payload type, its one signature
// verifying under pub, whose body is pub's answer to a's approval with a's
// decision. A file that cannot be read as one is not.
func filedEarlier(in *inbox.Inbox, name string, a approvals.Answer, pub ed25519.PublicKey) bool {
	env, err := in.FiledEnvelope(name)
	if err != nil || env.PayloadType != operators.AnswerPayloadType || len(env.Signatures) != 1 {
		return false
	}
	sig := env.Signatures[0]
	if bundle.VerifyBytesAs(operators.AnswerPayloadType, env.Payload, sig.Signature, sig.KeyID, bundle.Keyring{a.KeyID: pub}) != nil {
		return false
	}
	earlier, err := approvals.ReadAnswer(env.Payload)
	return err == nil && earlier.KeyID == a.KeyID && earlier.ApprovalID == a.ApprovalID && earlier.Decision == a.Decision
}

// failHinted is fail with what an operator can do about the refusal.
func failHinted(stderr io.Writer, command string, err error) int {
	if hint := answerHint(err); hint != "" {
		return fail(stderr, command, fmt.Errorf("%w; %s", err, hint))
	}
	return fail(stderr, command, err)
}

func publicHalf(key ed25519.PrivateKey) ed25519.PublicKey {
	pub, _ := key.Public().(ed25519.PublicKey)
	return pub
}
