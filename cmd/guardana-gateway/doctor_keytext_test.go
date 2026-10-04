package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/internal/policykey"
)

// forgedCheck is a check line a value would forge if doctor printed it raw.
const forgedCheck = "ok      upstreams     all reachable"

// fixtureKeyText is the fixture key's file and its body line.
func fixtureKeyText(t *testing.T) (pemFile, body string) {
	t.Helper()
	raw, err := policykey.MarshalPrivate(fixtureKey())
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), strings.Split(string(raw), "\n")[1]
}

// doctorOutput runs doctor over the configuration and returns both streams.
func doctorOutput(t *testing.T, config string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	run(context.Background(), []string{"doctor", "--config", config}, &stdout, &stderr)
	return stdout.String() + stderr.String()
}

// checkNoKeyTextOrForgedLine fails the test when doctor's output repeats any
// of the key file or starts a line with the forged check.
func checkNoKeyTextOrForgedLine(t *testing.T, out, body string) {
	t.Helper()
	if strings.Contains(out, body[:21]) || strings.Contains(out, "-----") {
		t.Errorf("doctor repeated key text:\n%s", out)
	}
	if strings.Contains(out, "\n"+forgedCheck) {
		t.Errorf("doctor printed a forged check line:\n%s", out)
	}
}

// TestDoctorRepeatsNoKeyTextAndForgesNoLine: a key's body line given for a
// path is refused by its key, a whole PEM given for another key is withheld
// from the settings, and a line break in a value is quoted, in the settings
// and in a check's line, so none reaches either stream and no forged check
// line appears.
func TestDoctorRepeatsNoKeyTextAndForgesNoLine(t *testing.T) {
	pemFile, body := fixtureKeyText(t)
	for name, c := range map[string]struct{ key, value, want string }{
		"body line as pause.file":         {"pause.file", body, "pause.file: holds a PEM marker"},
		"body line as policy.bundle_file": {"policy.bundle_file", body, "policy.bundle_file: holds a PEM marker"},
		"whole PEM as project_id":         {"project_id", pemFile, `"[key text withheld]\n"`},
		"forged ok line in environment":   {"environment", "dev\n" + forgedCheck, `"dev\n` + forgedCheck + `"`},
		"forged ok line in pause.file":    {"pause.file", "pause\n" + forgedCheck, `pause\n` + forgedCheck + ` cannot be read`},
	} {
		t.Run(name, func(t *testing.T) {
			tr := newTree(t)
			setEnv(t, c.key, c.value)
			out := doctorOutput(t, tr.config)
			checkNoKeyTextOrForgedLine(t, out, body)
			if !strings.Contains(out, c.want) {
				t.Errorf("doctor's output does not hold %q:\n%s", c.want, out)
			}
		})
	}
}

// TestDoctorWithholdsKeyTextInAnUnclassifiedTool: the line naming a tool no
// override classifies names its upstream, and an upstream named by a key's
// body line is withheld there as in the settings list.
func TestDoctorWithholdsKeyTextInAnUnclassifiedTool(t *testing.T) {
	_, body := fixtureKeyText(t)
	tr := newTree(t)
	setEnv(t, "upstreams.0.endpoint", upstream(t))
	setEnv(t, "upstreams.0.name", body)
	out := doctorOutput(t, tr.config)
	if want := policykey.KeyTextWithheld + "/read_order is unclassified"; !strings.Contains(out, want) {
		t.Errorf("doctor's output does not hold %q:\n%s", want, out)
	}
	checkNoKeyTextOrForgedLine(t, out, body)
	if holdsKeyWindow(out, body) {
		t.Errorf("doctor repeated key text:\n%s", out)
	}
}

