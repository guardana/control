package gatewayconfig

import (
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/brand"
)

// pdpBlock configures a decision point with every key it has.
const pdpBlock = `
pdp:
  identifier: https://pdp.example.test/tenant-a
  evaluation_endpoint: https://pdp.example.test/tenant-a/eval
  timeout: 250ms
  max_in_flight: 3
  allow_plaintext: true
  proxy: http://operator@proxy.example.test:3128
  headers:
    X-Tenant-Key: fixture-value
  informational_context:
    - reason
    - trace
`

// TestNoDecisionPointByDefault pins, as literals, what a configuration that
// names no decision point runs with.
func TestNoDecisionPointByDefault(t *testing.T) {
	pdp := load(t, write(t, "")).PDP
	switch {
	case pdp.Identifier != "" || pdp.EvaluationEndpoint != "" || pdp.Proxy != "":
		t.Errorf("a decision point is configured by default: %+v", pdp)
	case pdp.Timeout != 100*time.Millisecond:
		t.Errorf("pdp.timeout is %v, want 100ms", pdp.Timeout)
	case pdp.MaxInFlight != 16:
		t.Errorf("pdp.max_in_flight is %d, want 16", pdp.MaxInFlight)
	case pdp.AllowPlaintext:
		t.Error("a plaintext decision point is allowed by default; the safe default is off")
	case len(pdp.Headers) != 0 || len(pdp.InformationalContext) != 0:
		t.Errorf("headers %v and informational context %q by default", pdp.Headers, pdp.InformationalContext)
	}
}

// TestTheDecisionPointIsRead holds every key of the block, and the
// environment over it: a header by its variable, and an item of the list the
// file declared.
func TestTheDecisionPointIsRead(t *testing.T) {
	path := write(t, pdpBlock)
	t.Setenv(brand.EnvPrefix+EnvName(PDPHeadersPrefix)+"X_TENANT", "acme")
	setEnv(t, "pdp.informational_context.1", "trace_id")
	pdp := load(t, path).PDP
	switch {
	case pdp.Identifier != "https://pdp.example.test/tenant-a":
		t.Errorf("pdp.identifier is %q", pdp.Identifier)
	case pdp.EvaluationEndpoint != "https://pdp.example.test/tenant-a/eval":
		t.Errorf("pdp.evaluation_endpoint is %q", pdp.EvaluationEndpoint)
	case pdp.Timeout != 250*time.Millisecond || pdp.MaxInFlight != 3 || !pdp.AllowPlaintext:
		t.Errorf("timeout %v, in flight %d, plaintext %v", pdp.Timeout, pdp.MaxInFlight, pdp.AllowPlaintext)
	case pdp.Proxy != "http://operator@proxy.example.test:3128":
		t.Errorf("pdp.proxy is %q", pdp.Proxy)
	case len(pdp.Headers) != 2 || pdp.Headers["X-Tenant-Key"] != "fixture-value" || pdp.Headers["X-Tenant"] != "acme":
		t.Errorf("pdp.headers is %v, want the file's and the variable's", pdp.Headers)
	case !slices.Equal(pdp.InformationalContext, []string{"reason", "trace_id"}):
		t.Errorf("pdp.informational_context is %q, want the file's with the variable over its second item", pdp.InformationalContext)
	}
}

// TestAHeaderOfOneMapIsNotTheOthers: the exporter's credential never reaches
// the decision point, nor the other way round.
func TestAHeaderOfOneMapIsNotTheOthers(t *testing.T) {
	path := write(t, pdpBlock)
	t.Setenv(brand.EnvPrefix+EnvName(HeadersPrefix)+"X_COLLECTOR", "c")
	cfg := load(t, path)
	if _, leaked := cfg.PDP.Headers["X-Collector"]; leaked || cfg.Export.Headers["X-Collector"] != "c" {
		t.Errorf("export.headers %v, pdp.headers %v", cfg.Export.Headers, cfg.PDP.Headers)
	}
	if _, leaked := cfg.Export.Headers["X-Tenant-Key"]; leaked {
		t.Errorf("the decision point's header reached export.headers: %v", cfg.Export.Headers)
	}
}

