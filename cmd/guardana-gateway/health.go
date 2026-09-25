package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	adaptermcp "github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/adapters/otel"
	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/gateway"
	"github.com/guardana/control/internal/metrics"
	"github.com/guardana/control/internal/pause"
)

// healthAnswer is what GET /healthz says about the plane. It carries no
// configuration and no credential: the mode, the bundle it serves, what each
// seam counted, and whether the plane still takes material calls.
type healthAnswer struct {
	Status   string         `json:"status"`
	Mode     string         `json:"mode"`
	Adapter  string         `json:"adapter"`
	UptimeS  float64        `json:"uptime_seconds"`
	Halted   bool           `json:"halted"`
	Halt     haltAnswer     `json:"halt"`
	Bundle   bundleAnswer   `json:"bundle"`
	Pipeline map[string]any `json:"pipeline"`
	// Approvals is where a held request is kept, whether this plane keeps a
	// durable record of its own holds, and what its reconciliation settled.
	Approvals map[string]any `json:"approvals"`
	Spool     map[string]any `json:"spool"`
	Exporter  map[string]any `json:"exporter"`
	// Pause is the operator's pause state as a call admitted now would be
	// decided under it.
	Pause    pauseAnswer `json:"pause"`
	Problems []string    `json:"problems,omitempty"`
}

// pauseAnswer is the pause state without a reason in it: the state, how many
// entries are in force and whether one is global, the entries that pause
// only calls to names no upstream lists, how old the read is, why an unknown
// state is unknown, and what the reader counted.
type pauseAnswer struct {
	State   string `json:"state"`
	Cause   string `json:"cause,omitempty"`
	Entries int    `json:"entries"`
	Global  bool   `json:"global"`
	// UnlistedOnly names the entries of an upstream this plane does not have,
	// or of a tool its upstream does not list: each pauses only calls that
	// carry no provider, which are calls to names no upstream lists.
	UnlistedOnly []string `json:"unlisted_only"`
	// AgeMS is left out where nothing was read: a plane with no pause file.
	AgeMS *int64 `json:"age_ms,omitempty"`
	// Polls is what the reader counted: its reads, the failed ones by cause
	// and the changes of the snapshot. It is left out where nothing reads.
	Polls map[string]any `json:"polls,omitempty"`
}

// haltAnswer names the halts of ADR-0013 by what raised each. The pipeline
// reports one flag for both, so a mismatch is read from its own counter and an
// unwritable closing record from the flag that outlives it.
type haltAnswer struct {
	ExecutedArgsMismatch bool `json:"executed_args_mismatch"`
	EvidenceUnwritable   bool `json:"evidence_unwritable"`
}

type bundleAnswer struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	Digest      string `json:"digest"`
	Serial      int64  `json:"serial"`
	ConfirmedAt string `json:"confirmed_at"`
}

// healthMux answers the plane's own state and what the product is called.
func (p *plane) healthMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", p.serveHealth)
	mux.HandleFunc("GET /metrics", p.serveMetrics)
	mux.HandleFunc("GET /brand", brandHandler(p.logger))
	return mux
}

// serveMetrics answers every statistic in the text exposition format. A spool
// that cannot answer is reported by a flag, with its metrics left out rather
// than written as zero; a reading the text cannot carry truthfully is 503
// with no body.
func (p *plane) serveMetrics(w http.ResponseWriter, _ *http.Request) {
	body, err := p.metrics(p.pauseSource(), time.Now)
	if err != nil {
		p.logger.Warn("metrics cannot be reported", "err", err)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", metrics.ContentType)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		p.logger.Debug("answer not delivered", "err", err)
	}
}

// metrics reads every seam's statistics, the pause state from src before
// the clock, as health does.
func (p *plane) metrics(src gateway.PauseSource, clock func() time.Time) ([]byte, error) {
	snap := src.Current().At(clock())
	spoolStats, err := p.spool.Stats()
	if err != nil {
		p.logger.Warn("metrics: the spool cannot report", "err", err)
	}
	r := metrics.Reading{
		Pipeline: p.pipeline.Stats(), Adapter: p.adapter.Stats(),
		PauseState: snap.State(), PauseEntries: len(snap.Entries()),
		Spool: spoolStats, SpoolBroken: err != nil,
		Exporter: p.exporter.Stats(), ExporterStopped: p.stopped.Load() != nil,
	}
	if p.poller != nil {
		r.Pause = p.poller.Stats()
	}
	return metrics.Render(r)
}

