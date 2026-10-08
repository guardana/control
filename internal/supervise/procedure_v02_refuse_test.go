package supervise_test

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/guardana/control/internal/supervise"
)

const firstException = `{"id": "manual_refund_approved", "waives": "STEP_OUTSIDE_PROCEDURE", "tool": "refund_manual", ` +
	`"upstream": "pay", "condition": {"approval": "granted"}}`

// withException is the 0.2 fixture with its first exception replaced by e.
func withException(t *testing.T, e string) string {
	t.Helper()
	return v02With(t, firstException, e)
}

func exception(rule, target, condition string) string {
	return `{"id": "x", "waives": "` + rule + `", ` + target + `, "condition": ` + condition + `}`
}

const (
	onTool     = `"tool": "refund_manual", "upstream": "pay"`
	onStep     = `"step": "lookup"`
	approval   = `{"approval": "granted"}`
	stepFailed = `{"step_failed": "lookup"}`
	reasonDeny = `{"reason": "RULE_DENY"}`
)

func TestASchema02ProcedureIsRefused(t *testing.T) {
	cases := map[string]string{
		"another schema version":         v02With(t, `"schema_version": "0.2"`, `"schema_version": "0.3"`),
		"no bindings":                    v02With(t, `"bindings": {"order": "shop_order", "customer": "crm_customer"},`, ``),
		"bindings not an object":         v02With(t, `"bindings": {"order": "shop_order", "customer": "crm_customer"}`, `"bindings": []`),
		"a binding named twice":          v02With(t, `"customer": "crm_customer"`, `"order": "crm_customer"`),
		"an empty resource type":         v02With(t, `"customer": "crm_customer"`, `"customer": ""`),
		"a resource type not a string":   v02With(t, `"customer": "crm_customer"`, `"customer": 1`),
		"a binding name with a space":    v02With(t, `"customer": "crm_customer"`, `" customer": "crm_customer"`),
		"a step without binds":           v02With(t, `"required": true, "binds": ["order"]}`, `"required": true}`),
		"an allow entry without binds":   v02With(t, `"observed_as": ["search_docs"], "binds": []`, `"observed_as": ["search_docs"]`),
		"binds not an array":             v02With(t, `"binds": ["order"]`, `"binds": "order"`),
		"a tool in two bindings":         v02With(t, `"binds": ["order"]`, `"binds": ["order", "customer"]`),
		"an allowed tool in two":         v02With(t, `"binds": []`, `"binds": ["order", "customer"]`),
		"binds naming one binding twice": v02With(t, `"binds": ["order"]`, `"binds": ["order", "order"]`),
		"binds naming no binding":        v02With(t, `"binds": ["order"]`, `"binds": ["invoice"]`),
		"an allowed tool binding none":   v02With(t, `"binds": []`, `"binds": ["invoice"]`),
		"no exceptions":                  strings.Replace(withoutExceptions(t, v02Doc(t)), `"exceptions": [], `, ``, 1),
		"no children":                    v02With(t, `"children": "inherit",`, ``),
		"an unknown children mode":       v02With(t, `"children": "inherit"`, `"children": "both"`),
		"children in another case":       v02With(t, `"children": "inherit"`, `"children": "Inherit"`),
		"children not a string":          v02With(t, `"children": "inherit"`, `"children": true`),
		"a rule 0.2 knows missing":       withoutRules(t, v02Doc(t), "EXCEPTION_TAKEN"),
		"another 0.2 rule missing":       withoutRules(t, v02Doc(t), "DENIED_ACTION_RETRIED_AROUND"),
		"only the 0.1 rules": withoutRules(t, v02Doc(t), "RESOURCE_OUTSIDE_RUN", "DENIED_ACTION_RETRIED_ARGUMENTS",
			"DENIED_ACTION_RETRIED_RESOURCE", "DENIED_ACTION_RETRIED_AROUND", "EXCEPTION_TAKEN"),
		"an unknown rule configured": v02With(t, `"rules": {`, `"rules": {"NOT_A_RULE": {"severity": "info", "escalation": "inform"}, `),
		"a 0.1 document configuring a 0.2 rule": docWith(t, `"REPEATED_DENIAL":`,
			`"RESOURCE_OUTSIDE_RUN":{"severity":"high","escalation":"alert"},"REPEATED_DENIAL":`),
		"an unknown severity on a new rule": v02With(t, `"EXCEPTION_TAKEN": {"severity": "info"`, `"EXCEPTION_TAKEN": {"severity": "none"`),

		"two exceptions with one id": v02With(t, `"id": "lookup_denied"`, `"id": "manual_refund_approved"`),
		"an exception without id":    withException(t, `{"waives": "STEP_OUTSIDE_PROCEDURE", `+onTool+`, "condition": `+approval+`}`),
		"an exception member unknown": withException(t, `{"id": "x", "waives": "STEP_OUTSIDE_PROCEDURE", `+onTool+
			`, "condition": `+approval+`, "note": "y"}`),
		"an exception waiving no rule":   withException(t, exception("NOT_A_RULE", onStep, approval)),
		"an exception waiving lowercase": withException(t, exception("step_out_of_order", onStep, approval)),
		"an exception on no step":        withException(t, `{"id": "x", "waives": "STEP_OUT_OF_ORDER", "condition": `+approval+`}`),
		"an exception on a step and a tool": withException(t, exception("STEP_OUT_OF_ORDER",
			onStep+`, `+onTool, approval)),
		"an exception on a tool without upstream": withException(t, exception("STEP_OUTSIDE_PROCEDURE",
			`"tool": "refund_manual"`, approval)),
		"an exception on an upstream alone": withException(t, exception("STEP_OUTSIDE_PROCEDURE", `"upstream": "pay"`, approval)),
		"an exception on an unknown step":   withException(t, exception("STEP_OUT_OF_ORDER", `"step": "ghost"`, approval)),
		"a step outside the procedure is no step": withException(t, exception("STEP_OUTSIDE_PROCEDURE",
			onStep, approval)),
		"a step rule waived for a tool": withException(t, exception("STEP_OUT_OF_ORDER", onTool, approval)),
		"an outside tool a step holds": withException(t, exception("STEP_OUTSIDE_PROCEDURE",
			`"tool": "get_order", "upstream": "shop"`, approval)),
		"an outside tool allowed": withException(t, exception("STEP_OUTSIDE_PROCEDURE",
			`"tool": "search_docs", "upstream": "docs"`, approval)),
		"a step that failed is no step": withException(t, exception("STEP_OUT_OF_ORDER", onStep, `{"step_failed": "ghost"}`)),
		"a reason not registered":       withException(t, exception("STEP_OUT_OF_ORDER", onStep, `{"reason": "NOT_A_REASON"}`)),
		"an approval not granted":       withException(t, exception("STEP_OUT_OF_ORDER", onStep, `{"approval": "denied"}`)),
		"two conditions": withException(t, exception("STEP_OUT_OF_ORDER", onStep,
			`{"approval": "granted", "step_failed": "lookup"}`)),
		"no condition":                 withException(t, exception("STEP_OUT_OF_ORDER", onStep, `{}`)),
		"a condition unknown":          withException(t, exception("STEP_OUT_OF_ORDER", onStep, `{"weekday": "monday"}`)),
		"a condition a string":         withException(t, exception("STEP_OUT_OF_ORDER", onStep, `"approval"`)),
		"over the exceptions bound":    v02With(t, `"exceptions": [`, `"exceptions": [`+extraExceptions(125)),
		"over the bindings bound":      v02With(t, `"bindings": {`, `"bindings": {`+extraBindings(63)),
		"a 0.1 step that binds":        docWith(t, `"required":true}`, `"required":true,"binds":[]}`),
		"a 0.1 document with children": docWith(t, `"version":"1",`, `"version":"1","children":"inherit",`),
	}
	// Only STEP_OUTSIDE_PROCEDURE and the three absence rules take an
	// exception, and of those only the one that may stop is limited to an
	// approval, which the agent cannot grant itself.
	for _, rule := range []string{"REPEATED_DENIAL", "DEADLINE_EXCEEDED", "RESOURCE_OUTSIDE_RUN",
		"DENIED_ACTION_RETRIED_ARGUMENTS", "DENIED_ACTION_RETRIED_RESOURCE", "DENIED_ACTION_RETRIED_AROUND", "EXCEPTION_TAKEN"} {
		for _, target := range []string{onStep, onTool} {
			for _, cond := range []string{approval, stepFailed, reasonDeny} {
				cases["an exception waiving "+rule+" on "+target+" when "+cond] = withException(t, exception(rule, target, cond))
			}
		}
	}
	for _, cond := range []string{stepFailed, reasonDeny} {
		cases["STEP_OUTSIDE_PROCEDURE waived when "+cond] = withException(t, exception("STEP_OUTSIDE_PROCEDURE", onTool, cond))
	}
	// A step skipped has no call whose trail could hold an approval.
	cases["REQUIRED_STEP_SKIPPED waived on an approval"] = withException(t, exception("REQUIRED_STEP_SKIPPED", onStep, approval))
	for name, doc := range cases {
		p, err := supervise.ReadProcedure([]byte(doc))
		if !errors.Is(err, supervise.ErrProcedure) || p != nil {
			t.Errorf("%s: ReadProcedure = %v, %v; want ErrProcedure", name, p, err)
		}
	}
}

