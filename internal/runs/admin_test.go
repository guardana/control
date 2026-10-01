package runs_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/runs"
)

func TestOpenHoldsTheLifetimeToItsBounds(t *testing.T) {
	cases := []struct {
		ttl  time.Duration
		want error
	}{
		{0, runs.ErrTTL},
		{-time.Hour, runs.ErrTTL},
		{59 * time.Second, runs.ErrTTL},
		{time.Minute - time.Nanosecond, runs.ErrTTL},
		{time.Minute, nil},
		{720 * time.Hour, nil},
		{720*time.Hour + time.Nanosecond, runs.ErrTTL},
		{720*time.Hour + time.Minute, runs.ErrTTL},
	}
	_, a, _ := setup(t)
	for _, c := range cases {
		rec, token, err := a.Open(t.Context(), runs.OpenRequest{Who: alice, TTL: c.ttl, Now: opened})
		if !errors.Is(err, c.want) || (c.want == nil) != (err == nil) {
			t.Errorf("ttl %s: err %v, want %v", c.ttl, err, c.want)
			continue
		}
		if c.want != nil && (token != "" || rec != (runs.Record{})) {
			t.Errorf("ttl %s: a refusal returned a run", c.ttl)
		}
		if c.want == nil && !rec.ExpiresAt.Equal(opened.Add(c.ttl)) {
			t.Errorf("ttl %s: expires %s", c.ttl, rec.ExpiresAt)
		}
	}
}

func TestOpenRefusesAZeroClockAndAnIncompleteIdentity(t *testing.T) {
	_, a, _ := setup(t)
	if _, _, err := a.Open(t.Context(), runs.OpenRequest{Who: alice, TTL: time.Hour}); !errors.Is(err, runs.ErrZeroTime) {
		t.Fatalf("zero now: %v", err)
	}
	for name, edit := range map[string]func(*runs.Identity){
		"tenant":         func(w *runs.Identity) { w.TenantID = "" },
		"principal type": func(w *runs.Identity) { w.PrincipalType = "" },
		"principal":      func(w *runs.Identity) { w.PrincipalID = "" },
		"agent":          func(w *runs.Identity) { w.AgentID = "" },
		"control char":   func(w *runs.Identity) { w.AgentID = "agent\x00" },
		"over the bound": func(w *runs.Identity) { w.PrincipalID = strings.Repeat("p", runs.MaxIdentityBytes+1) },
	} {
		w := alice
		edit(&w)
		if _, _, err := a.Open(t.Context(), runs.OpenRequest{Who: w, TTL: time.Hour, Now: opened}); !errors.Is(err, runs.ErrIdentity) {
			t.Errorf("%s: %v, want ErrIdentity", name, err)
		}
	}
	w := alice
	w.PrincipalID = strings.Repeat("p", runs.MaxIdentityBytes)
	if _, _, err := a.Open(t.Context(), runs.OpenRequest{Who: w, TTL: time.Hour, Now: opened}); err != nil {
		t.Fatalf("a principal at the bound: %v", err)
	}
}

func TestOpenMintsAnIDAndASecretTheRecordKeepsOnlyTheHashOf(t *testing.T) {
	dir, a, _ := setup(t)
	rec, token := openRoot(t, a)
	id, secret, ok := strings.Cut(token, ".")
	if !ok || !regexp.MustCompile(`^run-[0-9a-f]{32}$`).MatchString(id) || rec.ID != id {
		t.Fatalf("token %q, record id %q", token, rec.ID)
	}
	raw, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(raw) != 32 {
		t.Fatalf("secret %q: %d bytes, %v", secret, len(raw), err)
	}
	sum := sha256.Sum256(raw)
	if rec.SecretSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("record hash %s is not the SHA-256 of the secret's bytes", rec.SecretSHA256)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, id+".run.json")) //nolint:gosec // G304: the test's own directory
	if err != nil {
		t.Fatal(err)
	}
	for where, text := range map[string]string{"the file": string(onDisk), "the record": fmt.Sprintf("%+v", rec)} {
		if strings.Contains(text, secret) || strings.Contains(text, hex.EncodeToString(raw)) {
			t.Fatalf("%s holds the secret", where)
		}
	}
}