func (p *plane) serveHealth(w http.ResponseWriter, _ *http.Request) {
	answer, ok := p.health(p.pauseSource(), time.Now)
	status := http.StatusOK
	if !ok {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, p.logger, status, answer)
}

// health reads every seam's counters, the pause state from src before the
// clock, as a call takes them. ok is false when the plane takes no material
// call, either halt of ADR-0013 or a spool that cannot answer, and when it
// takes no call at all because its pause state is unknown. An operator's
// pause is not a problem.
func (p *plane) health(src gateway.PauseSource, clock func() time.Time) (healthAnswer, bool) {
	snap := src.Current()
	now := clock()
	stats := p.pipeline.Stats()
	answer := healthAnswer{
		Status:  "ok",
		Mode:    p.cfg.ModeName,
		Adapter: p.adapter.Name(),
		UptimeS: now.Sub(p.started).Seconds(),
		Halted:  stats.Halted,
		Halt: haltAnswer{
			ExecutedArgsMismatch: stats.Mismatches > 0,
			EvidenceUnwritable:   stats.Halted && stats.Mismatches == 0,
		},
		Pipeline:  pipelineCounters(stats, p.adapter.Stats()),
		Approvals: approvalCounters(p.cfg.Approvals.Provider, stats),
		Exporter:  exporterCounters(p.exporter.Stats()),
	}
	if snap := p.holder.Current(); snap != nil {
		ref := snap.Ref()
		answer.Bundle = bundleAnswer{
			ID: ref.GetBundleId(), Version: ref.GetVersion(), Digest: ref.GetDigest(),
			Serial: snap.Serial(), ConfirmedAt: snap.ConfirmedAt().UTC().Format(time.RFC3339),
		}
	} else {
		answer.Problems = append(answer.Problems, "no policy bundle is installed")
	}
	answer.Pause = p.pauseAnswer(snap.At(now), now)
	if answer.Pause.State == pause.Unknown.String() {
		answer.Problems = append(answer.Problems,
			"pause: the pause state is unknown ("+answer.Pause.Cause+"), so every call is blocked")
	}
	if reason := p.stopped.Load(); reason != nil {
		answer.Exporter["stopped"] = (*reason).Error()
	}
	spoolStats, err := p.spool.Stats()
	if err != nil {
		answer.Problems = append(answer.Problems, "spool: "+err.Error())
	} else {
		answer.Spool = map[string]any{
			"segments": spoolStats.Segments, "bytes": spoolStats.Bytes,
			"unacknowledged": spoolStats.Unacknowledged, "reserved": spoolStats.Reserved,
			"open_trails": spoolStats.OpenTrails, "quarantined_records": spoolStats.QuarantinedRecords,
			"quarantined_bytes": spoolStats.QuarantinedBytes, "truncated": spoolStats.Truncated,
			"oldest_segment": spoolStats.Oldest.Segment, "oldest_offset": spoolStats.Oldest.Offset,
		}
	}
	if answer.Halted || len(answer.Problems) > 0 {
		answer.Status = "halted"
		return answer, false
	}
	return answer, true
}

// pauseAnswer is snap, the pause state a call admitted at now would take.
func (p *plane) pauseAnswer(snap pause.Snapshot, now time.Time) pauseAnswer {
	entries := snap.Entries()
	out := pauseAnswer{
		State: snap.State().String(), Entries: len(entries), Global: hasGlobal(entries), UnlistedOnly: []string{},
	}
	for _, e := range p.strayEntries(entries) {
		out.UnlistedOnly = append(out.UnlistedOnly, e.id)
	}
	if snap.State() == pause.Unknown {
		out.Cause = string(snap.Cause())
	}
	if !snap.ReadAt().IsZero() {
		age := now.Sub(snap.ReadAt()).Milliseconds()
		out.AgeMS = &age
	}
	if p.poller != nil {
		stats := p.poller.Stats()
		failed := make(map[string]uint64, len(stats.Failed))
		for cause, n := range stats.Failed {
			failed[string(cause)] = n
		}
		out.Polls = map[string]any{"made": stats.Polls, "failed": failed, "changes": stats.Changes}
	}
	return out
}

