package main

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/pkg/contract"
)

// parts are the members of a case, each as JSON text, so a test changes one
// and states the rest as the base below. external is the one a case may leave
// out, and "" leaves it out.
type parts struct {
	document, envelope, authorizedArgs, flow, options, loadedAt, decidedAt, expect, external string
}

// base is a valid READ of an order that a one-rule policy allows, fresh,
// decided at the load time.
func base() parts {
	return parts{
		document:       `{"apiVersion":"agent-policy/v1alpha1","bundle":{"id":"orders","version":"1","serial":1,"maxStaleSeconds":300},"rules":[` + allowReads + `]}`,
		envelope:       readEnvelope,
		authorizedArgs: `""`,
		flow:           `{"untrusted":false,"floor":"PUBLIC"}`,
		options:        `{"fail_open_read":false,"max_stale_seconds":600,"applicable":["cap_amount","redact_fields"]}`,
		loadedAt:       `"2026-09-11T12:00:00Z"`,
		decidedAt:      `"2026-09-11T12:00:00Z"`,
		expect:         `{"verdict":"ALLOW","action":"Execute","reason_codes":["RULE_ALLOW"]}`,
	}
}

const (
	allowReads   = `{"id":"allow-reads","effect":"ALLOW","when":{"action":{"effect":["READ"]}}}`
	readEnvelope = `{"schemaVersion":"1.0","requestId":"req-1","projectId":"proj-1","tenantId":"tenant-1","environment":"prod","occurredAt":"2026-09-11T11:59:00Z","principal":{"id":"user-1","tenantId":"tenant-1"},"agent":{"id":"agent-1"},"action":{"name":"orders.read","effect":"EFFECT_CLASS_READ","provider":"orders"},"resource":{"type":"order","id":"ord-1","tenantId":"tenant-1","environment":"prod"}}`
)

func (p parts) json() string {
	return `{"document":` + p.document +
		`,"envelope":` + p.envelope +
		`,"authorized_args":` + p.authorizedArgs +
		`,"flow":` + p.flow +
		`,"options":` + p.options +
		`,"loaded_at":` + p.loadedAt +
		`,"decided_at":` + p.decidedAt +
		external(p.external) +
		`,"expect":` + p.expect + `}`
}

func external(member string) string {
	if member == "" {
		return ""
	}
	return `,"external":` + member
}

// everyMember is the base with every member that has a second value set to
// it, so that a member read as its base value is caught.
func everyMember(t *testing.T) (parts, *testCase) {
	t.Helper()
	p := base()
	p.authorizedArgs = `"{\"amount\": 1}"`
	p.flow = `{"untrusted":true,"floor":"SECRET"}`
	p.options = `{"fail_open_read":true,"max_stale_seconds":45,"applicable":["cap_amount"]}`
	p.decidedAt = `"2026-09-11T12:00:01.5Z"`
	p.expect = `{"verdict":"INDETERMINATE","action":"AwaitApproval","reason_codes":["POLICY_STALE","RULE_ALLOW"]}`
	c, err := readCase([]byte(p.json()))
	if err != nil {
		t.Fatalf("readCase: %v", err)
	}
	return p, c
}

func TestReadCaseReadsTheInputs(t *testing.T) {
	p, c := everyMember(t)
	if string(c.document) != p.document {
		t.Errorf("document = %s, want the bytes as written", c.document)
	}
	if string(c.envelope) != readEnvelope {
		t.Errorf("envelope = %s, want the bytes as written", c.envelope)
	}
	if string(c.authorizedArgs) != `{"amount": 1}` {
		t.Errorf("authorizedArgs = %q", c.authorizedArgs)
	}
	if want := contract.NewFlowState(true, controlv1.Sensitivity_SENSITIVITY_SECRET); c.flow != want {
		t.Errorf("flow = %+v, want %+v", c.flow, want)
	}
	if c.options.Mode != controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE || !c.options.FailOpenRead ||
		c.options.MaxStale != 45*time.Second || !slices.Equal(c.options.Applicable, []string{"cap_amount"}) {
		t.Errorf("options = %+v", c.options)
	}
}

