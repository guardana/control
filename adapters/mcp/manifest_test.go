package mcp_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// TestFingerprintCoversTheWholeDefinition: the fingerprint of one fixed
// definition is pinned, and a change to any part the model reads changes
// it, while a change to a part it does not read leaves it alone.
func TestFingerprintCoversTheWholeDefinition(t *testing.T) {
	base := func() *sdk.Tool {
		hint := true
		return &sdk.Tool{
			Name:         "read_file",
			Description:  "reads a file",
			InputSchema:  map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
			OutputSchema: map[string]any{"type": "object"},
			Annotations:  &sdk.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &hint},
		}
	}
	const pinned = "sha256:d76be82066bcd88ee184af70a8a7f1055b43fdc31da343b3416b9cd3a9ac23b0"
	got, err := mcp.Fingerprint(base())
	if err != nil {
		t.Fatal(err)
	}
	if got != pinned {
		t.Errorf("Fingerprint = %s, want the pinned %s", got, pinned)
	}
	changes := map[string]func(*sdk.Tool){
		"name":        func(x *sdk.Tool) { x.Name = "read_files" },
		"description": func(x *sdk.Tool) { x.Description = "reads a file." },
		"input":       func(x *sdk.Tool) { x.InputSchema = map[string]any{"type": "object"} },
		"output":      func(x *sdk.Tool) { x.OutputSchema = nil },
		"annotations": func(x *sdk.Tool) { x.Annotations.ReadOnlyHint = false },
		"title":       func(x *sdk.Tool) { x.Title = "Read a file" },
		"icons":       func(x *sdk.Tool) { x.Icons = []sdk.Icon{{Source: "https://example/i.png"}} },
		"_meta":       func(x *sdk.Tool) { x.Meta = sdk.Meta{"k": "v"} },
	}
	for part, change := range changes {
		x := base()
		change(x)
		fp, err := mcp.Fingerprint(x)
		if err != nil {
			t.Fatalf("%s: %v", part, err)
		}
		if fp == got {
			t.Errorf("a change to %s left the fingerprint unchanged", part)
		}
	}
	x := base()
	x.InputSchema = map[string]any{"type": "number", "minimum": 0.5}
	if _, err := mcp.Fingerprint(x); err == nil {
		t.Errorf("a schema the canonical form refuses got a fingerprint")
	}
	if _, err := mcp.Fingerprint(nil); err == nil {
		t.Errorf("a nil tool got a fingerprint")
	}
}

// TestOverrideMustPinTheFingerprint: an override under another fingerprint
// leaves the tool unclassified, and a call to it is refused.
func TestOverrideMustPinTheFingerprint(t *testing.T) {
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{mode: modeEnforce, rules: []string{allowEverything}, overrides: func(v *victim, t *testing.T) []mcp.Override {
		o := v.overrides(t)
		o[0].Fingerprint = "sha256:" + strings.Repeat("0", 64)
		return o
	}})
	res, err := callTool(t, r.connect(t, "agent-a"), "read_file", map[string]any{"path": "/x"})
	if err != nil || !res.IsError || codesOf(t, res)[0] != "ACTION_UNCLASSIFIED" {
		t.Fatalf("stale classification on the wire: %v %+v", err, res)
	}
	if n := r.victim.count("read_file"); n != 0 {
		t.Fatalf("victim ran %d times", n)
	}
	e := entryOf(r.adapter, "read_file")
	if e == nil || e.Classified || e.Fingerprint == "" {
		t.Errorf("entry %+v", e)
	}
}

// TestResourceFromReadsTheResourceID: SeeResourceIDs. The id at the pointer
// lands in resource.id as a string or an integer's text, a pointer that
// resolves to nothing leaves it empty, and an escaped token resolves.
func TestResourceFromReadsTheResourceID(t *testing.T) {
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{overrides: func(v *victim, t *testing.T) []mcp.Override {
		return []mcp.Override{
			{Upstream: "victim", Tool: "read_file", Fingerprint: v.fingerprint(t, "read_file"), Effect: effectRead, ResourceType: "file", ResourceFrom: "/path"},
			{Upstream: "victim", Tool: "transfer", Fingerprint: v.fingerprint(t, "transfer"), Effect: effectRead, ResourceType: "account", ResourceFrom: "/account/0/a~1b"},
			{Upstream: "victim", Tool: "slow", Fingerprint: v.fingerprint(t, "slow"), Effect: effectRead, ResourceType: "file", ResourceFrom: "/path/deep"},
			{Upstream: "victim", Tool: "unlisted", Fingerprint: v.fingerprint(t, "unlisted"), Effect: effectRead, ResourceType: "file", ResourceFrom: "/n"},
		}
	}})
	agent := r.connect(t, "agent-a")
	cases := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"read_file", map[string]any{"path": "/x"}, "/x"},
		{"transfer", map[string]any{"account": []any{map[string]any{"a/b": "acc-1"}}}, "acc-1"},
		{"slow", map[string]any{"path": "/x"}, ""},
		{"unlisted", map[string]any{"n": 42}, "42"},
		{"read_file", map[string]any{"path": map[string]any{"x": 1}}, ""},
	}
	for i, tc := range cases {
		if _, err := callTool(t, agent, tc.tool, tc.args); err != nil {
			t.Fatal(err)
		}
		adm := r.pipe.admitted()
		if got := adm[i].a.Envelope.GetResource().GetId(); got != tc.want {
			t.Errorf("%s %v: resource.id = %q, want %q", tc.tool, tc.args, got, tc.want)
		}
	}
}

