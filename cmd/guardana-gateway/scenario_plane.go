package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/guardana/control/internal/gatewayconfig"
	"github.com/guardana/control/internal/pause"
)

// The bounds of one /healthz read and how often a wait on the plane asks.
const (
	maxHealthBytes = 1 << 20
	healthPoll     = 20 * time.Millisecond
)

// The protocol revision each HTTP listener serves.
var listenerProtocol = map[string]string{"stateless_http": "2026-07-28", "stateful_http": "2025-11-25"}

// planeTarget is where a runner reaches the plane its configuration names.
// approvals is empty when the plane keeps its holds in memory, and pauseFile
// when it reads no pause file.
type planeTarget struct {
	mcpURL, protocol, healthURL string
	approvals, pauseFile        string
	callTimeout                 time.Duration
}

// targetOf takes everything a runner reaches from the plane's own
// configuration and from nowhere else.
func targetOf(cfg *gatewayconfig.Config) (planeTarget, error) {
	protocol, ok := listenerProtocol[cfg.Listener.Kind]
	if !ok {
		return planeTarget{}, fmt.Errorf("listener.kind is %s, and a scenario reaches a plane over HTTP", cfg.Listener.Kind)
	}
	if cfg.Health.Address == "" {
		return planeTarget{}, errors.New("health.address is empty, and a scenario reads the plane's /healthz")
	}
	t := planeTarget{
		mcpURL: "http://" + cfg.Listener.Address, protocol: protocol,
		healthURL: "http://" + cfg.Health.Address + "/healthz", pauseFile: cfg.Resolve(cfg.Pause.File),
		callTimeout: cfg.Upstream.CallTimeout,
	}
	if cfg.Approvals.Provider == gatewayconfig.ProviderFile {
		t.approvals = cfg.Resolve(cfg.Approvals.Dir)
	}
	return t, nil
}

// planeState is what one /healthz answer says, with every counter a runner
// judges by present: an answer missing one is refused, never read as zero.
type planeState struct {
	status         int
	mode           string
	halted         bool
	bundleID       string
	digest         string
	admitted       uint64
	unacknowledged uint64
	quarantined    uint64
	acknowledged   uint64
	partial        uint64
	pauseState     string
	entries        int
	// polls is the pause file's completed reads, or -1 where nothing reads it.
	polls int64
}

type healthBody struct {
	Mode   string `json:"mode"`
	Halted *bool  `json:"halted"`
	Bundle struct {
		ID     string `json:"id"`
		Digest string `json:"digest"`
	} `json:"bundle"`
	Pipeline struct {
		Admitted *uint64 `json:"admitted"`
	} `json:"pipeline"`
	Spool *struct {
		Unacknowledged *uint64 `json:"unacknowledged"`
		Quarantined    *uint64 `json:"quarantined_records"`
	} `json:"spool"`
	Exporter struct {
		Acknowledged    *uint64 `json:"acknowledged"`
		PartialRejected *uint64 `json:"partial_rejected"`
	} `json:"exporter"`
	Pause struct {
		State   string `json:"state"`
		Entries *int   `json:"entries"`
		Polls   *struct {
			Made *int64 `json:"made"`
		} `json:"polls"`
	} `json:"pause"`
}

