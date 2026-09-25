package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/gatewayconfig"
)

// shutdownGrace bounds how long a listener is given to finish what it is
// answering once the process is asked to stop.
const shutdownGrace = 10 * time.Second

// readHeaderTimeout bounds how long a client may take over sending its
// headers, so an idle connection cannot hold a slot open.
const readHeaderTimeout = 10 * time.Second

// serve builds the plane and serves it until ctx ends. Nothing is bound and
// nothing is connected until every seam is built: a configuration the pipeline,
// the adapter or the spool refuses is printed and nothing starts.
func serve(ctx context.Context, path string, stdout, stderr io.Writer) int {
	cfg, err := gatewayconfig.Load(path, os.Environ())
	if err != nil {
		return fail(stderr, "run", err)
	}
	p, err := startPlane(ctx, cfg, stderr)
	if err != nil {
		return fail(stderr, "run", err)
	}
	defer p.closeReporting(stderr, "run")
	writeLine(stdout, fmt.Sprintf("%s %s: mode %s, bundle %s, evidence in %s",
		brand.Gateway, version, cfg.ModeName, oneLine(cfg.Policy.BundleID), oneLine(cfg.Resolve(cfg.Evidence.Dir))))
	p.settle(ctx, stdout)
	if err := p.run(ctx, stdout, listenTCP, 0); err != nil {
		return fail(stderr, "run", err)
	}
	return exitOK
}

// startPlane builds the serving plane from cfg and connects its upstreams,
// which is everything a plane does before it binds. A plane it cannot start
// is released before the refusal returns.
func startPlane(ctx context.Context, cfg *gatewayconfig.Config, stderr io.Writer) (*plane, error) {
	p, err := build(cfg, newLogger(cfg.LogLevel, stderr), time.Now(), roleServe)
	if err != nil {
		return nil, err
	}
	if err := p.startUpstreams(ctx); err != nil {
		return nil, errors.Join(err, p.close())
	}
	return p, nil
}

// closeReporting releases the plane and reports on stderr what could not be
// released.
func (p *plane) closeReporting(stderr io.Writer, command string) {
	if err := p.close(); err != nil {
		writeLine(stderr, brand.CLI+": "+command+": shutdown: "+oneLine(err.Error()))
	}
}

// settle closes the trails of the holds this plane lost to a restart and then
// forgets the approval records that expired. It runs on `run` and on nothing
// else: `doctor` builds the same plane and never reaches this, because closing
// a trail writes evidence and resolves records (ADR-0016). An incomplete pass
// is reported and the plane serves either way.
func (p *plane) settle(ctx context.Context, stdout io.Writer) {
	if p.holds == nil {
		writeLine(stdout, "no hold journal is configured: a hold this plane loses to a restart is never closed")
		return
	}
	r := p.pipeline.Reconcile(ctx)
	measured := "the pass read every entry"
	if !r.Complete {
		measured = "the pass did not read every entry, so these are a floor and not a total"
	}
	writeLine(stdout, fmt.Sprintf("lost holds: %d closed, %d left open, %d interrupted, %d unreadable; %s",
		r.Closed, r.Unclosed, r.Interrupted, r.Unreadable, measured))
	p.prune(ctx, stdout)
}

// prune forgets the approval records whose approvals expired, after the
// reconciliation has resolved the holds it closed: pruning first would drop a
// record an approver had answered before anything read the answer. A sweep
// that stopped on a record it could not read reports what it forgot so far,
// which is unmeasured and never done.
func (p *plane) prune(ctx context.Context, stdout io.Writer) {
	if p.records == nil {
		return
	}
	forgotten, err := p.records.Prune(ctx, time.Now())
	if err != nil {
		writeLine(stdout, fmt.Sprintf("expired approval records: %d forgotten, and the sweep is unmeasured: %s",
			forgotten, oneLine(err.Error())))
		p.logger.Error("the sweep of expired approval records stopped early", "forgotten", forgotten, "err", err)
		return
	}
	writeLine(stdout, fmt.Sprintf("expired approval records: %d forgotten", forgotten))
}

