package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/guardana/control/adapters/authzen"
	adaptermcp "github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/adapters/otel"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/approvals"
	"github.com/guardana/control/internal/core"
	ondisk "github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/gateway"
	"github.com/guardana/control/internal/gatewayconfig"
	"github.com/guardana/control/internal/holdjournal"
	"github.com/guardana/control/internal/pause"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/spool"
)

// maxBundleBytes bounds the bundle file this program reads. It sits above the
// 1 MiB document bound the loader enforces, so an over-long bundle meets that
// refusal and no second bound is kept here.
const maxBundleBytes = 2 << 20

// plane is one gateway: the seams, built and wired, with nothing serving yet.
// build makes every one of them or none, so a refusal leaves no listener bound
// and no directory locked.
type plane struct {
	cfg      *gatewayconfig.Config
	logger   *slog.Logger
	spool    *spool.Spool
	holder   *policy.Holder
	pipeline *gateway.Pipeline
	adapter  *adaptermcp.Adapter
	reader   *spool.Reader
	exporter *otel.Exporter
	// pdp is the decision point's client, nil where none is configured.
	pdp *authzen.Client
	// store is what the pipeline asks about a held request, and records is
	// the same directory when the file provider named one: the plane prunes
	// it and nothing else here reaches it.
	store   gateway.ApprovalStore
	records *approvals.Plane
	// holds is the seam the pipeline writes its journal through, and journal
	// the directory behind it. Both are nil where no journal is configured.
	holds   gateway.HoldJournal
	journal *holdjournal.Journal
	// poller reads the operator's pause file, and is nil where none is
	// configured: the pipeline is then handed the disabled source.
	poller *pause.Poller
	// listed is set once the upstreams answered and the manifest holds what
	// they list, so a pause entry's tool can be judged against it.
	listed  atomic.Bool
	started time.Time
	// stopped holds why the exporter's run ended, which /healthz reports. An
	// export that stopped blocks no decision; it fills the spool.
	stopped atomic.Pointer[error]
}

// role says what the plane being built is for. It is a parameter and not a
// default because both commands call this one function: `run` opens the
// durable approval directories, which takes their locks and makes a directory
// nobody has used yet a store; `doctor` opens neither, so a command that
// serves nothing holds no lock an approver's probe can read as a plane, and
// writes nothing in either (ADR-0016).
type role int

const (
	// roleServe is the plane that will serve, with the directories the
	// configuration names.
	roleServe role = iota
	// roleInspect is the plane a check builds to make the refusals of the
	// adapter, the kernel and the pipeline. Its approval store touches no
	// directory and it keeps no hold journal.
	roleInspect
)

// build wires the plane from cfg. It connects no upstream and binds no
// address: everything that can be refused is refused here, before anything
// serves.
func build(cfg *gatewayconfig.Config, logger *slog.Logger, now time.Time, r role) (*plane, error) {
	holder, err := installedPolicy(cfg, now)
	if err != nil {
		return nil, err
	}
	p := &plane{cfg: cfg, logger: logger, holder: holder, started: now}
	if p.poller, err = openPause(cfg, logger, p.strayIDs); err != nil {
		return nil, err
	}
	if p.spool, err = openSpool(cfg); err != nil {
		return nil, err
	}
	if err := p.open(r); err != nil {
		return nil, errors.Join(err, p.close())
	}
	return p, nil
}

// open takes the durable directories a serving plane needs and wires the
// seams over them. Nothing here reconciles anything, whatever the role: a
// reconciliation writes evidence and resolves records, and `run` is the one
// command that calls for it (ADR-0016).
func (p *plane) open(r role) error {
	if r == roleInspect {
		p.store = &gateway.MemoryApprovals{}
		return p.wire()
	}
	store, records, err := openApprovals(p.cfg)
	if err != nil {
		return err
	}
	p.store, p.records = store, records
	holds, journal, err := openJournal(p.cfg)
	if err != nil {
		return err
	}
	p.holds, p.journal = holds, journal
	return p.wire()
}

// wire builds the adapter, the decision point's client, the pipeline and the
// exporter over the spool this plane already holds.
func (p *plane) wire() error {
	adapterConfig, err := adapterConfig(p.cfg, p.logger)
	if err != nil {
		return err
	}
	adapter, err := adaptermcp.New(adapterConfig)
	if err != nil {
		return err
	}
	mode, err := p.cfg.Mode()
	if err != nil {
		return err
	}
	pdp, err := decisionPoint(p.cfg)
	if err != nil {
		return err
	}
	pipeline, err := gateway.New(p.pipelineConfig(adapter, mode, pdp))
	if err != nil {
		return explainDecisionPoint(err)
	}
	reader, err := p.spool.Reader(spool.Cursor{})
	if err != nil {
		return err
	}
	exporter, err := otel.New(otel.Options{
		Endpoint:       p.cfg.Export.Endpoint,
		AllowPlaintext: p.cfg.Export.AllowPlaintext,
		Headers:        p.cfg.Export.Headers,
		InFlight:       p.cfg.Export.InFlight,
		Timeout:        p.cfg.Export.Timeout,
		MaxBatch:       p.cfg.Export.MaxBatch,
		Linger:         p.cfg.Export.Linger,
		Backoff:        p.cfg.Export.Backoff,
		MaxBackoff:     p.cfg.Export.MaxBackoff,
		Logger:         p.logger,
	}, reader)
	if err != nil {
		return err
	}
	p.adapter, p.pipeline, p.reader, p.exporter, p.pdp = adapter, pipeline, reader, exporter, pdp
	return nil
}

