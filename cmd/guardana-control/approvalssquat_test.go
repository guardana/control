package main

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/approvals"
	"github.com/guardana/control/internal/approvals/inbox"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policykey"
)

// squatted plants junk under each of names in the inbox at dir, as any
// account that may write the inbox can before an operator answers; the
// command reads the files as that other account's.
func squatted(t *testing.T, dir string, names ...string) {
	t.Helper()
	prev := ownedHere
	ownedHere = func(string) bool { return false }
	t.Cleanup(func() { ownedHere = prev })
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("junk"), 0o644); err != nil { //nolint:gosec // G306: an answer file's mode
			t.Fatal(err)
		}
	}
}

// TestAnAnswerThisAccountFiledIsNotFiledAgain: a second reject by the same
// operator finds its own earlier answer under the name, is refused as filed
// and files nothing under a fresh label.
func TestAnAnswerThisAccountFiledIsNotFiledAgain(t *testing.T) {
	dir, plane := signedStore(t)
	hold(t, plane, waitingID, clockNow().Add(15*time.Minute))
	keyFile, _, box := operatorKey(t)
	if code, _, stderr := invoke(t, "approvals", "reject", "--key", keyFile, "--inbox", box, "--reason", "no", dir, waitingID); code != exitOK {
		t.Fatalf("first reject: exit %d, stderr %q", code, stderr)
	}
	before := files(t, box)
	code, stdout, stderr := invoke(t, "approvals", "reject", "--key", keyFile, "--inbox", box, "--reason", "no", dir, waitingID)
	if code != exitFail || stdout != "" || !strings.Contains(stderr, "this account's own") {
		t.Errorf("second reject: exit %d, stdout %q, stderr %q; want refused as this account's own answer", code, stdout, stderr)
	}
	if after := files(t, box); !maps.Equal(before, after) {
		t.Errorf("the inbox went from %v to %v; want nothing filed", before, after)
	}
}

// TestARejectionWhoseNameIsTakenIsFiledUnderAFreshLabel: reject, finding its
// name taken by a file it did not write, files under the next label of the
// same key and decision and says so; the inbox then reads the rejection,
// beside a batch it cannot call whole. With that label taken too it takes the
// one after.
func TestARejectionWhoseNameIsTakenIsFiledUnderAFreshLabel(t *testing.T) {
	for taken, want := range map[int]string{1: "-reject-2", 2: "-reject-3"} {
		t.Run(fmt.Sprintf("%d taken", taken), func(t *testing.T) {
			dir, plane := signedStore(t)
			hold(t, plane, waitingID, clockNow().Add(15*time.Minute))
			keyFile, pub, box := operatorKey(t)
			first := waitingID + "." + keyLabel(pub) + "-reject.answer"
			planted := []string{first, waitingID + "." + keyLabel(pub) + "-reject-2.answer"}[:taken]
			squatted(t, box, planted...)
			code, stdout, stderr := invoke(t, "approvals", "reject", "--key", keyFile, "--inbox", box, "--reason", "no", dir, waitingID)
			if code != exitOK {
				t.Fatalf("exit %d, stderr %q", code, stderr)
			}
			name := waitingID + "." + keyLabel(pub) + want + ".answer"
			if !strings.Contains(stderr, first) || !strings.Contains(stderr, "a file that is not this answer") || !strings.Contains(stderr, name) {
				t.Errorf("stderr %q names neither the taken name, that a file not this answer holds it, nor the fresh one", stderr)
			}
			if !strings.Contains(stdout, filepath.Join(box, name)) {
				t.Errorf("stdout %q does not name %s", stdout, name)
			}
			if body := signedBody(t, filepath.Join(box, name), pub); body["decision"] != "reject" || body["approvalId"] != waitingID {
				t.Errorf("the fresh file holds %v", body)
			}
			expectRejectionBesideASquat(t, box)
		})
	}
}

