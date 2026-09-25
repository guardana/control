package main

import (
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/pause"
	"github.com/guardana/control/internal/policykey"
)

// keyBody is a key file's body line, built at run time.
func keyBody(t *testing.T) string {
	t.Helper()
	pemFile, err := policykey.MarshalPrivate(ed25519.NewKeyFromSeed(rfcSeed(t)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(string(pemFile), "\n")[1]
}

// TestPauseListPrintsNoKeyText: an entry whose id, provider and reason hold a
// key's body line, written to the file by hand, is listed with each withheld.
func TestPauseListPrintsNoKeyText(t *testing.T) {
	body := keyBody(t)
	path := initialized(t)
	entry := pause.Entry{
		ID:        body,
		Scope:     pause.Scope{Kind: pause.ScopeProvider, Provider: body},
		CreatedAt: time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
		Reason:    "keep " + body,
	}
	if err := pause.Add(context.Background(), path, entry); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke(t, "pause", "list", path)
	want := "[key text withheld] provider [key text withheld] created 2026-09-24T12:00:00Z reason \"keep [key text withheld]\"\n"
	if code != exitOK || stdout != want || strings.Contains(stdout+stderr, body[:21]) {
		t.Errorf("pause list answered %d with %q%q, want %q", code, stdout, stderr, want)
	}
}

// TestPolicyTestPrintsNoKeyTextInACaseName: a case file whose name holds the
// start of a key's body line is run and named with that run withheld.
func TestPolicyTestPrintsNoKeyTextInACaseName(t *testing.T) {
	body := keyBody(t)
	dir := t.TempDir()
	name := body[:21] + "AAAA.json"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, stdout, stderr := invoke(t, "policy", "test", dir)
	if !strings.HasPrefix(stdout, "FAIL [key text withheld].json: ") || strings.Contains(stdout+stderr, body[:21]) {
		t.Errorf("policy test printed %q%q, want the case named with its key text withheld", stdout, stderr)
	}
}