// TestADecisionPointKeyWithNoDecisionPointIsRefused: each key of the block,
// set with no identifier, is refused naming itself; the same key with an
// identifier is read, so the refusal is the identifier's absence and nothing
// else.
func TestADecisionPointKeyWithNoDecisionPointIsRefused(t *testing.T) {
	const identifier = "pdp:\n  identifier: https://pdp.example.test\n"
	for _, c := range []struct {
		key  string
		file string
		env  [2]string
	}{
		{key: "pdp.evaluation_endpoint", file: "  evaluation_endpoint: https://pdp.example.test/eval\n"},
		{key: "pdp.timeout", file: "  timeout: 1s\n"},
		{key: "pdp.timeout", env: [2]string{"pdp.timeout", "100ms"}},
		{key: "pdp.max_in_flight", file: "  max_in_flight: 16\n"},
		{key: "pdp.allow_plaintext", file: "  allow_plaintext: false\n"},
		{key: "pdp.proxy", file: "  proxy: http://proxy.example.test:3128\n"},
		{key: "pdp.headers", file: "  headers:\n    X-Tenant-Key: x\n"},
		{key: "pdp.headers", env: [2]string{"pdp.headers.x_tenant_key", "x"}},
		{key: "pdp.informational_context", file: "  informational_context:\n    - reason\n"},
	} {
		t.Run(c.key, func(t *testing.T) {
			block := ""
			if c.file != "" {
				block = "pdp:\n" + c.file
			}
			path := write(t, block)
			if c.env[0] != "" {
				setEnv(t, c.env[0], c.env[1])
			}
			_, err := Load(path, os.Environ())
			if err == nil {
				t.Fatalf("the loader accepted %s with no pdp.identifier", c.key)
			}
			if !strings.HasPrefix(err.Error(), c.key+": ") || !strings.Contains(err.Error(), "pdp.identifier") {
				t.Errorf("the refusal is %q; it has to name %s and pdp.identifier", err, c.key)
			}

			with := write(t, identifier+c.file)
			if c.env[0] != "" {
				setEnv(t, c.env[0], c.env[1])
			}
			if _, err := Load(with, os.Environ()); err != nil {
				t.Errorf("with an identifier, %s is refused: %v", c.key, err)
			}
		})
	}
}

// TestAnEmptyIdentifierAloneIsNoDecisionPoint: naming no decision point in
// so many words is not a key set without one.
func TestAnEmptyIdentifierAloneIsNoDecisionPoint(t *testing.T) {
	path := write(t, "")
	setEnv(t, "pdp.identifier", "")
	if cfg := load(t, path); cfg.PDP.Identifier != "" {
		t.Errorf("pdp.identifier is %q", cfg.PDP.Identifier)
	}
}

// TestTheProxyIsNotPrinted: its userinfo is a credential, so a listing of
// the settings says it is set and never what it is.
func TestTheProxyIsNotPrinted(t *testing.T) {
	for _, s := range load(t, write(t, pdpBlock)).Settings() {
		if strings.Contains(s.Value, "operator@") {
			t.Errorf("%s shows the proxy's credential: %q", s.Path, s.Value)
		}
		if s.Path == "pdp.proxy" && s.Value != NotPrinted {
			t.Errorf("pdp.proxy shows %q, want %q", s.Value, NotPrinted)
		}
	}
	for _, s := range load(t, write(t, "")).Settings() {
		if s.Path == "pdp.proxy" && s.Value != "" {
			t.Errorf("an unset pdp.proxy shows %q; only a value that is set is withheld", s.Value)
		}
	}
}

