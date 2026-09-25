package console

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/approvals"
	"github.com/guardana/control/internal/core/approval"
)

// Fixtures for the page. Every function here builds an input; no expected
// value in any test is read back from one of them.

const testBundle approval.BundleDigest = "sha256:3333333333333333333333333333333333333333333333333333333333333333"

const testApprover = "page-tester"

var testArgs = []byte(`{"amount_minor":700}`)

func testEnvelope(requestID string) *controlv1.ActionEnvelope {
	return &controlv1.ActionEnvelope{
		SchemaVersion: "1.0",
		RequestId:     requestID,
		ProjectId:     "proj-1",
		TenantId:      "tenant-1",
		Environment:   "prod",
		Principal:     &controlv1.Principal{Id: "user-1", Type: "user", TenantId: "tenant-1"},
		Agent:         &controlv1.Agent{Id: "agent-1", Framework: "cli"},
		Action: &controlv1.Action{
			Kind: "tool.call", Name: "refund", Protocol: "mcp",
			Effect: controlv1.EffectClass_EFFECT_CLASS_TRANSACT, Provider: "payments",
		},
		Resource: &controlv1.Resource{Type: "payment", Id: "pay-1", TenantId: "tenant-1", Environment: "prod"},
	}
}

// newPlane opens a plane over a fresh directory and holds its lock until the
// test ends, so every answer the page files is one a plane would consume.
func newPlane(t *testing.T) (string, *approvals.Plane) {
	t.Helper()
	return newPlaneNamed(t, "approvals")
}

