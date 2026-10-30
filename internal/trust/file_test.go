package trust_test

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/trust"
)

func wantKey(t *testing.T, what string, a trust.Answer, subject string) {
	t.Helper()
	if a.State() != trust.StateKey || a.Key().Subject() != subject {
		t.Errorf("%s = state %d cause %q subject %q, want %s's key", what, a.State(), a.Cause(), a.Key().Subject(), subject)
	}
}

func wantNoKey(t *testing.T, what string, a trust.Answer, state trust.State, cause trust.Cause) {
	t.Helper()
	if a.State() != state || a.Cause() != cause || a.Key().ID() != "" {
		t.Errorf("%s = state %d cause %q key %q, want state %d cause %q", what, a.State(), a.Cause(), a.Key().ID(), state, cause)
	}
}

func TestTheFileKindNamesTheKeysOfTheRosterItTook(t *testing.T) {
	r := newRig(t, signed(t, doc(1, "alice")))
	wantKey(t, "alice", r.lookup(t, "alice"), "alice")
	wantNoKey(t, "bob, whom the roster does not name", r.lookup(t, "bob"), trust.StateUnknownKey, trust.CauseUnknownKey)
	wantNoKey(t, "the empty key id", r.roster.Lookup(t.Context(), ""), trust.StateUnknownKey, trust.CauseUnknownKey)
	f, err := policystate.ReadAuthority(r.floor, "roster-a")
	if err != nil || f.Serial != 1 {
		t.Errorf("the floor after the first take = %+v, %v; want serial 1", f, err)
	}
	s := r.roster.Status(t.Context())
	if s.Kind != trust.KindFile || s.Health != trust.HealthUsable || s.RosterID != "roster-a" || s.Serial != 1 || s.Last != trust.CauseNone {
		t.Errorf("Status = %+v", s)
	}
}

