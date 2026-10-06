package gatewayconfig

import (
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/reaction"
)

// reactionKeys are the four keys of a reaction block, in its order.
var reactionKeys = []string{"reaction.route", "reaction.public_key", "reaction.floor_dir", "reaction.stops"}

// reactionValues are the values each of reactionKeys takes in these tests.
var reactionValues = map[string]string{
	"reaction.route": "route.json", "reaction.public_key": "route.pub",
	"reaction.floor_dir": "routefloor", "reaction.stops": "stops",
}

// withRoute is a runs directory and a whole reaction block, each directory
// its own.
const withRoute = `
runs:
  dir: runs
reaction:
  route: route.json
  public_key: route.pub
  floor_dir: routefloor
  stops: stops
`

func TestReactionDefaultsStandWhereTheFileIsSilent(t *testing.T) {
	cfg := load(t, write(t, ""))
	if cfg.Reaction.Configured() || cfg.Reaction != (ReactionConfig{PollInterval: time.Second}) {
		t.Errorf("reaction = %+v; want no route and a poll every 1s", cfg.Reaction)
	}
	cfg = load(t, write(t, withRoute+"  poll_interval: 250ms\n"))
	want := ReactionConfig{Route: "route.json", PublicKey: "route.pub", FloorDir: "routefloor", Stops: "stops", PollInterval: 250 * time.Millisecond}
	if !cfg.Reaction.Configured() || cfg.Reaction != want {
		t.Errorf("reaction = %+v, want %+v", cfg.Reaction, want)
	}
}

// TestTheReactionKeysAreAllOrNothing: every proper, non-empty subset of the
// four names is refused, and the refusal names each key left out.
func TestTheReactionKeysAreAllOrNothing(t *testing.T) {
	for mask := 1; mask < 1<<len(reactionKeys)-1; mask++ {
		var set, missing []string
		for i, key := range reactionKeys {
			if mask&(1<<i) != 0 {
				set = append(set, key)
			} else {
				missing = append(missing, key)
			}
		}
		t.Run(strings.Join(set, "+"), func(t *testing.T) {
			path := write(t, "\nruns:\n  dir: runs\n")
			for _, key := range set {
				setEnv(t, key, reactionValues[key])
			}
			_, err := Load(path, os.Environ())
			if err == nil {
				t.Fatalf("only %v was accepted", set)
			}
			for _, key := range missing {
				if !strings.Contains(err.Error(), key) {
					t.Errorf("the refusal does not name %s: %v", key, err)
				}
			}
		})
	}
	path := write(t, "\nruns:\n  dir: runs\n")
	for _, key := range reactionKeys {
		setEnv(t, key, reactionValues[key])
	}
	if cfg := load(t, path); !cfg.Reaction.Configured() {
		t.Errorf("all four from the environment configure no route: %+v", cfg.Reaction)
	}
}

func TestTheStopPollIntervalIsBoundedAndNeedsARoute(t *testing.T) {
	for _, c := range []struct {
		value string
		ok    bool
	}{
		{"100ms", true}, {"99ms", false}, {"1m", true}, {"1m1ms", false}, {"0s", false},
	} {
		t.Run(c.value, func(t *testing.T) {
			path := write(t, withRoute)
			setEnv(t, "reaction.poll_interval", c.value)
			_, err := Load(path, os.Environ())
			switch {
			case c.ok && err != nil:
				t.Errorf("%s was refused: %v", c.value, err)
			case !c.ok && err == nil:
				t.Errorf("%s was accepted", c.value)
			case !c.ok && !strings.Contains(err.Error(), "reaction.poll_interval"):
				t.Errorf("the refusal does not name the key: %v", err)
			}
		})
	}
	path := write(t, "")
	setEnv(t, "reaction.poll_interval", "2s")
	if _, err := Load(path, os.Environ()); err == nil || !strings.Contains(err.Error(), "reaction.poll_interval") {
		t.Errorf("an interval with no route: %v, want a refusal naming reaction.poll_interval", err)
	}
}

