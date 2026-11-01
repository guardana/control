package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/trust"
)

// rosterTree is an owner-only directory holding the trust root's key pair
// and a roster source naming one operator key.
type rosterTree struct {
	dir, key, source, out string
}

func rosterKeyID(pub ed25519.PublicKey) string {
	sum := sha256.Sum256(pub)
	return "ed25519-" + hex.EncodeToString(sum[:8])
}

func rosterSource(operator ed25519.PublicKey) string {
	return `{"kind":"trust-roster/v1alpha1","rosterId":"roster-a","serial":3,"keys":[{"keyId":"` + rosterKeyID(operator) +
		`","alg":"ed25519","role":"operator","subject":"alice","publicKey":"` + base64.StdEncoding.EncodeToString(operator) +
		`","grants":[{"tenant":"t1","project":"p1","level":"operate"}]}]}`
}

func newRosterTree(t *testing.T) rosterTree {
	t.Helper()
	dir := t.TempDir()
	tr := rosterTree{dir: dir, key: filepath.Join(dir, "root-key", policykey.PrivateFile),
		source: filepath.Join(dir, "roster.json"), out: filepath.Join(dir, "roster.signed")}
	if err := policykey.WriteKeyPair(filepath.Dir(tr.key), seeded(0x72)); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, tr.source, rosterSource(seeded(0x61).Public().(ed25519.PublicKey)))
	return tr
}

func rosterSignAt(t *testing.T, tr rosterTree, validFor time.Duration, now time.Time) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := rosterSign(rosterSignValues{key: tr.key, out: tr.out, validFor: validFor}, tr.source, now, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRosterSignWritesARosterThePortVerifies(t *testing.T) {
	tr := newRosterTree(t)
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	code, stdout, stderr := rosterSignAt(t, tr, trust.DefaultLifetime, now)
	if code != exitOK || stderr != "" {
		t.Fatalf("roster sign answered %d: %q", code, stderr)
	}
	raw, err := os.ReadFile(tr.out)
	if err != nil {
		t.Fatal(err)
	}
	env, err := trust.ParseFile(raw)
	if err != nil {
		t.Fatal(err)
	}
	root := seeded(0x72).Public().(ed25519.PublicKey)
	d, err := trust.VerifyDocument(env, root)
	if err != nil {
		t.Fatalf("the roster written does not verify under the root: %v", err)
	}
	sum := sha256.Sum256(env.Payload)
	want := "roster_id: roster-a\nserial: 3\ndigest: sha256:" + hex.EncodeToString(sum[:]) + "\nkeys: 1\n" +
		"issued_at: 2026-10-30T09:00:00Z\nexpires_at: 2026-10-31T09:00:00Z\nroot_key_id: " + rosterKeyID(root) + "\nout: " + tr.out + "\n"
	if stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, want)
	}
	if d.Keys()[0].Subject() != "alice" {
		t.Errorf("the roster names %q", d.Keys()[0].Subject())
	}
	if info, err := os.Stat(tr.out); err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("the roster is written with %v: %v", info.Mode(), err)
	}
}

// TestRosterSignNeedsAHigherSerialThanTheRosterItReplaces: signing one
// source again would give a second roster at its serial, which every plane
// refuses as a fork.
func TestRosterSignNeedsAHigherSerialThanTheRosterItReplaces(t *testing.T) {
	tr := newRosterTree(t)
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	if code, _, stderr := rosterSignAt(t, tr, trust.DefaultLifetime, now); code != exitOK {
		t.Fatalf("the first signing answered %d: %q", code, stderr)
	}
	operator := seeded(0x61).Public().(ed25519.PublicKey)
	for _, serial := range []string{"3", "2"} {
		writeFixture(t, tr.source, strings.Replace(rosterSource(operator), `"serial":3`, `"serial":`+serial, 1))
		before := readFile(t, tr.out)
		code, stdout, stderr := rosterSignAt(t, tr, trust.DefaultLifetime, now.Add(time.Hour))
		want := "it holds roster roster-a at serial 3, and each signing needs a higher serial than that, not " + serial
		if code != exitFail || stdout != "" || !strings.Contains(stderr, want) {
			t.Errorf("signing serial %s over serial 3 answered %d %q %q", serial, code, stdout, stderr)
		}
		if readFile(t, tr.out) != before {
			t.Errorf("signing serial %s over serial 3 changed the roster", serial)
		}
	}
	writeFixture(t, tr.source, strings.Replace(rosterSource(operator), `"serial":3`, `"serial":4`, 1))
	code, stdout, stderr := rosterSignAt(t, tr, trust.DefaultLifetime, now.Add(time.Hour))
	if code != exitOK || !strings.Contains(stdout, "serial: 4\n") {
		t.Errorf("signing serial 4 over serial 3 answered %d %q %q", code, stdout, stderr)
	}
	other := strings.Replace(strings.Replace(rosterSource(operator), `"serial":3`, `"serial":1`, 1), "roster-a", "roster-b", 1)
	writeFixture(t, tr.source, other)
	if code, _, stderr := rosterSignAt(t, tr, trust.DefaultLifetime, now.Add(time.Hour)); code != exitOK {
		t.Errorf("signing another roster id over roster-a answered %d %q", code, stderr)
	}
}