func TestTheFileKindKeepsTheLastRosterUntilItExpires(t *testing.T) {
	r := newRig(t, signed(t, doc(1, "alice")))
	wantKey(t, "before the file is damaged", r.lookup(t, "alice"), "alice")
	if err := os.WriteFile(r.path, []byte("not a roster"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantKey(t, "after the file is damaged", r.lookup(t, "alice"), "alice")
	if s := r.roster.Status(t.Context()); s.Last != trust.CauseRefused || s.Counts[trust.CauseRefused] == 0 {
		t.Errorf("Status after a damaged file = %+v, want the refusal counted", s)
	}
	if err := os.Remove(r.path); err != nil {
		t.Fatal(err)
	}
	wantKey(t, "after the file is removed", r.lookup(t, "alice"), "alice")
	r.now = clockAt(t, "2026-10-30T23:59:59Z")
	wantKey(t, "a second before the expiry", r.lookup(t, "alice"), "alice")
	r.now = clockAt(t, expires)
	wantNoKey(t, "at the expiry", r.lookup(t, "alice"), trust.StateUnavailable, trust.CauseExpired)
	if s := r.roster.Status(t.Context()); s.Health != trust.HealthUnavailable {
		t.Errorf("Status at the expiry = %+v, want unavailable", s)
	}
	next := doc(2, "alice")
	next["issuedAt"], next["expiresAt"] = "2026-10-31T00:00:00Z", "2026-11-01T00:00:00Z"
	writeRoster(t, r.path, signed(t, next))
	wantKey(t, "after a newer roster arrives", r.lookup(t, "alice"), "alice")
}

func TestTheFileKindNeverTakesALowerOrForkedRoster(t *testing.T) {
	r := newRig(t, signed(t, doc(3, "alice", "bob")))
	wantKey(t, "bob at serial 3", r.lookup(t, "bob"), "bob")
	writeRoster(t, r.path, signed(t, doc(4, "alice")))
	wantNoKey(t, "bob revoked at serial 4", r.lookup(t, "bob"), trust.StateUnknownKey, trust.CauseUnknownKey)
	writeRoster(t, r.path, signed(t, doc(3, "alice", "bob")))
	wantNoKey(t, "bob after serial 3 is put back", r.lookup(t, "bob"), trust.StateUnknownKey, trust.CauseUnknownKey)
	if s := r.roster.Status(t.Context()); s.Last != trust.CauseBelowFloor || s.Serial != 4 {
		t.Errorf("Status after a lower roster = %+v", s)
	}
	writeRoster(t, r.path, signed(t, doc(4, "alice", "bob")))
	wantNoKey(t, "bob in a second roster at serial 4", r.lookup(t, "bob"), trust.StateUnknownKey, trust.CauseUnknownKey)
	if s := r.roster.Status(t.Context()); s.Last != trust.CauseForked {
		t.Errorf("Status after a forked roster = %+v", s)
	}
	if f, err := policystate.ReadAuthority(r.floor, "roster-a"); err != nil || f.Serial != 4 {
		t.Errorf("the floor = %+v, %v; want serial 4", f, err)
	}
}

// TestAFloorLoweredBehindThePlaneDoesNotLowerTheRosterHeld lowers the floor
// file by hand, which only write access to the directory can, and offers the
// lower roster: the floor would take it, the roster held refuses it.
func TestAFloorLoweredBehindThePlaneDoesNotLowerTheRosterHeld(t *testing.T) {
	r := newRig(t, signed(t, doc(1, "alice", "bob")))
	writeRoster(t, r.path, signed(t, doc(3, "alice")))
	wantNoKey(t, "bob at serial 3", r.lookup(t, "bob"), trust.StateUnknownKey, trust.CauseUnknownKey)
	name := "aa9cf7aaa04327b2aa05802ffb6b7c66b3c037b162ff5640229b8cf7cec71d5b.authority.json"
	lowered := `{"schema_version":"1.0","roster_id":"roster-a","serial":null,"digest":null}` + "\n"
	if err := os.WriteFile(filepath.Join(r.floor, name), []byte(lowered), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRoster(t, r.path, signed(t, doc(2, "alice", "bob")))
	wantNoKey(t, "bob at serial 2 after the floor was lowered", r.lookup(t, "bob"), trust.StateUnknownKey, trust.CauseUnknownKey)
	if err := os.WriteFile(filepath.Join(r.floor, name), []byte(lowered), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRoster(t, r.path, signed(t, doc(3, "alice", "bob")))
	wantNoKey(t, "bob in another serial 3 after the floor was lowered", r.lookup(t, "bob"), trust.StateUnknownKey, trust.CauseUnknownKey)
}

func TestAFirstRosterBelowTheFloorIsNeverTaken(t *testing.T) {
	dir := t.TempDir()
	floor := filepath.Join(dir, "floor")
	if err := policystate.InitAuthority(t.Context(), floor, "roster-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := policystate.RaiseAuthority(t.Context(), floor, "roster-a", 5, "sha256:"+hex64("5")); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		serial int64
		want   trust.Cause
	}{
		{"a lower serial", 4, trust.CauseBelowFloor},
		{"the floor's serial with another digest", 5, trust.CauseForked},
	}
	for _, c := range cases {
		path := filepath.Join(dir, "roster.json")
		writeRoster(t, path, signed(t, doc(c.serial, "alice")))
		roster, err := trust.Open(t.Context(), trust.Config{Root: public("root"), Path: path, FloorDir: floor, Clock: func() time.Time { return clockAt(t, "2026-10-30T12:00:00Z") }})
		if err != nil {
			t.Fatal(err)
		}
		wantNoKey(t, c.name, roster.Lookup(t.Context(), idOf("alice")), trust.StateUnavailable, c.want)
	}
}

func hex64(digit string) string {
	out := ""
	for range 64 {
		out += digit
	}
	return out
}

func TestTheFileKindRefusesWhatItMayNotTake(t *testing.T) {
	future := doc(2, "alice")
	future["issuedAt"], future["expiresAt"] = "2026-10-30T12:00:01Z", "2026-10-31T12:00:00Z"
	other := doc(2, "alice")
	other["rosterId"] = "roster-b"
	cases := []struct {
		name string
		env  trust.Envelope
		want trust.Cause
	}{
		{"a roster dated a second after the clock", signed(t, future), trust.CauseFuture},
		{"a roster of another id", signed(t, other), trust.CauseOtherRoster},
		{"a roster signed by another key", signBody(t, trust.PayloadType, canonical(t, doc(2, "alice")), "stranger"), trust.CauseRefused},
	}
	for _, c := range cases {
		r := newRig(t, signed(t, doc(1, "alice", "bob")))
		writeRoster(t, r.path, c.env)
		wantKey(t, c.name+", bob kept from the roster held", r.lookup(t, "bob"), "bob")
		if s := r.roster.Status(t.Context()); s.Last != c.want || s.Serial != 1 {
			t.Errorf("%s: Status = %+v, want last %q at serial 1", c.name, s, c.want)
		}
	}
	ok := doc(2, "alice")
	ok["issuedAt"] = "2026-10-30T12:00:00Z"
	r := newRig(t, signed(t, doc(1, "alice", "bob")))
	writeRoster(t, r.path, signed(t, ok))
	wantNoKey(t, "the control, a roster dated at the clock, revokes bob", r.lookup(t, "bob"), trust.StateUnknownKey, trust.CauseUnknownKey)
}

func TestTheFileKindRefusesARosterNamingAnotherAuthoritysKey(t *testing.T) {
	route := public("route")
	r := newRig(t, signed(t, doc(1, "alice", "route")), public("policy"), route)
	wantNoKey(t, "a roster naming the route key", r.lookup(t, "alice"), trust.StateUnavailable, trust.CauseRefused)
	writeRoster(t, r.path, signed(t, doc(1, "alice")))
	wantKey(t, "the control, the roster without it", r.lookup(t, "alice"), "alice")
}

func TestOpenChecksItsConfigurationOnce(t *testing.T) {
	clock := func() time.Time { return time.Time{} }
	base := trust.Config{Root: public("root"), Path: "roster.json", FloorDir: "floor", Clock: clock}
	if _, err := trust.Open(t.Context(), base); err != nil {
		t.Fatalf("the control: %v", err)
	}
	cases := []struct {
		name   string
		change func(*trust.Config)
		want   error
	}{
		{"an unknown kind", func(c *trust.Config) { c.Kind = "api" }, trust.ErrUnknownKind},
		{"no roster file", func(c *trust.Config) { c.Path = "" }, trust.ErrConfig},
		{"no floor directory", func(c *trust.Config) { c.FloorDir = "" }, trust.ErrConfig},
		{"no clock", func(c *trust.Config) { c.Clock = nil }, trust.ErrConfig},
		{"no root", func(c *trust.Config) { c.Root = nil }, trust.ErrRootKey},
		{"a root that is the lift key", func(c *trust.Config) { c.Others = []ed25519.PublicKey{public("lift"), public("root")} }, trust.ErrKeyReused},
	}
	for _, c := range cases {
		cfg := base
		c.change(&cfg)
		if _, err := trust.Open(t.Context(), cfg); !errors.Is(err, c.want) {
			t.Errorf("%s = %v, want %v", c.name, err, c.want)
		}
	}
}

func TestALookupAfterItsContextEndedNamesNoKey(t *testing.T) {
	r := newRig(t, signed(t, doc(1, "alice")))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	wantNoKey(t, "a cancelled lookup", r.roster.Lookup(ctx, idOf("alice")), trust.StateUnavailable, trust.CauseCanceled)
}

func TestAPortThatNeverTookARosterIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	roster, err := trust.Open(t.Context(), trust.Config{
		Root: public("root"), Path: filepath.Join(dir, "absent.json"), FloorDir: filepath.Join(dir, "floor"),
		Clock: func() time.Time { return clockAt(t, "2026-10-30T12:00:00Z") },
	})
	if err != nil {
		t.Fatal(err)
	}
	wantNoKey(t, "no roster file", roster.Lookup(t.Context(), idOf("alice")), trust.StateUnavailable, trust.CauseUnreadable)
	writeRoster(t, filepath.Join(dir, "absent.json"), signed(t, doc(1, "alice")))
	wantNoKey(t, "a roster and no floor directory", roster.Lookup(t.Context(), idOf("alice")), trust.StateUnavailable, trust.CauseFloor)
}
