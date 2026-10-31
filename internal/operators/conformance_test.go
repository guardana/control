package operators_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/guardana/control/internal/operators"
	"github.com/guardana/control/internal/trust"
)

const (
	authenticatorCases = "../../testdata/ports/operator-authenticator/cases.json"
	authorizerCases    = "../../testdata/ports/operator-authorizer/cases.json"
)

var payloadTypes = map[string]string{
	"answer": "application/vnd.agent-approval-answer+json",
	"route":  "application/vnd.agent-reaction-route+json",
	"roster": "application/vnd.agent-trust-roster+json",
}

type caseHeader struct {
	Rule    string `json:"rule"`
	Case    string `json:"case"`
	Control bool   `json:"control"`
}

type authnCase struct {
	caseHeader
	Roster    string `json:"roster"`
	Statement struct {
		Signer      string `json:"signer"`
		KeyID       string `json:"keyId"`
		PayloadType string `json:"payloadType"`
		Alg         string `json:"alg"`
		BodyKeyID   string `json:"bodyKeyId"`
	} `json:"statement"`
	State   string `json:"state"`
	Cause   string `json:"cause"`
	Subject string `json:"subject"`
}

type authzCase struct {
	caseHeader
	Who           string `json:"who"`
	Revoke        string `json:"revoke"`
	Veto          string `json:"veto"`
	OnUnavailable string `json:"on_unavailable"`
	Tenant        string `json:"tenant"`
	Project       string `json:"project"`
	State         string `json:"state"`
	Cause         string `json:"cause"`
	Level         string `json:"level"`
}

// readCases decodes a conformance file strictly into cases, refusing a port
// other than port, and returns each case's header.
func readCases[C any](t *testing.T, path, port string, header func(C) caseHeader) []C {
	t.Helper()
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the repository's own conformance file
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Port   string `json:"port"`
		Format string `json:"format"`
		About  string `json:"about"`
		Cases  []C    `json:"cases"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if file.Port != port || file.Format != "0.1" || len(file.Cases) == 0 {
		t.Fatalf("%s names port %q format %q with %d cases", path, file.Port, file.Format, len(file.Cases))
	}
	seen := map[string][2]int{}
	for _, c := range file.Cases {
		h := header(c)
		n := seen[h.Rule]
		if h.Control {
			n[1]++
		} else {
			n[0]++
		}
		seen[h.Rule] = n
	}
	for rule, n := range seen {
		if n[0] == 0 || n[1] == 0 {
			t.Errorf("%s: rule %s has %d cases and %d negative controls", path, rule, n[0], n[1])
		}
	}
	return file.Cases
}

func TestEveryAuthenticatorKindPassesTheConformanceCases(t *testing.T) {
	cases := readCases(t, authenticatorCases, "operator-authenticator", func(c authnCase) caseHeader { return c.caseHeader })
	states := map[string]operators.AuthState{"unavailable": operators.AuthUnavailable, "refused": operators.AuthRefused, "authenticated": operators.AuthAuthenticated}
	for _, kind := range []operators.AuthenticatorKind{operators.AuthenticatorRoster} {
		for _, c := range cases {
			t.Run(string(kind)+"/"+c.Rule+"/"+c.Case, func(t *testing.T) {
				roster := trust.Roster(newWorld(t, standard()).roster)
				switch c.Roster {
				case "":
				case "unavailable":
					roster = &fakeRoster{answer: trust.Unavailable(trust.CauseExpired)}
				case "silent":
					silent := &fakeRoster{release: make(chan struct{}), deaf: true}
					defer close(silent.release)
					roster = silent
				default:
					t.Fatalf("roster %q", c.Roster)
				}
				a, err := operators.NewAuthenticator(kind, roster, operators.Bounds{Timeout: 50 * time.Millisecond})
				if err != nil {
					t.Fatal(err)
				}
				got := a.Authenticate(t.Context(), operators.ActApprove, statementOf(t, c))
				want, ok := states[c.State]
				if !ok || got.State() != want || string(got.Cause()) != c.Cause || got.Subject() != c.Subject {
					t.Errorf("= state %d cause %q subject %q, want %s %q %q", got.State(), got.Cause(), got.Subject(), c.State, c.Cause, c.Subject)
				}
			})
		}
	}
}

func statementOf(t *testing.T, c authnCase) operators.Envelope {
	t.Helper()
	s := c.Statement
	or := func(v, d string) string {
		if v == "" {
			return d
		}
		return v
	}
	pt, ok := payloadTypes[or(s.PayloadType, "answer")]
	if !ok {
		t.Fatalf("payload type %q", s.PayloadType)
	}
	body := answerWith(t, s.Signer, map[string]any{"alg": or(s.Alg, "ed25519"), "keyId": idOf(or(s.BodyKeyID, s.Signer))}).Payload
	return sign(t, pt, body, s.Signer, idOf(or(s.KeyID, s.Signer)))
}

func TestEveryAuthorizerKindPassesTheConformanceCases(t *testing.T) {
	cases := readCases(t, authorizerCases, "operator-authorizer", func(c authzCase) caseHeader { return c.caseHeader })
	states := map[string]operators.PermState{"unavailable": operators.PermUnavailable, "denied": operators.PermDenied, "permitted": operators.PermPermitted}
	levels := map[string]trust.Level{"": trust.LevelNone, "read": trust.LevelRead, "operate": trust.LevelOperate, "admin": trust.LevelAdmin}
	for _, kind := range []operators.AuthorizerKind{operators.AuthorizerRoster} {
		for _, c := range cases {
			t.Run(string(kind)+"/"+c.Rule+"/"+c.Case, func(t *testing.T) {
				w := newWorld(t, standard())
				who := authenticated(t, w, c.Who)
				z, err := operators.NewAuthorizer(kind, w.roster, operators.Bounds{})
				if err != nil {
					t.Fatal(err)
				}
				ctx := t.Context()
				if c.Veto != "" {
					z, ctx = withCaseVeto(t, z, c)
				}
				if c.Revoke != "" {
					var keep []any
					for _, e := range standard() {
						if e.(map[string]any)["subject"] != c.Revoke {
							keep = append(keep, e)
						}
					}
					w.write(t, 2, keep)
				}
				got := z.Authorize(ctx, who, approve(c.Tenant, c.Project))
				want, ok := states[c.State]
				if !ok || got.State() != want || string(got.Cause()) != c.Cause || got.Grant().Level != levels[c.Level] {
					t.Errorf("= state %d cause %q level %s, want %s %q %q", got.State(), got.Cause(), got.Grant().Level, c.State, c.Cause, c.Level)
				}
			})
		}
	}
}

// withCaseVeto puts the case's veto after z, and returns the context the
// case authorizes under: one the veto cancels when the case says so.
func withCaseVeto(t *testing.T, z operators.Authorizer, c authzCase) (operators.Authorizer, context.Context) {
	t.Helper()
	v := &fakeVeto{}
	ctx := t.Context()
	switch c.Veto {
	case "none":
		v.answer = operators.NoObjection()
	case "objects":
		v.answer = operators.Objects()
	case "zero":
	case "silent":
		v.release = make(chan struct{})
		t.Cleanup(func() { close(v.release) })
	case "cancels":
		v.answer = operators.Objects()
		v.release = make(chan struct{})
		t.Cleanup(func() { close(v.release) })
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		v.cancel = cancel
	default:
		t.Fatalf("veto %q", c.Veto)
	}
	on, err := operators.ParseOnUnavailable(c.OnUnavailable)
	if err != nil {
		t.Fatal(err)
	}
	out, err := operators.WithVeto(z, v, on, operators.Bounds{Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return out, ctx
}
