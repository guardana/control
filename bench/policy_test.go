// Benchmark of the kernel's Decide over documents of 10, 100 and 1000 rules,
// and over 100 rules that all carry obligations, the union of which a decision
// has to clone (bounded only by the document). Every row is checked before it
// is timed: Decide is at its fastest on a request it refuses at admission, so
// a row whose envelope stopped validating would report a fast refusal rather
// than a decision.
package bench_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
)

// row is one measured document: rules of it in the document, how many of
// them match the envelope, and what the decision has to be.
type row struct {
	name    string
	rules   int
	matched int
	verdict controlv1.Verdict
	action  core.EnforcementAction
	// obligations is the count on the decision: two per matched rule when
	// every rule carries them.
	obligations int
}

func rows() []row {
	return []row{
		{"rules=10", 10, 5, controlv1.Verdict_VERDICT_ALLOW, core.Execute, 0},
		{"rules=100", 100, 50, controlv1.Verdict_VERDICT_ALLOW, core.Execute, 0},
		{"rules=1000", 1000, 500, controlv1.Verdict_VERDICT_ALLOW, core.Execute, 0},
		{"obligations=100", 100, 100, controlv1.Verdict_VERDICT_ALLOW_WITH_OBLIGATIONS, core.ExecuteWithObligations, 200},
	}
}

// documentFor writes a v1alpha1 document of r.rules rules. In the ALLOW rows
// the even rules match every READ and the odd ones name an action the
// envelope does not carry, so half the rules match and half fail. In the
// obligations row every rule matches and carries two obligations the kernel
// can apply, each distinct: the union keeps one copy of equal obligations,
// so equal ones would measure a union of two.
func documentFor(r row) []byte {
	rules := make([]string, 0, r.rules)
	for i := range r.rules {
		switch {
		case r.obligations > 0:
			rules = append(rules, fmt.Sprintf(`{"id":"capped-%d","effect":"ALLOW_WITH_OBLIGATIONS","obligations":[{"type":"redact_fields","params":{"fields":"field-%d"}},{"type":"cap_amount","params":{"max":"%d"}}],"when":{"action":{"effect":["READ"]}}}`, i, i, 1000+i))
		case i%2 == 0:
			rules = append(rules, fmt.Sprintf(`{"id":"allow-%d","effect":"ALLOW","when":{"action":{"effect":["READ"]}}}`, i))
		default:
			rules = append(rules, fmt.Sprintf(`{"id":"deny-%d","effect":"DENY","when":{"action":{"name":["archive-%d"]}}}`, i, i))
		}
	}
	return []byte(`{"apiVersion":"agent-policy/v1alpha1","bundle":{"id":"bench","version":"1","serial":1,"maxStaleSeconds":3600},"rules":[` +
		strings.Join(rules, ",") + `]}`)
}

func envelope() *controlv1.ActionEnvelope {
	return &controlv1.ActionEnvelope{
		SchemaVersion: "1.0",
		RequestId:     "req-bench",
		ProjectId:     "proj-1",
		TenantId:      "tenant-1",
		Environment:   "prod",
		OccurredAt:    timestamppb.New(time.Date(2026, time.September, 11, 12, 0, 0, 0, time.UTC)),
		Principal:     &controlv1.Principal{Id: "user-1", TenantId: "tenant-1"},
		Agent:         &controlv1.Agent{Id: "agent-1"},
		Action:        &controlv1.Action{Name: "orders.read", Effect: controlv1.EffectClass_EFFECT_CLASS_READ, Provider: "orders"},
		Resource:      &controlv1.Resource{Type: "order", Id: "ord-1", TenantId: "tenant-1", Environment: "prod"},
	}
}

// decideSubject is one row signed, loaded and ready to decide.
type decideSubject struct {
	row    row
	kernel *core.Kernel
	snap   *policy.Snapshot
	req    core.Request
}

