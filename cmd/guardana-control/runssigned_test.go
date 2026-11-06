package main

import (
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/runs"
	"github.com/guardana/control/internal/runs/signedruns"
	"github.com/guardana/control/internal/trust"
)

// signedRuns is a roster signed now in which alice operates t1/p1, alice's
// key file, the trust root's public key file, and an empty runs directory.
type signedRuns struct {
	dir, key, roster, root string
	doc                    trust.Document
}

// tenantP1 is the identity of the runs these tests sign: alice may operate
// there.
var tenantP1 = runs.Identity{ProjectID: "p1", TenantID: "t1", PrincipalType: "user", PrincipalID: "bob", AgentID: "planner"}

func newSignedRuns(t *testing.T) signedRuns {
	t.Helper()
	tr := newRosterTree(t)
	if code, _, stderr := rosterSignAt(t, tr, trust.DefaultLifetime, time.Now().Add(-time.Minute).Truncate(time.Second)); code != exitOK {
		t.Fatalf("roster sign answered %d: %q", code, stderr)
	}
	operator := filepath.Join(tr.dir, "operator")
	if err := policykey.WriteKeyPair(operator, seeded(0x61)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(tr.out)
	if err != nil {
		t.Fatal(err)
	}
	env, err := trust.ParseFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := trust.VerifyDocument(env, seeded(0x72).Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	return signedRuns{dir: runsDir(t), key: filepath.Join(operator, policykey.PrivateFile), roster: tr.out,
		root: filepath.Join(filepath.Dir(tr.key), policykey.PublicFile), doc: doc}
}

func (s signedRuns) flags() []string {
	return []string{"--key", s.key, "--roster", s.roster, "--trust-root", s.root}
}

// resolves reports how a plane of t1/p1 judging with the roster answers token.
func (s signedRuns) resolve(t *testing.T, token string) error {
	t.Helper()
	p, err := runs.OpenSignedPlane(s.dir, signedruns.Roster{Document: s.doc, Tenant: "t1", Project: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	_, err = p.Resolve(t.Context(), token, tenantP1, time.Now())
	return err
}

// TestRunsOpenWithAKeySignsARecordThePlaneTakes: runs open with the three
// flags makes an empty directory a signed layout and writes a record a plane
// judging with the roster resolves; runs close with them signs the close,
// printing what the open record held and who signed it, and the token is
// closed after.
func TestRunsOpenWithAKeySignsARecordThePlaneTakes(t *testing.T) {
	s := newSignedRuns(t)
	code, stdout, stderr := invoke(t, openArgs(s.dir, tenantP1, "1h", s.flags()...)...)
	if code != exitOK || stderr != "" {
		t.Fatalf("runs open --key answered %d: %q %q", code, stdout, stderr)
	}
	keyLine := "signed_by: key " + keyLabel(seeded(0x61).Public().(ed25519.PublicKey)) + "\n"
	if !strings.Contains(stdout, keyLine) {
		t.Errorf("stdout %q lacks %q", stdout, keyLine)
	}
	id, token := fieldAfter(t, stdout, "run_id: "), fieldAfter(t, stdout, "token: ")
	if err := s.resolve(t, token); err != nil {
		t.Fatalf("the plane refuses the signed run: %v", err)
	}
	code, stdout, stderr = invoke(t, append(append([]string{"runs", "close"}, s.flags()...), s.dir, id)...)
	if code != exitOK || stderr != "" {
		t.Fatalf("runs close --key answered %d: %q", code, stderr)
	}
	for _, want := range []string{"closed " + id + "\n", " project_id p1 tenant_id t1 principal_type user principal_id bob agent_id planner ", "opened by alice (key "} {
		if !strings.Contains(stdout, want) {
			t.Errorf("runs close --key printed %q, which lacks %q", stdout, want)
		}
	}
	if err := s.resolve(t, token); causeOf(err) != runs.CauseClosed {
		t.Errorf("the token after the signed close = %v, want closed", err)
	}
}

// TestASignedLayoutRefusesAnUnsignedWriteNamingTheKey: runs open and runs
// close without --key on a signed layout fail and name the flags, and a
// partial set of them is a usage error.
func TestASignedLayoutRefusesAnUnsignedWriteNamingTheKey(t *testing.T) {
	s := newSignedRuns(t)
	o := openRunOK(t, s, tenantP1)
	for name, args := range map[string][]string{
		"open":  openArgs(s.dir, tenantP1, "1h"),
		"close": {"runs", "close", s.dir, o},
	} {
		code, _, stderr := invoke(t, args...)
		if code == exitOK || !strings.Contains(stderr, "--key <operator key file> --roster") {
			t.Errorf("unsigned runs %s answered %d: %q; want a refusal naming --key", name, code, stderr)
		}
	}
	code, _, stderr := invoke(t, openArgs(s.dir, tenantP1, "1h", "--key", s.key)...)
	if code != exitUsage || !strings.Contains(stderr, "all three") {
		t.Errorf("runs open --key alone answered %d: %q; want a usage error", code, stderr)
	}
}

// TestRunsCloseWithAKeyNeverSignsOverARecordTheRosterRefuses: a record a
// writer of records/ put there that no operator signed is not closed by a
// signed close, and stays as it was: the close would otherwise sign what it
// carries over.
func TestRunsCloseWithAKeyNeverSignsOverARecordTheRosterRefuses(t *testing.T) {
	s := newSignedRuns(t)
	id := openRunOK(t, s, tenantP1)
	path := filepath.Join(s.dir, runs.RecordsDir, id+".run.json")
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a file under the test's own directory
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.Replace(string(raw), `"sig":"`, `"sig":"AAAA`, 1)
	if err := os.WriteFile(path, []byte(forged), 0o600); err != nil { //nolint:gosec // G703: a file under the test's temp dir
		t.Fatal(err)
	}
	code, stdout, stderr := invoke(t, append(append([]string{"runs", "close"}, s.flags()...), s.dir, id)...)
	if code == exitOK || !strings.Contains(stderr, runs.ErrUnauthenticated.Error()) {
		t.Fatalf("runs close --key over a forged record answered %d: %q %q; want it refused as unauthenticated", code, stdout, stderr)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != forged { //nolint:gosec // G304: as above
		t.Errorf("the refused close rewrote the record: %v", err)
	}
}

// TestRunsListAndPruneReadASignedLayout: list prints a signed layout's runs
// and prune archives an expired tree's records, leaving state/ alone.
func TestRunsListAndPruneReadASignedLayout(t *testing.T) {
	s := newSignedRuns(t)
	id := openRunOK(t, s, tenantP1)
	code, stdout, stderr := invoke(t, "runs", "list", s.dir)
	if code != exitOK || !strings.HasPrefix(stdout, id+" open ") {
		t.Fatalf("runs list answered %d: %q %q", code, stdout, stderr)
	}
	archive := filepath.Join(t.TempDir(), "archive")
	cutoff := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
	code, _, stderr = invoke(t, "runs", "prune", "--expired-before", cutoff, "--archive", archive, s.dir)
	if code == exitOK || !strings.Contains(stderr, "cutoff") {
		t.Fatalf("a prune past now answered %d: %q; want the cutoff refused", code, stderr)
	}
	admin, err := runs.OpenAdmin(t.Context(), s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = admin.Close() }()
	cut := time.Now().Add(2 * time.Hour)
	got, err := admin.Prune(t.Context(), runs.PruneRequest{ExpiredBefore: cut, Archive: archive, Now: cut})
	if err != nil || len(got.Trees) != 1 || got.Trees[0] != id {
		t.Fatalf("Prune over the signed layout = %+v, %v", got, err)
	}
	if entries, err := os.ReadDir(filepath.Join(s.dir, runs.StateDir)); err != nil || len(entries) != 0 {
		t.Errorf("state/ after the prune = %v, %v; want it untouched and empty", entries, err)
	}
}

func openRunOK(t *testing.T, s signedRuns, w runs.Identity) string {
	t.Helper()
	code, stdout, stderr := invoke(t, openArgs(s.dir, w, "1h", s.flags()...)...)
	if code != exitOK {
		t.Fatalf("runs open --key answered %d: %q", code, stderr)
	}
	return fieldAfter(t, stdout, "run_id: ")
}

func fieldAfter(t *testing.T, stdout, label string) string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		if v, ok := strings.CutPrefix(line, label); ok {
			return v
		}
	}
	t.Fatalf("stdout %q has no %q line", stdout, label)
	return ""
}

func causeOf(err error) runs.Cause {
	var r *runs.Refusal
	if errors.As(err, &r) {
		return r.Cause
	}
	return ""
}
