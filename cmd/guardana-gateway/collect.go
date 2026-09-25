package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/guardana/control/adapters/otel"
	"github.com/guardana/control/internal/loopback"
	"github.com/guardana/control/internal/trailfile"
)

// logsPath is where OTLP/HTTP sends logs, and the one path collect answers.
const logsPath = "/v1/logs"

// The collector's HTTP bounds. A request is one batch the exporter holds
// until it is answered, and an answer waits on the file's sync.
const (
	collectReadHeaderTimeout = 10 * time.Second
	collectReadTimeout       = time.Minute
	collectIdleTimeout       = 2 * time.Minute
	collectShutdown          = 10 * time.Second
)

func declareCollect(flags *flag.FlagSet) commandFunc {
	listen := flags.String("listen", "", "the loopback address to listen on, host:port; port 0 takes a free one")
	out := flags.String("out", "", "the trail file to append what arrives to")
	return func(ctx context.Context, args []string, stdout, stderr io.Writer) int {
		switch {
		case *listen == "":
			writeLine(stderr, flags.Name()+": --listen names the loopback address to listen on")
			return exitUsage
		case *out == "":
			writeLine(stderr, flags.Name()+": --out names the trail file to append to")
			return exitUsage
		case len(args) > 0:
			writeLine(stderr, flags.Name()+": unexpected argument "+oneLine(args[0]))
			return exitUsage
		}
		return collect(ctx, *listen, *out, stdout, stderr)
	}
}

// collect receives OTLP/HTTP logs on a loopback address and appends the
// evidence each one carries to the trail file, until ctx ends. A plane reaches
// a plaintext collector only under export.allow_plaintext, which is meant for
// the loopback, so nothing else is listened on.
func collect(ctx context.Context, listen, out string, stdout, stderr io.Writer) int {
	if err := loopbackOnly(listen); err != nil {
		return fail(stderr, "collect", err)
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listen)
	if err != nil {
		return fail(stderr, "collect", err)
	}
	c, err := startCollector(ln, out, stderr)
	if err != nil {
		return fail(stderr, "collect", fmt.Errorf("--out %w", err))
	}
	if _, err := fmt.Fprintf(stdout, "collect: listening on %s\ncollect: appending to %s\n"+
		"collect: a plane sends here with export.endpoint: %s and export.allow_plaintext: true\n",
		c.url, oneLine(out), c.url); err != nil {
		return fail(stderr, "collect", errors.Join(fmt.Errorf("writing to standard output: %w", err), c.stop()))
	}
	select {
	case err := <-c.served:
		return fail(stderr, "collect", errors.Join(err, c.trail.Close()))
	case <-ctx.Done():
	}
	if err := c.stop(); err != nil {
		return fail(stderr, "collect", err)
	}
	return exitOK
}

// collector is a collect serving on a listener it was handed: the server,
// the trail file it appends to, the URL a plane sends to, and what Serve
// returned once it stops.
type collector struct {
	srv    *http.Server
	trail  *trailfile.Writer
	url    string
	served chan error
}

// startCollector opens the trail file at out and serves ln until stop. It
// takes ln over: a refusal closes it, and names out.
func startCollector(ln net.Listener, out string, stderr io.Writer) (*collector, error) {
	w, err := trailfile.Open(out)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("%s: %w", strconv.Quote(out), err), ln.Close())
	}
	log := slog.New(printableLog(slog.NewTextHandler(stderr, nil)))
	receiver, err := otel.NewReceiver(w, log)
	if err != nil {
		return nil, errors.Join(err, w.Close(), ln.Close())
	}
	mux := http.NewServeMux()
	mux.Handle(logsPath, receiver)
	c := &collector{
		srv: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: collectReadHeaderTimeout,
			ReadTimeout:       collectReadTimeout,
			IdleTimeout:       collectIdleTimeout,
			ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
		},
		trail:  w,
		url:    "http://" + ln.Addr().String() + logsPath,
		served: make(chan error, 1),
	}
	go func() { c.served <- c.srv.Serve(ln) }()
	return c, nil
}

// stop waits for the requests in flight, each of which answers only after
// the file holds its records or refuses them, and closes the file.
func (c *collector) stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), collectShutdown)
	defer cancel()
	err := c.srv.Shutdown(ctx)
	if err != nil {
		err = fmt.Errorf("stopping: %w", err)
	}
	return errors.Join(err, c.trail.Close())
}

// loopbackOnly refuses a --listen that is not host:port with a loopback IP
// literal as its host.
func loopbackOnly(addr string) error {
	if err := loopback.Check(addr); err != nil {
		return fmt.Errorf("--listen %w; collect listens on the loopback only", err)
	}
	return nil
}
