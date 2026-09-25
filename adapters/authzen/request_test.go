package authzen

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// golden reads a mapping golden and compacts it, so the file can be indented
// for a reader and still pin exact bytes.
func golden(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := fs.ReadFile(os.DirFS("testdata"), name)
	if err != nil {
		t.Fatalf("reading the golden: %v", err)
	}
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		t.Fatalf("the golden %s is not JSON: %v", name, err)
	}
	return out.Bytes()
}

// TestMappingV1Golden pins mapping version 1, for a call that names every
// field it reads and for one that names only what validation requires.
func TestMappingV1Golden(t *testing.T) {
	minimal := &controlv1.ActionEnvelope{
		SchemaVersion: "1.0",
		RequestId:     "req-min",
		ProjectId:     "proj-a",
		TenantId:      "tenant-a",
		Principal:     &controlv1.Principal{Id: "user-7"},
		Action:        &controlv1.Action{Name: "files.read", Effect: controlv1.EffectClass_EFFECT_CLASS_READ},
		Resource:      &controlv1.Resource{Id: "file-1"},
	}
	cases := map[string]*controlv1.ActionEnvelope{
		"mapping-v1.json":         envelope(),
		"mapping-v1-minimal.json": minimal,
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			got, ok := mapV1(env)
			if !ok {
				t.Fatal("mapV1 refused an envelope with every identifier")
			}
			if want := golden(t, name); !bytes.Equal(got, want) {
				t.Errorf("mapping v1 drifted\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// TestRequest: what goes on the wire is a POST of the golden to the
// evaluation endpoint, with the identifier and the configured headers, and
// no cookie a decision point set earlier.
func TestRequest(t *testing.T) {
	p := newPDP(t, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "affinity", Value: "node-3", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		answer(`{"decision":true}`)(w, r)
	})
	opts := options(p)
	opts.Headers = map[string]string{"X-Route": "blue\tgreen", "X-Tenant-Key": "tk-value"}
	c := client(t, opts)
	for range 2 {
		if got := c.Ask(within(t, 10*time.Second), envelope()); got != core.ExternalAllowed() {
			t.Fatalf("Ask = %v, want allowed", got)
		}
	}
	r := p.lastRequest(t)
	checks := map[string][2]string{
		"method":       {r.Method, "POST"},
		"path":         {r.URL.Path, askedPath},
		"query":        {r.URL.RawQuery, ""},
		"content type": {r.Header.Get("Content-Type"), "application/json"},
		"accept":       {r.Header.Get("Accept"), "application/json"},
		"request id":   {strings.Join(r.Header.Values("X-Request-Id"), ","), "req-0001"},
		"header one":   {r.Header.Get("X-Route"), "blue\tgreen"},
		"header two":   {r.Header.Get("X-Tenant-Key"), "tk-value"},
		"cookie":       {r.Header.Get("Cookie"), ""},
	}
	for what, c := range checks {
		if c[0] != c[1] {
			t.Errorf("%s = %q, want %q", what, c[0], c[1])
		}
	}
	if got, want := p.lastBody(t), golden(t, "mapping-v1.json"); !bytes.Equal(got, want) {
		t.Errorf("the body is not mapping v1\n got: %s\nwant: %s", got, want)
	}
}

// TestEndpointOverride: a configured endpoint on the identifier's origin is
// the one asked.
func TestEndpointOverride(t *testing.T) {
	p := newPDP(t, answer(`{"decision":false}`))
	opts := options(p)
	opts.Identifier = p.URL + "/tenant-a/"
	opts.Endpoint = p.URL + "/v2/evaluate"
	if got := client(t, opts).Ask(within(t, 10*time.Second), envelope()); got != core.ExternalDenied() {
		t.Errorf("Ask = %v, want denied", got)
	}
	defaulted := options(p)
	defaulted.Identifier = p.URL + "/tenant-a/"
	if got := client(t, defaulted).Ask(within(t, 10*time.Second), envelope()); got != core.ExternalDenied() {
		t.Errorf("Ask = %v, want denied", got)
	}
	want := []string{"/v2/evaluate", "/tenant-a" + askedPath}
	p.mu.Lock()
	defer p.mu.Unlock()
	if strings.Join(p.paths, " ") != strings.Join(want, " ") {
		t.Errorf("paths asked = %q, want %q", p.paths, want)
	}
}

// TestNoQuestionWithoutIdentifiers: nothing stands in for an identifier the
// envelope does not name, so the question is not asked.
func TestNoQuestionWithoutIdentifiers(t *testing.T) {
	cases := map[string]func(*controlv1.ActionEnvelope){
		"no resource id":  func(e *controlv1.ActionEnvelope) { e.Resource.Id = "" },
		"no resource":     func(e *controlv1.ActionEnvelope) { e.Resource = nil },
		"no principal id": func(e *controlv1.ActionEnvelope) { e.Principal.Id = "" },
		"no action name":  func(e *controlv1.ActionEnvelope) { e.Action.Name = "" },
		"no request id":   func(e *controlv1.ActionEnvelope) { e.RequestId = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := newPDP(t, answer(`{"decision":true}`))
			env := envelope()
			mutate(env)
			if got := client(t, options(p)).Ask(within(t, 10*time.Second), env); got != core.ExternalUnavailable() {
				t.Errorf("Ask = %v, want unavailable", got)
			}
			if p.hits() != 0 {
				t.Errorf("the decision point was asked %d times", p.hits())
			}
		})
	}
	t.Run("no envelope", func(t *testing.T) {
		p := newPDP(t, answer(`{"decision":true}`))
		if got := client(t, options(p)).Ask(within(t, 10*time.Second), nil); got != core.ExternalUnavailable() {
			t.Errorf("Ask(nil) = %v, want unavailable", got)
		}
	})
	t.Run("a client nobody built", func(t *testing.T) {
		var zero Client
		if got := zero.Ask(within(t, 10*time.Second), envelope()); got != core.ExternalUnavailable() {
			t.Errorf("the zero client = %v, want unavailable", got)
		}
		var none *Client
		if got := none.Ask(within(t, 10*time.Second), envelope()); got != core.ExternalUnavailable() {
			t.Errorf("a nil client = %v, want unavailable", got)
		}
	})
}

const canary = "zq-canary"

// canaried sets every field mapping version 1 never sends to a value holding
// the canary.
func canaried() *controlv1.ActionEnvelope {
	env := envelope()
	env.SchemaVersion = canary + "-schema"
	env.TraceId = canary + "-trace"
	env.SpanId = canary + "-span"
	env.OccurredAt = timestamppb.New(time.Date(2031, 7, 9, 1, 2, 3, 0, time.UTC))
	env.Agent.InstanceId = canary + "-instance"
	env.Agent.Version = canary + "-version"
	env.Agent.ModelRef = canary + "-model"
	env.Delegation = []*controlv1.Delegation{{
		From: canary + "-from", To: canary + "-to", Scopes: []string{canary + "-scope"}, Reason: canary + "-reason",
		IssuedAt:  timestamppb.New(time.Date(2031, 7, 9, 1, 2, 3, 0, time.UTC)),
		ExpiresAt: timestamppb.New(time.Date(2031, 7, 9, 2, 2, 3, 0, time.UTC)),
	}}
	env.Data.Sources = []string{canary + "-source"}
	env.Arguments = &controlv1.Arguments{
		CanonicalHash:    "sha256:" + strings.Repeat("c", 64),
		RedactedPreview:  canary + "-preview",
		SchemaRef:        canary + "-schema-ref",
		RedactionProfile: canary + "-profile",
	}
	env.Context = &controlv1.RunContext{
		SessionId: canary + "-session", RunId: canary + "-run", StepId: canary + "-step", Risk: canary + "-risk",
		Budgets: map[string]int64{canary + "-budget": 1_234_567_891}, Tags: []string{canary + "-tag"},
	}
	return env
}

// TestNeverSent: no field outside mapping version 1 reaches the request
// bytes, headers included.
func TestNeverSent(t *testing.T) {
	p := newPDP(t, answer(`{"decision":true}`))
	if got := client(t, options(p)).Ask(within(t, 10*time.Second), canaried()); got != core.ExternalAllowed() {
		t.Fatalf("Ask = %v, want allowed", got)
	}
	var wire bytes.Buffer
	wire.Write(p.lastBody(t))
	if err := p.lastRequest(t).Header.Write(&wire); err != nil {
		t.Fatalf("writing the headers: %v", err)
	}
	for _, needle := range []string{canary, strings.Repeat("c", 64), "2031", "1941325323", "1941328923", "1234567891"} {
		if bytes.Contains(wire.Bytes(), []byte(needle)) {
			t.Errorf("the request carries %q:\n%s", needle, wire.Bytes())
		}
	}
	if !bytes.Contains(wire.Bytes(), []byte("inv-42")) {
		t.Error("the request does not carry the resource id, so the search above examined nothing")
	}
}

// TestProposedAndAuthorizedAskTheSame: the envelope a rewrite authorizes
// differs from the proposed one only in its arguments, and asks the same
// question byte for byte.
func TestProposedAndAuthorizedAskTheSame(t *testing.T) {
	proposed := envelope()
	authorized := proto.Clone(proposed).(*controlv1.ActionEnvelope)
	authorized.Arguments.CanonicalHash = "sha256:" + strings.Repeat("0", 64)
	authorized.Arguments.RedactedPreview = `{"month":"04"}`
	a, okA := mapV1(proposed)
	b, okB := mapV1(authorized)
	if !okA || !okB || !bytes.Equal(a, b) {
		t.Errorf("the two questions differ:\n%s\n%s", a, b)
	}
	if proto.Equal(proposed, authorized) {
		t.Error("the two envelopes are equal, so the comparison above examined nothing")
	}
}
