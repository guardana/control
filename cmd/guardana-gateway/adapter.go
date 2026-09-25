package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	adaptermcp "github.com/guardana/control/adapters/mcp"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/gatewayconfig"
	"github.com/guardana/control/pkg/contract"
)

// adapterConfig translates the configuration into the adapter's, which is
// where every MCP-side refusal is made.
func adapterConfig(cfg *gatewayconfig.Config, logger *slog.Logger) (adaptermcp.Config, error) {
	kinds := map[string]adaptermcp.Kind{
		"stateless_http": adaptermcp.KindStatelessHTTP,
		"stateful_http":  adaptermcp.KindStatefulHTTP,
		"stdio":          adaptermcp.KindStdio,
	}
	shapings := map[string]adaptermcp.Shaping{
		"none": adaptermcp.ShapeNone, "annotate": adaptermcp.ShapeAnnotate, "hide": adaptermcp.ShapeHide,
	}
	tenant := cfg.Listener.PrincipalTenant
	if tenant == "" {
		tenant = cfg.TenantID
	}
	upstreams, err := upstreams(cfg)
	if err != nil {
		return adaptermcp.Config{}, err
	}
	overrides, err := overrides(cfg)
	if err != nil {
		return adaptermcp.Config{}, err
	}
	return adaptermcp.Config{
		Listener: adaptermcp.Listener{
			Kind:    kinds[cfg.Listener.Kind],
			Origins: cfg.Listener.Origins,
			Identity: adaptermcp.Identity{
				Principal: &controlv1.Principal{Id: cfg.Listener.PrincipalID, Type: cfg.Listener.PrincipalType, TenantId: tenant},
				Agent: &controlv1.Agent{
					Id:        cfg.Listener.AgentID,
					Framework: cfg.Listener.AgentFramework,
					Version:   cfg.Listener.AgentVersion,
				},
			},
		},
		Upstreams:   upstreams,
		Overrides:   overrides,
		Shaping:     shapings[cfg.List.Shaping],
		ListTTL:     cfg.List.TTL,
		CallTimeout: cfg.Upstream.CallTimeout,
		ListTimeout: cfg.Upstream.ListTimeout,
		ProjectID:   cfg.ProjectID,
		TenantID:    cfg.TenantID,
		Environment: cfg.Environment,
		Clock:       time.Now,
		NewID:       newID,
		Logger:      logger,
	}, nil
}

// upstreams builds one transport per configured server: Streamable HTTP for
// an endpoint, a child process for a command. An HTTP upstream's redirect is
// not followed: it reaches the adapter as the upstream's answer, a failed
// call, so a call goes only to the endpoint the operator configured.
func upstreams(cfg *gatewayconfig.Config) ([]adaptermcp.Upstream, error) {
	out := make([]adaptermcp.Upstream, 0, len(cfg.Upstreams))
	for i, up := range cfg.Upstreams {
		var transport mcp.Transport
		if up.Endpoint != "" {
			transport = &mcp.StreamableClientTransport{
				Endpoint: up.Endpoint,
				HTTPClient: &http.Client{
					CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
				},
			}
		} else {
			path, err := exec.LookPath(cfg.Resolve(up.Command))
			if err != nil {
				return nil, fmt.Errorf("upstreams.%d.command: %w", i, err)
			}
			// The command and its arguments are the operator's own
			// configuration: an upstream server is a program they chose to run,
			// and nothing an agent sends reaches this line.
			//nolint:gosec // G204: the command comes from the operator's configuration file, never from a request
			transport = &mcp.CommandTransport{Command: exec.Command(filepath.Clean(path), up.Args...)}
		}
		out = append(out, adaptermcp.Upstream{
			Name: up.Name, Transport: transport, TenantID: up.TenantID, Environment: up.Environment,
		})
	}
	return out, nil
}

// overrides turns the operator's classifications into the manifest's.
func overrides(cfg *gatewayconfig.Config) ([]adaptermcp.Override, error) {
	out := make([]adaptermcp.Override, 0, len(cfg.Overrides))
	for i := range cfg.Overrides {
		o := &cfg.Overrides[i]
		effect, err := contract.ParseEffect(o.Effect)
		if err != nil {
			return nil, fmt.Errorf("overrides.%d.effect: %w", i, err)
		}
		zone, err := o.Zone()
		if err != nil {
			return nil, fmt.Errorf("overrides.%d.trust_zone: %w", i, err)
		}
		returnsZone, err := o.ReturnsZone()
		if err != nil {
			return nil, fmt.Errorf("overrides.%d.returns.trust: %w", i, err)
		}
		returnsLevel, err := o.ReturnsLevel()
		if err != nil {
			return nil, fmt.Errorf("overrides.%d.returns.sensitivity: %w", i, err)
		}
		out = append(out, adaptermcp.Override{
			Upstream: o.Upstream, Tool: o.Tool, Fingerprint: o.Fingerprint, Effect: effect,
			ResourceType: o.ResourceType, ResourceFrom: o.ResourceFrom, TrustZone: zone,
			ReturnsTrust: returnsZone, ReturnsSensitivity: returnsLevel,
		})
	}
	return out, nil
}

// orWord is value, or what its absence means.
func orWord(value, absent string) string {
	if value == "" {
		return absent
	}
	return value
}

// startUpstreams connects every upstream, after which the manifest holds
// what they listed. The transport's refusal quotes the URL it dialled, so
// where an upstream's endpoint is one ShowAddress withholds, the refusal
// names the key and why instead of its own text.
func (p *plane) startUpstreams(ctx context.Context) error {
	err := p.adapter.Start(ctx, p.pipeline)
	if err == nil {
		p.listed.Store(true)
		return nil
	}
	for i, up := range p.cfg.Upstreams {
		if shown := gatewayconfig.ShowAddress(up.Endpoint); shown != up.Endpoint {
			return fmt.Errorf("the transport's error is not printed, because upstreams.%d.endpoint is %s", i, shown)
		}
	}
	return err
}
