package authzen

import (
	"encoding/json"
	"strings"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// Mapping version 1 (ADR-0017): the question carries what the policy format
// can read of the envelope and nothing else. The field order and the names
// below are the version; changing either is a new version with its own
// golden.
type question struct {
	Subject  subject  `json:"subject"`
	Action   action   `json:"action"`
	Resource resource `json:"resource"`
	Context  asked    `json:"context"`
}

type subject struct {
	Type       string            `json:"type"`
	ID         string            `json:"id"`
	Properties subjectProperties `json:"properties"`
}

type subjectProperties struct {
	TenantID      string            `json:"tenant_id"`
	AuthnStrength string            `json:"authn_strength"`
	Attributes    map[string]string `json:"attributes"`
}

type action struct {
	Name       string           `json:"name"`
	Properties actionProperties `json:"properties"`
}

type actionProperties struct {
	Kind     string `json:"kind"`
	Provider string `json:"provider"`
	Protocol string `json:"protocol"`
	Effect   string `json:"effect"`
}

type resource struct {
	Type       string             `json:"type"`
	ID         string             `json:"id"`
	Properties resourceProperties `json:"properties"`
}

type resourceProperties struct {
	TenantID    string            `json:"tenant_id"`
	Environment string            `json:"environment"`
	Labels      map[string]string `json:"labels"`
}

type asked struct {
	Agent                agent       `json:"agent"`
	Destination          destination `json:"destination"`
	Data                 data        `json:"data"`
	ProjectID            string      `json:"project_id"`
	TenantID             string      `json:"tenant_id"`
	Environment          string      `json:"environment"`
	RequestID            string      `json:"request_id"`
	SupportedObligations []string    `json:"supported_obligations"`
}

type agent struct {
	ID        string `json:"id"`
	Framework string `json:"framework"`
}

type destination struct {
	TrustZone string `json:"trust_zone"`
	Host      string `json:"host"`
}

type data struct {
	Sensitivities   []string `json:"sensitivities"`
	ContainsSecrets bool     `json:"contains_secrets"`
}

// mapV1 is the question mapping version 1 asks about env, and false when env
// lacks an identifier the question needs: nothing stands in for one.
func mapV1(env *controlv1.ActionEnvelope) ([]byte, bool) {
	p, a, r := env.GetPrincipal(), env.GetAction(), env.GetResource()
	if env.GetRequestId() == "" || p.GetId() == "" || a.GetName() == "" || r.GetId() == "" {
		return nil, false
	}
	q := question{
		Subject: subject{Type: p.GetType(), ID: p.GetId(), Properties: subjectProperties{
			TenantID: p.GetTenantId(), AuthnStrength: p.GetAuthnStrength(), Attributes: nonNil(p.GetAttributes()),
		}},
		Action: action{Name: a.GetName(), Properties: actionProperties{
			Kind: a.GetKind(), Provider: a.GetProvider(), Protocol: a.GetProtocol(),
			Effect: strings.TrimPrefix(a.GetEffect().String(), "EFFECT_CLASS_"),
		}},
		Resource: resource{Type: r.GetType(), ID: r.GetId(), Properties: resourceProperties{
			TenantID: r.GetTenantId(), Environment: r.GetEnvironment(), Labels: nonNil(r.GetLabels()),
		}},
		Context: asked{
			Agent: agent{ID: env.GetAgent().GetId(), Framework: env.GetAgent().GetFramework()},
			Destination: destination{
				TrustZone: strings.TrimPrefix(env.GetDestination().GetTrustZone().String(), "TRUST_ZONE_"),
				Host:      env.GetDestination().GetHost(),
			},
			Data:        data{Sensitivities: sensitivities(env.GetData()), ContainsSecrets: env.GetData().GetContainsSecrets()},
			ProjectID:   env.GetProjectId(),
			TenantID:    env.GetTenantId(),
			Environment: env.GetEnvironment(),
			RequestID:   env.GetRequestId(),
			// This plane fulfils no obligation.
			SupportedObligations: []string{},
		},
	}
	out, err := json.Marshal(q)
	return out, err == nil
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func sensitivities(d *controlv1.DataLabels) []string {
	out := make([]string, 0, len(d.GetSensitivities()))
	for _, s := range d.GetSensitivities() {
		out = append(out, strings.TrimPrefix(s.String(), "SENSITIVITY_"))
	}
	return out
}