// TestNewRefusesEachMisconfiguration: every refusal of New, at the input
// where the check is what refuses it.
func TestNewRefusesEachMisconfiguration(t *testing.T) {
	v := newVictim()
	ct, _ := sdk.NewInMemoryTransports()
	valid := func() mcp.Config { return newConfig(t, v, mcp.KindStdio, ct, rigOptions{}) }
	longToken := "/" + strings.Repeat("a", 65)
	deep := strings.Repeat("/a", 9)
	cases := []struct {
		name string
		mut  func(*mcp.Config)
		want error
	}{
		{"zero listener kind", func(c *mcp.Config) { c.Listener.Kind = 0 }, mcp.ErrListener},
		{"unknown listener kind", func(c *mcp.Config) { c.Listener.Kind = 99 }, mcp.ErrListener},
		{"authenticator on stdio", func(c *mcp.Config) { c.Listener.Authenticator = bearer() }, mcp.ErrListener},
		{"no principal", func(c *mcp.Config) { c.Listener.Identity.Principal = nil }, mcp.ErrIdentity},
		{"no agent", func(c *mcp.Config) { c.Listener.Identity.Agent = nil }, mcp.ErrIdentity},
		{"unknown shaping", func(c *mcp.Config) { c.Shaping = 3 }, mcp.ErrShaping},
		{"nil clock", func(c *mcp.Config) { c.Clock = nil }, mcp.ErrNoClock},
		{"nil id source", func(c *mcp.Config) { c.NewID = nil }, mcp.ErrNoIDSource},
		{"negative list timeout", func(c *mcp.Config) { c.ListTimeout = -time.Second }, mcp.ErrListTimeout},
		{"no upstream", func(c *mcp.Config) { c.Upstreams = nil }, mcp.ErrUpstream},
		{"upstream without a name", func(c *mcp.Config) { c.Upstreams[0].Name = "" }, mcp.ErrUpstream},
		{"upstream without a transport", func(c *mcp.Config) { c.Upstreams[0].Transport = nil }, mcp.ErrUpstream},
		{"two upstreams with one name", func(c *mcp.Config) { c.Upstreams = append(c.Upstreams, c.Upstreams[0]) }, mcp.ErrUpstream},
		{"override on an unknown upstream", func(c *mcp.Config) { c.Overrides[0].Upstream = "other" }, mcp.ErrOverride},
		{"override without a tool", func(c *mcp.Config) { c.Overrides[0].Tool = "" }, mcp.ErrOverride},
		{"override without a fingerprint", func(c *mcp.Config) { c.Overrides[0].Fingerprint = "" }, mcp.ErrOverride},
		{"override without a resource type", func(c *mcp.Config) { c.Overrides[0].ResourceType = "" }, mcp.ErrOverride},
		{"override without an effect", func(c *mcp.Config) { c.Overrides[0].Effect = 0 }, mcp.ErrOverride},
		{"override with an undeclared effect", func(c *mcp.Config) { c.Overrides[0].Effect = 99 }, mcp.ErrOverride},
		{"pointer without a slash", func(c *mcp.Config) { c.Overrides[0].ResourceFrom = "path" }, mcp.ErrOverride},
		{"pointer with a bare tilde", func(c *mcp.Config) { c.Overrides[0].ResourceFrom = "/a~2" }, mcp.ErrOverride},
		{"pointer too deep", func(c *mcp.Config) { c.Overrides[0].ResourceFrom = deep }, mcp.ErrOverride},
		{"pointer token too long", func(c *mcp.Config) { c.Overrides[0].ResourceFrom = longToken }, mcp.ErrOverride},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid()
			tc.mut(&cfg)
			a, err := mcp.New(cfg)
			if a != nil || !errors.Is(err, tc.want) {
				t.Errorf("New = %v, %v; want nil, %v", a, err, tc.want)
			}
		})
	}
	// The bounds from the other side: eight tokens of 64 bytes pass.
	cfg := valid()
	cfg.Overrides[0].ResourceFrom = strings.Repeat("/"+strings.Repeat("a", 64), 8)
	if _, err := mcp.New(cfg); err != nil {
		t.Errorf("a pointer at the bound was refused: %v", err)
	}
	cfg = valid()
	cfg.Overrides[0].ResourceFrom = "/a~0b~1c"
	if _, err := mcp.New(cfg); err != nil {
		t.Errorf("an escaped pointer was refused: %v", err)
	}
}