func TestReadCaseReadsTheTimesAndTheExpectation(t *testing.T) {
	_, c := everyMember(t)
	if want := time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC); !c.loadedAt.Equal(want) || c.loadedAt.Location() != time.UTC {
		t.Errorf("loadedAt = %v, want %v in UTC", c.loadedAt, want)
	}
	if want := time.Date(2026, time.September, 11, 12, 0, 1, 500000000, time.UTC); !c.decidedAt.Equal(want) {
		t.Errorf("decidedAt = %v, want %v", c.decidedAt, want)
	}
	want := outcome{
		verdict: controlv1.Verdict_VERDICT_INDETERMINATE,
		action:  core.AwaitApproval,
		codes:   []string{"POLICY_STALE", "RULE_ALLOW"},
	}
	if c.expect.verdict != want.verdict || c.expect.action != want.action || !slices.Equal(c.expect.codes, want.codes) {
		t.Errorf("expect = %+v, want %+v", c.expect, want)
	}
}

// Each answer a case may state is read as the kernel's own value for it, and
// a case that states none asks nothing.
func TestReadCaseReadsTheExternalAnswer(t *testing.T) {
	for member, want := range map[string]core.External{
		"":                          {},
		`"allowed"`:                 core.ExternalAllowed(),
		`"denied"`:                  core.ExternalDenied(),
		`"denied_with_obligations"`: core.ExternalDeniedObligations(),
		`"timeout"`:                 core.ExternalTimeout(),
		`"unavailable"`:             core.ExternalUnavailable(),
		`"answer_refused"`:          core.ExternalAnswerRefused(),
	} {
		p := base()
		p.external = member
		c, err := readCase([]byte(p.json()))
		if err != nil {
			t.Errorf("%s: readCase: %v", member, err)
			continue
		}
		if c.external != want {
			t.Errorf("%s: external = %v, want %v", member, c.external, want)
		}
	}
}

func TestReadCaseReadsAnEmptyArgumentsStringAsNone(t *testing.T) {
	c, err := readCase([]byte(base().json()))
	if err != nil {
		t.Fatalf("readCase: %v", err)
	}
	if c.authorizedArgs != nil {
		t.Errorf("authorizedArgs = %q, want nil", c.authorizedArgs)
	}
	if len(c.expect.codes) != 1 {
		t.Errorf("codes = %q, want one", c.expect.codes)
	}
}

func TestReadCaseReadsAnEmptyCodeListAsNone(t *testing.T) {
	p := base()
	p.expect = `{"verdict":"ALLOW","action":"Execute","reason_codes":[]}`
	c, err := readCase([]byte(p.json()))
	if err != nil {
		t.Fatalf("readCase: %v", err)
	}
	if len(c.expect.codes) != 0 {
		t.Errorf("codes = %q, want none", c.expect.codes)
	}
}

