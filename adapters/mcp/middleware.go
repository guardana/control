package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/gateway"
)

// Method names the middleware answers itself. Everything else goes to the
// library's own handler, which knows no tool, resource or prompt.
const (
	methodListTools     = "tools/list"
	methodCallTool      = "tools/call"
	methodReadResource  = "resources/read"
	methodGetPrompt     = "prompts/get"
	methodListResources = "resources/list"
	methodListTemplates = "resources/templates/list"
	methodListPrompts   = "prompts/list"
)

// sender turns the authorized bytes into the one upstream call that carries
// exactly them, and says what will be sent. It refuses bytes the method
// cannot carry, which is a call the adapter never sends.
type sender func(authorized []byte) (send func(context.Context, *mcp.ClientSession) (mcp.Result, error), sent []byte, err error)

// outcome is what one admitted call came to: a block with what to add, a
// pending state, or the upstream's answer, each with the decision the
// pipeline answered with.
type outcome struct {
	decision *controlv1.Decision
	blocked  bool
	extra    map[string]any
	pending  *gateway.Pending
	res      mcp.Result
	err      error
}

// middleware is the one interception point (ADR-0013): it runs after the
// library parsed the body and checked the routing headers against it, so
// the verdict is made from the body and never from a header.
func (a *Adapter) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case methodListTools:
			return a.listTools(ctx, req)
		case methodCallTool:
			r := req.(*mcp.CallToolRequest)
			c := a.toolCall(r)
			return a.callTool(ctx, c, toolSender(r.Params.Name))
		case methodReadResource:
			r := req.(*mcp.ReadResourceRequest)
			c := a.read(req, kindResource, r.Params.URI, []byte(noArguments), nil)
			return a.readThrough(ctx, c, resourceSender(r.Params.URI))
		case methodGetPrompt:
			r := req.(*mcp.GetPromptRequest)
			args, err := promptArguments(r.Params.Arguments)
			c := a.read(req, kindPrompt, r.Params.Name, args, err)
			return a.readThrough(ctx, c, promptSender(r.Params.Name))
		case methodListResources, methodListTemplates, methodListPrompts:
			return a.forwardList(ctx, method)
		}
		return next(ctx, method, req)
	}
}

// toolSender sends the authorized bytes as the call's arguments, in params
// the adapter builds: what the agent put beside them, its _meta and its
// input responses, is the agent's business with the gateway and reaches no
// upstream.
func toolSender(name string) sender {
	return func(authorized []byte) (func(context.Context, *mcp.ClientSession) (mcp.Result, error), []byte, error) {
		params := &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(authorized)}
		return func(ctx context.Context, cs *mcp.ClientSession) (mcp.Result, error) {
			return cs.CallTool(ctx, params)
		}, authorized, nil
	}
}

// resourceSender sends the authorized URI and nothing else; a read carries
// no arguments, so anything but the empty document refuses.
func resourceSender(uri string) sender {
	return func(authorized []byte) (func(context.Context, *mcp.ClientSession) (mcp.Result, error), []byte, error) {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(authorized, &fields); err != nil || len(fields) > 0 {
			return nil, nil, fmt.Errorf("%w: a resources/read carries no arguments", ErrArguments)
		}
		params := &mcp.ReadResourceParams{URI: uri}
		return func(ctx context.Context, cs *mcp.ClientSession) (mcp.Result, error) {
			return cs.ReadResource(ctx, params)
		}, []byte(noArguments), nil
	}
}

// promptSender sends the authorized arguments, decoded into the map the
// protocol carries, in params the adapter builds.
func promptSender(name string) sender {
	return func(authorized []byte) (func(context.Context, *mcp.ClientSession) (mcp.Result, error), []byte, error) {
		var args map[string]string
		if err := json.Unmarshal(authorized, &args); err != nil {
			return nil, nil, fmt.Errorf("%w: a prompts/get takes an object of strings: %w", ErrArguments, err)
		}
		sent, err := promptArguments(args)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrArguments, err)
		}
		params := &mcp.GetPromptParams{Name: name, Arguments: args}
		return func(ctx context.Context, cs *mcp.ClientSession) (mcp.Result, error) {
			return cs.GetPrompt(ctx, params)
		}, sent, nil
	}
}

