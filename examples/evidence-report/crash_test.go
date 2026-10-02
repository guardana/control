package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// recordSyncs makes every sync of the state directory's files say which
// file it forced to disk, in order.
func recordSyncs(t *testing.T) *[]string {
	t.Helper()
	var synced []string
	t.Cleanup(func() { syncFile = (*os.File).Sync })
	syncFile = func(f *os.File) error {
		synced = append(synced, filepath.Base(f.Name()))
		return f.Sync()
	}
	return &synced
}

// TestFollowOutageCrash crashes the outage's consumer after it appended the
// alerts and before it replaced the state, once at each point it can stop,
// and runs it again: every alert is in the log once, in the one-shot
// export's order, and the request that straddles the outage ends once.
func TestFollowOutageCrash(t *testing.T) {
	synced := recordSyncs(t)
	dir := newState(t)
	var stdout, raised strings.Builder
	feed := func(export string, code int, syncs ...string) {
		t.Helper()
		*synced = nil
		got := consume(t, dir, fixture(t, export))
		if got.code != code {
			t.Errorf("%s: exit %d, want %d; stderr:\n%s", export, got.code, code, got.stderr)
		}
		if !slices.Equal(*synced, syncs) {
			t.Errorf("%s: synced %q, want %q in that order", export, *synced, syncs)
		}
		stdout.WriteString(got.stdout)
		raised.WriteString(alertLines(got.stderr))
	}
	feed("outage-1.jsonl", 0, "state.json.tmp", ".")
	feed("outage-2.jsonl", 1, "alerts.jsonl", "state.json.tmp", ".")
	saved := stateOf(t, dir)

	crashOnce(t, "alerts")
	feed("outage-3.jsonl", 2, "alerts.jsonl")
	if got := stateOf(t, dir); got != saved {
		t.Fatalf("a crash before the state was replaced changed it:\n%s", got)
	}
	feed("outage-3.jsonl", 0, "state.json.tmp", ".")

	crashOnce(t, "state")
	feed("outage-4.jsonl", 2, "alerts.jsonl", "state.json.tmp")
	if _, err := os.Lstat(filepath.Join(dir, "state.json.tmp")); err != nil {
		t.Fatalf("the crash left no temporary state behind, so it examined nothing: %v", err)
	}
	feed("outage-4.jsonl", 0, "state.json.tmp", ".")
	feed("outage-5.jsonl", 1, "alerts.jsonl", "state.json.tmp", ".")

	var want strings.Builder
	for _, a := range []expectedAlert{alertC2, alertGap, alertD3, alertE4, alertE5} {
		want.WriteString(a.in(t, "outage.jsonl") + "\n")
	}
	if got := alertLog(t, dir); got != want.String() {
		t.Errorf("alerts.jsonl after two crashes:\n%s\nwant each alert once:\n%s", got, want.String())
	}
	if raised.String() != want.String() {
		t.Errorf("the runs raised:\n%s\nwant each alert once:\n%s", raised.String(), want.String())
	}
	if n := strings.Count(stdout.String(), rowA+"\n"); n != 1 {
		t.Errorf("the request that straddles the outage ended %d times, want once:\n%s", n, stdout.String())
	}
	if _, err := os.Lstat(filepath.Join(dir, "state.json.tmp")); err == nil {
		t.Error("the temporary state outlived the run after the crash")
	}
}

// TestFollowCutAlertLine: a line of the alert log a crash cut short past the
// checkpoint is dropped before the next append, and the alert it held is
// raised again.
func TestFollowCutAlertLine(t *testing.T) {
	dir := newState(t)
	consume(t, dir, fixture(t, "outage-1.jsonl"))
	crashOnce(t, "alerts")
	consume(t, dir, fixture(t, "outage-2.jsonl"))
	log := alertLog(t, dir)
	first, _, _ := strings.Cut(log, "\n")
	writeIn(t, dir, "alerts.jsonl", first+"\n"+`{"v":1,"alert":"evid`)
	got := consume(t, dir, fixture(t, "outage-2.jsonl"))
	if got.code != 1 || alertLines(got.stderr) != alertGap.in(t, "outage.jsonl")+"\n" {
		t.Errorf("exit %d, alerts:\n%s\nwant 1 and the gap's alert raised again", got.code, alertLines(got.stderr))
	}
	if got := alertLog(t, dir); got != log {
		t.Errorf("alerts.jsonl:\n%s\nwant:\n%s", got, log)
	}
}
