package trust_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/trust"
)

// conformanceFile is the port's conformance cases, which every kind passes.
const conformanceFile = "../../testdata/ports/trust-roster/cases.json"

// payloadTypes names the payload types a case may sign a roster under.
var payloadTypes = map[string]string{
	"roster":    "application/vnd.agent-trust-roster+json",
	"freshness": "application/vnd.agent-policy-freshness+json",
	"answer":    "application/vnd.agent-approval-answer+json",
}

type rosterCases struct {
	Port   string       `json:"port"`
	Format string       `json:"format"`
	About  string       `json:"about"`
	Cases  []rosterCase `json:"cases"`
}

type rosterCase struct {
	Rule    string       `json:"rule"`
	Case    string       `json:"case"`
	Control bool         `json:"control"`
	Steps   []rosterStep `json:"steps"`
}

type rosterStep struct {
	Write  *rosterWrite `json:"write"`
	Damage bool         `json:"damage"`
	Clock  string       `json:"clock"`
	Lookup string       `json:"lookup"`
	State  string       `json:"state"`
	Cause  string       `json:"cause"`
}

type rosterWrite struct {
	Serial      int64    `json:"serial"`
	Keys        []string `json:"keys"`
	Signer      string   `json:"signer"`
	PayloadType string   `json:"payloadType"`
	RosterID    string   `json:"rosterId"`
	IssuedAt    string   `json:"issuedAt"`
	ExpiresAt   string   `json:"expiresAt"`
}

var stateNames = map[string]trust.State{"unavailable": trust.StateUnavailable, "unknown_key": trust.StateUnknownKey, "key": trust.StateKey}

func readRosterCases(t *testing.T) rosterCases {
	t.Helper()
	raw, err := os.ReadFile(conformanceFile)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var cs rosterCases
	if err := dec.Decode(&cs); err != nil {
		t.Fatalf("%s: %v", conformanceFile, err)
	}
	if cs.Port != "trust-roster" || cs.Format != "0.1" || len(cs.Cases) == 0 {
		t.Fatalf("%s names port %q format %q with %d cases", conformanceFile, cs.Port, cs.Format, len(cs.Cases))
	}
	return cs
}

// TestEveryRosterRuleHasANegativeControl refuses a rule whose cases are all
// passes or all refusals: a rule with no control cannot tell a kind that
// enforces it from one that ignores it.
func TestEveryRosterRuleHasANegativeControl(t *testing.T) {
	seen := map[string][2]int{}
	for _, c := range readRosterCases(t).Cases {
		n := seen[c.Rule]
		if c.Control {
			n[1]++
		} else {
			n[0]++
		}
		seen[c.Rule] = n
	}
	for rule, n := range seen {
		if n[0] == 0 || n[1] == 0 {
			t.Errorf("rule %s has %d cases and %d controls", rule, n[0], n[1])
		}
	}
}

func TestEveryRosterKindPassesTheConformanceCases(t *testing.T) {
	cases := readRosterCases(t)
	for _, kind := range []trust.Kind{trust.KindFile} {
		for _, c := range cases.Cases {
			t.Run(string(kind)+"/"+c.Rule+"/"+c.Case, func(t *testing.T) { runRosterCase(t, kind, c) })
		}
	}
}

func runRosterCase(t *testing.T, kind trust.Kind, c rosterCase) {
	dir := t.TempDir()
	path, floor := filepath.Join(dir, "roster.json"), filepath.Join(dir, "floor")
	if err := policystate.InitAuthority(t.Context(), floor, "roster-a"); err != nil {
		t.Fatal(err)
	}
	now := clockAt(t, "2026-10-30T12:00:00Z")
	roster, err := trust.Open(t.Context(), trust.Config{Kind: kind, Root: public("root"), Path: path, FloorDir: floor, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range c.Steps {
		switch {
		case s.Write != nil:
			writeRoster(t, path, caseEnvelope(t, *s.Write))
		case s.Damage:
			if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		case s.Clock != "":
			now = clockAt(t, s.Clock)
		case s.Lookup != "":
			lookupStep(t, i, roster, s)
		default:
			t.Fatalf("step %d does nothing", i)
		}
	}
}

func lookupStep(t *testing.T, i int, roster trust.Roster, s rosterStep) {
	t.Helper()
	want, ok := stateNames[s.State]
	if !ok {
		t.Fatalf("step %d names state %q", i, s.State)
	}
	a := roster.Lookup(t.Context(), idOf(s.Lookup))
	if a.State() != want || string(a.Cause()) != s.Cause || (want == trust.StateKey) != (a.Key().Subject() == s.Lookup) {
		t.Errorf("step %d: lookup %s = state %d cause %q subject %q, want %s %q", i, s.Lookup, a.State(), a.Cause(), a.Key().Subject(), s.State, s.Cause)
	}
}

func caseEnvelope(t *testing.T, w rosterWrite) trust.Envelope {
	t.Helper()
	d := doc(w.Serial, w.Keys...)
	for member, v := range map[string]string{"rosterId": w.RosterID, "issuedAt": w.IssuedAt, "expiresAt": w.ExpiresAt} {
		if v != "" {
			d[member] = v
		}
	}
	signer, pt := w.Signer, payloadTypes["roster"]
	if signer == "" {
		signer = "root"
	}
	if w.PayloadType != "" {
		var ok bool
		if pt, ok = payloadTypes[w.PayloadType]; !ok {
			t.Fatalf("a write names payload type %q", w.PayloadType)
		}
	}
	return signBody(t, pt, canonical(t, d), signer)
}
