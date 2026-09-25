package mcp_test

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/adapters/mcp"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/gateway"
)

func obligation(typ string, params map[string]string) *controlv1.Obligation {
	return &controlv1.Obligation{Type: typ, Params: params}
}

// assertRefused checks that the call was answered as a block naming the
// obligation and that the victim never ran the tool.
func assertRefused(t *testing.T, r *rig, res *sdk.CallToolResult, err error, tool string) {
	t.Helper()
	if err != nil || !res.IsError {
		t.Fatalf("%s under the obligation: %v %+v", tool, err, res)
	}
	if codes := codesOf(t, res); !slices.Equal(codes, []string{"OBLIGATIONS_ATTACHED", "OBLIGATION_NOT_UNDERSTOOD"}) {
		t.Errorf("codes %v", codes)
	}
	if n := r.victim.count(tool); n != 0 {
		t.Errorf("victim ran %s %d times", tool, n)
	}
	aborts := r.pipe.aborted()
	if len(aborts) == 0 || aborts[len(aborts)-1].cause != gateway.AbortObligation {
		t.Errorf("the refusal was not aborted as an obligation: %+v", aborts)
	}
}

// TestReadOnlyObligation: read_only lets a read through and refuses a call
// whose manifest effect is not a read.
func TestReadOnlyObligation(t *testing.T) {
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{})
	r.pipe.decide = executeWith(obligation("read_only", nil))
	agent := r.connect(t, "agent-a")
	if res, err := callTool(t, agent, "read_file", map[string]any{"path": "/x"}); err != nil || res.IsError {
		t.Fatalf("read under read_only: %v %+v", err, res)
	}
	res, err := callTool(t, agent, "delete_file", map[string]any{"path": "/x"})
	assertRefused(t, r, res, err, "delete_file")
	if r.victim.count("read_file") != 1 {
		t.Errorf("read ran %d times", r.victim.count("read_file"))
	}
	// Advisory: the same obligation marked advisory does not stop the call.
	r.pipe.decide = executeWith(&controlv1.Obligation{Type: "read_only", Advisory: true})
	if res, err := callTool(t, agent, "delete_file", map[string]any{"path": "/x"}); err != nil || res.IsError {
		t.Fatalf("advisory read_only stopped the call: %v %+v", err, res)
	}
}

// TestRestrictResourcesObligation: the resource named by the call has to
// be one of the ids or under the prefix; no parameter allows nothing, and
// the prefix is literal.
func TestRestrictResourcesObligation(t *testing.T) {
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{})
	agent := r.connect(t, "agent-a")
	cases := []struct {
		name   string
		params map[string]string
		path   string
		allow  bool
	}{
		{"exact id", map[string]string{"ids": "/a,/b"}, "/b", true},
		{"id outside", map[string]string{"ids": "/a,/b"}, "/c", false},
		{"prefix", map[string]string{"prefix": "/srv/data/"}, "/srv/data/x", true},
		{"prefix is literal", map[string]string{"prefix": "/srv/data/"}, "/srv/data-evil/x", false},
		{"no parameter", nil, "/a", false},
		{"empty ids", map[string]string{"ids": ""}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r.pipe.decide = executeWith(obligation("restrict_resources", tc.params))
			before := r.victim.count("read_file")
			res, err := callTool(t, agent, "read_file", map[string]any{"path": tc.path})
			if err != nil {
				t.Fatal(err)
			}
			if tc.allow && (res.IsError || r.victim.count("read_file") != before+1) {
				t.Fatalf("allowed resource refused: %+v", res)
			}
			if !tc.allow && (!res.IsError || r.victim.count("read_file") != before) {
				t.Fatalf("resource outside the set sent: %+v", res)
			}
		})
	}
}

