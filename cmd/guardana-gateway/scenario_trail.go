package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/evidence"
	"github.com/guardana/control/internal/trailfile"
)

// maxTrailBytes bounds the trail file a runner reads whole. A file past it is
// refused rather than read in part.
const maxTrailBytes = 256 << 20

// trailScope is one trail's key, as the trail reader groups them.
type trailScope struct{ tenant, project, request string }

// trailRead is one read of the trail file: every trail it holds, in the order
// of its links, beside the trail reader's own verdict on it.
type trailRead struct {
	// events counts the distinct events the file holds.
	events  int
	scopes  map[string][]trailScope
	trails  map[trailScope][]*controlv1.Event
	verdict map[trailScope]trailfile.Trail
}

// requests is every request id the file holds a trail for.
func (t trailRead) requests() map[string]bool {
	out := make(map[string]bool, len(t.scopes))
	for id := range t.scopes {
		out[id] = true
	}
	return out
}

// lengths is how many distinct events each trail holds.
func (t trailRead) lengths() map[trailScope]int {
	out := make(map[trailScope]int, len(t.trails))
	for key, events := range t.trails {
		out[key] = len(events)
	}
	return out
}

// grewBesides is every request other than request whose trail holds more
// events than prev says it held, sorted, each once.
func (t trailRead) grewBesides(prev map[trailScope]int, request string) []string {
	var out []string
	for key, events := range t.trails {
		if key.request != request && len(events) > prev[key] && !slices.Contains(out, key.request) {
			out = append(out, key.request)
		}
	}
	slices.Sort(out)
	return out
}

// readTrail reads the trail file once, up to its last newline, through the
// trail reader, and groups and links its events the way that reader does. An
// absent file holds nothing when absent is allowed and is refused otherwise.
func readTrail(path string, absent bool) (trailRead, error) {
	out := trailRead{scopes: map[string][]trailScope{}, trails: map[trailScope][]*controlv1.Event{}, verdict: map[trailScope]trailfile.Trail{}}
	raw, err := trailBytes(path, absent)
	if err != nil || raw == nil {
		return out, err
	}
	rep, err := trailfile.Read(bytes.NewReader(raw), trailfile.DefaultMaxLines)
	if err != nil {
		return trailRead{}, fmt.Errorf("the trail file: %w", err)
	}
	out.events = rep.Lines - rep.Duplicates
	for _, tr := range rep.Trails {
		out.verdict[trailScope{tr.TenantID, tr.ProjectID, tr.RequestID}] = tr
	}
	seen := map[string]bool{}
	for line := range bytes.Lines(raw) {
		events, err := evidence.DecodeJSONL(bytes.NewReader(line), 1)
		if err != nil {
			return trailRead{}, fmt.Errorf("the trail file: %w", err)
		}
		ev := events[0]
		if id := ev.GetEventId(); id != "" {
			if seen[id] {
				continue
			}
			seen[id] = true
		}
		key := trailScope{ev.GetTenantId(), ev.GetProjectId(), ev.GetRequestId()}
		if _, ok := out.trails[key]; !ok {
			out.scopes[key.request] = append(out.scopes[key.request], key)
		}
		out.trails[key] = append(out.trails[key], ev)
	}
	for key, events := range out.trails {
		out.trails[key] = linkOrder(events)
	}
	return out, nil
}

// trailBytes is the trail file up to its last newline, nil for a file that is
// absent where absent is allowed.
func trailBytes(path string, absent bool) ([]byte, error) {
	info, err := os.Stat(path)
	switch {
	case absent && errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("the trail file: %w", err)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("the trail file %s is not a regular file", path)
	case info.Size() > maxTrailBytes:
		return nil, fmt.Errorf("the trail file is over %d bytes; point the plane's collector at a new one", maxTrailBytes)
	}
	f, err := os.Open(path) //nolint:gosec // G304: the operator's own --trail, only read
	if err != nil {
		return nil, fmt.Errorf("the trail file: %w", err)
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, maxTrailBytes+1))
	switch {
	case err != nil:
		return nil, fmt.Errorf("the trail file: %w", err)
	case len(raw) > maxTrailBytes:
		return nil, fmt.Errorf("the trail file is over %d bytes; point the plane's collector at a new one", maxTrailBytes)
	}
	return raw[:bytes.LastIndexByte(raw, '\n')+1], nil
}

// trail returns request's events in link order, none when the file holds no
// trail for it. A trail the chain check does not pass, or one the trail
// reader counts other than this read does, cannot be compared and is refused.
func (t trailRead) trail(request string) ([]*controlv1.Event, error) {
	scopes := t.scopes[request]
	switch len(scopes) {
	case 0:
		return nil, nil
	case 1:
	default:
		return nil, fmt.Errorf("request %s has a trail in %d projects", request, len(scopes))
	}
	events, judged := t.trails[scopes[0]], t.verdict[scopes[0]]
	switch {
	case judged.Verdict == trailfile.Failed || judged.Verdict == trailfile.Indeterminate:
		return nil, fmt.Errorf("the trail of request %s is %s: %w", request, judged.Verdict, judged.Reason)
	case judged.Events != len(events) || events[len(events)-1].GetKind() != judged.Last:
		return nil, fmt.Errorf("the trail of request %s reads as %d events ending %s, and the trail reader counts %d ending %s",
			request, len(events), kindName(events[len(events)-1].GetKind()), judged.Events, kindName(judged.Last))
	}
	return events, nil
}

// linkOrder puts events in the order their links give, as the trail reader
// does: the event that links to nothing, then the one linking to it, and so
// on. Events whose links make no one chain stay in the file's order, where
// the chain check refuses them.
func linkOrder(events []*controlv1.Event) []*controlv1.Event {
	next := make(map[string]*controlv1.Event, len(events))
	var head *controlv1.Event
	for _, ev := range events {
		if prev := ev.GetPrevEventId(); prev != "" {
			next[prev] = ev
		} else {
			head = ev
		}
	}
	out := make([]*controlv1.Event, 0, len(events))
	for ev := head; ev != nil && len(out) < len(events); ev = next[ev.GetEventId()] {
		out = append(out, ev)
	}
	if len(out) != len(events) {
		return events
	}
	return out
}