// subjects builds every row, and fails on anything the measured path would
// refuse: a document the loader rejects would otherwise be measured as the
// no-snapshot branch.
func decideSubjects(tb testing.TB) []decideSubject {
	tb.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, ed25519.SeedSize))
	keys := bundle.Keyring{"k1": ed25519.PublicKey(slices.Clone(key[ed25519.SeedSize:]))}
	opts := core.Options{
		Mode:       controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE,
		MaxStale:   time.Hour,
		Applicable: []string{"redact_fields", "cap_amount"},
	}
	// The real clock: the latency field is part of what a decision costs.
	// Every snapshot is confirmed now, so no row is stale until an hour
	// passes, which the guard test would show.
	k, err := core.New(opts, time.Now, func() string { return "decision" })
	if err != nil {
		tb.Fatalf("New: %v", err)
	}
	loadedAt := time.Now()
	out := make([]decideSubject, 0, len(rows()))
	for _, r := range rows() {
		b, err := policy.Sign(documentFor(r), key, "k1")
		if err != nil {
			tb.Fatalf("%s: Sign: %v", r.name, err)
		}
		snap, err := policy.Load(b, keys, loadedAt)
		if err != nil {
			tb.Fatalf("%s: Load: %v", r.name, err)
		}
		out = append(out, decideSubject{row: r, kernel: k, snap: snap, req: core.Request{Envelope: envelope()}})
	}
	return out
}

// series is prefix0, prefix<step> and so on, n names.
func series(prefix string, step, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i*step)
	}
	return out
}

// cappedObligations spells the two obligations of each of n capped rules.
func cappedObligations(n int) []string {
	out := make([]string, 0, 2*n)
	for i := range n {
		out = append(out, fmt.Sprintf("redact_fields{fields=field-%d}", i), fmt.Sprintf("cap_amount{max=%d}", 1000+i))
	}
	return out
}

// wantDecided is what each row's decision names: its rules and obligations.
var wantDecided = map[string]struct{ rules, obligations []string }{
	"rules=10":        {rules: []string{"allow-0", "allow-2", "allow-4", "allow-6", "allow-8"}},
	"rules=100":       {rules: series("allow-", 2, 50)},
	"rules=1000":      {rules: series("allow-", 2, 500)},
	"obligations=100": {rules: series("capped-", 1, 100), obligations: cappedObligations(100)},
}

// spelled is an obligation as type{key=value,...}, its keys sorted, and
// marked when advisory.
func spelled(o *controlv1.Obligation) string {
	keys := slices.Sorted(maps.Keys(o.GetParams()))
	params := make([]string, len(keys))
	for i, k := range keys {
		params[i] = k + "=" + o.GetParams()[k]
	}
	s := o.GetType() + "{" + strings.Join(params, ",") + "}"
	if o.GetAdvisory() {
		s += " advisory"
	}
	return s
}

// decidedDefects names where d's rules and obligations differ from the
// row's, each list compared whole and in order.
func decidedDefects(name string, d *controlv1.Decision) []string {
	want := wantDecided[name]
	obligations := make([]string, len(d.GetObligations()))
	for i, o := range d.GetObligations() {
		obligations[i] = spelled(o)
	}
	var out []string
	if diff := firstDiff(d.GetPolicyRuleIds(), want.rules); diff != "" {
		out = append(out, "rules: "+diff)
	}
	if diff := firstDiff(obligations, want.obligations); diff != "" {
		out = append(out, "obligations: "+diff)
	}
	return out
}

// firstDiff names the first place got and want differ, empty when equal.
func firstDiff(got, want []string) string {
	for i := range min(len(got), len(want)) {
		if got[i] != want[i] {
			return fmt.Sprintf("[%d] is %q, want %q", i, got[i], want[i])
		}
	}
	if len(got) != len(want) {
		return fmt.Sprintf("%d of them, want %d", len(got), len(want))
	}
	return ""
}