// TestShortenTimeoutObligation: the upstream call gets the obligation's
// deadline; a tool that outlives it is cut off and closed as TIMEOUT. A
// missing or non-positive ms refuses the call.
func TestShortenTimeoutObligation(t *testing.T) {
	forEachKind(t, rigOptions{}, func(t *testing.T, r *rig) {
		r.pipe.decide = executeWith(obligation("shorten_timeout", map[string]string{"ms": "100"}))
		agent := r.connect(t, "agent-a")
		start := time.Now()
		_, err := callTool(t, agent, "slow", map[string]any{"path": "/x"})
		elapsed := time.Since(start)
		if err == nil {
			t.Fatalf("the slow tool completed under a 100ms obligation")
		}
		if elapsed >= 2*time.Second {
			t.Fatalf("the call took %v; the deadline did not apply", elapsed)
		}
		closes := r.pipe.closed()
		if len(closes) != 1 || closes[0].result.GetStatus() != controlv1.ResultStatus_RESULT_STATUS_TIMEOUT {
			t.Fatalf("Close calls %+v", closes)
		}
		for _, params := range []map[string]string{nil, {"ms": "0"}, {"ms": "-5"}, {"ms": "soon"}} {
			r.pipe.decide = executeWith(obligation("shorten_timeout", params))
			res, err := callTool(t, agent, "read_file", map[string]any{"path": "/x"})
			assertRefused(t, r, res, err, "read_file")
		}
	})
}

// TestDenyExternalSinkObligation: a tool whose destination is not trusted
// is refused; one classified as trusted internal proceeds; one with no
// destination at all is refused, because nobody vouched for it.
func TestDenyExternalSinkObligation(t *testing.T) {
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{overrides: func(v *victim, t *testing.T) []mcp.Override {
		o := v.overrides(t)
		o = append(o, mcp.Override{Upstream: "victim", Tool: "unlisted", Fingerprint: v.fingerprint(t, "unlisted"), Effect: effectComm, ResourceType: "peer", ResourceFrom: "/path", TrustZone: zoneInternal})
		return o
	}})
	r.pipe.decide = executeWith(obligation("deny_external_sink", nil))
	agent := r.connect(t, "agent-a")
	res, err := callTool(t, agent, "send_mail", map[string]any{"to": "x@example.com"})
	assertRefused(t, r, res, err, "send_mail")
	res, err = callTool(t, agent, "read_file", map[string]any{"path": "/x"})
	assertRefused(t, r, res, err, "read_file")
	if res, err := callTool(t, agent, "unlisted", map[string]any{"path": "/x"}); err != nil || res.IsError {
		t.Fatalf("trusted sink refused: %v %+v", err, res)
	}
}

// TestUndeclaredObligationIsRefused: an obligation outside the declared set
// stops the call unless advisory, whatever the pipeline let through.
func TestUndeclaredObligationIsRefused(t *testing.T) {
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{})
	agent := r.connect(t, "agent-a")
	r.pipe.decide = executeWith(obligation("cap_amount", map[string]string{"max": "1"}))
	res, err := callTool(t, agent, "read_file", map[string]any{"path": "/x"})
	assertRefused(t, r, res, err, "read_file")
	r.pipe.decide = executeWith(&controlv1.Obligation{Type: "emit_alert", Advisory: true})
	if res, err := callTool(t, agent, "read_file", map[string]any{"path": "/x"}); err != nil || res.IsError {
		t.Fatalf("advisory obligation stopped the call: %v %+v", err, res)
	}
}

// bearer authenticates "<user>-token" as that user for alice and bob, and
// nothing else.
func bearer() func(http.Handler) http.Handler {
	return auth.RequireBearerToken(func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		user, ok := strings.CutSuffix(token, "-token")
		if !ok || (user != "alice" && user != "bob") {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: user, Expiration: time.Now().Add(time.Hour)}, nil
	}, nil)
}

