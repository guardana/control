package e2e_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/guardana/control/adapters/authzen"
	"github.com/guardana/control/adapters/mcp"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// The rules that let the decision point veto what the bundle allows.
const (
	vetoReads     = `{"id":"veto-reads","effect":"DENY","when":{"action":{"effect":["READ"]},"external":{"denies":true}}}`
	vetoDeletes   = `{"id":"veto-deletes","effect":"DENY","when":{"action":{"effect":["DELETE"]},"external":{"denies":true}}}`
	vetoTransfers = `{"id":"veto-transfers","effect":"DENY","when":{"action":{"effect":["TRANSACT"]},"external":{"denies":true}}}`
)

// The codes a consulted answer leaves on a decision, as the registry spells
// them.
const (
	codePDPAllow         = "PDP_ALLOW"
	codePDPDeny          = "PDP_DENY"
	codePDPTimeout       = "PDP_TIMEOUT"
	codePDPUnavailable   = "PDP_UNAVAILABLE"
	codePDPAnswerRefused = "PDP_ANSWER_REFUSED"
	codeObligationsUnmet = "OBLIGATION_NOT_UNDERSTOOD"
	codePolicyStale      = "POLICY_STALE"
	codeFailOpenRead     = "FAIL_OPEN_READ_CONFIGURED"
)

// staleBy is past both staleness budgets the planes run under, ten minutes,
// by a margin no scheduling delay closes.
const staleBy = 11 * time.Minute

// askTimeout bounds one ask, in the pipeline and in the client.
const askTimeout = 200 * time.Millisecond

// What the AuthZEN double answers. answerObligations is a true that holds an
// obligation, which the plane cannot fulfil.
const (
	answerAllow       = "allow"
	answerDeny        = "deny"
	answerObligations = "obligations"
	answerSilent      = "silent"
	answer500         = "500"
	answerUnechoed    = "unechoed"
)

// authzenDouble is an AuthZEN 1.0 decision point on the loopback, answering
// every question at the evaluation endpoint the way it is told to. It
// records the X-Request-ID of each question.
type authzenDouble struct {
	url string

	mu     sync.Mutex
	answer string
	asked  []string
}

func newAuthZENDouble(t *testing.T, answer string) *authzenDouble {
	t.Helper()
	d := &authzenDouble{answer: answer}
	ts := httptest.NewServer(d)
	t.Cleanup(ts.Close)
	d.url = ts.URL
	return d
}

func (d *authzenDouble) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/access/v1/evaluation" {
		http.NotFound(w, r)
		return
	}
	// Reading the question through lets the server see the asker give up.
	_, _ = io.Copy(io.Discard, r.Body)
	id := r.Header.Get("X-Request-ID")
	d.mu.Lock()
	d.asked = append(d.asked, id)
	answer := d.answer
	d.mu.Unlock()
	switch answer {
	case answerSilent:
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		return
	case answer500:
		http.Error(w, "the decision point failed", http.StatusInternalServerError)
		return
	case answerUnechoed:
		id = ""
	}
	w.Header().Set("Content-Type", "application/json")
	if id != "" {
		w.Header().Set("X-Request-ID", id)
	}
	body := `{"decision":false}`
	switch answer {
	case answerAllow, answerUnechoed:
		body = `{"decision":true}`
	case answerObligations:
		body = `{"decision":true,"context":{"obligations":[{"id":"notify"}]}}`
	}
	_, _ = io.WriteString(w, body)
}

func (d *authzenDouble) set(answer string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.answer = answer
}

func (d *authzenDouble) questions() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.asked)
}

// client is the AuthZEN adapter pointed at the double.
func (d *authzenDouble) client(t *testing.T) *authzen.Client {
	t.Helper()
	c, err := authzen.New(authzen.Options{Identifier: d.url, AllowPlaintext: true, Timeout: askTimeout, InFlight: 4})
	if err != nil {
		t.Fatalf("authzen.New refused a double this test builds as valid: %v", err)
	}
	return c
}

// expectConsulted asserts the recorded decision carries code and names the
// double in pdp_instance.
func expectConsulted(t *testing.T, decision *controlv1.Decision, code, identifier string) {
	t.Helper()
	if !slices.Contains(decision.GetReasonCodes(), code) {
		t.Errorf("the recorded decision says %v, want %s among them", decision.GetReasonCodes(), code)
	}
	if decision.GetPdpInstance() != identifier {
		t.Errorf("pdp_instance = %q, want %q", decision.GetPdpInstance(), identifier)
	}
}