func TestTwoRunsShareNeitherIDNorSecret(t *testing.T) {
	_, a, _ := setup(t)
	_, first := openRoot(t, a)
	_, second := openRoot(t, a)
	id1, secret1, _ := strings.Cut(first, ".")
	id2, secret2, _ := strings.Cut(second, ".")
	if id1 == id2 || secret1 == secret2 {
		t.Fatalf("two runs share an id or a secret: %s, %s", id1, id2)
	}
}

func TestARootRunStartsWithItsStateAndLock(t *testing.T) {
	dir, a, p := setup(t)
	root, _ := openRoot(t, a)
	for _, name := range []string{root.ID + ".state.json", root.ID + ".lock"} {
		if info, err := os.Lstat(filepath.Join(dir, name)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("%s: %v", name, err)
		}
	}
	s, err := p.State(t.Context(), root.ID)
	if err != nil || s != (runs.State{Untrusted: false, MaxRead: controlv1.Sensitivity_SENSITIVITY_PUBLIC}) {
		t.Fatalf("initial state %+v, %v", s, err)
	}
}

func TestAChildSharesItsRootAndOutlivesItsParent(t *testing.T) {
	dir, a, p := setup(t)
	root, _ := openRoot(t, a)
	child, childToken := openUnder(t, a, root.ID, alice)
	grandchild, _ := openUnder(t, a, child.ID, runs.Identity{TenantID: "tenant-a", PrincipalType: "user", PrincipalID: "bob", AgentID: "agent-9"})
	if child.Root != root.ID || child.Parent != root.ID || grandchild.Root != root.ID || grandchild.Parent != child.ID {
		t.Fatalf("roots: child %+v, grandchild %+v", child, grandchild)
	}
	for _, id := range []string{child.ID, grandchild.ID} {
		for _, suffix := range []string{".state.json", ".lock"} {
			if _, err := os.Lstat(filepath.Join(dir, id+suffix)); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("a child has %s: %v", suffix, err)
			}
		}
	}
	if err := a.CloseRun(t.Context(), root.ID, opened.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	run, err := p.Resolve(t.Context(), childToken, alice, opened.Add(2*time.Minute))
	if err != nil || run.Root != root.ID {
		t.Fatalf("a child after its parent closed: %+v, %v", run, err)
	}
}

