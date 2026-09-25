package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/gateway"
)

// Kind is the transport a listener serves toward the agent.
type Kind int

const (
	// KindUnspecified is no listener; New refuses it.
	KindUnspecified Kind = iota
	// KindStatelessHTTP is Streamable HTTP without sessions, which serves
	// protocol 2026-07-28.
	KindStatelessHTTP
	// KindStatefulHTTP is Streamable HTTP with sessions, which serves
	// 2025-11-25 and older and answers a 2026-07-28 request with -32022.
	KindStatefulHTTP
	// KindStdio is one agent over a pipe; nothing authenticates it.
	KindStdio
)

// Shaping is what tools/list subtracts for a principal.
type Shaping int

const (
	// ShapeNone answers the upstream's list.
	ShapeNone Shaping = iota
	// ShapeAnnotate marks each tool with the verdict the policy gives a call
	// to it, and an unclassified tool with ACTION_UNCLASSIFIED.
	ShapeAnnotate
	// ShapeHide omits a tool the policy denies and every unclassified tool.
	ShapeHide
)

// Identity is who a listener's calls are made by when nothing authenticates
// them, and what an authenticated listener fills in around the user it
// bound.
type Identity struct {
	Principal *controlv1.Principal
	Agent     *controlv1.Agent
}

// Listener is the agent-facing side.
type Listener struct {
	Kind Kind
	// Origins are the browser origins an HTTP listener accepts, loopback
	// included: a request whose Origin is outside them is refused. The
	// adapter serves a handler and binds no address of its own.
	Origins []string
	// Authenticator wraps the HTTP handler and establishes the end user in
	// the request's context, the way auth.RequireBearerToken does. With one,
	// the principal's id is the user the token names; without one, nobody is
	// authenticated and the principal is Identity's.
	Authenticator func(http.Handler) http.Handler
	// AuthnStrength is recorded on a principal the Authenticator bound.
	AuthnStrength string
	Identity      Identity
}

// Upstream is one server the gateway calls as itself.
type Upstream struct {
	Name      string
	Transport mcp.Transport
	// TenantID and Environment are the resources' the upstream serves, for
	// the envelope's resource.tenant_id and resource.environment.
	TenantID    string
	Environment string
}

// Config configures an Adapter once, at New.
type Config struct {
	Listener  Listener
	Upstreams []Upstream
	Overrides []Override
	Shaping   Shaping
	// ListTTL is the ttlMs a shaped tools/list carries and how long the
	// adapter keeps one per principal; zero means immediately stale.
	ListTTL time.Duration
	// CallTimeout bounds every upstream call; zero means no bound of the
	// adapter's own.
	CallTimeout time.Duration
	// ListTimeout bounds reading one upstream's list, on a manifest refresh
	// and on a forwarded list; zero takes the package's own bound.
	ListTimeout time.Duration
	// ProjectID, TenantID and Environment say where the request was received.
	ProjectID   string
	TenantID    string
	Environment string
	Clock       func() time.Time
	NewID       func() string
	// Logger takes what the adapter cannot answer to anyone: a closing record
	// that failed, a manifest that could not be refreshed. Nil discards.
	Logger *slog.Logger
}

// Stats counts what the adapter did, for health and for a test that has to
// see a failure nothing else reports.
type Stats struct {
	Admitted      int64
	Blocked       int64
	Sent          int64
	CloseFailures int64
	// RefreshFailures counts tools/list reads that failed, each of which
	// dropped that upstream's entries.
	RefreshFailures int64
}

// Adapter is the MCP adapter as the pipeline sees it and as the agent
// reaches it: it translates, and the meaning of a call lives in the envelope
// it builds and in the policy, never here.
type Adapter struct {
	cfg      Config
	server   *mcp.Server
	manifest *manifest
	names    []string
	logger   *slog.Logger

	mu        sync.RWMutex
	pipeline  Pipeline
	upstreams map[string]*mcp.ClientSession
	lists     *listCache

	admitted, blocked, sent, closeFailures, refreshFailures atomic.Int64
}

var _ gateway.Adapter = (*Adapter)(nil)