// pipelineConfig is the pipeline's configuration over the seams this plane
// holds, the adapter and the decision point's client.
func (p *plane) pipelineConfig(adapter *adaptermcp.Adapter, mode controlv1.EnforcementMode, pdp *authzen.Client) gateway.Config {
	c := gateway.Config{
		Mode:    mode,
		Adapter: adapter,
		KernelOptions: core.Options{
			MaxStale:     p.cfg.Policy.MaxStale,
			FailOpenRead: p.cfg.Policy.FailOpenRead,
		},
		Policy:               p.holder,
		Pause:                p.pauseSource(),
		Sink:                 p.spool,
		Approvals:            p.store,
		Journal:              p.holds,
		ReconcileMax:         p.cfg.Approvals.ReconcileMax,
		Clock:                time.Now,
		NewID:                newID,
		ApprovalTTL:          p.cfg.Approvals.TTL,
		RetryAfter:           p.cfg.Approvals.RetryAfter,
		MaxHeld:              p.cfg.Approvals.MaxHeld,
		MaxOpen:              p.cfg.Approvals.MaxOpen,
		MaxRuns:              p.cfg.Flow.MaxRuns,
		AllowReadsUnrecorded: p.cfg.Evidence.OnUnwritable == "allow_reads",
	}
	withDecisionPoint(&c, pdp, p.cfg.PDP.Timeout)
	return c
}

// pauseSource is what the pipeline reads the pause state from. A nil poller
// is never handed over as a source: it would be a non-nil interface serving
// the unknown state, which blocks every call of a plane that configured no
// pause at all.
func (p *plane) pauseSource() gateway.PauseSource {
	if p.poller == nil {
		return gateway.PauseDisabled()
	}
	return p.poller
}

// openPause opens the reader of the pause file, whose first read has to find
// a state it can serve: a plane never starts on a pause state it cannot read
// (ADR-0019). The reader takes no lock and writes nothing, so doctor opens it
// too; it logs with each change the entries unlisted names. Without pause.file
// there is nothing to read and it returns nil.
func openPause(cfg *gatewayconfig.Config, logger *slog.Logger, unlisted func([]pause.Entry) []string) (*pause.Poller, error) {
	if cfg.Pause.File == "" {
		return nil, nil
	}
	poller, err := pause.Open(pause.Options{
		Path:         cfg.Resolve(cfg.Pause.File),
		Interval:     cfg.Pause.PollInterval,
		Clock:        time.Now,
		Logger:       logger,
		UnlistedOnly: unlisted,
	})
	if err != nil {
		return nil, fmt.Errorf("pause.file: %w", err)
	}
	return poller, nil
}

// close releases what build took, in the order that leaves nothing holding the
// spool's directory.
func (p *plane) close() error {
	var errs []error
	if p.adapter != nil {
		errs = append(errs, p.adapter.Close())
	}
	if p.reader != nil {
		errs = append(errs, p.reader.Close())
	}
	if p.spool != nil {
		errs = append(errs, p.spool.Close())
	}
	if p.journal != nil {
		errs = append(errs, p.journal.Close())
	}
	if p.records != nil {
		errs = append(errs, p.records.Close())
	}
	return errors.Join(errs...)
}

// installedPolicy verifies the configured bundle against the configured key
// and installs it in a holder pinned to the configured id, which refuses every
// other bundle for the life of the process.
func installedPolicy(cfg *gatewayconfig.Config, now time.Time) (*policy.Holder, error) {
	key, err := policykey.ParsePublic(cfg.Policy.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("policy.public_key: %w", err)
	}
	raw, err := readBounded(cfg.Resolve(cfg.Policy.BundleFile), maxBundleBytes)
	if err != nil {
		return nil, fmt.Errorf("policy.bundle_file: %w", err)
	}
	var b controlv1.PolicyBundle
	if err := proto.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("policy.bundle_file: not a serialized policy bundle: %w", err)
	}
	holder := policy.NewHolder(cfg.Policy.BundleID)
	if err := holder.Install(&b, bundle.Keyring{cfg.Policy.KeyID: key}, now); err != nil {
		if errors.Is(err, policy.ErrKey) && b.GetKeyId() != cfg.Policy.KeyID {
			return nil, fmt.Errorf("policy.bundle_file: signed under key_id %q, and policy.key_id is %q: %w",
				shortID(b.GetKeyId()), cfg.Policy.KeyID, err)
		}
		return nil, fmt.Errorf("policy.bundle_file: %w", err)
	}
	return holder, nil
}

// shortID bounds a key id read from a bundle file, which nothing has verified
// yet, before it reaches a log line.
func shortID(id string) string {
	const limit = 64
	if len(id) > limit {
		return id[:limit] + "..."
	}
	return id
}

func openSpool(cfg *gatewayconfig.Config) (*spool.Spool, error) {
	fsync := spool.FsyncEveryRecord
	if cfg.Evidence.Fsync == "interval" {
		fsync = spool.FsyncInterval
	}
	sp, err := spool.Open(spool.Options{
		Dir:            cfg.Resolve(cfg.Evidence.Dir),
		MaxBytes:       cfg.Evidence.MaxBytes,
		SegmentBytes:   cfg.Evidence.SegmentBytes,
		ClosingReserve: cfg.Evidence.ClosingReserve,
		Fsync:          fsync,
		Interval:       cfg.Evidence.FsyncInterval,
	})
	if err != nil {
		return nil, fmt.Errorf("evidence: %w", err)
	}
	return sp, nil
}

// newID names decisions, events, approvals and executions. Every call returns
// a fresh value; the pipeline probes that at start.
func newID() string { return rand.Text() }

// readBounded reads a regular file of at most limit bytes. The open does not
// block, so a named pipe configured as the bundle is refused rather than
// waited on for good.
func readBounded(path string, limit int64) ([]byte, error) {
	raw, err := ondisk.ReadRegular(filepath.Clean(path), limit, 0)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return raw, nil
}