func TestRosterSignRefusesAndWritesNothing(t *testing.T) {
	now := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		prepare  func(t *testing.T, tr rosterTree)
		validFor time.Duration
		want     string
	}{
		{"a lifetime past a week", func(*testing.T, rosterTree) {}, trust.MaxLifetime + time.Second, string(trust.ErrTimes)},
		{"a source that sets its own issuedAt", func(t *testing.T, tr rosterTree) {
			body := strings.Replace(rosterSource(seeded(0x61).Public().(ed25519.PublicKey)), `"serial":3`, `"serial":3,"issuedAt":"2026-10-30T09:00:00Z"`, 1)
			writeFixture(t, tr.source, body)
		}, trust.DefaultLifetime, "the source names issuedAt"},
		{"a roster naming the root as an operator", func(t *testing.T, tr rosterTree) {
			writeFixture(t, tr.source, rosterSource(seeded(0x72).Public().(ed25519.PublicKey)))
		}, trust.DefaultLifetime, string(trust.ErrKeyReused)},
		{"an output path holding another file", func(t *testing.T, tr rosterTree) {
			writeFixture(t, tr.out, "not a roster\n")
		}, trust.DefaultLifetime, "is not a roster's envelope"},
	}
	for _, c := range cases {
		tr := newRosterTree(t)
		c.prepare(t, tr)
		before, _ := os.ReadFile(tr.out)
		code, stdout, stderr := rosterSignAt(t, tr, c.validFor, now)
		if code != exitFail || stdout != "" || !strings.Contains(stderr, c.want) {
			t.Errorf("%s: answered %d %q %q, want a refusal holding %q", c.name, code, stdout, stderr, c.want)
		}
		after, _ := os.ReadFile(tr.out)
		if !bytes.Equal(before, after) {
			t.Errorf("%s: the output changed", c.name)
		}
	}
}

func TestRosterSignThroughTheCommandLine(t *testing.T) {
	tr := newRosterTree(t)
	code, stdout, stderr := invoke(t, "operators", "roster", "sign", "--key", tr.key, "--out", tr.out, "--valid-for", "2h", tr.source)
	if code != exitOK || stderr != "" || !strings.Contains(stdout, "serial: 3\n") {
		t.Fatalf("answered %d %q %q", code, stdout, stderr)
	}
	if code, _, stderr := invoke(t, "operators", "roster", "sign", "--key", tr.key, tr.source); code != exitUsage || stderr == "" {
		t.Errorf("no --out answered %d %q", code, stderr)
	}
}

func TestFloorInitMakesAnAuthorityFloorOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "floor")
	code, stdout, stderr := invoke(t, "operators", "floor", "init", "--roster-id", "roster-a", dir)
	if code != exitOK || stdout != "roster_id: roster-a\nfloor: no serial\n" || stderr != "" {
		t.Fatalf("floor init answered %d %q %q", code, stdout, stderr)
	}
	if f, err := policystate.ReadAuthority(dir, "roster-a"); err != nil || f.HasSerial() {
		t.Errorf("the floor = %+v, %v", f, err)
	}
	if code, _, stderr := invoke(t, "operators", "floor", "init", "--roster-id", "roster-a", dir); code != exitFail || !strings.Contains(stderr, string(policystate.ErrExists)) {
		t.Errorf("a second floor init answered %d %q", code, stderr)
	}
	if code, _, _ := invoke(t, "operators", "floor", "init", dir); code != exitUsage {
		t.Errorf("no --roster-id answered %d", code)
	}
}
