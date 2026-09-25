package pause_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guardana/control/internal/pause"
)

// settable is a clock a test moves.
type settable struct {
	mu  sync.Mutex
	now time.Time
}

func (c *settable) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *settable) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func TestOpenRefusesWhatItCannotServe(t *testing.T) {
	path := dirWith(t, docJSON(), 0o600)
	for _, o := range []pause.Options{
		{Interval: time.Second, Clock: at},
		{Path: path, Clock: at},
		{Path: path, Interval: -time.Second, Clock: at},
		{Path: path, Interval: time.Second},
	} {
		if p, err := pause.Open(o); !errors.Is(err, pause.ErrOptions) || p != nil {
			t.Errorf("Open(%+v) = %v, %v; want ErrOptions", o, p, err)
		}
	}
	for _, bad := range []string{
		dirWith(t, `{`, 0o600),
		dirWith(t, docJSON(), 0o622),
		dirWith(t, docJSON(), 0o600) + ".missing",
	} {
		if p, err := pause.Open(pause.Options{Path: bad, Interval: time.Second, Clock: at}); !errors.Is(err, pause.ErrNotServable) || p != nil {
			t.Errorf("Open over %s = %v, %v; want ErrNotServable", bad, p, err)
		}
	}
}

// TestThePollerServesEachReadAndCountsIt: a change to the file reaches the
// snapshot at the next poll, a broken file replaces the last good read, and
// the counters and the log say what happened.
func TestThePollerServesEachReadAndCountsIt(t *testing.T) {
	ctx := context.Background()
	path := dirWith(t, docJSON(), 0o600)
	clock := &settable{now: at()}
	var logged bytes.Buffer
	p, err := pause.Open(pause.Options{
		Path: path, Interval: time.Second, Clock: clock.read,
		Logger: slog.New(slog.NewTextHandler(&logged, nil)),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if s := p.Current(); s.State() != pause.Clear {
		t.Fatalf("after Open: %s", s.State())
	}
	if err := pause.Add(ctx, path, pause.Entry{ID: "p1", Scope: pause.Scope{Kind: "global"}, CreatedAt: at(), Reason: "secret-reason"}); err != nil {
		t.Fatal(err)
	}
	if s := p.Current(); s.State() != pause.Clear {
		t.Errorf("a write reached the snapshot before a poll: %s", s.State())
	}
	clock.set(at().Add(time.Second))
	p.Poll()
	s := p.Current()
	if s.State() != pause.Paused || !s.ReadAt().Equal(at().Add(time.Second)) {
		t.Errorf("after a poll: %s read at %v", s.State(), s.ReadAt())
	}
	p.Poll()
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Poll()
	if s := p.Current(); s.State() != pause.Unknown || s.Cause() != pause.CauseMalformed {
		t.Errorf("after the file broke: %s, %q; want unknown, malformed", s.State(), s.Cause())
	}
	p.Poll()
	expectCounted(t, p)
	expectLogged(t, logged.String())
}

// expectCounted holds the counters to five polls, of which the first, the
// second and the fourth changed the snapshot and the last two failed.
func expectCounted(t *testing.T, p *pause.Poller) {
	t.Helper()
	stats := p.Stats()
	if stats.Polls != 5 || stats.Changes != 3 || stats.Failed[pause.CauseMalformed] != 2 || len(stats.Failed) != 1 {
		t.Errorf("Stats = %+v; want 5 polls, 3 changes, 2 failed as malformed", stats)
	}
	stats.Failed[pause.CauseMissing] = 9
	if p.Stats().Failed[pause.CauseMissing] != 0 {
		t.Errorf("Stats handed out the poller's own map")
	}
}

// expectLogged holds the log to one line per change, naming the ids and the
// cause, never a reason.
func expectLogged(t *testing.T, out string) {
	t.Helper()
	for _, want := range []string{"added=[p1]", "state=paused", "cause=malformed"} {
		if !strings.Contains(out, want) {
			t.Errorf("the log lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret-reason") {
		t.Errorf("the log holds a reason:\n%s", out)
	}
	if strings.Count(out, "\n") != 3 {
		t.Errorf("the log holds %d lines, want one per change:\n%s", strings.Count(out, "\n"), out)
	}
}

// TestRunPollsUntilItsContextEnds: a pause written while the poller runs
// reaches the snapshot within a few intervals, and Run returns when its
// context ends.
func TestRunPollsUntilItsContextEnds(t *testing.T) {
	path := dirWith(t, docJSON(), 0o600)
	p, err := pause.Open(pause.Options{Path: path, Interval: 5 * time.Millisecond, Clock: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()
	if err := pause.Add(context.Background(), path, pause.Entry{ID: "p1", Scope: pause.Scope{Kind: "global"}, CreatedAt: at()}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for p.Current().State() != pause.Paused {
		if time.Now().After(deadline) {
			t.Fatal("the pause never reached the snapshot")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return when its context ended")
	}
}

// TestRunPollsOnEntry: a write made before Run starts reaches the snapshot
// without waiting for the first tick, which here would take an hour.
func TestRunPollsOnEntry(t *testing.T) {
	path := dirWith(t, docJSON(), 0o600)
	p, err := pause.Open(pause.Options{Path: path, Interval: time.Hour, Clock: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err := pause.Add(context.Background(), path, pause.Entry{ID: "p1", Scope: pause.Scope{Kind: "global"}, CreatedAt: at()}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for p.Current().State() != pause.Paused {
		if time.Now().After(deadline) {
			t.Fatal("Run did not poll before its first tick")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestAChangeNamesTheEntriesThatPauseOnlyUnlistedCalls: a change that leaves
// an entry pausing only calls to names no upstream lists is logged at warn
// naming it; one that leaves none, at info.
func TestAChangeNamesTheEntriesThatPauseOnlyUnlistedCalls(t *testing.T) {
	path := dirWith(t, docJSON(), 0o600)
	var logged bytes.Buffer
	p, err := pause.Open(pause.Options{
		Path: path, Interval: time.Second, Clock: at,
		Logger: slog.New(slog.NewTextHandler(&logged, nil)),
		UnlistedOnly: func(entries []pause.Entry) []string {
			var out []string
			for _, e := range entries {
				if e.Scope.Provider == "billing" {
					out = append(out, e.ID)
				}
			}
			return out
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := pause.Add(ctx, path, entry("stray", pause.Scope{Kind: "provider", Provider: "billing"})); err != nil {
		t.Fatal(err)
	}
	p.Poll()
	if err := pause.Remove(ctx, path, "stray"); err != nil {
		t.Fatal(err)
	}
	p.Poll()
	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("the log holds %d lines, want one per change:\n%s", len(lines), logged.String())
	}
	for i, want := range []string{"level=INFO", "level=WARN", "level=INFO"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("line %d is %q, want %s", i, lines[i], want)
		}
	}
	if !strings.Contains(lines[1], "unlisted_only=[stray]") {
		t.Errorf("the warning does not name the entry: %q", lines[1])
	}
}