// TestAuthenticatedListenerBindsTheUser: Authenticates and BindEndUser. The
// principal is the user the token names, with the listener's strength; a
// request with no token never reaches the pipeline.
func TestAuthenticatedListenerBindsTheUser(t *testing.T) {
	for _, k := range kinds[:2] {
		t.Run(k.name, func(t *testing.T) {
			r := newRig(t, k.kind, rigOptions{auth: bearer()})
			caps := r.adapter.Capabilities()
			if !caps.Authenticates || !caps.BindEndUser {
				t.Fatalf("capabilities %+v", caps)
			}
			if _, err := callTool(t, r.connect(t, "agent-a"), "read_file", map[string]any{"path": "/x"}); err != nil {
				t.Fatal(err)
			}
			bob := connectHTTP(t, r.url, "agent-a", r.version, http.Header{"Authorization": {"Bearer bob-token"}})
			if _, err := callTool(t, bob, "read_file", map[string]any{"path": "/x"}); err != nil {
				t.Fatal(err)
			}
			assertPrincipals(t, r, "alice", "bob")
			c := sdk.NewClient(&sdk.Implementation{Name: "anon", Version: "0"}, nil)
			_, err := c.Connect(ctxT(t), &sdk.StreamableClientTransport{Endpoint: r.url}, &sdk.ClientSessionOptions{ProtocolVersion: r.version})
			if err == nil {
				t.Fatal("a client with no token connected")
			}
			if len(r.pipe.admitted()) != 2 || r.victim.count("read_file") != 2 {
				t.Errorf("the unauthenticated request reached something: %d admissions", len(r.pipe.admitted()))
			}
		})
	}
}

func assertPrincipals(t *testing.T, r *rig, users ...string) {
	t.Helper()
	adm := r.pipe.admitted()
	if len(adm) != len(users) {
		t.Fatalf("admissions %d, want %d", len(adm), len(users))
	}
	for i, want := range users {
		p := adm[i].a.Envelope.GetPrincipal()
		if p.GetId() != want || p.GetAuthnStrength() != "bearer" || p.GetTenantId() != "t1" {
			t.Errorf("principal %v, want %s", p, want)
		}
	}
}

// TestAuthenticatorWithoutUserBlocks: an authenticator that lets a request
// through with no user identity binds nothing; the call is refused before
// the policy sees a principal.
func TestAuthenticatorWithoutUserBlocks(t *testing.T) {
	passThrough := func(next http.Handler) http.Handler { return next }
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{auth: passThrough})
	res, err := callTool(t, r.connect(t, "agent-a"), "read_file", map[string]any{"path": "/x"})
	if err != nil || !res.IsError {
		t.Fatalf("no user, yet: %v %+v", err, res)
	}
	adm := r.pipe.admitted()
	if len(adm) != 1 || !errors.Is(adm[0].a.Refusal, mcp.ErrNoEndUser) || adm[0].a.Envelope != nil {
		t.Fatalf("admission %+v", adm)
	}
	if n := r.victim.count("read_file"); n != 0 {
		t.Fatalf("victim ran %d times", n)
	}
}

// TestUnauthenticatedListenerDeclaresNoBinding: without an authenticator
// the adapter declares neither capability and the pipeline refuses to
// bind an end user on it.
func TestUnauthenticatedListenerDeclaresNoBinding(t *testing.T) {
	r := newRig(t, mcp.KindStatelessHTTP, rigOptions{})
	caps := r.adapter.Capabilities()
	if caps.Authenticates || caps.BindEndUser {
		t.Fatalf("capabilities %+v", caps)
	}
	if !caps.ObserveRequest || !caps.ObserveResult || !caps.Block || !caps.SeeResourceIDs || caps.SeeDelegation {
		t.Fatalf("capabilities %+v", caps)
	}
	if !slices.Equal(caps.Obligations, []string{"read_only", "restrict_resources", "shorten_timeout", "deny_external_sink"}) {
		t.Fatalf("obligations %v", caps.Obligations)
	}
}