func pipelineCounters(s gateway.Stats, adapter adaptermcp.Stats) map[string]any {
	return map[string]any{
		"blocks": s.Blocks, "pending": s.Pending, "executed": s.Executed,
		"sink_failures_before_effect": s.SinkFailuresBeforeEffect,
		"sink_failures_after_effect":  s.SinkFailuresAfterEffect,
		"reads_unrecorded":            s.ReadsUnrecorded, "mismatches": s.Mismatches,
		"multi_use_refused": s.MultiUseRefused, "open": s.Open, "held": s.Held,
		"runs": s.Runs, "flow_uncomputed": s.FlowUncomputed,
		"admitted": adapter.Admitted, "blocked": adapter.Blocked, "sent": adapter.Sent,
		"close_failures": adapter.CloseFailures, "refresh_failures": adapter.RefreshFailures,
		"asks": askCounters(s.Asks),
	}
}

// askCounters is what the plane asked the external decision point, by what
// each ask came back as, and the time spent waiting on it.
func askCounters(a gateway.Asks) map[string]any {
	return map[string]any{
		"made": a.Made, "allowed": a.Allowed, "denied": a.Denied,
		"denied_obligations": a.DeniedObligations, "timed_out": a.TimedOut,
		"unavailable": a.Unavailable, "answer_refused": a.AnswerRefused, "wait_us": a.Micros,
	}
}

// approvalCounters is what an operator needs about the holds: the store a held
// request is kept in, whether a hold lost to a restart is closed at all, and
// whether the last reconciliation read every entry. An incomplete pass is
// reported and never stops the plane from serving (ADR-0016).
func approvalCounters(provider string, s gateway.Stats) map[string]any {
	out := map[string]any{
		"provider": provider, "hold_journal": s.HoldJournal,
		"reconcile_incomplete": s.ReconcileIncomplete,
		"holds_closed":         s.HoldsClosed, "holds_unmeasured": s.HoldsUnmeasured,
		"unheld_records": s.UnheldRecords, "journal_refusals": s.JournalRefusals,
		"held_trails_left_open": s.HeldTrailsLeftOpen,
	}
	if !s.HoldJournal {
		out["limits"] = []string{"a hold this plane loses to a restart is never closed: no hold journal is configured"}
	}
	return out
}

func exporterCounters(s otel.Stats) map[string]any {
	return map[string]any{
		"acknowledged": s.Acknowledged, "quarantined": s.Quarantined,
		"partial_rejected": s.PartialRejected, "refused": s.Refused, "retries": s.Retries,
		"retried_transport": s.RetriedTransport, "retried_redirect": s.RetriedRedirect,
		"retried_auth": s.RetriedAuth, "retried_throttled": s.RetriedThrottled,
		"retried_server": s.RetriedServer, "retried_answer": s.RetriedAnswer,
		"retried_status": s.RetriedStatus,
	}
}

// brandHandler answers what the product is called, which is what the brand
// package holds and nothing a configuration can change.
func brandHandler(logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, logger, http.StatusOK, map[string]string{
			"name": brand.Name, "slug": brand.Slug, "command": brand.Gateway,
			"version": version, "proto_package": brand.ProtoPackage, "otel_namespace": brand.OTelNamespace,
		})
	}
}

// writeJSON answers one document. A write that fails has nobody left to tell:
// the status line is already sent, so the error goes to the log.
func writeJSON(w http.ResponseWriter, logger *slog.Logger, status int, body any) {
	raw, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "cannot encode the answer", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(append(raw, '\n')); err != nil {
		logger.Debug("answer not delivered", "err", err)
	}
}
