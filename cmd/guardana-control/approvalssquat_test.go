package main

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/approvals"
	"github.com/guardana/control/internal/approvals/inbox"
)

// squatted plants junk under each of names in the inbox at dir, as any
// account that may write the inbox can before an operator answers.
func squatted(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("junk"), 0o644); err != nil { //nolint:gosec // G306: an answer file's mode
			t.Fatal(err)
		}
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
			if !strings.Contains(stderr, first) || !strings.Contains(stderr, "did not write") || !strings.Contains(stderr, name) {
				t.Errorf("stderr %q names neither the taken name, that this command did not write it, nor the fresh one", stderr)
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