// New checks cfg once and returns an adapter that is not yet connected to
// anything: Start connects it. It refuses a listener kind it does not serve,
// an authenticator on stdio, a listener without an identity, an upstream
// without a name or a transport, two upstreams with one name, an override
// that names no known upstream or is incomplete, a shaping it does not know,
// and a nil clock or id source.
func New(cfg Config) (*Adapter, error) {
	if err := checkConfig(cfg); err != nil {
		return nil, err
	}
	a := &Adapter{cfg: cfg, manifest: newManifest(cfg.Overrides), lists: newListCache(cfg.ListTTL), logger: cfg.Logger}
	if a.logger == nil {
		a.logger = slog.New(slog.DiscardHandler)
	}
	for _, up := range cfg.Upstreams {
		a.names = append(a.names, up.Name)
	}
	// The library emits tools/list_changed only when its own registry
	// changes, and the gateway registers no tools of its own, so a listener
	// that promised the notification would never send one.
	a.server = mcp.NewServer(&mcp.Implementation{Name: brand.Gateway, Version: "0"}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tools:     &mcp.ToolCapabilities{ListChanged: false},
			Resources: &mcp.ResourceCapabilities{},
			Prompts:   &mcp.PromptCapabilities{},
		},
	})
	a.server.AddReceivingMiddleware(a.middleware)
	return a, nil
}

func checkConfig(cfg Config) error {
	if err := checkListener(cfg.Listener); err != nil {
		return err
	}
	if cfg.Shaping < ShapeNone || cfg.Shaping > ShapeHide {
		return fmt.Errorf("%w: %d", ErrShaping, cfg.Shaping)
	}
	if cfg.Clock == nil {
		return ErrNoClock
	}
	if cfg.NewID == nil {
		return ErrNoIDSource
	}
	if cfg.ListTimeout < 0 {
		return fmt.Errorf("%w: %v", ErrListTimeout, cfg.ListTimeout)
	}
	names, err := checkUpstreams(cfg.Upstreams)
	if err != nil {
		return err
	}
	return checkOverrides(cfg.Overrides, names)
}

func checkListener(l Listener) error {
	switch l.Kind {
	case KindStatelessHTTP, KindStatefulHTTP:
	case KindStdio:
		if l.Authenticator != nil {
			return fmt.Errorf("%w: stdio has no request to authenticate", ErrListener)
		}
	default:
		return fmt.Errorf("%w: kind %d", ErrListener, l.Kind)
	}
	if l.Identity.Principal == nil || l.Identity.Agent == nil {
		return ErrIdentity
	}
	if l.Authenticator == nil {
		return nil
	}
	// The token names the user; configuration says only which tenant and
	// which type of principal the listener serves. An id, a strength or
	// attributes set here would be a claim about a user nobody authenticated.
	p := l.Identity.Principal
	if p.GetId() != "" || p.GetAuthnStrength() != "" || len(p.GetAttributes()) > 0 {
		return fmt.Errorf("%w: an authenticated listener configures the tenant and the type only", ErrIdentity)
	}
	return nil
}

func checkUpstreams(upstreams []Upstream) (map[string]bool, error) {
	if len(upstreams) == 0 {
		return nil, fmt.Errorf("%w: no upstream", ErrUpstream)
	}
	names := map[string]bool{}
	for i, up := range upstreams {
		if up.Name == "" || up.Transport == nil || names[up.Name] {
			return nil, fmt.Errorf("%w: Upstreams[%d]", ErrUpstream, i)
		}
		names[up.Name] = true
	}
	return names, nil
}

func checkOverrides(overrides []Override, names map[string]bool) error {
	for i, o := range overrides {
		switch {
		case !names[o.Upstream]:
			return fmt.Errorf("%w: Overrides[%d] names no configured upstream", ErrOverride, i)
		case o.Tool == "" || o.Fingerprint == "" || o.ResourceType == "":
			return fmt.Errorf("%w: Overrides[%d] needs a tool, a fingerprint and a resource type", ErrOverride, i)
		case o.Effect == controlv1.EffectClass_EFFECT_CLASS_UNSPECIFIED || o.Effect.Descriptor().Values().ByNumber(o.Effect.Number()) == nil:
			return fmt.Errorf("%w: Overrides[%d] names no effect class", ErrOverride, i)
		}
		if err := checkPointer(o.ResourceFrom); err != nil {
			return fmt.Errorf("overrides[%d]: %w", i, err)
		}
	}
	return nil
}