// callTool answers a tools/call as a tool result the model can read: a
// block and a pending state are isError results, never JSON-RPC errors; an
// upstream error passes through with its own code and message. Every answer
// the pipeline decided names the trail the decision was recorded on.
func (a *Adapter) callTool(ctx context.Context, c call, send sender) (mcp.Result, error) {
	o, err := a.run(ctx, c, send)
	switch {
	case err != nil:
		return nil, err
	case o.pending != nil:
		return named(pending(o.pending), o.decision), nil
	case o.blocked:
		return named(blocked(o.decision, o.extra), o.decision), nil
	case o.err != nil:
		return nil, namedError(o.err, o.decision)
	}
	return named(upstreamResult(o.res), o.decision), nil
}

// readThrough answers a resources/read or a prompts/get, whose results
// cannot say isError: a block or a pending state is a JSON-RPC error with
// the adapter's own code.
func (a *Adapter) readThrough(ctx context.Context, c call, send sender) (mcp.Result, error) {
	o, err := a.run(ctx, c, send)
	switch {
	case err != nil:
		return nil, err
	case o.pending != nil:
		return nil, pendingError(o.pending)
	case o.blocked:
		return nil, blockedError(o.decision, o.extra)
	case o.err != nil:
		return nil, upstreamError(o.err)
	}
	return upstreamResult(o.res), nil
}

// run asks the pipeline and does exactly what it says: block, answer the
// pending state, or send the authorized bytes once their digest is the
// authorized one and every obligation holds. What was sent and what came
// back go to Close with the very disposition Admit returned; an execution
// the adapter does not send is aborted, never closed.
func (a *Adapter) run(ctx context.Context, c call, send sender) (outcome, error) {
	p, sessions, err := a.started()
	if err != nil {
		return outcome{}, err
	}
	d := p.Admit(ctx, c.admission)
	a.admitted.Add(1)
	switch d.Action {
	case core.Execute, core.ExecuteWithObligations:
	case core.AwaitApproval:
		return outcome{decision: d.Decision, pending: d.Pending}, nil
	default:
		a.blocked.Add(1)
		return outcome{decision: d.Decision, blocked: true}, nil
	}
	if refusal := c.admission.Refusal; refusal != nil && !errors.Is(refusal, gateway.ErrUnclassified) {
		// A translation the adapter refused is not sent, whatever the mode
		// let through; what could not be translated cannot be authorized.
		return a.abort(ctx, p, d, gateway.AbortUntranslatable, []string{c.code()}, refusal), nil
	}
	cs := sessions[c.upstream]
	if cs == nil {
		return a.abort(ctx, p, d, gateway.AbortUnroutable, []string{codeUnclassified}, ErrUnroutable), nil
	}
	got, err := canon.ArgumentsHashV1(d.AuthorizedArgs)
	if err != nil || got != d.AuthorizedDigest {
		return a.abort(ctx, p, d, gateway.AbortArgsMismatch, []string{codeExecutedArgsMismatch}, nil), nil
	}
	obl, err := applyObligations(&c, d.Obligations)
	if err != nil {
		codes := append(append([]string(nil), d.Decision.GetReasonCodes()...), codeObligationNotApplied)
		return a.abort(ctx, p, d, gateway.AbortObligation, codes, err), nil
	}
	do, sent, err := send(d.AuthorizedArgs)
	if err != nil {
		// The bytes are the authorized ones; this protocol cannot carry them.
		return a.abort(ctx, p, d, gateway.AbortUntranslatable, []string{codeInvalidFieldValue}, err), nil
	}
	sendCtx, cancel := a.deadline(ctx, obl.timeout)
	defer cancel()
	started := a.cfg.Clock()
	a.sent.Add(1)
	res, callErr := do(sendCtx, cs)
	a.close(ctx, p, d, sent, resultOf(d, started, a.cfg.Clock(), res, callErr))
	return outcome{decision: d.Decision, res: res, err: callErr}, nil
}

// abort records an execution the adapter was handed and did not send, and
// answers the block the agent sees. The decision is the pipeline's; the
// codes say what the adapter refused.
func (a *Adapter) abort(ctx context.Context, p Pipeline, d gateway.Disposition, cause gateway.AbortCause, codes []string, why error) outcome {
	a.blocked.Add(1)
	if err := p.Abort(ctx, d, cause); err != nil {
		a.closeFailures.Add(1)
		a.logger.Error("aborting record failed", "execution_id", d.ExecutionID, "err", err)
	}
	extra := map[string]any{keyReasonCodes: codes}
	if why != nil {
		extra[keyRefused] = why.Error()
	}
	return outcome{decision: d.Decision, blocked: true, extra: extra}
}