// newPlaneNamed is newPlane over a directory called name.
func newPlaneNamed(t *testing.T, name string) (string, *approvals.Plane) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p, err := approvals.OpenPlane(dir)
	if err != nil {
		t.Fatalf("opening a plane over %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return dir, p
}

// hold records one pending request under approvalID, held now and expiring
// in a quarter of an hour.
func hold(t *testing.T, p *approvals.Plane, approvalID string) {
	t.Helper()
	holdAt(t, p, approvalID, time.Now().UTC().Truncate(time.Second))
}

// holdAt records one pending request under approvalID, held at now and
// expiring a quarter of an hour later. Each request id names its own
// resource, so each hold has an action digest of its own.
func holdAt(t *testing.T, p *approvals.Plane, approvalID string, now time.Time) {
	t.Helper()
	holdRequest(t, p, approvalID, "req-"+strings.ToLower(approvalID), now)
}

// holdRequest is holdAt with the request id given.
func holdRequest(t *testing.T, p *approvals.Plane, approvalID, requestID string, now time.Time) {
	t.Helper()
	env := testEnvelope(requestID)
	env.Resource.Id = "pay-" + strings.ToLower(approvalID)
	digest, binding, err := approval.Bind(env, testArgs, testBundle)
	if err != nil {
		t.Fatalf("binding the fixture: %v", err)
	}
	h := approvals.Hold{
		Approval: &controlv1.Approval{
			SchemaVersion:      "1.0",
			ApprovalId:         approvalID,
			RequestId:          requestID,
			ActionDigest:       string(digest),
			PolicyBundleDigest: string(testBundle),
			State:              controlv1.ApprovalState_APPROVAL_STATE_PENDING,
			RequestedAt:        timestamppb.New(now),
			ExpiresAt:          timestamppb.New(now.Add(15 * time.Minute)),
		},
		Binding:  binding,
		Envelope: env,
		Decision: &controlv1.Decision{
			SchemaVersion: "1.0", DecisionId: "dec-" + requestID, RequestId: requestID,
			PolicyRuleIds: []string{"rule-a"}, Verdict: controlv1.Verdict_VERDICT_REQUIRE_APPROVAL,
		},
	}
	if err := p.Hold(context.Background(), h, now); err != nil {
		t.Fatalf("holding %s: %v", approvalID, err)
	}
}

// site is one running page and what a caller of it needs: the token it was
// started with, and the session token that one was traded for.
type site struct {
	addr, printed, token string
	store                *approvals.Approver
}

// serve starts a page over dir, and over pauseFile when it is not empty, on a
// loopback port of its own, and trades its printed token as the page's
// script does.
func serve(t *testing.T, dir, pauseFile string) *site {
	t.Helper()
	s := serveAt(t, dir, pauseFile, nil)
	s.token = s.trade(t)
	return s
}

// serveAt starts a page on the clock now, or the system's for nil, and trades
// nothing.
func serveAt(t *testing.T, dir, pauseFile string, now func() time.Time) *site {
	t.Helper()
	return serveAs(t, dir, pauseFile, testApprover, now)
}

// serveAs is serveAt under the approver id approver.
func serveAs(t *testing.T, dir, pauseFile, approver string, now func() time.Time) *site {
	t.Helper()
	store, err := approvals.OpenApprover(dir)
	if err != nil {
		t.Fatalf("opening %s for an approver: %v", dir, err)
	}
	t.Cleanup(func() { _ = store.Close() })
	srv := httptest.NewUnstartedServer(nil)
	s := &site{addr: srv.Listener.Addr().String(), printed: NewToken(), store: store}
	h, err := New(Options{Approvals: store, Directory: dir, PauseFile: pauseFile, ApproverID: approver, Host: s.addr, Token: s.printed, Now: now})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.Config.Handler = h
	srv.Start()
	t.Cleanup(srv.Close)
	return s
}

// tradeCall is the trade of the printed token as the page's script sends it.
func (s *site) tradeCall() call {
	return s.write(SessionPath, "{}").with(TokenHeader, s.printed)
}

// trade trades the printed token and returns the session token, failing the
// test when the page refuses.
func (s *site) trade(t *testing.T) string {
	t.Helper()
	a := s.do(t, s.tradeCall())
	var got struct{ Session string }
	decode(t, a, &got)
	if a.status != http.StatusOK || len(got.Session) != 43 || got.Session == s.printed {
		t.Fatalf("the trade answered %d %q", a.status, a.body)
	}
	return got.Session
}

// digestOf reads one record's action digest straight from the store.
func (s *site) digestOf(t *testing.T, approvalID string) string {
	t.Helper()
	l, err := s.store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entries {
		if e.Record.ApprovalID == approvalID {
			return e.Record.Approval.GetActionDigest()
		}
	}
	t.Fatalf("the store holds no record %s", approvalID)
	return ""
}

// answering is the body of an answer to approvalID carrying the digest the
// store holds for it, as the page's script sends it after a confirmation.
func (s *site) answering(t *testing.T, approvalID, reason string) string {
	t.Helper()
	raw, err := json.Marshal(struct {
		ID           string `json:"id"`
		Reason       string `json:"reason"`
		ActionDigest string `json:"action_digest"`
	}{approvalID, reason, s.digestOf(t, approvalID)})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// call is one request as it goes on the wire. The zero headers map sends
// only Host.
type call struct {
	method, path, host string
	headers            [][2]string
	body               string
}

// write is a well-formed write to path, as the page's own script sends it
// from the page's own origin.
func (s *site) write(path, body string) call {
	return call{
		method: http.MethodPost, path: path, host: s.addr, body: body,
		headers: [][2]string{
			{TokenHeader, s.token},
			{"Content-Type", "application/json"},
			{"Origin", "http://" + s.addr},
			{"Sec-Fetch-Site", "same-origin"},
		},
	}
}

// readState is a well-formed read of the page's state.
func (s *site) readState() call {
	return call{method: http.MethodGet, path: "/api/state", host: s.addr, headers: [][2]string{{TokenHeader, s.token}, {"Sec-Fetch-Site", "same-origin"}}}
}

// without returns c with every header named name removed.
func (c call) without(name string) call {
	var kept [][2]string
	for _, h := range c.headers {
		if !strings.EqualFold(h[0], name) {
			kept = append(kept, h)
		}
	}
	c.headers = kept
	return c
}

// with returns c with name set to value, in place of any it carried.
func (c call) with(name, value string) call {
	c = c.without(name)
	c.headers = append(c.headers, [2]string{name, value})
	return c
}

type answer struct {
	status int
	header http.Header
	body   string
}

// do sends c exactly as written, the path unaltered, and fails the test when
// the answer lacks any header every answer carries.
func (s *site) do(t *testing.T, c call) answer {
	t.Helper()
	conn, err := net.Dial("tcp", s.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n", c.method, c.path, c.host)
	for _, h := range c.headers {
		fmt.Fprintf(&b, "%s: %s\r\n", h[0], h[1])
	}
	if c.body != "" || c.method == http.MethodPost {
		fmt.Fprintf(&b, "Content-Length: %s\r\n", strconv.Itoa(len(c.body)))
	}
	b.WriteString("\r\n" + c.body)
	if _, err := io.WriteString(conn, b.String()); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("%s %s: %v", c.method, c.path, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	a := answer{status: res.StatusCode, header: res.Header, body: string(body)}
	carriesEveryHeader(t, c, a)
	return a
}

// carriesEveryHeader holds one answer to the headers every answer carries,
// spelled out here rather than read from the package.
func carriesEveryHeader(t *testing.T, c call, a answer) {
	t.Helper()
	csp := a.header.Get("Content-Security-Policy")
	for _, directive := range []string{
		"default-src 'none'", "script-src 'self'", "style-src 'self'", "connect-src 'self'",
		"frame-ancestors 'none'", "base-uri 'none'", "form-action 'none'", "require-trusted-types-for 'script'",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("%s %s (%d): the policy %q lacks %q", c.method, c.path, a.status, csp, directive)
		}
	}
	for name, want := range map[string]string{
		"X-Frame-Options":            "DENY",
		"Cache-Control":              "no-store",
		"Referrer-Policy":            "no-referrer",
		"X-Content-Type-Options":     "nosniff",
		"Cross-Origin-Opener-Policy": "same-origin",
	} {
		if got := a.header.Get(name); got != want {
			t.Errorf("%s %s (%d): %s is %q, want %q", c.method, c.path, a.status, name, got, want)
		}
	}
}

// stateOf reads one record's state straight from the store, not through the
// page.
func (s *site) stateOf(t *testing.T, approvalID string) controlv1.ApprovalState {
	t.Helper()
	l, err := s.store.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range l.Entries {
		if e.Record.ApprovalID == approvalID {
			return e.Record.Approval.GetState()
		}
	}
	t.Fatalf("the store holds no record %s", approvalID)
	return 0
}

// decode reads an answer's body as JSON into v.
func decode(t *testing.T, a answer, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(a.body), v); err != nil {
		t.Fatalf("the answer %q is not JSON: %v", a.body, err)
	}
}