// TestDoctorPrintsNoKeyTextFromAnUpstreamOrAnOverride: an upstream's
// argument may carry a certificate and loads, and an override may name key
// text; the settings list prints no argument at all, only how many there
// are, and withholds the override's key text, keeping the rest of its line.
func TestDoctorPrintsNoKeyTextFromAnUpstreamOrAnOverride(t *testing.T) {
	_, body := fixtureKeyText(t)
	tr := newTree(t)
	raw, err := os.ReadFile(tr.config)
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.Replace(string(raw), "    endpoint: http://127.0.0.1:1/mcp\n",
		"    command: server\n    args:\n      - --ca=-----BEGIN CERTIFICATE-----x-----END CERTIFICATE-----\n"+
			`      - "a\n`+forgedCheck+`"`+"\n", 1)
	doc += "\noverrides:\n  - upstream: orders\n    tool: " + body + "\n    fingerprint: abc\n" +
		"    effect: READ\n    resource_type: order\n    resource_from: " + body + "\n"
	if err := os.WriteFile(tr.config, []byte(doc), 0o600); err != nil { //nolint:gosec // G703: the configuration copy under the test's temp dir

		t.Fatal(err)
	}
	out := doctorOutput(t, tr.config)
	checkNoKeyTextOrForgedLine(t, out, body)
	for _, want := range []string{
		`orders -> command server (2 arguments)`,
		`orders/[key text withheld] is READ on order from "[key text withheld]"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor's output does not hold %q:\n%s", want, out)
		}
	}
}

// endpointMarker stands for a credential in an upstream's endpoint or in
// what a hostile upstream reflects; no stream of doctor's or run's holds it.
const endpointMarker = "endpoint-private-marker"

// refusingUpstream answers every tools/list with a wire error whose message
// reflects endpointMarker, and returns its URL.
func refusingUpstream(t *testing.T) string {
	t.Helper()
	server := sdk.NewServer(&sdk.Implementation{Name: "refusing-upstream", Version: "0"}, nil)
	server.AddReceivingMiddleware(func(next sdk.MethodHandler) sdk.MethodHandler {
		return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
			if method == "tools/list" {
				return nil, &jsonrpc.Error{Code: -32042, Message: "reflected " + endpointMarker}
			}
			return next(ctx, method, req)
		}
	})
	ts := httptest.NewServer(sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{Stateless: true}))
	t.Cleanup(ts.Close)
	return ts.URL
}

// TestNoUpstreamFailureRepeatsACredential: an upstream run and doctor cannot
// reach, through a query or a username holding a credential, and one whose
// tools/list reflects a credential in its error, are named in both commands'
// output by the upstream and what failed, and the credential is in neither.
func TestNoUpstreamFailureRepeatsACredential(t *testing.T) {
	gone := httptest.NewServer(http.NotFoundHandler())
	host := strings.TrimPrefix(gone.URL, "http://")
	gone.Close()
	for name, c := range map[string]struct{ endpoint, cause string }{
		"a query":         {"http://" + host + "/mcp?token=" + endpointMarker, "mcp: connect orders: Post: dial tcp"},
		"a username":      {"http://" + endpointMarker + ":pw@" + host + "/mcp", "mcp: connect orders: Post: dial tcp"},
		"a reflected one": {refusingUpstream(t) + "/mcp", "mcp: tools/list from orders: JSON-RPC error -32042"},
	} {
		t.Run(name, func(t *testing.T) {
			tr := newTree(t)
			setEnv(t, "upstreams.0.endpoint", c.endpoint)
			setEnv(t, "listener.address", "127.0.0.1:0")
			setEnv(t, "health.address", "")
			var stdout, stderr bytes.Buffer
			if status := serve(context.Background(), tr.config, "", &stdout, &stderr); status != exitFail {
				t.Fatalf("run answered %d over an upstream it cannot list; it must refuse to start", status)
			}
			for command, out := range map[string]string{"run": stdout.String() + stderr.String(), "doctor": doctorOutput(t, tr.config)} {
				if strings.Contains(out, endpointMarker) || !strings.Contains(out, c.cause) {
					t.Errorf("%s says:\n%s\nwant %q and no credential", command, out, c.cause)
				}
			}
		})
	}
}