// TestTheDecisionPointsAnswerPerEffectClass drives a read and a material call
// through the real AuthZEN adapter: an allow runs the call once, and a
// denial, a timeout, a 500 and an answer the adapter will not read each block
// it, a read under fail_open_read included, on a trail that validates and
// names the decision point. A read under a stale bundle runs on an allow only
// because fail_open_read is on, and the decision says so.
func TestTheDecisionPointsAnswerPerEffectClass(t *testing.T) {
	calls := []struct {
		name         string
		tool         string
		rules        []string
		failOpenRead bool
		policyAge    time.Duration
	}{
		{"read", toolRead, []string{allowReads, vetoReads}, true, 0},
		{"stale read", toolRead, []string{allowReads, vetoReads}, true, staleBy},
		{"delete", toolDelete, []string{allowDeletes, vetoDeletes}, false, 0},
	}
	answers := []struct {
		answer string
		code   string
		runs   bool
	}{
		{answerAllow, codePDPAllow, true},
		{answerDeny, codePDPDeny, false},
		{answerSilent, codePDPTimeout, false},
		{answer500, codePDPUnavailable, false},
		{answerUnechoed, codePDPAnswerRefused, false},
	}
	for _, c := range calls {
		for _, a := range answers {
			t.Run(c.name+"/"+a.answer, func(t *testing.T) {
				dp := newAuthZENDouble(t, a.answer)
				p := newPlane(t, options{kind: mcp.KindStatelessHTTP, mode: modeEnforce, rules: c.rules,
					policyAge: c.policyAge, decisionPoint: dp.client(t), failOpenRead: c.failOpenRead})
				agent := p.connect(t, "agent-a")
				res := call(t, agent, c.tool, map[string]any{"path": "/x"})
				trail := p.oneTrail(t)
				if a.runs {
					if res.IsError {
						t.Fatalf("an allowed call came back as a refusal: %+v", res.StructuredContent)
					}
					expectTrail(t, trail, kindProposed, kindDecided, kindStarted, kindCompleted)
				} else {
					blockedWith(t, res, a.code)
					expectTrail(t, trail, kindProposed, kindDecided, kindBlocked)
				}
				if n, want := p.victim.count(c.tool), map[bool]int{true: 1, false: 0}[a.runs]; n != want {
					t.Errorf("the upstream ran %d time(s), want %d", n, want)
				}
				decision := eventOf(t, trail, kindDecided).GetDecision()
				expectConsulted(t, decision, a.code, dp.url)
				codes := decision.GetReasonCodes()
				if stale := slices.Contains(codes, codePolicyStale); stale != (c.policyAge > 0) {
					t.Errorf("the recorded decision says %v; %s among them is %t, want %t", codes, codePolicyStale, stale, c.policyAge > 0)
				}
				if opened, want := slices.Contains(codes, codeFailOpenRead), c.policyAge > 0 && a.runs; opened != want {
					t.Errorf("the recorded decision says %v; %s among them is %t, want %t", codes, codeFailOpenRead, opened, want)
				}
				if asked := dp.questions(); !slices.Equal(asked, []string{trail[0].GetRequestId()}) {
					t.Errorf("the decision point was asked about %q, want the trail's request %s", asked, trail[0].GetRequestId())
				}
			})
		}
	}
}

// TestAHeldRequestTheDecisionPointNowDeniesDoesNotRun: the decision point
// allowed the call that was held, the approver approved it, and the retry is
// asked afresh; a denial then blocks it, the upstream never runs, and the
// held trail is left as it was.
func TestAHeldRequestTheDecisionPointNowDeniesDoesNotRun(t *testing.T) {
	dp := newAuthZENDouble(t, answerAllow)
	p := newPlane(t, options{kind: mcp.KindStatelessHTTP, mode: modeEnforce,
		rules: []string{approveTransfers, vetoTransfers}, decisionPoint: dp.client(t)})
	agent := p.connect(t, "agent-a")
	args := map[string]any{"account": "a1", "amount": 250}
	requestID, approvalID, _ := expectHeld(t, p, agent, args)
	if err := p.approvals.Answer(approvalID, controlv1.ApprovalState_APPROVAL_STATE_APPROVED,
		"approver-1", "ok", time.Now()); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	asked := len(dp.questions())

	dp.set(answerDeny)
	blockedWith(t, call(t, agent, toolTransfer, args), codePDPDeny)
	if n := p.victim.count(toolTransfer); n != 0 {
		t.Fatalf("the upstream ran %d time(s) on a retry the decision point denied", n)
	}
	if n := len(dp.questions()); n != asked+1 {
		t.Errorf("the retry was asked about %d time(s), want once afresh", n-asked)
	}
	order, byRequest := p.trails()
	if len(order) != 2 || order[0] != requestID {
		t.Fatalf("trails %v, want the held request's and the denied retry's", order)
	}
	expectTrail(t, byRequest[requestID], kindProposed, kindDecided, kindRequested)
	denied := byRequest[order[1]]
	expectTrail(t, denied, kindProposed, kindDecided, kindBlocked)
	expectConsulted(t, eventOf(t, denied, kindDecided).GetDecision(), codePDPDeny, dp.url)
	if s := p.pipeline.Stats(); s.Executed != 0 || s.Asks.Denied != 1 {
		t.Errorf("stats %+v", s)
	}
}

// TestAListingAsksNothing: shaping a listing previews every tool and asks the
// decision point about none of them, so the tool its veto governs stays
// listed and the decision point hears nothing. The tool no rule allows is
// hidden, which shows the listing was shaped.
func TestAListingAsksNothing(t *testing.T) {
	dp := newAuthZENDouble(t, answerDeny)
	p := newPlane(t, options{kind: mcp.KindStatelessHTTP, mode: modeEnforce, shaping: mcp.ShapeHide,
		rules: []string{allowReads, vetoReads}, decisionPoint: dp.client(t)})
	agent := p.connect(t, "agent-a")
	list, err := agent.ListTools(ctxT(t), nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var names []string
	for _, tool := range list.Tools {
		names = append(names, tool.Name)
	}
	if !slices.Contains(names, toolRead) {
		t.Errorf("the listing %v leaves out %s, which only a call's ask can veto", names, toolRead)
	}
	if slices.Contains(names, toolMail) {
		t.Errorf("the listing %v shows %s, which no rule allows; the listing was not shaped", names, toolMail)
	}
	if asked := dp.questions(); len(asked) != 0 {
		t.Errorf("the listing asked the decision point %d time(s)", len(asked))
	}
	if made := p.pipeline.Stats().Asks.Made; made != 0 {
		t.Errorf("the pipeline counted %d ask(s) for a listing", made)
	}
}
