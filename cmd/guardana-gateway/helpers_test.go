package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"google.golang.org/protobuf/proto"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/gatewayconfig"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/spool"
)

// The fixture policy: one rule that allows reads and one that denies a
// transfer, signed with the seed below. Nothing here is a credential of
// anyone's: the seed is a constant so the bundle's digest is the same on every
// machine and the golden can carry it.
const fixtureDocument = `{"apiVersion":"agent-policy/v1alpha1",
  "bundle":{"id":"gateway-fixture","version":"2026-09-20.1","serial":1,"maxStaleSeconds":600},
  "rules":[
    {"id":"allow-reads","effect":"ALLOW","when":{"action":{"effect":["READ"]}}},
    {"id":"deny-transfers","effect":"DENY","when":{"action":{"effect":["TRANSACT"]}}}
  ]}`

const fixtureKeyID = "k1"

func fixtureKey() ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
}

func fixturePublicKey() string {
	return base64.StdEncoding.EncodeToString(fixtureKey()[ed25519.SeedSize:])
}

// fixtureConfig is the committed configuration every test starts from, copied
// into the temporary tree so its relative paths point there.
const fixtureConfig = "doctor.yaml"

// tree lays out what a plane needs on disk: the signed bundle, an empty spool
// directory, and the configuration copied from testdata so the committed
// fixture is the one under test. Relative paths in a configuration resolve
// against its own directory, so the copy makes them point into the temporary
// tree.
type tree struct {
	dir    string
	config string
}

func newTree(t *testing.T) tree {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "spool"), 0o750); err != nil {
		t.Fatalf("making the spool directory: %v", err)
	}
	writeBundle(t, filepath.Join(dir, "policy.bundle"), fixtureDocument)
	raw, err := os.ReadFile(filepath.Clean(filepath.Join("testdata", fixtureConfig)))
	if err != nil {
		t.Fatalf("reading the fixture configuration: %v", err)
	}
	config := filepath.Join(dir, fixtureConfig)
	if err := os.WriteFile(config, raw, 0o600); err != nil { //nolint:gosec // G703: the configuration copy under the test's temp dir
		t.Fatalf("writing the configuration: %v", err)
	}
	// Nothing of the environment reaches a test's configuration unless the
	// test puts it there: a variable in the developer's shell would otherwise
	// change what the loader reads.
	clearProductEnvironment(t)
	return tree{dir: dir, config: config}
}

// writeBundle signs the document and writes the serialized bundle.
func writeBundle(t *testing.T, path, document string) {
	t.Helper()
	b, err := policy.Sign([]byte(document), fixtureKey(), fixtureKeyID)
	if err != nil {
		t.Fatalf("signing the fixture bundle: %v", err)
	}
	raw, err := proto.Marshal(b)
	if err != nil {
		t.Fatalf("serializing the fixture bundle: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("writing the fixture bundle: %v", err)
	}
}

// clearProductEnvironment unsets every variable under the product's prefix for
// the duration of the test.
func clearProductEnvironment(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if name, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, brand.EnvPrefix) {
			t.Setenv(name, "")
			if err := os.Unsetenv(name); err != nil {
				t.Fatalf("unsetting %s: %v", name, err)
			}
		}
	}
}

// setEnv sets one configuration key through its environment variable for the
// duration of the test.
func setEnv(t *testing.T, path, value string) {
	t.Helper()
	t.Setenv(brand.Env(gatewayconfig.EnvName(path)), value)
}

// load reads the tree's configuration the way a command does.
func (tr tree) load(t *testing.T) *gatewayconfig.Config {
	t.Helper()
	cfg, err := gatewayconfig.Load(tr.config, os.Environ())
	if err != nil {
		t.Fatalf("Load refused a configuration this test builds as valid: %v", err)
	}
	return cfg
}

// plane builds the serving plane over the tree's configuration, without
// connecting an upstream or binding an address.
func (tr tree) plane(t *testing.T) *plane {
	t.Helper()
	p, err := build(tr.load(t), slog.New(slog.DiscardHandler), time.Now(), roleServe)
	if err != nil {
		t.Fatalf("build refused a configuration this test builds as valid: %v", err)
	}
	t.Cleanup(func() {
		// A test that closed the spool itself, to see what the plane answers
		// then, is the one case where a second close has nothing left to do.
		if err := p.close(); err != nil && !errors.Is(err, spool.ErrClosed) {
			t.Errorf("closing the plane: %v", err)
		}
	})
	return p
}

// upstreamAnswer is what the fixture upstream's one tool answers.
const upstreamAnswer = "read_order ran"

// upstream is an in-process MCP server over stateless Streamable HTTP, with one
// tool. Its URL is what an upstream's endpoint is set to.
func upstream(t *testing.T) string {
	t.Helper()
	ts := httptest.NewServer(upstreamHandler())
	t.Cleanup(ts.Close)
	return ts.URL
}

// upstreamHandler serves the fixture upstream's one tool.
func upstreamHandler() http.Handler {
	server := sdk.NewServer(&sdk.Implementation{Name: "fixture-upstream", Version: "0"}, nil)
	schema := map[string]any{"type": "object", "properties": map[string]any{"id": map[string]any{"type": "string"}}}
	server.AddTool(&sdk.Tool{Name: "read_order", Description: "reads an order", InputSchema: schema},
		func(context.Context, *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: upstreamAnswer}}}, nil
		})
	return sdk.NewStreamableHTTPHandler(
		func(*http.Request) *sdk.Server { return server },
		&sdk.StreamableHTTPOptions{Stateless: true})
}

// output is what a command wrote, with everything that changes between runs
// replaced: the temporary tree's path, and the tail of the one line that
// carries an operating system's own words.
func (tr tree) output(raw string) string {
	out := strings.ReplaceAll(raw, tr.dir, "<tree>")
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		if prefix, _, ok := strings.Cut(line, "not every upstream answered:"); ok {
			lines[i] = prefix + "not every upstream answered: <reason>"
		}
	}
	return strings.Join(lines, "\n")
}

// golden compares what a command wrote with the committed shape. A golden that
// does not exist is a failure, never a file the test writes for itself.
func golden(t *testing.T, name, got string) {
	t.Helper()
	want, err := os.ReadFile(filepath.Clean(filepath.Join("testdata", name)))
	if err != nil {
		t.Fatalf("reading the golden %s: %v", name, err)
	}
	if got != string(want) {
		t.Errorf("output does not match testdata/%s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// fingerprintOf reads the fingerprint doctor printed for one tool, which is
// what an override has to name.
func fingerprintOf(t *testing.T, out, tool string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "/"+tool+" is unclassified") {
			fields := strings.Fields(line)
			return fields[len(fields)-1]
		}
	}
	t.Fatalf("no fingerprint for %s in:\n%s", tool, out)
	return ""
}

// command runs the entry point and returns its status and what it wrote to
// standard output.
func command(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	status := run(context.Background(), slices.Clone(args), &stdout, &stderr)
	return status, stdout.String()
}

// syncBuffer is what a command writes to while a test reads it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// waitFor returns the line carrying prefix once the command has written it, or
// fails: a test that went on without it would read an address nobody bound.
func waitFor(t *testing.T, out *syncBuffer, prefix string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.Contains(line, prefix) {
				return line
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the command never wrote a line with %q; it wrote:\n%s", prefix, out.String())
	return ""
}
