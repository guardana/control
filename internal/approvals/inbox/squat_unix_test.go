//go:build unix

package inbox_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/guardana/control/internal/approvals"
	"github.com/guardana/control/internal/approvals/inbox"
	"github.com/guardana/control/internal/policy"
)

// envelopeOf is the file of an answer of key-1 to APPROVAL1 deciding decision,
// with edit applied to the body and the signature naming sigKey. The inbox
// verifies no signature, so the bytes are a stand-in.
func envelopeOf(t *testing.T, decision, sigKey string, edit func(*approvals.Answer)) []byte {
	t.Helper()
	a := sampleAnswer()
	a.Decision = decision
	if edit != nil {
		edit(&a)
	}
	body, err := approvals.MarshalAnswer(a)
	if err != nil {
		t.Fatal(err)
	}
	return envelopeFile(t, body, sigKey)
}

func envelopeFile(t *testing.T, body []byte, sigKey string) []byte {
	t.Helper()
	raw, err := inbox.MarshalAnswerFile(policy.StatementEnvelope{
		PayloadType: "application/vnd.agent-approval-answer+json", Payload: body,
		Signatures: []policy.StatementSignature{{KeyID: sigKey, Signature: bytes.Repeat([]byte{1}, 64)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// squat is a file an account that may write the inbox plants, before key-1's
// operator answers, under a name that operator's rejection would take or a
// name the file system may take as that one.
type squat struct {
	name  string
	raw   func(t *testing.T) []byte
	cause string
}

func squats() map[string]squat {
	reject := func(sigKey string, edit func(*approvals.Answer)) func(t *testing.T) []byte {
		return func(t *testing.T) []byte { return envelopeOf(t, approvals.DecisionReject, sigKey, edit) }
	}
	return map[string]squat{
		"a body for another approval": {"APPROVAL1.key-1-reject.answer",
			reject("key-1", func(a *approvals.Answer) { a.ApprovalID = "APPROVAL2" }), inbox.InboxOtherApproval},
		"a junk body under a junk signature": {"APPROVAL1.key-1-reject.answer",
			func(t *testing.T) []byte { return envelopeFile(t, []byte("junk"), "key-1") }, inbox.InboxMalformed},
		"a label naming another key than the body":      {"APPROVAL1.key-2-reject.answer", reject("key-1", nil), inbox.InboxMisnamed},
		"a signature naming another key than the label": {"APPROVAL1.key-1-reject.answer", reject("key-2", nil), inbox.InboxMisnamed},
		"a decision unlike the body's": {"APPROVAL1.key-1-reject.answer",
			func(t *testing.T) []byte { return envelopeOf(t, approvals.DecisionApprove, "key-1", nil) }, inbox.InboxMisnamed},
		"the approval id in another case": {"approval1.key-1-reject.answer", reject("key-1", nil), inbox.InboxMisnamed},
		"the label in another case":       {"APPROVAL1.KEY-1-REJECT.answer", reject("key-1", nil), inbox.InboxMisnamed},
		"a suffix folding onto the answer suffix": {"APPROVAL1.key-1-reject.anſwer",
			reject("key-1", nil), inbox.InboxMisnamed},
		"a fresh label past the bound": {"APPROVAL1.key-1-reject-17.answer", reject("key-1", nil), inbox.InboxMisnamed},
		"a fresh label of one":         {"APPROVAL1.key-1-reject-1.answer", reject("key-1", nil), inbox.InboxMisnamed},
		"a fresh label with a zero":    {"APPROVAL1.key-1-reject-02.answer", reject("key-1", nil), inbox.InboxMisnamed},
		"no signature": {"APPROVAL1.key-1-reject.answer", func(t *testing.T) []byte {
			body, err := approvals.MarshalAnswer(func() approvals.Answer { a := sampleAnswer(); a.Decision = approvals.DecisionReject; return a }())
			if err != nil {
				t.Fatal(err)
			}
			raw, err := inbox.MarshalAnswerFile(policy.StatementEnvelope{PayloadType: "application/vnd.agent-approval-answer+json", Payload: body})
			if err != nil {
				t.Fatal(err)
			}
			return raw
		}, inbox.InboxMisnamed},
	}
}

// TestASquattedAnswerNameUnjudgesTheBatch: each file planted where key-1's
// rejection of APPROVAL1 would go, or under a name a case-blind volume takes
// as such a name, may stand where that rejection was refused, so the batch
// is unjudged and counted under its cause. key-1's approval beside it is
// still read, and alone it is judged whole.
func TestASquattedAnswerNameUnjudgesTheBatch(t *testing.T) {
	for name, s := range squats() {
		t.Run(name, func(t *testing.T) {
			dir := inboxDir(t, 0o750)
			put(t, dir, "APPROVAL1.key-1-approve.answer", envelopeOf(t, approvals.DecisionApprove, "key-1", nil))
			in := openInbox(t, dir)
			if batch, err := in.Read(context.Background(), "APPROVAL1"); err != nil || batch.Unjudged || len(batch.Envelopes) != 1 {
				t.Fatalf("alone: %d envelope(s), unjudged %v, %v", len(batch.Envelopes), batch.Unjudged, err)
			}
			put(t, dir, s.name, s.raw(t))
			batch, err := in.Read(context.Background(), "APPROVAL1")
			if err != nil || !batch.Unjudged || batch.Truncated {
				t.Fatalf("with %s: unjudged %v, truncated %v, %v; want unjudged", s.name, batch.Unjudged, batch.Truncated, err)
			}
			if len(batch.Envelopes) != 1 {
				t.Errorf("with %s: %d envelope(s); want key-1's approval alone", s.name, len(batch.Envelopes))
			}
			if got := in.Counts()[s.cause]; got != 1 {
				t.Errorf("counts %v; want one %s", in.Counts(), s.cause)
			}
		})
	}
}

// TestANameOutsideASCIIUnjudgesEveryBatch: a volume may fold a name outside
// ASCII onto an answer's name by full case folding (ß onto SS, the ligature
// ﬆ onto ST) or simple folding (the Kelvin sign onto K), which no comparison
// here reproduces, so such a name unjudges the batch of every approval id;
// an ASCII name of no approval is left out and unjudges none.
func TestANameOutsideASCIIUnjudgesEveryBatch(t *testing.T) {
	for name, folded := range map[string]struct{ id, file string }{
		"sharp s onto SS":    {"APPROVALSS1", "APPROVAL\u00df1.key-1-reject.answer"},
		"a ligature onto ST": {"APPROVALST1", "APPROVAL\ufb061.key-1-reject.answer"},
		"the Kelvin sign":    {"ASK1", "AS\u212a1.key-1-reject.answer"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := inboxDir(t, 0o750)
			put(t, dir, "notes.txt", []byte("not an answer"))
			in := openInbox(t, dir)
			if batch, err := in.Read(context.Background(), folded.id); err != nil || batch.Unjudged {
				t.Fatalf("%s beside an ASCII name of no approval: unjudged %v, %v; want judged", folded.id, batch.Unjudged, err)
			}
			put(t, dir, folded.file, envelopeOf(t, approvals.DecisionReject, "key-1", func(a *approvals.Answer) { a.ApprovalID = folded.id }))
			for _, id := range []string{folded.id, "OTHER2"} {
				batch, err := in.Read(context.Background(), id)
				if err != nil || !batch.Unjudged || len(batch.Envelopes) != 0 {
					t.Errorf("%s with %q planted: %d envelope(s), unjudged %v, %v; want unjudged", id, folded.file, len(batch.Envelopes), batch.Unjudged, err)
				}
			}
			if got := in.Counts()[inbox.InboxMisnamed]; got != 2 {
				t.Errorf("counts %v; want misnamed twice, once per read", in.Counts())
			}
		})
	}
}

// TestEveryNameAnAnswerMayTakeIsRead: key-1's answers under their own label
// and under each fresh label up to the bound are read, whole; so is another
// approval's file the listing of APPROVAL1 does not name, left alone.
func TestEveryNameAnAnswerMayTakeIsRead(t *testing.T) {
	dir := inboxDir(t, 0o750)
	put(t, dir, "APPROVAL1.key-1-reject.answer", envelopeOf(t, approvals.DecisionReject, "key-1", nil))
	put(t, dir, "APPROVAL1.key-1-reject-2.answer", envelopeOf(t, approvals.DecisionReject, "key-1", nil))
	put(t, dir, "APPROVAL1.key-1-approve-16.answer", envelopeOf(t, approvals.DecisionApprove, "key-1", nil))
	put(t, dir, "APPROVAL1.key-2-approve.answer", envelopeOf(t, approvals.DecisionApprove, "key-2", func(a *approvals.Answer) { a.KeyID = "key-2" }))
	put(t, dir, "APPROVAL2.key-1-reject.answer", envelopeOf(t, approvals.DecisionReject, "key-1", func(a *approvals.Answer) { a.ApprovalID = "APPROVAL2" }))
	in := openInbox(t, dir)
	batch, err := in.Read(context.Background(), "APPROVAL1")
	if err != nil || batch.Unjudged || batch.Truncated || len(batch.Envelopes) != 4 {
		t.Fatalf("read %d envelope(s), unjudged %v, truncated %v, %v; want four, whole", len(batch.Envelopes), batch.Unjudged, batch.Truncated, err)
	}
	if got := in.Counts(); len(got) != 0 {
		t.Errorf("counts %v; want none", got)
	}
}