// close hands the pipeline the disposition, the bytes that were sent and
// the result. An error means the record is not durable: the result is
// delivered anyway and the pipeline stops taking material calls itself; the
// adapter counts and logs what it cannot answer to anyone.
func (a *Adapter) close(ctx context.Context, p Pipeline, d gateway.Disposition, sent []byte, result *controlv1.ActionResult) {
	if err := p.Close(ctx, d, sent, result); err != nil {
		a.closeFailures.Add(1)
		a.logger.Error("closing record failed", "request_id", result.GetRequestId(), "err", err)
	}
}

// deadline bounds the upstream call by the shortest of the operator's and
// the obligations' timeouts; zero for both is no bound of the adapter's.
func (a *Adapter) deadline(ctx context.Context, obligation time.Duration) (context.Context, context.CancelFunc) {
	d := a.cfg.CallTimeout
	if obligation > 0 && (d == 0 || obligation < d) {
		d = obligation
	}
	if d <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

// listTimeout bounds reading one upstream's list, on a refresh and on a
// forwarded list.
func (a *Adapter) listTimeout() time.Duration {
	if a.cfg.ListTimeout > 0 {
		return a.cfg.ListTimeout
	}
	return defaultListTimeout
}

// forwardList merges the upstreams' resource, template or prompt lists.
// A list is not a call: it names what could be read, and the read is what
// the policy decides. The merged list carries the adapter's own cache
// scope, never an upstream's, and nothing under the gateway's own _meta
// namespace that an upstream put there.
func (a *Adapter) forwardList(ctx context.Context, method string) (mcp.Result, error) {
	_, sessions, err := a.started()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, a.listTimeout())
	defer cancel()
	switch method {
	case methodListResources:
		items, err := merged(ctx, a.names, sessions, func(ctx context.Context, cs *mcp.ClientSession, cursor string) ([]*mcp.Resource, string, error) {
			res, err := cs.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
			if err != nil {
				return nil, "", err
			}
			return res.Resources, res.NextCursor, nil
		})
		if err != nil {
			return nil, err
		}
		out := &mcp.ListResourcesResult{Resources: stripped(items, func(r *mcp.Resource) *mcp.Meta { return &r.Meta })}
		out.TTLMs, out.CacheScope = a.ttlMs(), cacheScopePrivate
		return out, nil
	case methodListTemplates:
		items, err := merged(ctx, a.names, sessions, func(ctx context.Context, cs *mcp.ClientSession, cursor string) ([]*mcp.ResourceTemplate, string, error) {
			res, err := cs.ListResourceTemplates(ctx, &mcp.ListResourceTemplatesParams{Cursor: cursor})
			if err != nil {
				return nil, "", err
			}
			return res.ResourceTemplates, res.NextCursor, nil
		})
		if err != nil {
			return nil, err
		}
		out := &mcp.ListResourceTemplatesResult{ResourceTemplates: stripped(items, func(r *mcp.ResourceTemplate) *mcp.Meta { return &r.Meta })}
		out.TTLMs, out.CacheScope = a.ttlMs(), cacheScopePrivate
		return out, nil
	default:
		items, err := merged(ctx, a.names, sessions, func(ctx context.Context, cs *mcp.ClientSession, cursor string) ([]*mcp.Prompt, string, error) {
			res, err := cs.ListPrompts(ctx, &mcp.ListPromptsParams{Cursor: cursor})
			if err != nil {
				return nil, "", err
			}
			return res.Prompts, res.NextCursor, nil
		})
		if err != nil {
			return nil, err
		}
		out := &mcp.ListPromptsResult{Prompts: stripped(items, func(p *mcp.Prompt) *mcp.Meta { return &p.Meta })}
		out.TTLMs, out.CacheScope = a.ttlMs(), cacheScopePrivate
		return out, nil
	}
}

// merged walks one paginated list of every upstream, in configured order,
// each bounded like a manifest refresh.
func merged[T any](ctx context.Context, names []string, sessions map[string]*mcp.ClientSession, page func(context.Context, *mcp.ClientSession, string) ([]T, string, error)) ([]T, error) {
	items := []T{}
	for _, name := range names {
		cs := sessions[name]
		got, err := readAll(ctx, func(ctx context.Context, cursor string) ([]T, string, error) {
			return page(ctx, cs, cursor)
		})
		if err != nil {
			return nil, fmt.Errorf("mcp: list from %s: %w", name, err)
		}
		items = append(items, got...)
	}
	return items, nil
}
