package main

import (
	"context"
	"errors"
	"time"

	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/runs"
)

// stopsAnswer is the stop state a call admitted now would be decided under
// (ADR-0046): the route and its floor as the start left them, the list's id,
// the state and why an unknown one is unknown, how old the read is, the
// active stops, at most reaction.MaxListedEntries of them, the list's use of
// its bounds, the active stops naming a run the runs directory does not hold,
// and what the reader counted. A plane with no route says only disabled.
type stopsAnswer struct {
	State  string       `json:"state"`
	Cause  string       `json:"cause,omitempty"`
	Route  *routeAnswer `json:"route,omitempty"`
	Floor  string       `json:"floor,omitempty"`
	ListID string       `json:"list_id,omitempty"`
	// AgeMS is left out where nothing was read.
	AgeMS *int64 `json:"age_ms,omitempty"`
	// Active is how many stops are active; Entries lists the first of them.
	Active  int               `json:"active"`
	Entries []stopEntryAnswer `json:"entries"`
	Usage   *usageAnswer      `json:"usage,omitempty"`
	// Degraded says the list is past nine tenths of a bound, past which the
	// emitter can write no more stops.
	Degraded bool `json:"degraded"`
	// UnheldRuns are the listed stops' runs the runs directory holds no
	// record of, and UnreadRuns those whose record could not be read.
	UnheldRuns []string       `json:"unheld_runs"`
	UnreadRuns []string       `json:"unread_runs"`
	Polls      map[string]any `json:"polls,omitempty"`
}

type routeAnswer struct {
	ID     string `json:"id"`
	Serial int64  `json:"serial"`
	Digest string `json:"digest"`
}

type stopEntryAnswer struct {
	EntryID   string `json:"entry_id"`
	RunID     string `json:"run_id"`
	FindingID string `json:"finding_id"`
	RuleID    string `json:"rule_id"`
	ExpiresAt string `json:"expires_at"`
}

type usageAnswer struct {
	Bytes    int64 `json:"bytes"`
	MaxBytes int64 `json:"max_bytes"`
	Lines    int64 `json:"lines"`
	MaxLines int64 `json:"max_lines"`
}

// stopsAnswer is snap as a call admitted at now would take it.
func (p *plane) stopsAnswer(snap reaction.Snapshot, now time.Time) stopsAnswer {
	at := snap.At(now)
	out := stopsAnswer{State: at.State().String(), Entries: []stopEntryAnswer{}, UnheldRuns: []string{}, UnreadRuns: []string{}}
	if at.State() == reaction.Unknown {
		out.Cause = string(at.Cause())
	}
	if p.stops == nil {
		return out
	}
	route := p.stops.route
	out.Route = &routeAnswer{ID: route.ID(), Serial: route.Serial(), Digest: route.Digest()}
	out.Floor = routeFloorText(p.stops.floor)
	out.ListID = snap.Header().ListID
	if !snap.ReadAt().IsZero() {
		age := now.Sub(snap.ReadAt()).Milliseconds()
		out.AgeMS = &age
	}
	entries, total := at.ActiveEntries(now, now)
	out.Active = total
	for _, e := range entries {
		out.Entries = append(out.Entries, stopEntryAnswer{
			EntryID: e.EntryID, RunID: e.RunID, FindingID: e.FindingID, RuleID: e.RuleID,
			ExpiresAt: e.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	out.UnheldRuns, out.UnreadRuns = unheldRuns(p.runsDir, entries)
	if u := snap.Usage(); u != (reaction.Usage{}) {
		out.Usage = &usageAnswer{Bytes: u.Bytes, MaxBytes: reaction.MaxListBytes, Lines: u.Lines, MaxLines: reaction.MaxListLines}
		out.Degraded = u.Degraded()
	}
	stats := p.stops.poller.Stats()
	failed := make(map[string]uint64, len(stats.Failed))
	for cause, n := range stats.Failed {
		failed[string(cause)] = n
	}
	out.Polls = map[string]any{"made": stats.Polls, "failed": failed}
	return out
}

// unheldRuns names, in order, the runs of entries the runs directory holds no
// record of, and those whose record it could not read: a stop of a run that
// is not there stops nothing, and one that cannot be read is not known to.
func unheldRuns(dir *runs.Plane, entries []reaction.Entry) (unheld, unread []string) {
	unheld, unread = []string{}, []string{}
	for _, e := range entries {
		if dir == nil {
			unread = append(unread, e.RunID)
			continue
		}
		_, err := dir.Lookup(context.Background(), e.RunID)
		switch {
		case errors.Is(err, runs.ErrNoRun):
			unheld = append(unheld, e.RunID)
		case err != nil:
			unread = append(unread, e.RunID)
		}
	}
	return unheld, unread
}
