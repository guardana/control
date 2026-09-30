// Command vulnerable-mcp-agent serves one of the demo's two victim MCP
// servers, orders or web, over standard input and output, the way a plane
// starts an upstream it names by command.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// journalDirVariable names the directory each server appends its journal
// to, one JSON line per call it received. demo.yaml lists it in each
// upstream's env, so a test that starts the plane reaches the journal
// through the plane's environment.
const journalDirVariable = "VICTIM_JOURNAL_DIR"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], &sdk.StdioTransport{}, os.Getenv(journalDirVariable), os.Stderr)
	stop()
	os.Exit(code)
}

// run serves the server --serve names on transport until the transport or
// ctx ends, and exits 2 on a usage error and 1 when it cannot serve.
func run(ctx context.Context, args []string, transport sdk.Transport, journalDir string, stderr io.Writer) int {
	flags := flag.NewFlagSet("vulnerable-mcp-agent", flag.ContinueOnError)
	flags.SetOutput(stderr)
	serve := flags.String("serve", "", "the server to serve: orders or web")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	tools, ok := servers[*serve]
	if !ok || flags.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "vulnerable-mcp-agent: --serve orders or --serve web, and no other argument")
		return 2
	}
	j, err := openJournal(journalDir, *serve)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "vulnerable-mcp-agent:", err)
		return 1
	}
	defer func() { _ = j.close() }()
	err = newServer(*serve, tools, j).Run(ctx, transport)
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, io.EOF) {
		_, _ = fmt.Fprintln(stderr, "vulnerable-mcp-agent:", err)
		return 1
	}
	return 0
}