// run starts the exporter, the pause file's reader, the health answers and
// the listener, and returns when ctx ends or a listener stops on its own.
// listen binds each address the configuration names, or hands over one bound
// already. The pause file is read once more before anything is bound: the
// read the start made is as old as the start took, which can be past what it
// answers for. Once the listeners are shut, the exporter is given up to drain
// to ship what the spool holds before it is stopped.
func (p *plane) run(ctx context.Context, stdout io.Writer, listen listenFunc, drain time.Duration) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	exportCtx, stopExport := context.WithCancel(context.WithoutCancel(ctx))
	defer stopExport()
	if p.poller != nil {
		p.poller.Poll()
		p.warnStray()
	}
	bound, err := p.bind(stdout, listen)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	stopped := make(chan error, len(bound)+1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		p.export(exportCtx)
	}()
	if p.poller != nil {
		// A poller that stopped leaves its last read standing for three
		// intervals and then blocks every call, so it runs as long as the
		// plane does. It logs each change of the snapshot, never a reason.
		wg.Add(1)
		go func() {
			defer wg.Done()
			p.poller.Run(ctx)
		}()
	}
	for _, b := range bound {
		wg.Add(1)
		go func(b listening) {
			defer wg.Done()
			if err := b.server.Serve(b.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				stopped <- err
			}
		}(b)
	}
	if p.cfg.Listener.Kind == "stdio" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			stopped <- p.adapter.ServeStdio(ctx, os.Stdin, os.Stdout)
		}()
	}
	var first error
	select {
	case <-ctx.Done():
	case first = <-stopped:
	}
	cancel()
	for _, b := range bound {
		shutdown, done := context.WithTimeout(context.Background(), shutdownGrace)
		if err := b.server.Shutdown(shutdown); err != nil {
			p.logger.Error("listener did not shut down cleanly", "addr", b.server.Addr, "err", err)
		}
		done()
	}
	p.drainSpool(drain)
	stopExport()
	wg.Wait()
	return first
}

// drainPoll is how often a drain reads the spool's depth.
const drainPoll = 20 * time.Millisecond

// drainSpool waits up to bound until the spool holds nothing unacknowledged.
// It returns early once the exporter has stopped, or when the spool cannot
// say: then nothing more would be shipped by waiting.
func (p *plane) drainSpool(bound time.Duration) {
	deadline := time.Now().Add(bound)
	tick := time.NewTicker(drainPoll)
	defer tick.Stop()
	for time.Now().Before(deadline) && p.stopped.Load() == nil {
		st, err := p.spool.Stats()
		if err != nil || st.Unacknowledged == 0 {
			return
		}
		<-tick.C
	}
}

// listenFunc binds the address a plane's configuration names, or returns the
// listener already bound at it.
type listenFunc func(addr string) (net.Listener, error)

func listenTCP(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }

// listening is one bound address and the server that will answer on it.
type listening struct {
	server   *http.Server
	listener net.Listener
}

// bind takes every address this configuration asks for before anything serves:
// the agent-facing listener when it speaks HTTP, and the plane's own answers
// when an address is configured for them. An address that is taken is a
// refusal, and whatever was bound before it is released.
func (p *plane) bind(stdout io.Writer, listen listenFunc) ([]listening, error) {
	var wanted []wantedServer
	if p.cfg.Listener.Kind != "stdio" {
		wanted = append(wanted, wantedServer{p.cfg.Listener.Address, p.adapter.Handler, "listening for agents on "})
	}
	if p.cfg.Health.Address != "" {
		health := func() (http.Handler, error) { return p.healthMux(), nil }
		wanted = append(wanted, wantedServer{p.cfg.Health.Address, health, "answering /healthz, /metrics and /brand on "})
	}
	var out []listening
	for _, w := range wanted {
		handler, err := w.handler()
		if err != nil {
			return nil, errors.Join(err, closeListeners(out))
		}
		listener, err := listen(w.addr)
		if err != nil {
			return nil, errors.Join(err, closeListeners(out))
		}
		out = append(out, listening{
			server:   &http.Server{Addr: w.addr, Handler: handler, ReadHeaderTimeout: readHeaderTimeout},
			listener: listener,
		})
		writeLine(stdout, w.says+listener.Addr().String())
	}
	return out, nil
}

// wantedServer is one address this configuration asks to be answered on.
type wantedServer struct {
	addr    string
	handler func() (http.Handler, error)
	says    string
}

func closeListeners(bound []listening) error {
	var errs []error
	for _, b := range bound {
		errs = append(errs, b.listener.Close())
	}
	return errors.Join(errs...)
}

// export drains the spool into the collector. A run that ends is recorded and
// reported by /healthz: it never blocks a decision, and the spool's depth is
// what says how much an outage is costing.
func (p *plane) export(ctx context.Context) {
	err := p.exporter.Run(ctx)
	if err == nil || errors.Is(err, context.Canceled) {
		return
	}
	p.stopped.Store(&err)
	p.logger.Error("the evidence exporter stopped; the spool will fill to its budget and then block material calls", "err", err)
}

// newLogger writes the plane's own log as lines on stderr at the configured
// level, every value through oneLine. Nothing a policy or an agent sent is
// logged here; the evidence trail is the record.
func newLogger(level string, stderr io.Writer) *slog.Logger {
	levels := map[string]slog.Level{
		"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError,
	}
	return slog.New(printableLog(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: levels[level]})))
}
