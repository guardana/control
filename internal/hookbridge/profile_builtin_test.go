package hookbridge_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/hookbridge"
)

// The statuses the runtime acts on and the timeout it gives the hook are
// stated in the cases apart from the profile: a block status the runtime
// does not block on would let every denied call run.
func TestEachBuiltInProfileRepliesAsItsCasesSay(t *testing.T) {
	for _, s := range suites(t) {
		p := s.profile
		if p.Reply.BlockExitCode != s.BlockExitCode || p.Reply.ProblemExitCode != s.ProblemExitCode ||
			p.HookTimeout != time.Duration(s.HookTimeoutSeconds)*time.Second {
			t.Errorf("%s: the profile blocks with %d, reports a problem with %d and assumes %v; its cases say %d, %d and %ds",
				s.dir, p.Reply.BlockExitCode, p.Reply.ProblemExitCode, p.HookTimeout, s.BlockExitCode, s.ProblemExitCode, s.HookTimeoutSeconds)
		}
	}
}

// A completion is padded with a member no profile reads, ahead of all the
// others, to the bound its cases state and one byte past it.
func TestEachBuiltInProfileBoundsAnEventAsItsCasesSay(t *testing.T) {
	for _, s := range suites(t) {
		for _, m := range s.boundMisses(t) {
			t.Errorf("%s: %s", s.dir, m)
		}
	}
}

func (s *profileSuite) boundMisses(t *testing.T) []string {
	const pad = "padding"
	p := s.profile
	if p.MaxEventBytes != s.MaxEventBytes {
		return []string{fmt.Sprintf("the profile bounds an event at %d, its cases at %d", p.MaxEventBytes, s.MaxEventBytes)}
	}
	m := p.Members
	if slices.Contains([]string{m.Event, m.Tool, m.Input, m.CallID, m.SessionID, m.Interrupt}, pad) {
		return []string{"the profile reads the member this test pads with"}
	}
	raw := bytes.TrimRight(s.fixture(t, s.BoundFrom), "\n")
	head := `{"` + pad + `":"`
	fill := int(s.MaxEventBytes) - len(head) - len(`",`) - (len(raw) - 1)
	if len(raw) == 0 || raw[0] != '{' || fill < 0 {
		return []string{s.BoundFrom + " cannot be padded to the bound"}
	}
	at := []byte(head + strings.Repeat("x", fill) + `",` + string(raw[1:]))
	var miss []string
	ev, err := hookbridge.ReadEvent(p, bytes.NewReader(at))
	check(&miss, int64(len(at)) != s.MaxEventBytes || err != nil || string(ev.Input) != s.Cases[s.BoundFrom].Input,
		"an event of %d bytes: %v", len(at), err)
	over := append([]byte(head+"x"), at[len(head):]...)
	_, err = hookbridge.ReadEvent(p, bytes.NewReader(over))
	check(&miss, !errors.Is(err, hookbridge.ErrEventTooLarge), "one byte more = %v", err)
	return miss
}

// qualificationPrefix marks a claim's case as one of the profile's
// qualification against the real runtime rather than a fixture.
const qualificationPrefix = "qualification/"

// passedOnTheRuntime is every case a results file in the profile's
// qualification directory, named for the runtime's version, records as
// passed.
func passedOnTheRuntime(t *testing.T, dir string) map[string]bool {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "qualification", "[0-9]*.json"))
	if err != nil {
		t.Fatal(err)
	}
	passed := map[string]bool{}
	for _, path := range paths {
		raw, err := os.ReadFile(path) //nolint:gosec // G304: a results file this package keeps
		if err != nil {
			t.Fatal(err)
		}
		var results struct {
			Cases []struct {
				ID      string `json:"id"`
				Verdict string `json:"verdict"`
			} `json:"cases"`
		}
		if err := json.Unmarshal(raw, &results); err != nil || len(results.Cases) == 0 {
			t.Fatalf("%s holds no cases: %v", path, err)
		}
		for _, c := range results.Cases {
			if c.Verdict == "pass" {
				passed[c.ID] = true
			}
		}
	}
	return passed
}

// unbacked returns every case id the claims name that is neither a fixture
// of the profile's directory nor, after qualificationPrefix, a case its
// qualification passed on the runtime.
func unbacked(dir string, c hookbridge.Claims, passed map[string]bool) []string {
	ids := append([]string{}, c.Runtime.BackedBy...)
	ids = append(ids, c.FailClosed.BackedBy...)
	ids = append(ids, c.Pending.BackedBy...)
	ids = append(ids, c.CallID.BackedBy...)
	ids = append(ids, c.ToolsSeen.BackedBy...)
	for _, b := range c.Bypasses {
		ids = append(ids, b.BackedBy...)
	}
	var missing []string
	for _, id := range ids {
		if q, ok := strings.CutPrefix(id, qualificationPrefix); ok {
			if !passed[q] {
				missing = append(missing, id)
			}
			continue
		}
		_, err := os.Stat(filepath.Join(dir, "fixtures", id+".json"))
		if strings.ContainsAny(id, `/.`) || err != nil {
			missing = append(missing, id)
		}
	}
	return missing
}

func TestEveryClaimOfABuiltInProfileIsBackedByACase(t *testing.T) {
	paths, err := filepath.Glob("profiles/*/profile.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no built-in profile: %v", err)
	}
	for _, path := range paths {
		dir := filepath.Dir(path)
		p, err := hookbridge.LoadProfile(filepath.Base(dir))
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		if missing := unbacked(dir, p.Claims, passedOnTheRuntime(t, dir)); len(missing) != 0 {
			t.Errorf("%s: claims name cases that do not exist or did not pass: %v", dir, missing)
		}
	}
}

// The first case id of each built-in profile's claims is replaced by one
// that names no fixture, by a path to one that does, and by a qualification
// case that no results file records as passed; a passed one is backed.
func TestAClaimNamingNoFixtureIsCaught(t *testing.T) {
	for _, s := range suites(t) {
		passed := passedOnTheRuntime(t, s.dir)
		raw, err := os.ReadFile(filepath.Join(s.dir, "profile.json"))
		if err != nil {
			t.Fatal(err)
		}
		const member = `"backed_by": ["`
		at := bytes.Index(raw, []byte(member))
		if at < 0 {
			t.Fatalf("%s: no claim names a case", s.dir)
		}
		start := at + len(member)
		end := start + bytes.IndexByte(raw[start:], '"')
		doctor := func(id string) hookbridge.Claims {
			p, err := hookbridge.ParseProfile(append(append(bytes.Clone(raw[:start]), id...), raw[end:]...))
			if err != nil {
				t.Fatal(err)
			}
			return p.Claims
		}
		for _, id := range []string{"absent-case", "../fixtures/" + string(raw[start:end]), qualificationPrefix + "absent-case"} {
			if missing := unbacked(s.dir, doctor(id), passed); len(missing) != 1 || missing[0] != id {
				t.Errorf("%s: a claim backed by %q: unbacked %v", s.dir, id, missing)
			}
		}
		for name := range passed {
			id := qualificationPrefix + name
			if missing := unbacked(s.dir, doctor(id), passed); len(missing) != 0 {
				t.Errorf("%s: a claim backed by the passed %q: unbacked %v", s.dir, id, missing)
			}
			if missing := unbacked(s.dir, doctor(id), map[string]bool{}); !slices.Contains(missing, id) {
				t.Errorf("%s: a claim backed by %q, which passed nowhere: unbacked %v", s.dir, id, missing)
			}
			break
		}
	}
}
