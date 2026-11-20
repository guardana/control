package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guardana/control/internal/recovery"
)

// TestASpentApprovalStaysSpentAcrossARestore is the recovery guide's check:
// an approval is spent, the spool and the hold journal are lost, the plane's
// state is restored from a backup taken after the spend, and the same call
// retried with the approver's answer still on disk never runs a second time.
func TestASpentApprovalStaysSpentAcrossARestore(t *testing.T) {
	tr := newTree(t)
	records, holds := tr.fileProvider(t)
	collector := newAcceptingCollector(t)
	upstream := tr.approveConfiguration(t, collector.url)

	first := tr.startPlane(t)
	agent := connectAgent(t, first.address(t))
	answerFrom(t, records, pendingApproval(t, callTransfer(t, agent, "a")), true)
	if granted := callTransfer(t, agent, "a"); granted.IsError {
		t.Fatalf("the approved retry was refused: %+v", granted.StructuredContent)
	}
	if ran := upstream.count(); ran != 1 {
		t.Fatalf("the upstream ran %d times before the restore, want 1", ran)
	}
	first.stop(t)

	cfg := tr.load(t)
	backup := filepath.Join(t.TempDir(), "backup")
	if _, err := recovery.Backup(t.Context(), cfg, backup, time.Now()); err != nil {
		t.Fatalf("state backup: %v", err)
	}
	for _, dir := range []string{holds, filepath.Join(tr.dir, "spool")} {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(dir, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := recovery.Restore(t.Context(), cfg, backup, time.Now()); err != nil {
		t.Fatalf("state restore: %v", err)
	}

	second := tr.startPlane(t)
	again := callTransfer(t, connectAgent(t, second.address(t)), "a")
	second.stop(t)
	if ran := upstream.count(); ran != 1 {
		t.Fatalf("the upstream ran %d times; the retry after the restore ran on a spent approval: %+v", ran, again.StructuredContent)
	}
	if !again.IsError {
		t.Errorf("the retry after the restore was answered as run: %+v", again.StructuredContent)
	}
}