// Each refusal names the member it is about, and each input differs from
// the base in that one member.
func TestReadCaseRefusals(t *testing.T) {
	for name, tc := range map[string]struct {
		raw  string
		want string // named in the error
	}{
		"not an object":                    {`[]`, "object"},
		"trailing data":                    {base().json() + `{}`, "after"},
		"missing member":                   {strings.Replace(base().json(), `"decided_at":"2026-09-11T12:00:00Z",`, "", 1), "decided_at"},
		"unknown member":                   {strings.Replace(base().json(), `"expect":`, `"note":"x","expect":`, 1), "note"},
		"a member spelled in another case": {strings.Replace(base().json(), `"expect":`, `"Expect":`, 1), "Expect"},
		"a member twice":                   {strings.Replace(base().json(), `"expect":`, `"loaded_at":"2026-09-11T12:00:00Z","expect":`, 1), "loaded_at"},
		"document not an object":           {func() string { p := base(); p.document = `"x"`; return p.json() }(), "document"},
		"envelope not an object":           {func() string { p := base(); p.envelope = `[]`; return p.json() }(), "envelope"},
		"arguments not a string":           {func() string { p := base(); p.authorizedArgs = `{}`; return p.json() }(), "authorized_args"},
		"flow unknown member":              {func() string { p := base(); p.flow = `{"untrusted":false,"floor":"PUBLIC","x":1}`; return p.json() }(), "flow"},
		"flow missing member":              {func() string { p := base(); p.flow = `{"untrusted":false}`; return p.json() }(), "floor"},
		"floor with the prefix": {func() string {
			p := base()
			p.flow = `{"untrusted":false,"floor":"SENSITIVITY_PUBLIC"}`
			return p.json()
		}(), "floor"},
		"floor undeclared":     {func() string { p := base(); p.flow = `{"untrusted":false,"floor":"TOP_SECRET"}`; return p.json() }(), "floor"},
		"floor lowercase":      {func() string { p := base(); p.flow = `{"untrusted":false,"floor":"public"}`; return p.json() }(), "floor"},
		"untrusted not a bool": {func() string { p := base(); p.flow = `{"untrusted":"no","floor":"PUBLIC"}`; return p.json() }(), "untrusted"},
		"stale budget a float": {func() string {
			p := base()
			p.options = `{"fail_open_read":false,"max_stale_seconds":1.5,"applicable":[]}`
			return p.json()
		}(), "max_stale_seconds"},
		"stale budget a string": {func() string {
			p := base()
			p.options = `{"fail_open_read":false,"max_stale_seconds":"600","applicable":[]}`
			return p.json()
		}(), "max_stale_seconds"},
		"stale budget past a Duration": {func() string {
			p := base()
			p.options = `{"fail_open_read":false,"max_stale_seconds":9223372037,"applicable":[]}`
			return p.json()
		}(), "max_stale_seconds"},
		"applicable not a list": {func() string {
			p := base()
			p.options = `{"fail_open_read":false,"max_stale_seconds":600,"applicable":"cap_amount"}`
			return p.json()
		}(), "applicable"},
		"loaded_at with an offset": {func() string { p := base(); p.loadedAt = `"2026-09-11T14:00:00+02:00"`; return p.json() }(), "loaded_at"},
		"loaded_at not a time":     {func() string { p := base(); p.loadedAt = `"2026-09-11"`; return p.json() }(), "loaded_at"},
		"decided_at a number":      {func() string { p := base(); p.decidedAt = `1757592000`; return p.json() }(), "decided_at"},
		"verdict with the prefix": {func() string {
			p := base()
			p.expect = `{"verdict":"VERDICT_ALLOW","action":"Execute","reason_codes":[]}`
			return p.json()
		}(), "verdict"},
		"verdict unspecified": {func() string {
			p := base()
			p.expect = `{"verdict":"UNSPECIFIED","action":"Execute","reason_codes":[]}`
			return p.json()
		}(), "verdict"},
		"verdict lowercase": {func() string {
			p := base()
			p.expect = `{"verdict":"allow","action":"Execute","reason_codes":[]}`
			return p.json()
		}(), "verdict"},
		"action unknown": {func() string {
			p := base()
			p.expect = `{"verdict":"ALLOW","action":"execute","reason_codes":[]}`
			return p.json()
		}(), "action"},
		"codes not a list": {func() string {
			p := base()
			p.expect = `{"verdict":"ALLOW","action":"Execute","reason_codes":"RULE_ALLOW"}`
			return p.json()
		}(), "reason_codes"},
		"external not an answer":     {func() string { p := base(); p.external = `"allow"`; return p.json() }(), "external: want"},
		"external in another case":   {func() string { p := base(); p.external = `"ALLOWED"`; return p.json() }(), "external: want"},
		"external not asked by name": {func() string { p := base(); p.external = `"not_asked"`; return p.json() }(), "external: want"},
		"external a bool":            {func() string { p := base(); p.external = `true`; return p.json() }(), "external: want"},
		"external null":              {func() string { p := base(); p.external = `null`; return p.json() }(), "external: want"},
		"external twice": {func() string {
			p := base()
			p.external = `"allowed","external":"denied"`
			return p.json()
		}(), "external: given twice"},
		"expect unknown member": {func() string {
			p := base()
			p.expect = `{"verdict":"ALLOW","action":"Execute","reason_codes":[],"rule_ids":[]}`
			return p.json()
		}(), "rule_ids"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readCase([]byte(tc.raw)); err == nil {
				t.Fatal("readCase accepted the case")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal %q does not name %q", err, tc.want)
			}
		})
	}
}

// A refusal of the case format is never a refusal the kernel would classify:
// it stops the case before anything is signed.
func TestReadCaseRefusalIsNotAKernelRefusal(t *testing.T) {
	_, err := readCase([]byte(`{}`))
	if err == nil {
		t.Fatal("readCase accepted an empty object")
	}
	if errors.Is(err, contract.ErrMissingField) {
		t.Errorf("a case refusal reuses the contract's sentinel: %v", err)
	}
}