// TestAListIndexIsSpelledOnce: an index that is negative, written a second
// way, or past the items read is refused naming the list, never bound, and
// never a crash.
func TestAListIndexIsSpelledOnce(t *testing.T) {
	for name, c := range map[string]struct{ items, wants string }{
		"a negative index":             {"    -1: reason\n", "pdp.informational_context takes a sequence"},
		"one index spelled two ways":   {"    0: reason\n    00: trace\n", "pdp.informational_context takes a sequence"},
		"an index past the items read": {"    1: reason\n", "pdp.informational_context.1 follows no item 0"},
		"a signed index":               {"    +0: reason\n", "pdp.informational_context takes a sequence"},
	} {
		t.Run(name, func(t *testing.T) {
			path := write(t, "pdp:\n  identifier: https://pdp.example.test\n  informational_context:\n"+c.items)
			_, err := Load(path, os.Environ())
			if err == nil {
				t.Fatalf("the loader accepted %s", name)
			}
			if !strings.Contains(err.Error(), c.wants) {
				t.Errorf("the refusal is %q, which does not say %q", err, c.wants)
			}
		})
	}
}

// TestAVariableAddsNoItemToAList: a variable sets an item the file declared;
// one past them names the list it reached into.
func TestAVariableAddsNoItemToAList(t *testing.T) {
	path := write(t, "pdp:\n  identifier: https://pdp.example.test\n  informational_context:\n    - reason\n")
	setEnv(t, "pdp.informational_context.1", "trace")
	_, err := Load(path, os.Environ())
	if err == nil || !strings.Contains(err.Error(), "names no pdp.informational_context entry") {
		t.Fatalf("the refusal is %v", err)
	}
}

// TestCollectionsSpellTheListsAndMaps pins the listing as literals, in order.
func TestCollectionsSpellTheListsAndMaps(t *testing.T) {
	var got []string
	for _, c := range Collections() {
		got = append(got, c.Path+" "+c.Env)
		if c.Holds == "" {
			t.Errorf("%s says nothing of what it holds", c.Path)
		}
	}
	want := []string{
		"listener.origins.N " + brand.Env("LISTENER_ORIGINS_N"),
		"pdp.informational_context.N " + brand.Env("PDP_INFORMATIONAL_CONTEXT_N"),
		"upstreams.N.args.N " + brand.Env("UPSTREAMS_N_ARGS_N"),
		"export.headers.<name> " + brand.Env("EXPORT_HEADERS_<NAME>"),
		"pdp.headers.<name> " + brand.Env("PDP_HEADERS_<NAME>"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("Collections is\n%q\nwant\n%q", got, want)
	}
}

// TestCollectionsFollowTheTables is the property the reference page rests
// on: a list the loader binds is a list it lists, from the one table.
func TestCollectionsFollowTheTables(t *testing.T) {
	lists := listFields
	t.Cleanup(func() { listFields = lists })
	listFields = append(slices.Clone(listFields), listField{"probe.items", "one probe",
		func(c *Config) *[]string { return &c.PDP.InformationalContext }})
	cfg := load(t, write(t, "pdp:\n  identifier: https://pdp.example.test\nprobe:\n  items:\n    - a\n"))
	if !slices.Equal(cfg.PDP.InformationalContext, []string{"a"}) {
		t.Fatalf("the loader did not bind the added list: %q", cfg.PDP.InformationalContext)
	}
	if !slices.ContainsFunc(Collections(), func(c Collection) bool { return c.Path == "probe.items.N" }) {
		t.Error("the added list is bound and not listed")
	}
}

// TestNoKeyReadsAMapsVariable: a variable under a map's prefix is always a
// header, so a key whose variable starts with that prefix could never be set.
func TestNoKeyReadsAMapsVariable(t *testing.T) {
	for _, m := range mapFields {
		prefix := brand.Env(EnvName(m.prefix))
		for _, f := range Fields() {
			if strings.HasPrefix(f.Env, prefix) {
				t.Errorf("%s reads %s, which the map %s owns", f.Path, f.Env, m.prefix)
			}
		}
	}
}