// TestARouteNeedsARunsDirectory: a stop names an opened run, and a plane
// without a runs directory serves only local runs.
func TestARouteNeedsARunsDirectory(t *testing.T) {
	block := strings.Replace(withRoute, "runs:\n  dir: runs\n", "", 1)
	_, err := Load(write(t, block), os.Environ())
	if err == nil || !strings.Contains(err.Error(), "runs.dir") {
		t.Errorf("a route without runs.dir: %v, want a refusal naming runs.dir", err)
	}
}

// TestTheStopsDirectoryStandsApart: the stops directory may be none of the
// directories the plane or its commands lock or own, may not sit inside one
// and may not hold one. A sibling whose name starts like one is accepted.
func TestTheStopsDirectoryStandsApart(t *testing.T) {
	extra := fileProvider + "pause:\n  file: control/pause.json\n"
	for _, c := range []struct{ key, dir string }{
		{"runs.dir", "runs"},
		{"evidence.dir", "spool"},
		{"approvals.dir", "approvals"},
		{"approvals.hold_journal_dir", "holds"},
		{"policy.state_dir", "floors"},
		{"reaction.floor_dir", "routefloor"},
		{"pause.file", "control"},
	} {
		for _, where := range []struct{ name, stops string }{
			{"the directory itself", c.dir},
			{"inside it", c.dir + "/stops"},
			{"around it", "outer"},
		} {
			t.Run(c.key+"/"+where.name, func(t *testing.T) {
				path := write(t, withRoute+extra)
				setEnv(t, "reaction.stops", where.stops)
				if where.name == "around it" {
					setKeyDir(t, c.key, "outer/"+c.dir)
				}
				_, err := Load(path, os.Environ())
				if err == nil {
					t.Fatalf("a stops directory %s of %s was accepted", where.name, c.key)
				}
				if !strings.Contains(err.Error(), "reaction.stops") || !strings.Contains(err.Error(), c.key) {
					t.Errorf("the refusal names neither reaction.stops nor %s: %v", c.key, err)
				}
			})
		}
	}
	path := write(t, withRoute+extra)
	setEnv(t, "reaction.stops", "spooled")
	if _, err := Load(path, os.Environ()); err != nil {
		t.Errorf("a sibling named like the spool was refused: %v", err)
	}
	if _, err := Load(write(t, withRoute+extra), os.Environ()); err != nil {
		t.Errorf("a stops directory of its own was refused: %v", err)
	}
}

// setKeyDir moves the directory key names to dir, through its variable.
func setKeyDir(t *testing.T, key, dir string) {
	t.Helper()
	if key == "pause.file" {
		setEnv(t, key, dir+"/pause.json")
		return
	}
	setEnv(t, key, dir)
}

// seeded is a key from a seed of one repeated byte, made at run time so no
// key text is in the tree.
func seeded(b byte) ed25519.PublicKey {
	return ed25519.NewKeyFromSeed([]byte(strings.Repeat(string([]byte{b}), ed25519.SeedSize))).Public().(ed25519.PublicKey)
}

func keyText(k ed25519.PublicKey) string { return base64.StdEncoding.EncodeToString(k) }

// negated is k with bit 255, the sign of x, flipped: the point's negation,
// which the same private key signs for.
func negated(k ed25519.PublicKey) ed25519.PublicKey {
	out := slices.Clone(k)
	out[31] ^= 0x80
	return out
}

// routeFor is a route of tenant whose lift key is lift, read as a reader of
// it reads it.
func routeFor(t *testing.T, tenant string, lift ed25519.PublicKey) reaction.Route {
	t.Helper()
	doc := `{"kind":"reaction-route/v1alpha1","route_id":"refunds","serial":1,"tenant_id":"` + tenant + `",` +
		`"scope":"run","lift_public_key":"` + keyText(lift) + `","rules":[{"procedure_id":"refund","version":"3",` +
		`"digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","rule_id":"STEP_OUTSIDE_PROCEDURE","rule_version":"1"}]}`
	r, err := reaction.ParseRoute([]byte(doc))
	if err != nil {
		t.Fatalf("the test's route does not parse: %v", err)
	}
	return r
}