// health reads /healthz once. A status other than 200 is returned with what
// the body says, and it is the caller's to refuse.
func (r *runner) health(ctx context.Context) (planeState, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.plane.healthURL, nil)
	if err != nil {
		return planeState{}, err
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return planeState{}, fmt.Errorf("reading /healthz: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxHealthBytes+1))
	if err != nil {
		return planeState{}, fmt.Errorf("reading /healthz: %w", err)
	}
	if len(raw) > maxHealthBytes {
		return planeState{}, fmt.Errorf("/healthz answered over %d bytes", maxHealthBytes)
	}
	var b healthBody
	if err := json.Unmarshal(raw, &b); err != nil {
		return planeState{}, fmt.Errorf("/healthz answered %d with a body that is not its document: %w", resp.StatusCode, err)
	}
	return b.state(resp.StatusCode)
}

func (b healthBody) state(status int) (planeState, error) {
	s := planeState{status: status, mode: b.Mode, bundleID: b.Bundle.ID, digest: b.Bundle.Digest, pauseState: b.Pause.State, polls: -1}
	missing := func(name string) (planeState, error) {
		return planeState{}, fmt.Errorf("/healthz answered %d without %s", status, name)
	}
	switch {
	case b.Halted == nil:
		return missing("halted")
	case b.Pipeline.Admitted == nil:
		return missing("pipeline.admitted")
	case b.Spool == nil || b.Spool.Unacknowledged == nil || b.Spool.Quarantined == nil:
		return missing("the spool's counters")
	case b.Exporter.Acknowledged == nil || b.Exporter.PartialRejected == nil:
		return missing("the exporter's counters")
	case b.Pause.Entries == nil:
		return missing("pause.entries")
	}
	s.halted, s.admitted, s.entries = *b.Halted, *b.Pipeline.Admitted, *b.Pause.Entries
	s.unacknowledged, s.quarantined = *b.Spool.Unacknowledged, *b.Spool.Quarantined
	s.acknowledged, s.partial = *b.Exporter.Acknowledged, *b.Exporter.PartialRejected
	if b.Pause.Polls != nil && b.Pause.Polls.Made != nil {
		s.polls = *b.Pause.Polls.Made
	}
	return s, nil
}

// serving reads /healthz and refuses any answer but 200 from a plane that is
// not halted.
func (r *runner) serving(ctx context.Context) (planeState, error) {
	s, err := r.health(ctx)
	switch {
	case err != nil:
		return planeState{}, err
	case s.status != http.StatusOK || s.halted:
		return planeState{}, fmt.Errorf("/healthz answered %d, halted %t, pause %s", s.status, s.halted, s.pauseState)
	}
	return s, nil
}

// drained waits until the spool holds nothing unacknowledged, which is when
// the collector has written every record of the step, and refuses a spool
// that quarantined a record or an exporter a collector partly refused since
// base: either is a record the trail file will never hold.
func (r *runner) drained(ctx context.Context, base planeState) (planeState, error) {
	deadline := time.Now().Add(r.timeout)
	for {
		s, err := r.serving(ctx)
		switch {
		case err != nil:
			return planeState{}, err
		case s.quarantined != base.quarantined:
			return planeState{}, fmt.Errorf("the spool quarantined %d record(s) the trail file will never hold", s.quarantined-base.quarantined)
		case s.partial != base.partial:
			return planeState{}, fmt.Errorf("the collector dropped %d record(s) of requests it otherwise accepted", s.partial-base.partial)
		case s.unacknowledged == 0:
			return s, nil
		case time.Now().After(deadline):
			return planeState{}, fmt.Errorf("the spool still holds %d unacknowledged bytes after %v", s.unacknowledged, r.timeout)
		}
		if err := pauseFor(ctx, healthPoll); err != nil {
			return planeState{}, err
		}
	}
}

// pauseRead waits until the plane has completed two reads of its pause file
// after the write, since the read in progress may have begun before it, and
// shows entries entries in force.
func (r *runner) pauseRead(ctx context.Context, entries int) error {
	first, err := r.serving(ctx)
	if err != nil {
		return err
	}
	if first.polls < 0 {
		return errors.New("/healthz counts no read of the pause file")
	}
	want := pause.Clear.String()
	if entries > 0 {
		want = pause.Paused.String()
	}
	deadline := time.Now().Add(r.timeout)
	for s := first; ; {
		if s.polls >= first.polls+2 && s.pauseState == want && s.entries == entries {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("after %v the plane read the pause file %d time(s) since the write, as %s with %d entries; want 2 reads and %s with %d",
				r.timeout, s.polls-first.polls, s.pauseState, s.entries, want, entries)
		}
		if err := pauseFor(ctx, healthPoll); err != nil {
			return err
		}
		if s, err = r.serving(ctx); err != nil {
			return err
		}
	}
}

func pauseFor(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
