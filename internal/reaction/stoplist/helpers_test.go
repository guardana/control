package stoplist

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/supervise"
)

// clock0 is the plane's clock in most tests, and poll its poll interval.
var clock0 = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

const poll = 30 * time.Second

var refundOnce struct {
	sync.Once
	digest string
	err    error
}

// refundDigest is the digest supervise gives the refund example's procedure,
// the spelling a finding record carries.
func refundDigest(t testing.TB) string {
	t.Helper()
	refundOnce.Do(func() {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "examples", "refund-supervision", "refund.procedure.json"))
		if err != nil {
			refundOnce.err = err
			return
		}
		p, err := supervise.ReadProcedure(raw)
		if err != nil {
			refundOnce.err = err
			return
		}
		refundOnce.digest = p.Digest()
	})
	if refundOnce.err != nil {
		t.Fatalf("the refund procedure: %v", refundOnce.err)
	}
	return refundOnce.digest
}

// liftKey derives the lift key from a seed at run time, so no key text is in
// the tree.
func liftKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x4c}, ed25519.SeedSize))
}

func testRoute(t testing.TB) reaction.Route {
	t.Helper()
	pub := base64.StdEncoding.EncodeToString(liftKey().Public().(ed25519.PublicKey))
	doc := `{"kind":"reaction-route/v1alpha1","route_id":"refunds","serial":3,"tenant_id":"acme","scope":"run",` +
		`"lift_public_key":"` + pub + `","rules":[` +
		`{"procedure_id":"refund","version":"1","digest":"` + refundDigest(t) + `","rule_id":"STEP_OUTSIDE_PROCEDURE","rule_version":"1","expires_seconds":3600}]}`
	r, err := reaction.ParseRoute([]byte(doc))
	if err != nil {
		t.Fatalf("ParseRoute: %v", err)
	}
	return r
}

// lines joins each line with its newline.
func lines(ls ...[]byte) []byte {
	var out bytes.Buffer
	for _, l := range ls {
		out.Write(l)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

// must takes a marshalled line and fails the test on a refusal.
func must(t testing.TB) func([]byte, error) []byte {
	return func(line []byte, err error) []byte {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return line
	}
}

func headerLine(t testing.TB, r reaction.Route, listID string) []byte {
	t.Helper()
	return must(t)(reaction.HeaderFor(r, listID).Marshal())
}

func stopLine(t testing.TB, finding, run string, created time.Time) []byte {
	t.Helper()
	return must(t)(reaction.Stop{
		EntryID: reaction.EntryID(finding), TenantID: "acme", RunID: run, FindingID: finding,
		ProcedureID: "refund", ProcedureVersion: "1", ProcedureDigest: refundDigest(t),
		RuleID: "STEP_OUTSIDE_PROCEDURE", RuleVersion: "1", CreatedAt: created, ExpiresAt: created.Add(time.Hour),
	}.Marshal())
}

func liftLine(t testing.TB, r reaction.Route, listID, run string, through int64) []byte {
	t.Helper()
	env, err := reaction.SignLift(reaction.Lift{
		Version: "1.0", ListID: listID, RouteDigest: r.Digest(), RunID: run, ThroughLine: through,
	}, liftKey())
	if err != nil {
		t.Fatal(err)
	}
	return must(t)(reaction.LiftLine{RunID: run, ThroughLine: through, Envelope: env}.Marshal())
}

// stoppingList is a list whose one stop, of run-1, is active at clock0.
func stoppingList(t testing.TB) []byte {
	t.Helper()
	return lines(headerLine(t, testRoute(t), "list-1"), stopLine(t, "f-1", "run-1", clock0.Add(-time.Minute)))
}

// listDir writes content as the stop list in a fresh directory of mode 0700,
// both owned by the account running the test, and returns the directory.
func listDir(t testing.TB, content []byte) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "stops")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, FileName), content, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeList(t testing.TB, dir string, content []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, FileName), content, 0o600); err != nil {
		t.Fatal(err)
	}
}

// appendTo appends raw bytes to the list, as a writer's append would.
func appendTo(t testing.TB, dir string, raw []byte) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(dir, FileName), os.O_WRONLY|os.O_APPEND, 0) //nolint:gosec // G304: the test's own temporary file
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(raw); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