// routeConfig is a configuration with a route whose policy and freshness keys
// are policy and fresh.
func routeConfig(t *testing.T, policy, fresh ed25519.PublicKey) *Config {
	t.Helper()
	path := write(t, withRoute)
	setEnv(t, "policy.public_key", keyText(policy))
	setEnv(t, "policy.freshness_public_key", keyText(fresh))
	return load(t, path)
}

// TestARouteIsOfTheListenersTenant: the listener's tenant is its principal's
// where one is set, and the plane's otherwise; a route of any other tenant is
// refused.
func TestARouteIsOfTheListenersTenant(t *testing.T) {
	policy, fresh, routeKey, lift := seeded(1), seeded(2), seeded(3), seeded(4)
	cfg := routeConfig(t, policy, fresh)
	if err := cfg.CheckRoute(routeFor(t, "acme", lift), routeKey); err != nil {
		t.Errorf("a route of the plane's tenant was refused: %v", err)
	}
	if err := cfg.CheckRoute(routeFor(t, "globex", lift), routeKey); err == nil || !strings.Contains(err.Error(), "tenant") {
		t.Errorf("a route of another tenant: %v, want a refusal naming the tenant", err)
	}
	cfg = routeConfig(t, policy, fresh)
	cfg.Listener.PrincipalTenant = "globex"
	if err := cfg.CheckRoute(routeFor(t, "acme", lift), routeKey); err == nil {
		t.Error("a route of the plane's tenant was accepted for a listener whose principal is of another")
	}
	if err := cfg.CheckRoute(routeFor(t, "globex", lift), routeKey); err != nil {
		t.Errorf("a route of the listener principal's tenant was refused: %v", err)
	}
}

// TestTheRouteKeysAreKeysOfTheirOwn: the route key and the lift key are each
// neither the policy key nor the freshness key nor the other, and a key
// whose sign bit alone differs is the same key.
func TestTheRouteKeysAreKeysOfTheirOwn(t *testing.T) {
	policy, fresh, routeKey, lift := seeded(1), seeded(2), seeded(3), seeded(4)
	for _, c := range []struct {
		name           string
		routeKey, lift ed25519.PublicKey
		first, second  string
	}{
		{"the route key is the policy key", policy, lift, "reaction.public_key", "policy.public_key"},
		{"the route key is the freshness key", fresh, lift, "reaction.public_key", "policy.freshness_public_key"},
		{"the lift key is the policy key", routeKey, policy, "lift_public_key", "policy.public_key"},
		{"the lift key is the freshness key", routeKey, fresh, "lift_public_key", "policy.freshness_public_key"},
		{"the route key is the lift key", lift, lift, "reaction.public_key", "lift_public_key"},
	} {
		for _, flip := range []bool{false, true} {
			name := c.name
			rk := c.routeKey
			if flip {
				name += " with its sign flipped"
				rk = negated(rk)
			}
			t.Run(name, func(t *testing.T) {
				cfg := routeConfig(t, policy, fresh)
				err := cfg.CheckRoute(routeFor(t, "acme", c.lift), rk)
				if !errors.Is(err, reaction.ErrKeysEqual) {
					t.Fatalf("CheckRoute = %v, want %v", err, reaction.ErrKeysEqual)
				}
				if !strings.Contains(err.Error(), c.first) || !strings.Contains(err.Error(), c.second) {
					t.Errorf("the refusal does not name %s and %s: %v", c.first, c.second, err)
				}
			})
		}
	}
	cfg := routeConfig(t, policy, fresh)
	if err := cfg.CheckRoute(routeFor(t, "acme", lift), routeKey); err != nil {
		t.Errorf("four keys of their own were refused: %v", err)
	}
}
