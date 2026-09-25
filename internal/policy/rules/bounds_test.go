package rules

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Each bound gets the input at which it decides: the largest accepted and the
// smallest refused, which would be accepted too if the bound were gone.

func TestDocumentSizeBound(t *testing.T) {
	t.Parallel()
	base := docWithRules(denyRuleJSON)
	pad := func(n int) string { return base + strings.Repeat(" ", n-len(base)) }
	atLimit, over := pad(1048576), pad(1048577)
	if len(atLimit) != 1<<20 || len(over) != 1<<20+1 {
		t.Fatalf("padded to %d and %d bytes", len(atLimit), len(over))
	}
	expectAccepted(t, atLimit)
	expectRefusal(t, over, refusal{ErrTooLarge, "", ""})
	// The bound runs before anything reads the bytes.
	expectRefusal(t, strings.Repeat("[", 1<<20+1), refusal{ErrTooLarge, "", ""})
}

func TestRuleCountBound(t *testing.T) {
	t.Parallel()
	rules := func(n int) string {
		items := make([]string, n)
		for i := range items {
			items[i] = `{"id":"r` + strconv.Itoa(i) + `","effect":"DENY","when":{"action":{"name":["a"]}}}`
		}
		return docWithRules(items...)
	}
	if got := len(expectAccepted(t, rules(4096)).Rules); got != 4096 {
		t.Fatalf("%d rules read from 4096", got)
	}
	over := rules(4097)
	if len(over) >= 1<<20 {
		t.Fatalf("4097 rules take %d bytes, so the size bound would decide this case", len(over))
	}
	expectRefusal(t, over, refusal{ErrTooLarge, "rules", ""})
}

func TestValuesPerListBound(t *testing.T) {
	t.Parallel()
	distinct := func(i int) string { return quote("v" + strconv.Itoa(i)) }
	same := func(v string) func(int) string { return func(int) string { return v } }
	list := func(n int, value func(int) string) string {
		items := make([]string, n)
		for i := range items {
			items[i] = value(i)
		}
		return strings.Join(items, ",")
	}
	const w = "rules[0].when."
	positions := []struct {
		fragment, field string
		value           func(int) string
	}{
		{`{"principal":{"id":[%s]}}`, w + "principal.id", distinct},
		{`{"principal":{"type":[%s]}}`, w + "principal.type", distinct},
		{`{"principal":{"authnStrength":[%s]}}`, w + "principal.authnStrength", distinct},
		{`{"principal":{"tenantId":[%s]}}`, w + "principal.tenantId", distinct},
		{`{"principal":{"attributes":{"k":[%s]}}}`, w + "principal.attributes[0]", distinct},
		{`{"agent":{"id":[%s]}}`, w + "agent.id", distinct},
		{`{"agent":{"framework":[%s]}}`, w + "agent.framework", distinct},
		{`{"action":{"name":[%s]}}`, w + "action.name", distinct},
		{`{"action":{"provider":[%s]}}`, w + "action.provider", distinct},
		{`{"action":{"protocol":[%s]}}`, w + "action.protocol", distinct},
		{`{"action":{"kind":[%s]}}`, w + "action.kind", distinct},
		{`{"action":{"effect":[%s]}}`, w + "action.effect", same(`"READ"`)},
		{`{"resource":{"type":[%s]}}`, w + "resource.type", distinct},
		{`{"resource":{"id":[%s]}}`, w + "resource.id", distinct},
		{`{"resource":{"tenantId":[%s]}}`, w + "resource.tenantId", distinct},
		{`{"resource":{"environment":[%s]}}`, w + "resource.environment", distinct},
		{`{"resource":{"labels":{"k":[%s]}}}`, w + "resource.labels[0]", distinct},
		{`{"destination":{"trustZone":[%s]}}`, w + "destination.trustZone", same(`"PARTNER"`)},
		{`{"destination":{"host":[%s]}}`, w + "destination.host", distinct},
		{`{"delegation":{"scopes":[%s]}}`, w + "delegation.scopes", distinct},
	}
	for _, pos := range positions {
		t.Run(pos.field, func(t *testing.T) {
			t.Parallel()
			expectAccepted(t, docWithWhen(strings.Replace(pos.fragment, "%s", list(64, pos.value), 1)))
			expectRefusal(t, docWithWhen(strings.Replace(pos.fragment, "%s", list(65, pos.value), 1)),
				refusal{ErrTooLarge, pos.field, "r"})
		})
	}
}

func TestKeysPerMapBound(t *testing.T) {
	t.Parallel()
	entries := func(n int, value string) string {
		items := make([]string, n)
		for i := range items {
			items[i] = `"k` + strconv.Itoa(i) + `":` + value
		}
		return "{" + strings.Join(items, ",") + "}"
	}
	positions := []struct {
		doc          func(string) string
		field, value string
	}{
		{func(m string) string { return docWithWhen(`{"principal":{"attributes":` + m + `}}`) }, "rules[0].when.principal.attributes", `["v"]`},
		{func(m string) string { return docWithWhen(`{"resource":{"labels":` + m + `}}`) }, "rules[0].when.resource.labels", `["v"]`},
		{func(m string) string {
			return docWithRules(`{"id":"r","effect":"REQUIRE_APPROVAL","obligations":[{"type":"cap_amount","params":` + m + `}],"when":{"action":{"name":["x"]}}}`)
		}, "rules[0].obligations[0].params", `"v"`},
	}
	for _, pos := range positions {
		t.Run(pos.field, func(t *testing.T) {
			t.Parallel()
			expectAccepted(t, pos.doc(entries(32, pos.value)))
			expectRefusal(t, pos.doc(entries(33, pos.value)), refusal{ErrTooLarge, pos.field, "r"})
		})
	}
}

func TestObligationsPerRuleBound(t *testing.T) {
	t.Parallel()
	obligations := func(n int) string {
		items := make([]string, n)
		for i := range items {
			items[i] = `{"type":"emit_alert"}`
		}
		return docWithRules(`{"id":"r","effect":"REQUIRE_APPROVAL","obligations":[` + strings.Join(items, ",") + `],"when":{"action":{"name":["x"]}}}`)
	}
	if got := len(expectAccepted(t, obligations(8)).Rules[0].Obligations); got != 8 {
		t.Fatalf("%d obligations read from 8", got)
	}
	expectRefusal(t, obligations(9), refusal{ErrTooLarge, "rules[0].obligations", "r"})
}

// TestStaleBudgetBound: the budget becomes a time.Duration, which holds at
// most 9223372036 whole seconds; past that, the conversion would wrap into a
// different budget than the one signed.
func TestStaleBudgetBound(t *testing.T) {
	t.Parallel()
	if math.MaxInt64/int64(time.Second) != 9223372036 {
		t.Fatal("the literals below no longer name the largest budget a Duration holds")
	}
	budget := func(s string) string {
		return docWithBundle(`{"id":"p","version":"1","serial":1,"maxStaleSeconds":` + s + `}`)
	}
	if got := expectAccepted(t, budget("9223372036")).Bundle.MaxStaleSeconds; got != 9223372036 {
		t.Fatalf("budget read as %d", got)
	}
	expectRefusal(t, budget("9223372037"), refusal{ErrTooLarge, "bundle.maxStaleSeconds", ""})
	// The serial has no bound of its own inside the canonical form's range.
	serial := docWithBundle(`{"id":"p","version":"1","serial":9007199254740991,"maxStaleSeconds":1}`)
	if got := expectAccepted(t, serial).Bundle.Serial; got != 9007199254740991 {
		t.Fatalf("serial read as %d", got)
	}
}