// TestSchema02AdmitsWhatItsRefusalsBorderOn is each refusal's counterpart:
// the input one step inside the line the refusal draws.
func TestSchema02AdmitsWhatItsRefusalsBorderOn(t *testing.T) {
	cases := map[string]string{
		"exceptions at the bound": v02With(t, `"exceptions": [`, `"exceptions": [`+extraExceptions(124)),
		"bindings at the bound":   v02With(t, `"bindings": {`, `"bindings": {`+extraBindings(62)),
		"no binding and no binds": v02With(t, `"bindings": {"order": "shop_order", "customer": "crm_customer"}`, `"bindings": {}`,
			`"binds": ["order"]`, `"binds": []`, `"binds": ["order"]`, `"binds": []`,
			`"binds": ["customer"]`, `"binds": []`, `"binds": ["customer"]`, `"binds": []`),
		"no exception":                                 withoutExceptions(t, v02Doc(t)),
		"no max_denials and no deadline":               v02With(t, `"max_denials": 4,`, ``, `"deadline_seconds": 600,`, ``),
		"STEP_OUTSIDE_PROCEDURE waived on an approval": withException(t, exception("STEP_OUTSIDE_PROCEDURE", onTool, approval)),
		"an outside tool on another upstream": withException(t, exception("STEP_OUTSIDE_PROCEDURE",
			`"tool": "get_order", "upstream": "pay"`, approval)),
	}
	for _, rule := range []string{"REQUIRED_STEP_SKIPPED", "STEP_OUT_OF_ORDER", "CONTINUED_AFTER_FAILURE"} {
		for _, cond := range []string{approval, stepFailed, reasonDeny} {
			if rule != "REQUIRED_STEP_SKIPPED" || cond != approval {
				cases[rule+" waived when "+cond] = withException(t, exception(rule, onStep, cond))
			}
		}
	}
	for name, doc := range cases {
		if _, err := supervise.ReadProcedure([]byte(doc)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func extraExceptions(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(`{"id": "extra` + strconv.Itoa(i) + `", "waives": "STEP_OUTSIDE_PROCEDURE", "tool": "t` +
			strconv.Itoa(i) + `", "upstream": "u", "condition": {"approval": "granted"}}, `)
	}
	return b.String()
}

func extraBindings(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString(`"extra` + strconv.Itoa(i) + `": "t", `)
	}
	return b.String()
}

// withoutRules is doc with the named rules taken out of its rules member.
func withoutRules(t *testing.T, doc string, ids ...string) string {
	t.Helper()
	for _, id := range ids {
		cut := regexp.MustCompile(`,\s*"` + id + `": \{[^}]*\}`)
		if !cut.MatchString(doc) {
			t.Fatalf("0.2 fixture configures no %s after another rule", id)
		}
		doc = cut.ReplaceAllString(doc, "")
	}
	return doc
}

// withoutExceptions is doc with an empty exceptions member.
func withoutExceptions(t *testing.T, doc string) string {
	t.Helper()
	i, j := strings.Index(doc, `"exceptions": [`), strings.Index(doc, `"children"`)
	if i < 0 || j < i {
		t.Fatal("0.2 fixture holds no exceptions before children")
	}
	return doc[:i] + `"exceptions": [], ` + doc[j:]
}