// TestStartRefusals: a nil pipeline, a second start, and a listener served
// before Start.
func TestStartRefusals(t *testing.T) {
	v := newVictim()
	ct, st := sdk.NewInMemoryTransports()
	ss, err := v.server.Connect(ctxT(t), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	a, err := mcp.New(newConfig(t, v, mcp.KindStatelessHTTP, ct, rigOptions{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(ctxT(t), nil); !errors.Is(err, mcp.ErrNoPipeline) {
		t.Errorf("Start(nil) = %v, want ErrNoPipeline", err)
	}
	h, err := a.Handler()
	if err != nil {
		t.Fatal(err)
	}
	url := serveHTTP(t, h)
	if _, err := connectHTTP(t, url, "early", v20260728, nil).ListTools(ctxT(t), nil); err == nil || !strings.Contains(err.Error(), "not started") {
		t.Errorf("a list before Start = %v, want ErrNotStarted", err)
	}
	if err := a.Start(ctxT(t), &fakePipeline{decide: execute}); err != nil {
		t.Fatal(err)
	}
	if err := a.Start(ctxT(t), &fakePipeline{decide: execute}); !errors.Is(err, mcp.ErrStarted) {
		t.Errorf("second Start = %v, want ErrStarted", err)
	}
	if err := a.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

// FuzzManifest: overrides of any shape either refuse at New or build a
// manifest whose every classified entry names an override with its exact
// fingerprint, and a tool no override pins is never classified.
func FuzzManifest(f *testing.F) {
	f.Add("victim", "read_file", "sha256:0", int32(1), "file", "/path", "")
	f.Add("victim", "read_file", "", int32(1), "file", "/path", "reads a file")
	f.Add("other", "read_file", "sha256:0", int32(1), "file", "/path", "")
	f.Add("victim", "delete_file", "sha256:0", int32(3), "", "/a/b~1c", "deletes")
	f.Add("victim", "read_file", "sha256:0", int32(0), "file", "path", "")
	f.Add("victim", "read_file", "sha256:0", int32(99), "file", strings.Repeat("/a", 9), "")
	f.Add("victim", "read_file", "pin", int32(1), "file", "/path", "pinned")
	f.Fuzz(func(t *testing.T, upstream, tool, fp string, effect int32, rtype, from, description string) {
		v := newVictim()
		listed := &sdk.Tool{Name: "read_file", Description: description, InputSchema: map[string]any{"type": "object"}}
		v.server.RemoveTools("read_file")
		v.server.AddTool(listed, v.handler("read_file"))
		if fp == "pin" {
			var err error
			if fp, err = mcp.Fingerprint(listed); err != nil {
				t.Skip()
			}
		}
		o := mcp.Override{Upstream: upstream, Tool: tool, Fingerprint: fp, Effect: controlv1.EffectClass(effect), ResourceType: rtype, ResourceFrom: from}
		a := buildWith(t, v, o)
		if a == nil {
			return
		}
		defer func() { _ = a.Close() }()
		checkManifest(t, a, listed, o)
	})
}

// buildWith builds and starts an adapter over v with one override, or
// returns nil when New refused it, after checking the refusal was due.
func buildWith(t *testing.T, v *victim, o mcp.Override) *mcp.Adapter {
	t.Helper()
	ct, st := sdk.NewInMemoryTransports()
	ss, err := v.server.Connect(ctxT(t), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cfg := newConfig(t, v, mcp.KindStdio, ct, rigOptions{overrides: func(*victim, *testing.T) []mcp.Override { return []mcp.Override{o} }})
	a, err := mcp.New(cfg)
	if err != nil {
		if a != nil {
			t.Fatalf("New returned an adapter beside %v", err)
		}
		return nil
	}
	if o.Upstream != "victim" || o.Tool == "" || o.Fingerprint == "" || o.ResourceType == "" || o.Effect == 0 {
		t.Fatalf("New accepted an incomplete override %+v", o)
	}
	if err := a.Start(ctxT(t), &fakePipeline{decide: execute}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return a
}

// checkManifest holds every entry to the override: only the listed tool
// under its exact fingerprint is classified, and it carries the override.
func checkManifest(t *testing.T, a *mcp.Adapter, listed *sdk.Tool, o mcp.Override) {
	t.Helper()
	want, err := mcp.Fingerprint(listed)
	for _, e := range a.Entries() {
		if e.Tool.Name != listed.Name {
			if e.Classified {
				t.Fatalf("%s is classified with no override", e.Tool.Name)
			}
			continue
		}
		pinned := err == nil && o.Tool == listed.Name && o.Fingerprint == want
		if e.Classified != pinned {
			t.Fatalf("%s classified=%v, want %v (fingerprint %s, override %s)", listed.Name, e.Classified, pinned, want, o.Fingerprint)
		}
		if e.Classified && (e.Effect != o.Effect || e.ResourceFrom != o.ResourceFrom) {
			t.Fatalf("entry %+v does not carry the override", e)
		}
	}
}