// expectRejectionBesideASquat reads the inbox at dir as the plane does: one
// envelope, the rejection, in a batch it cannot call whole.
func expectRejectionBesideASquat(t *testing.T, dir string) {
	t.Helper()
	in, err := inbox.OpenInbox(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	batch, err := in.Read(context.Background(), waitingID)
	if err != nil || !batch.Unjudged || len(batch.Envelopes) != 1 {
		t.Fatalf("the inbox read %d envelope(s), unjudged %v, %v; want the rejection beside the squat", len(batch.Envelopes), batch.Unjudged, err)
	}
	if a, err := approvals.ReadAnswer(batch.Envelopes[0].Payload); err != nil || a.Decision != approvals.DecisionReject {
		t.Errorf("the envelope read is %+v, %v; want the rejection", a, err)
	}
}

// TestARejectionIsRefusedOnceEveryLabelIsTaken: with its own label and every
// fresh one up to the bound taken, reject refuses, names the taken file and
// files nothing; with the last one free it files there.
func TestARejectionIsRefusedOnceEveryLabelIsTaken(t *testing.T) {
	for _, free := range []bool{false, true} {
		dir, plane := signedStore(t)
		hold(t, plane, waitingID, clockNow().Add(15*time.Minute))
		keyFile, pub, box := operatorKey(t)
		base := waitingID + "." + keyLabel(pub) + "-reject"
		planted := []string{base + ".answer"}
		for n := 2; n <= inbox.MaxAnswersPerApproval; n++ {
			planted = append(planted, fmt.Sprintf("%s-%d.answer", base, n))
		}
		if free {
			planted = planted[:len(planted)-1]
		}
		squatted(t, box, planted...)
		before := files(t, box)
		code, stdout, stderr := invoke(t, "approvals", "reject", "--key", keyFile, "--inbox", box, dir, waitingID)
		if free {
			last := fmt.Sprintf("%s-%d.answer", base, inbox.MaxAnswersPerApproval)
			if _, err := os.Stat(filepath.Join(box, last)); code != exitOK || err != nil {
				t.Errorf("with %s free: exit %d, %v, stderr %q", last, code, err, stderr)
			}
			continue
		}
		if line := lastLine(stderr); code != exitFail || stdout != "" || !strings.Contains(line, "owned by uid") {
			t.Errorf("exit %d, stdout %q, stderr %q; want a refusal naming the taken file's owner", code, stdout, line)
		}
		if after := files(t, box); !maps.Equal(before, after) {
			t.Error("a refused answer changed the inbox")
		}
	}
}

// rejectedElsewhere files this key's rejection of approvalID in a fresh inbox
// of its own and returns the file's path and its envelope.
func rejectedElsewhere(t *testing.T, dir, keyFile, approvalID string) (string, policy.StatementEnvelope) {
	t.Helper()
	spare := filepath.Join(t.TempDir(), "spare")
	if err := os.Mkdir(spare, 0o700); err != nil {
		t.Fatal(err)
	}
	if code, _, stderr := invoke(t, "approvals", "reject", "--key", keyFile, "--inbox", spare, dir, approvalID); code != exitOK {
		t.Fatalf("rejecting %s elsewhere: exit %d, %q", approvalID, code, stderr)
	}
	names, err := os.ReadDir(spare)
	if err != nil || len(names) != 1 {
		t.Fatalf("the spare inbox holds %v, %v; want one answer", names, err)
	}
	path := filepath.Join(spare, names[0].Name())
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the test's own inbox
	if err != nil {
		t.Fatal(err)
	}
	env, err := policykey.ParseEnvelope(raw, 1<<16)
	if err != nil {
		t.Fatal(err)
	}
	return path, env
}

// writeEnvelope writes env as an answer file at path.
func writeEnvelope(t *testing.T, path string, env policy.StatementEnvelope) {
	t.Helper()
	raw, err := inbox.MarshalAnswerFile(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil { //nolint:gosec // G306: an answer file's mode
		t.Fatal(err)
	}
}

// link hard-links from to to, as any account that may write the inbox can do
// to a file another account owns.
func link(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Link(from, to); err != nil {
		t.Fatal(err)
	}
}

// squatSetup is one hold and one operator's key and inbox, and the name the
// operator's rejection takes first.
type squatSetup struct {
	dir     string
	plane   *approvals.Plane
	keyFile string
	pub     ed25519.PublicKey
	box     string
	first   string
}

func newSquatSetup(t *testing.T) squatSetup {
	t.Helper()
	dir, plane := signedStore(t)
	hold(t, plane, waitingID, clockNow().Add(15*time.Minute))
	keyFile, pub, box := operatorKey(t)
	return squatSetup{dir, plane, keyFile, pub, box, waitingID + "." + keyLabel(pub) + "-reject.answer"}
}

// failOn fails the test on err.
func failOn(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// signedNaming is env with its body naming keyID, signed again by the key
// at keyFile.
func signedNaming(t *testing.T, env policy.StatementEnvelope, keyID, keyFile string) policy.StatementEnvelope {
	t.Helper()
	a, err := approvals.ReadAnswer(env.Payload)
	failOn(t, err)
	a.KeyID = keyID
	key, err := policykey.ReadPrivate(keyFile)
	failOn(t, err)
	env.Payload, err = approvals.MarshalAnswer(a)
	failOn(t, err)
	env.Signatures[0].Signature, err = bundle.SignBytesAs(env.PayloadType, env.Payload, key)
	failOn(t, err)
	return env
}

// filesOfThisAccounts plants, at the rejection's name, a file this account
// owns that is not this key's rejection of this approval, one per case.
func filesOfThisAccounts() map[string]func(t *testing.T, s squatSetup) {
	return map[string]func(t *testing.T, s squatSetup){
		"an unrelated file, linked": func(t *testing.T, s squatSetup) {
			elsewhere := filepath.Join(t.TempDir(), "unrelated")
			failOn(t, os.WriteFile(elsewhere, []byte("not an answer"), 0o644)) //nolint:gosec // G306: an answer file's mode
			link(t, elsewhere, filepath.Join(s.box, s.first))
		},
		"this key's approval of this approval, linked": func(t *testing.T, s squatSetup) {
			if code, _, stderr := invoke(t, "approvals", "approve", "--key", s.keyFile, "--inbox", s.box, s.dir, waitingID); code != exitOK {
				t.Fatalf("approve: exit %d, %q", code, stderr)
			}
			link(t, filepath.Join(s.box, waitingID+"."+keyLabel(s.pub)+"-approve.answer"), filepath.Join(s.box, s.first))
		},
		"this key's rejection of another approval, linked": func(t *testing.T, s squatSetup) {
			hold(t, s.plane, answeredID, clockNow().Add(15*time.Minute))
			path, _ := rejectedElsewhere(t, s.dir, s.keyFile, answeredID)
			link(t, path, filepath.Join(s.box, s.first))
		},
		"a symbolic link to this key's rejection of this approval": func(t *testing.T, s squatSetup) {
			path, _ := rejectedElsewhere(t, s.dir, s.keyFile, waitingID)
			failOn(t, os.Symlink(path, filepath.Join(s.box, s.first)))
		},
		"a symbolic link within the inbox to this key's rejection of this approval": func(t *testing.T, s squatSetup) {
			_, env := rejectedElsewhere(t, s.dir, s.keyFile, waitingID)
			writeEnvelope(t, filepath.Join(s.box, "kept"), env)
			failOn(t, os.Symlink("kept", filepath.Join(s.box, s.first)))
		},
		"this key's rejection with its signature broken": func(t *testing.T, s squatSetup) {
			_, env := rejectedElsewhere(t, s.dir, s.keyFile, waitingID)
			env.Signatures[0].Signature[0] ^= 1
			writeEnvelope(t, filepath.Join(s.box, s.first), env)
		},
		"this key's rejection under another payload type": func(t *testing.T, s squatSetup) {
			_, env := rejectedElsewhere(t, s.dir, s.keyFile, waitingID)
			env.PayloadType = "application/vnd.other+json"
			writeEnvelope(t, filepath.Join(s.box, s.first), env)
		},
		"this key's rejection beside a second signature": func(t *testing.T, s squatSetup) {
			_, env := rejectedElsewhere(t, s.dir, s.keyFile, waitingID)
			env.Signatures = append(env.Signatures, policy.StatementSignature{KeyID: "ed25519-0123456789abcdef", Signature: env.Signatures[0].Signature})
			writeEnvelope(t, filepath.Join(s.box, s.first), env)
		},
		"a rejection of this approval naming another key, signed by this key": func(t *testing.T, s squatSetup) {
			_, env := rejectedElsewhere(t, s.dir, s.keyFile, waitingID)
			writeEnvelope(t, filepath.Join(s.box, s.first), signedNaming(t, env, "ed25519-0123456789abcdef", s.keyFile))
		},
	}
}

// TestAFileOfThisAccountsIsPassedOverUnlessItIsThisAnswer: a file this
// account owns at the rejection's name, which any account that may write the
// inbox can put there by linking, is passed over for the next label, with
// both names and its owner on stderr, unless it is one envelope this key
// signed whose body rejects this approval. An approval of this approval is
// passed over too: the plane reads it under a rejection's name as unjudged,
// so refusing would leave the call with no rejection that wins.
func TestAFileOfThisAccountsIsPassedOverUnlessItIsThisAnswer(t *testing.T) {
	for name, plant := range filesOfThisAccounts() {
		t.Run(name, func(t *testing.T) {
			s := newSquatSetup(t)
			plant(t, s)
			code, stdout, stderr := invoke(t, "approvals", "reject", "--key", s.keyFile, "--inbox", s.box, "--reason", "no", s.dir, waitingID)
			if code != exitOK {
				t.Fatalf("exit %d, stderr %q; want the rejection filed under the next label", code, stderr)
			}
			fresh := waitingID + "." + keyLabel(s.pub) + "-reject-2.answer"
			if !strings.Contains(stderr, s.first) || !strings.Contains(stderr, fresh) || !strings.Contains(stderr, "owned by uid") {
				t.Errorf("stderr %q names neither the taken name, the fresh one, nor the taken file's owner", stderr)
			}
			if !strings.Contains(stdout, filepath.Join(s.box, fresh)) {
				t.Errorf("stdout %q does not name %s", stdout, fresh)
			}
			if body := signedBody(t, filepath.Join(s.box, fresh), s.pub); body["decision"] != "reject" || body["approvalId"] != waitingID {
				t.Errorf("the fresh file holds %v", body)
			}
		})
	}
}

// TestThisAnswerFiledEarlierIsRefusedHoweverItCameThere: this key's own
// rejection of this approval at the rejection's name, written there or
// linked there, is the answer filed earlier: reject refuses and files nothing.
func TestThisAnswerFiledEarlierIsRefusedHoweverItCameThere(t *testing.T) {
	for _, how := range []string{"written", "linked"} {
		t.Run(how, func(t *testing.T) {
			s := newSquatSetup(t)
			path, env := rejectedElsewhere(t, s.dir, s.keyFile, waitingID)
			if how == "linked" {
				link(t, path, filepath.Join(s.box, s.first))
			} else {
				writeEnvelope(t, filepath.Join(s.box, s.first), env)
			}
			before := files(t, s.box)
			code, stdout, stderr := invoke(t, "approvals", "reject", "--key", s.keyFile, "--inbox", s.box, s.dir, waitingID)
			if code != exitFail || stdout != "" || !strings.Contains(stderr, "this account's own") {
				t.Errorf("exit %d, stdout %q, stderr %q; want refused as this account's own answer", code, stdout, stderr)
			}
			if after := files(t, s.box); !maps.Equal(before, after) {
				t.Errorf("the inbox went from %v to %v; want nothing filed", before, after)
			}
		})
	}
}
