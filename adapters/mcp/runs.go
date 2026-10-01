package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/guardana/control/internal/gateway"
)

// RunTokenHeader is the header an HTTP request presents its run token in
// (ADR-0034). Nothing else a request carries selects a run.
const RunTokenHeader = "Run-Token"

// runRefusedBody and runRefusedMessage are the one answer to a refused run
// token whatever the cause, so the agent learns nothing about which runs
// exist; the cause goes to the counters only.
const (
	runRefusedBody    = "run token refused\n"
	runRefusedMessage = "run token refused"
)

// RunRefusals counts refused run tokens by cause. Every cause of
// gateway.RunCauses is a key, at zero when nothing was refused for it; no key
// ever names a run.
type RunRefusals map[gateway.RunCause]uint64

// checkRunsSource holds the token source to the listener kind: the header on
// HTTP and RunToken on stdio, so neither can stand in for the other.
func checkRunsSource(l Listener) error {
	switch {
	case l.RunToken != "" && l.Runs == nil:
		return fmt.Errorf("%w: a run token without a resolver", ErrRunsSource)
	case l.RunToken != "" && l.Kind != KindStdio:
		return fmt.Errorf("%w: an HTTP listener reads the token from the %s header only", ErrRunsSource, RunTokenHeader)
	case l.Runs != nil && l.Kind == KindStdio && l.RunToken == "":
		return fmt.Errorf("%w: a stdio listener with a resolver needs the token", ErrRunsSource)
	}
	return nil
}

// runCounter counts refusals by the position of their cause in
// gateway.RunCauses.
type runCounter struct {
	causes []gateway.RunCause
	n      []atomic.Uint64
}

func newRunCounter() *runCounter {
	causes := gateway.RunCauses()
	return &runCounter{causes: causes, n: make([]atomic.Uint64, len(causes))}
}

// count adds one refusal; a cause outside the set, which resolve never
// returns, is counted as unreadable rather than panicking a handler.
func (c *runCounter) count(cause gateway.RunCause) {
	i := slices.Index(c.causes, cause)
	if i < 0 {
		i = slices.Index(c.causes, gateway.RunUnreadable)
	}
	c.n[i].Add(1)
}

func (c *runCounter) snapshot() RunRefusals {
	out := make(RunRefusals, len(c.causes))
	for i, cause := range c.causes {
		out[cause] = c.n[i].Load()
	}
	return out
}

// runsCheck refuses a request whose run token does not resolve before the
// library reads it, on every verb: 401 with the challenge and one body. It
// sits inside the authenticator, so the user it bound is who the run must be
// for.
func (a *Adapter) runsCheck(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, cause := headerToken(r.Header)
		if cause == "" {
			_, cause = a.resolve(r.Context(), token, auth.TokenInfoFromContext(r.Context()))
		}
		if cause != "" {
			a.refusedAtRequest.count(cause)
			w.Header().Set("WWW-Authenticate", RunTokenHeader)
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(runRefusedBody)) // the status is sent; a client gone mid-body has nothing to read
			return
		}
		next.ServeHTTP(w, r)
	})
}

// messageRun resolves the run of one message, whatever its method: from the
// header the library handed the message on HTTP, from the operator's token on
// stdio. Nil without a runs resolver; a refusal is the error to answer with.
func (a *Adapter) messageRun(ctx context.Context, req mcp.Request) (*gateway.OpenedRun, error) {
	if a.cfg.Listener.Runs == nil {
		return nil, nil
	}
	var header http.Header
	var info *auth.TokenInfo
	if extra := req.GetExtra(); extra != nil {
		header, info = extra.Header, extra.TokenInfo
	}
	token, cause := a.cfg.Listener.RunToken, gateway.RunCause("")
	if a.cfg.Listener.Kind != KindStdio {
		token, cause = headerToken(header)
	}
	var run *gateway.OpenedRun
	if cause == "" {
		run, cause = a.resolve(ctx, token, info)
	}
	if cause != "" {
		a.refusedAtMessage.count(cause)
		return nil, &jsonrpc.Error{Code: CodeRunRefused, Message: runRefusedMessage}
	}
	return run, nil
}

// headerToken is the one Run-Token value of h. None, or an empty one, is
// missing; two are malformed, since a proxy and the gateway could each read
// another.
func headerToken(h http.Header) (string, gateway.RunCause) {
	values := h.Values(RunTokenHeader)
	switch {
	case len(values) > 1:
		return "", gateway.RunMalformed
	case len(values) == 0 || values[0] == "":
		return "", gateway.RunMissing
	}
	return values[0], ""
}

// resolve asks the resolver about token for the listener's identity at the
// adapter's clock. A refusal naming one of gateway.RunCauses is that cause;
// any other error, a refusal naming none (the empty cause included, which
// would read as no refusal), or a run without an id, is unreadable.
func (a *Adapter) resolve(ctx context.Context, token string, info *auth.TokenInfo) (*gateway.OpenedRun, gateway.RunCause) {
	if token == "" {
		return nil, gateway.RunMissing
	}
	who, ok := a.runIdentity(info)
	if !ok {
		return nil, gateway.RunOtherIdentity
	}
	run, err := a.cfg.Listener.Runs.Resolve(ctx, token, who, a.cfg.Clock())
	if err != nil {
		var refusal *gateway.RunRefusal
		if errors.As(err, &refusal) && slices.Contains(gateway.RunCauses(), refusal.Cause) {
			return nil, refusal.Cause
		}
		return nil, gateway.RunUnreadable
	}
	if run.ID == "" {
		return nil, gateway.RunUnreadable
	}
	return &run, ""
}

// runIdentity is who a run must be for: the configured tenant, principal
// type and agent, and the configured principal or the user the authenticator
// bound. An authenticated listener that bound nobody has nobody a run could
// be for.
func (a *Adapter) runIdentity(info *auth.TokenInfo) (gateway.RunIdentity, bool) {
	p, agent := a.cfg.Listener.Identity.Principal, a.cfg.Listener.Identity.Agent
	who := gateway.RunIdentity{TenantID: p.GetTenantId(), PrincipalType: p.GetType(), PrincipalID: p.GetId(), AgentID: agent.GetId()}
	if a.cfg.Listener.Authenticator == nil {
		return who, true
	}
	if info == nil || info.UserID == "" {
		return gateway.RunIdentity{}, false
	}
	who.PrincipalID = info.UserID
	return who, true
}
