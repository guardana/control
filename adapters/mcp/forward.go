package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// forwardList merges the upstreams' resource, template or prompt lists.
// A list is not a call: it names what could be read, and the read is what
// the policy decides. The merged list carries the adapter's own cache
// scope, never an upstream's, and nothing under the gateway's own _meta
// namespace that an upstream put there. A request whose caller the listener
// cannot name is refused as a tools/list is, before any upstream is asked.
func (a *Adapter) forwardList(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
	_, sessions, err := a.started()
	if err != nil {
		return nil, err
	}
	if _, _, err := a.identity(req); err != nil {
		return nil, blockedError(nil, map[string]any{keyReasonCodes: []string{"REQUIRED_FIELD_ABSENT"}})
	}
	ctx, cancel := context.WithTimeout(ctx, a.listTimeout())
	defer cancel()
	l := lister{a: a, sessions: sessions, method: method}
	switch method {
	case methodListResources:
		items, err := forward(ctx, l, func(ctx context.Context, cs *mcp.ClientSession, cursor string) ([]*mcp.Resource, string, error) {
			res, err := cs.ListResources(ctx, &mcp.ListResourcesParams{Cursor: cursor})
			if err != nil {
				return nil, "", err
			}
			return res.Resources, res.NextCursor, nil
		}, func(r *mcp.Resource) *mcp.Meta { return &r.Meta })
		if err != nil {
			return nil, err
		}
		out := &mcp.ListResourcesResult{Resources: items}
		out.TTLMs, out.CacheScope = a.ttlMs(), cacheScopePrivate
		return out, nil
	case methodListTemplates:
		items, err := forward(ctx, l, func(ctx context.Context, cs *mcp.ClientSession, cursor string) ([]*mcp.ResourceTemplate, string, error) {
			res, err := cs.ListResourceTemplates(ctx, &mcp.ListResourceTemplatesParams{Cursor: cursor})
			if err != nil {
				return nil, "", err
			}
			return res.ResourceTemplates, res.NextCursor, nil
		}, func(r *mcp.ResourceTemplate) *mcp.Meta { return &r.Meta })
		if err != nil {
			return nil, err
		}
		out := &mcp.ListResourceTemplatesResult{ResourceTemplates: items}
		out.TTLMs, out.CacheScope = a.ttlMs(), cacheScopePrivate
		return out, nil
	default:
		items, err := forward(ctx, l, func(ctx context.Context, cs *mcp.ClientSession, cursor string) ([]*mcp.Prompt, string, error) {
			res, err := cs.ListPrompts(ctx, &mcp.ListPromptsParams{Cursor: cursor})
			if err != nil {
				return nil, "", err
			}
			return res.Prompts, res.NextCursor, nil
		}, func(p *mcp.Prompt) *mcp.Meta { return &p.Meta })
		if err != nil {
			return nil, err
		}
		out := &mcp.ListPromptsResult{Prompts: items}
		out.TTLMs, out.CacheScope = a.ttlMs(), cacheScopePrivate
		return out, nil
	}
}

// lister is one forwarded list: the adapter, the sessions it reads and the
// method the agent asked.
type lister struct {
	a        *Adapter
	sessions map[string]*mcp.ClientSession
	method   string
}

// forward walks one paginated list of every upstream, in configured order,
// each bounded like a manifest refresh, and keeps each upstream's items
// without the gateway's namespace. An upstream whose items quote a secret,
// or cannot be scanned, fails the whole list, as an upstream that fails does.
func forward[T any](ctx context.Context, l lister, page func(context.Context, *mcp.ClientSession, string) ([]*T, string, error), meta func(*T) *mcp.Meta) ([]*T, error) {
	items := []*T{}
	for _, name := range l.a.names {
		cs := l.sessions[name]
		got, err := readAll(ctx, func(ctx context.Context, cursor string) ([]*T, string, error) {
			return page(ctx, cs, cursor)
		})
		if err != nil {
			return nil, l.failed(name, fmt.Errorf("mcp: list from %s: %w", name, err))
		}
		kept := stripped(got, meta)
		if v := scanned(l.a.cfg.Secrets, kept); v.Withhold() {
			l.a.withheldAnswer("", l.method, name, v)
			return nil, withheldAnswerError()
		}
		items = append(items, kept...)
	}
	return items, nil
}

// failed is a forwarded list's failure as the agent sees it: the bound,
// which is the adapter's own, as it is, an upstream's wire error that quoted
// a secret withheld, and anything else as an upstream's.
func (l lister) failed(upstream string, err error) error {
	if errors.Is(err, ErrListBound) {
		return err
	}
	if ans := inspect(l.a.cfg.Secrets, nil, err); ans.withheld {
		l.a.withheldAnswer("", l.method, upstream, ans.verdict)
		return withheldError(err, nil)
	}
	return upstreamError(err)
}
