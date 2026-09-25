package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/guardana/control/adapters/authzen"
	"github.com/guardana/control/internal/gateway"
	"github.com/guardana/control/internal/gatewayconfig"
)

// decisionPoint builds the client of the decision point the configuration
// names, and nil where it names none. It contacts nothing: whether the
// decision point answers is never a condition of starting (ADR-0017).
func decisionPoint(cfg *gatewayconfig.Config) (*authzen.Client, error) {
	if cfg.PDP.Identifier == "" {
		return nil, nil
	}
	client, err := authzen.New(pdpOptions(cfg))
	if err != nil {
		return nil, fmt.Errorf("pdp: %w", err)
	}
	return client, nil
}

// pdpOptions carries the pdp block to the client, key for key.
func pdpOptions(cfg *gatewayconfig.Config) authzen.Options {
	return authzen.Options{
		Identifier:     cfg.PDP.Identifier,
		Endpoint:       cfg.PDP.EvaluationEndpoint,
		AllowPlaintext: cfg.PDP.AllowPlaintext,
		Headers:        cfg.PDP.Headers,
		Timeout:        cfg.PDP.Timeout,
		InFlight:       cfg.PDP.MaxInFlight,
		Informational:  cfg.PDP.InformationalContext,
		Proxy:          cfg.PDP.Proxy,
	}
}

// withDecisionPoint sets the pipeline's three decision point fields together,
// or none of them. A nil client stays a nil interface: a typed nil would be a
// decision point the pipeline asks.
func withDecisionPoint(cfg *gateway.Config, client *authzen.Client, timeout time.Duration) {
	if client == nil {
		return
	}
	cfg.DecisionPoint, cfg.DecisionPointID, cfg.DecisionTimeout = client, client.Identifier(), timeout
}

// explainDecisionPoint keeps the pipeline's refusal of a bundle and a
// decision point that disagree, and says which key or which bundle fixes it.
func explainDecisionPoint(err error) error {
	switch {
	case errors.Is(err, gateway.ErrNoDecisionPoint):
		return fmt.Errorf("%w; set pdp.identifier to the decision point the bundle's external rules ask, or serve a bundle without them", err)
	case errors.Is(err, gateway.ErrDecisionPointUnused):
		return fmt.Errorf("%w; unset pdp.identifier, or serve a bundle with a DENY rule that reads external", err)
	}
	return err
}

// pdp reports whether the decision point's published metadata names the
// configured identifier and endpoint. It runs last and only here: `run` never
// fetches the metadata, and a plane starts whatever it says.
func (d *examination) pdp(ctx context.Context) (string, string, string) {
	if d.cfg.PDP.Identifier == "" {
		return verdictOK, "pdp", "not configured: no decision point is asked, and a bundle whose rules read external is refused"
	}
	if d.plane == nil || d.plane.pdp == nil {
		return verdictUnknown, "pdp", "the plane was not built, so the decision point's client was not made"
	}
	r := d.plane.pdp.CheckDiscovery(ctx)
	verdict := verdictUnknown
	switch r.Verdict {
	case authzen.DiscoveryAgrees:
		verdict = verdictOK
	case authzen.DiscoveryDisagrees:
		verdict = verdictFail
	}
	return verdict, "pdp", fmt.Sprintf("%s, asked within %s, at most %d in flight; discovery %s: %s",
		d.plane.pdp.Identifier(), d.cfg.PDP.Timeout, d.cfg.PDP.MaxInFlight, r.Verdict, r.Detail)
}