// Name is the protocol's.
func (*Adapter) Name() string { return "mcp" }

// Capabilities declares what the adapter does, each backed by a conformance
// test in this package. Authenticates and BindEndUser hold only for a
// listener with an authenticator. Obligations are the ones the adapter
// applies to a call itself; the pipeline applies the ones that rewrite.
func (a *Adapter) Capabilities() gateway.Capabilities {
	authenticates := a.cfg.Listener.Authenticator != nil
	return gateway.Capabilities{
		ObserveRequest: true,
		ObserveResult:  true,
		Block:          true,
		Authenticates:  authenticates,
		BindEndUser:    authenticates,
		SeeResourceIDs: true,
		Obligations:    AppliedObligations(),
	}
}

// Start connects every upstream, reads its tools into the manifest and
// binds the pipeline. An upstream that cannot be reached, or whose list
// cannot be read or is longer than the bound, fails Start rather than
// serving without it.
func (a *Adapter) Start(ctx context.Context, p Pipeline) error {
	if p == nil {
		return ErrNoPipeline
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.upstreams != nil {
		return ErrStarted
	}
	sessions := map[string]*mcp.ClientSession{}
	for _, up := range a.cfg.Upstreams {
		cs, err := a.connect(ctx, up)
		if err != nil {
			closeAll(sessions)
			return err
		}
		sessions[up.Name] = cs
		if err := a.refresh(ctx, up.Name, cs); err != nil {
			closeAll(sessions)
			return err
		}
	}
	a.pipeline, a.upstreams = p, sessions
	return nil
}

// refresh reads one upstream's tools into the manifest under the list
// timeout, and drops every shaped list, which was shaped from what the
// manifest held before.
func (a *Adapter) refresh(ctx context.Context, upstream string, cs *mcp.ClientSession) error {
	ctx, cancel := context.WithTimeout(ctx, a.listTimeout())
	defer cancel()
	err := a.manifest.refresh(ctx, upstream, cs)
	a.lists.reset(a.manifest.currentGeneration())
	return err
}

func (a *Adapter) connect(ctx context.Context, up Upstream) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: brand.Gateway, Version: "0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(ctx context.Context, req *mcp.ToolListChangedRequest) {
			if err := a.refresh(ctx, up.Name, req.Session); err != nil {
				a.refreshFailures.Add(1)
				a.logger.Error("manifest refresh failed", "upstream", up.Name, "err", err)
			}
		},
	})
	cs, err := client.Connect(ctx, up.Transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: connect %s: %w", up.Name, err)
	}
	return cs, nil
}

func closeAll(sessions map[string]*mcp.ClientSession) {
	for _, cs := range sessions {
		_ = cs.Close() // the sessions are being abandoned; nothing reads a close error here
	}
}

// Close disconnects every upstream.
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	var errs []error
	for name, cs := range a.upstreams {
		if err := cs.Close(); err != nil {
			errs = append(errs, fmt.Errorf("mcp: close %s: %w", name, err))
		}
	}
	a.upstreams = nil
	return errors.Join(errs...)
}

// Stats returns the counters.
func (a *Adapter) Stats() Stats {
	return Stats{
		Admitted: a.admitted.Load(), Blocked: a.blocked.Load(), Sent: a.sent.Load(),
		CloseFailures: a.closeFailures.Load(), RefreshFailures: a.refreshFailures.Load(),
	}
}

// Entries returns the manifest as it stands, one entry per listed tool.
func (a *Adapter) Entries() []Entry {
	out, _ := a.manifest.snapshot(a.names)
	return out
}

// started returns the pipeline and the upstream session for name, or
// ErrNotStarted before Start.
func (a *Adapter) started() (Pipeline, map[string]*mcp.ClientSession, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.upstreams == nil {
		return nil, nil, ErrNotStarted
	}
	return a.pipeline, a.upstreams, nil
}