// TestBenchmarkedDecideSucceeds holds every row to the decision it is meant
// to measure: the verdict and action, every matched rule named once, and the
// whole obligation union on the decision with its parameters. It runs under
// `go test ./bench/`, which scripts/bench.sh runs before it records anything.
func TestBenchmarkedDecideSucceeds(t *testing.T) {
	for _, s := range decideSubjects(t) {
		t.Run(s.row.name, func(t *testing.T) {
			out := s.kernel.Decide(context.Background(), s.req, s.snap)
			d := out.Decision
			if d.GetVerdict() != s.row.verdict || out.Action != s.row.action {
				t.Errorf("verdict %s, action %d, codes %q; want %s and %d", d.GetVerdict(), out.Action, d.GetReasonCodes(), s.row.verdict, s.row.action)
			}
			if got := len(d.GetPolicyRuleIds()); got != s.row.matched {
				t.Errorf("%d rules named, want %d", got, s.row.matched)
			}
			if got := len(d.GetObligations()); got != s.row.obligations {
				t.Errorf("%d obligations, want %d", got, s.row.obligations)
			}
			for _, defect := range decidedDefects(s.row.name, d) {
				t.Error(defect)
			}
			if d.GetPolicyFreshness() != controlv1.PolicyFreshness_POLICY_FRESHNESS_FRESH {
				t.Errorf("freshness %s: the snapshot is stale against the real clock, and the row measures the stale branch", d.GetPolicyFreshness())
			}
		})
	}
}

// TestDecidedDefectsSeeAWrongDecision holds the comparison above to failing
// on a decision with the right counts and the wrong content: one rule named
// twice in place of another, or an obligation with another parameter.
func TestDecidedDefectsSeeAWrongDecision(t *testing.T) {
	for _, s := range decideSubjects(t) {
		if s.row.obligations == 0 {
			continue
		}
		d := s.kernel.Decide(context.Background(), s.req, s.snap).Decision
		if defects := decidedDefects(s.row.name, d); len(defects) != 0 {
			t.Fatalf("the decision as made: %q", defects)
		}
		repeated := proto.CloneOf(d)
		repeated.PolicyRuleIds[1] = repeated.PolicyRuleIds[0]
		reparamed := proto.CloneOf(d)
		reparamed.Obligations[3].Params["max"] = "1002"
		advisory := proto.CloneOf(d)
		advisory.Obligations[0].Advisory = true
		for name, wrong := range map[string]*controlv1.Decision{"a rule named twice": repeated, "another parameter": reparamed, "an advisory obligation": advisory} {
			if defects := decidedDefects(s.row.name, wrong); len(defects) != 1 {
				t.Errorf("%s: %d defects, want 1: %q", name, len(defects), defects)
			}
		}
	}
}

// BenchmarkDecide measures one Decide per iteration and reports, beside the
// mean go test computes, the median and the 99th percentile of the
// per-call durations, because the mean of a path with a slow tail says
// nothing about the tail.
func BenchmarkDecide(b *testing.B) {
	for _, s := range decideSubjects(b) {
		b.Run(s.row.name, func(b *testing.B) {
			b.ReportAllocs()
			samples := make([]time.Duration, 0, 1<<16)
			for b.Loop() {
				start := time.Now()
				out := s.kernel.Decide(context.Background(), s.req, s.snap)
				samples = append(samples, time.Since(start))
				if out.Action != s.row.action {
					b.Fatalf("action %d, want %d", out.Action, s.row.action)
				}
			}
			reportPercentiles(b, samples)
		})
	}
}

func reportPercentiles(b *testing.B, samples []time.Duration) {
	b.Helper()
	if len(samples) == 0 {
		b.Fatal("no sample")
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	at := func(p float64) float64 {
		i := int(p * float64(len(samples)-1))
		return float64(samples[i].Nanoseconds())
	}
	b.ReportMetric(at(0.50), "p50-ns/op")
	b.ReportMetric(at(0.99), "p99-ns/op")
}