func TestOpenRefusesAParentThatIsNotAnOpenRunOfTheTenant(t *testing.T) {
	_, a, _ := setup(t)
	parent, _ := openRoot(t, a)
	closed, _ := openRoot(t, a)
	if err := a.CloseRun(t.Context(), closed.ID, opened); err != nil {
		t.Fatal(err)
	}
	other := alice
	other.TenantID = "tenant-b"
	expires := opened.Add(time.Hour)
	cases := []struct {
		name   string
		parent string
		who    runs.Identity
		now    time.Time
		want   error
	}{
		{"another tenant", parent.ID, other, opened, runs.ErrParentTenant},
		{"closed", closed.ID, alice, opened, runs.ErrParentClosed},
		{"expired at now", parent.ID, alice, expires, runs.ErrParentExpired},
		{"expired before now", parent.ID, alice, expires.Add(time.Second), runs.ErrParentExpired},
		{"unknown", "run-" + strings.Repeat("0", 32), alice, opened, runs.ErrParentUnknown},
		{"not an id", "../x", alice, opened, runs.ErrRunID},
	}
	for _, c := range cases {
		rec, token, err := a.Open(t.Context(), runs.OpenRequest{Who: c.who, Parent: c.parent, TTL: time.Hour, Now: c.now})
		if !errors.Is(err, c.want) || token != "" || rec != (runs.Record{}) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
	listing, err := a.List(t.Context(), 100)
	if err != nil || len(listing.Records) != 2 {
		t.Fatalf("a refused opening left a record: %d records, %v", len(listing.Records), err)
	}
	stillOpen := opened.Add(time.Hour - time.Nanosecond)
	bob := alice
	bob.PrincipalID = "bob"
	if _, _, err := a.Open(t.Context(), runs.OpenRequest{Who: bob, Parent: parent.ID, TTL: time.Hour, Now: stillOpen}); err != nil {
		t.Fatalf("a parent of the tenant one nanosecond before its expiry: %v", err)
	}
}

func TestCloseRunRefusesWhatItCannotClose(t *testing.T) {
	_, a, p := setup(t)
	rec, token := openRoot(t, a)
	if err := a.CloseRun(t.Context(), rec.ID, time.Time{}); !errors.Is(err, runs.ErrZeroTime) {
		t.Fatalf("zero now: %v", err)
	}
	if err := a.CloseRun(t.Context(), "run-"+strings.Repeat("a", 32), opened); !errors.Is(err, runs.ErrNoRun) {
		t.Fatalf("unknown run: %v", err)
	}
	if err := a.CloseRun(t.Context(), "run-x", opened); !errors.Is(err, runs.ErrRunID) {
		t.Fatalf("not an id: %v", err)
	}
	if _, err := p.Resolve(t.Context(), token, alice, opened); err != nil {
		t.Fatalf("a refused close closed the run: %v", err)
	}
	closedAt := opened.Add(5 * time.Minute)
	if err := a.CloseRun(t.Context(), rec.ID, closedAt); err != nil {
		t.Fatal(err)
	}
	if err := a.CloseRun(t.Context(), rec.ID, closedAt.Add(time.Minute)); !errors.Is(err, runs.ErrAlreadyClosed) {
		t.Fatalf("second close: %v", err)
	}
	l, err := a.List(t.Context(), 10)
	if err != nil || len(l.Records) != 1 || !l.Records[0].ClosedAt.Equal(closedAt) {
		t.Fatalf("listing after close: %+v, %v", l, err)
	}
}

func TestListIsBounded(t *testing.T) {
	_, a, _ := setup(t)
	for range 3 {
		openRoot(t, a)
	}
	for _, c := range []struct {
		bound    int
		records  int
		complete bool
	}{{2, 2, false}, {3, 3, true}, {4, 3, true}} {
		l, err := a.List(t.Context(), c.bound)
		if err != nil || len(l.Records) != c.records || l.Complete != c.complete {
			t.Fatalf("bound %d: %d records, complete %t, %v", c.bound, len(l.Records), l.Complete, err)
		}
		for i := 1; i < len(l.Records); i++ {
			if l.Records[i-1].ID >= l.Records[i].ID {
				t.Fatalf("bound %d: not sorted by id", c.bound)
			}
		}
	}
	for _, bound := range []int{0, -1} {
		if _, err := a.List(t.Context(), bound); !errors.Is(err, runs.ErrBound) {
			t.Fatalf("bound %d: %v", bound, err)
		}
	}
}

func TestTheZeroAdminRefusesEverything(t *testing.T) {
	var a runs.Admin
	if _, _, err := a.Open(t.Context(), runs.OpenRequest{Who: alice, TTL: time.Hour, Now: opened}); !errors.Is(err, runs.ErrClosed) {
		t.Errorf("Open: %v", err)
	}
	if err := a.CloseRun(t.Context(), "run-"+strings.Repeat("a", 32), opened); !errors.Is(err, runs.ErrClosed) {
		t.Errorf("CloseRun: %v", err)
	}
	if _, err := a.List(t.Context(), 1); !errors.Is(err, runs.ErrClosed) {
		t.Errorf("List: %v", err)
	}
	if err := a.Close(); !errors.Is(err, runs.ErrClosed) {
		t.Errorf("Close: %v", err)
	}
}
