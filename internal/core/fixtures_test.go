package core_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/pkg/contract"
)

// fixtureDir holds the cases `policy test` reads. Each is one document, one
// envelope, its arguments, flow, options, load and decision times, and what
// Decide has to answer. This file runs every one of them through Decide.
const fixtureDir = "../../testdata/policy/fixtures"

// fixtureCase is the case format. It is decoded only after caseMembers has
// walked the file, because encoding/json alone matches a name in any case and
// keeps the last of two members with one name.
type fixtureCase struct {
	Document       json.RawMessage `json:"document"`
	Envelope       json.RawMessage `json:"envelope"`
	AuthorizedArgs string          `json:"authorized_args"`
	Flow           struct {
		Untrusted bool   `json:"untrusted"`
		Floor     string `json:"floor"`
	} `json:"flow"`
	Options struct {
		FailOpenRead    bool     `json:"fail_open_read"`
		MaxStaleSeconds int64    `json:"max_stale_seconds"`
		Applicable      []string `json:"applicable"`
	} `json:"options"`
	LoadedAt  string  `json:"loaded_at"`
	DecidedAt string  `json:"decided_at"`
	External  *string `json:"external"`
	Expect    struct {
		Verdict     string   `json:"verdict"`
		Action      string   `json:"action"`
		ReasonCodes []string `json:"reason_codes"`
	} `json:"expect"`
}

// fixture is one case read and resolved: the times parsed, the envelope
// decoded the way a command line would decode it, the expectation typed.
type fixture struct {
	name      string
	document  []byte
	envelope  []byte // the envelope as written, for the fuzz seeds
	env       *controlv1.ActionEnvelope
	refusal   error
	args      []byte
	flow      contract.FlowState
	opts      core.Options
	loadedAt  time.Time
	decidedAt time.Time
	answer    answer
	want      expect
}

func (f fixture) request() core.Request {
	return core.Request{Envelope: f.env, Refusal: f.refusal, AuthorizedArgs: f.args, Flow: f.flow, External: f.answer.external}
}

// readFixtures reads every file of the directory. An empty directory is a
// failure: a corpus that vanished would otherwise pass this file's tests.
func readFixtures(t testing.TB) []fixture {
	t.Helper()
	fsys := os.DirFS(fixtureDir)
	names, err := fs.Glob(fsys, "*.json")
	if err != nil {
		t.Fatalf("list %s: %v", fixtureDir, err)
	}
	if len(names) == 0 {
		t.Fatalf("no fixture under %s", fixtureDir)
	}
	out := make([]fixture, 0, len(names))
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		out = append(out, resolveFixture(t, strings.TrimSuffix(name, ".json"), raw))
	}
	return out
}

// caseMembers names the members of the case and of each object of its own,
// as fixtureCase spells them. The document and the envelope are read by their
// own strict decoders and are not walked here.
var caseMembers = map[string][]string{
	"":        {"document", "envelope", "authorized_args", "flow", "options", "loaded_at", "decided_at", "external", "expect"},
	"flow":    {"untrusted", "floor"},
	"options": {"fail_open_read", "max_stale_seconds", "applicable"},
	"expect":  {"verdict", "action", "reason_codes"},
}

// exactMembers walks the members of the object at `at` ("" for the case) and
// refuses one spelled other than caseMembers names it, or given twice, the
// way the policy test command reads a case. Values are left unparsed; the typed
// decode follows.
func exactMembers(raw []byte, at string) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return fmt.Errorf("%s: want a JSON object", memberLabel(at, ""))
	}
	seen := map[string]bool{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("%s: %w", memberLabel(at, ""), err)
		}
		key, _ := tok.(string)
		if !slices.Contains(caseMembers[at], key) {
			return fmt.Errorf("%s: a member the case format does not have", memberLabel(at, key))
		}
		if seen[key] {
			return fmt.Errorf("%s: given twice", memberLabel(at, key))
		}
		seen[key] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return fmt.Errorf("%s: %w", memberLabel(at, key), err)
		}
		if _, nested := caseMembers[key]; nested && at == "" {
			if err := exactMembers(value, key); err != nil {
				return err
			}
		}
	}
	return nil
}

// memberLabel names a member for a refusal: "flow.floor", "floor" at the top,
// and the object itself when key is empty.
func memberLabel(at, key string) string {
	switch {
	case at == "" && key == "":
		return "the case"
	case at == "":
		return key
	case key == "":
		return at
	}
	return at + "." + key
}

func resolveFixture(t testing.TB, name string, raw []byte) fixture {
	t.Helper()
	if err := exactMembers(raw, ""); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var c fixtureCase
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if dec.More() {
		t.Fatalf("%s: trailing data after the case", name)
	}
	if len(c.Document) == 0 || len(c.Envelope) == 0 {
		t.Fatalf("%s: document and envelope are required", name)
	}
	f := fixture{
		name:      name,
		document:  c.Document,
		envelope:  c.Envelope,
		loadedAt:  utcTime(t, name, "loaded_at", c.LoadedAt),
		decidedAt: utcTime(t, name, "decided_at", c.DecidedAt),
		flow:      contract.NewFlowState(c.Flow.Untrusted, controlv1.Sensitivity(enumValue(t, name, controlv1.Sensitivity_value, "SENSITIVITY_", c.Flow.Floor))),
		opts: core.Options{
			Mode:         controlv1.EnforcementMode_ENFORCEMENT_MODE_ENFORCE,
			FailOpenRead: c.Options.FailOpenRead,
			MaxStale:     time.Duration(c.Options.MaxStaleSeconds) * time.Second,
			Applicable:   c.Options.Applicable,
		},
		want: expect{
			verdict: verdictNamed(t, name, c.Expect.Verdict),
			action:  actionNamed(t, name, c.Expect.Action),
			codes:   c.Expect.ReasonCodes,
		},
	}
	if c.AuthorizedArgs != "" {
		f.args = []byte(c.AuthorizedArgs)
	}
	f.answer = answers()[0]
	if c.External != nil {
		f.answer = answerNamed(t, name, *c.External)
	}
	// The strict decoder, so a refusal reaches Decide the way a decoder's
	// does: handed in, with whatever envelope the decoder still returned.
	f.env, f.refusal = contract.DecodeJSON(c.Envelope)
	return f
}

// utcTime reads an RFC 3339 instant that has to be written in UTC: a zone
// offset in a fixture would make the case read differently on two machines.
func utcTime(t testing.TB, name, key, s string) time.Time {
	t.Helper()
	if !strings.HasSuffix(s, "Z") {
		t.Fatalf("%s: %s %q is not written in UTC", name, key, s)
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("%s: %s: %v", name, key, err)
	}
	return parsed.UTC()
}

// enumValue resolves a name written without its prefix. UNSPECIFIED is a
// name like any other here: as a flow floor it says nothing has been read
// yet, which is a state the command's reader accepts as well.
func enumValue(t testing.TB, name string, values map[string]int32, prefix, s string) int32 {
	t.Helper()
	n, ok := values[prefix+s]
	if !ok {
		t.Fatalf("%s: %q is not a %s name", name, s, strings.TrimSuffix(prefix, "_"))
	}
	return n
}

// verdictNamed resolves an expected verdict, and refuses UNSPECIFIED, which
// is no answer.
func verdictNamed(t testing.TB, name, s string) controlv1.Verdict {
	t.Helper()
	n := enumValue(t, name, controlv1.Verdict_value, "VERDICT_", s)
	if n == 0 {
		t.Fatalf("%s: %q is not one of the five verdicts", name, s)
	}
	return controlv1.Verdict(n)
}

// answerNamed resolves the external answer a case states, spelled as the
// policy test command spells it; a case that states none asks nothing.
func answerNamed(t testing.TB, name, s string) answer {
	t.Helper()
	spelled := map[string]string{
		"allowed": "allowed", "denied": "denied", "denied_with_obligations": "denied with obligations",
		"timeout": "timeout", "unavailable": "unavailable", "answer_refused": "answer refused",
	}
	i := slices.IndexFunc(answers(), func(a answer) bool { return a.name == spelled[s] })
	if i < 0 {
		t.Fatalf("%s: %q is not an external answer", name, s)
	}
	return answers()[i]
}

func actionNamed(t testing.TB, name, s string) core.EnforcementAction {
	t.Helper()
	switch s {
	case "Block":
		return core.Block
	case "Execute":
		return core.Execute
	case "ExecuteWithObligations":
		return core.ExecuteWithObligations
	case "AwaitApproval":
		return core.AwaitApproval
	}
	t.Fatalf("%s: %q is not an enforcement action", name, s)
	return core.Block
}

// refusing is a test whose Fatalf hands the refusal to the caller and stops
// the goroutine, the way a real Fatalf stops a test.
type refusing struct {
	*testing.T
	refusal string
}

func (r *refusing) Fatalf(format string, args ...any) {
	r.refusal = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

// resolved runs the reader on raw and returns the fixture and the refusal
// that stopped it, or "" when the case resolved.
func resolved(t *testing.T, raw string) (fixture, string) {
	t.Helper()
	r := &refusing{T: t}
	var f fixture
	done := make(chan struct{})
	go func() {
		defer close(done)
		f = resolveFixture(r, "case", []byte(raw))
	}()
	<-done
	return f, r.refusal
}

// mutated is the allow fixture with one substring replaced; the replacement
// has to have happened, so a row cannot test the unchanged file.
func mutated(t *testing.T, from, to string) string {
	t.Helper()
	raw, err := os.ReadFile(fixtureDir + "/allow.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), from) {
		t.Fatalf("allow.json does not hold %q", from)
	}
	return strings.Replace(string(raw), from, to, 1)
}

// The reader's strictness rules, each on the input that meets it: a member
// this file does not name, a time written with an offset. An UNSPECIFIED
// floor is not refused, so a case the command runs is a case this file runs.
func TestResolveFixtureStrictness(t *testing.T) {
	for name, tc := range map[string]struct{ raw, want string }{
		"an unknown member":        {mutated(t, `"authorized_args": "",`, `"authorized_args": "", "note": "x",`), `note: a member the case format does not have`},
		"a member in another case": {mutated(t, `"expect": {`, `"Expect": {`), `Expect: a member the case format does not have`},
		"a member given twice":     {mutated(t, `"authorized_args": "",`, `"authorized_args": "", "authorized_args": "",`), `authorized_args: given twice`},
		"a nested member twice":    {mutated(t, `"untrusted": false,`, `"untrusted": false, "untrusted": true,`), `flow.untrusted: given twice`},
		"a time with an offset":    {mutated(t, `"loaded_at": "2026-09-11T12:00:00Z"`, `"loaded_at": "2026-09-11T13:00:00+01:00"`), `loaded_at "2026-09-11T13:00:00+01:00" is not written in UTC`},
		"an UNSPECIFIED floor":     {mutated(t, `"floor": "PUBLIC"`, `"floor": "UNSPECIFIED"`), ""},
	} {
		t.Run(name, func(t *testing.T) {
			f, refusal := resolved(t, tc.raw)
			if tc.want == "" && refusal != "" {
				t.Fatalf("refused: %s", refusal)
			}
			if tc.want != "" && !strings.Contains(refusal, tc.want) {
				t.Fatalf("refusal %q, want one holding %q", refusal, tc.want)
			}
			if tc.want == "" && f.flow.MaxSensitivityRead != controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED {
				t.Errorf("floor %s, want UNSPECIFIED", f.flow.MaxSensitivityRead)
			}
		})
	}
}

// TestFixturesThroughDecide runs every case with its bundle signed and loaded
// in memory: the verdict, the action and the codes are exactly the case's,
// and the bundle digest is on every decision made with a snapshot.
func TestFixturesThroughDecide(t *testing.T) {
	fixtures := readFixtures(t)
	for _, f := range fixtures {
		t.Run(f.name, func(t *testing.T) {
			k := kernel(t, f.opts, fixedClock(f.decidedAt))
			snap := snapshotAt(t, f.document, f.loadedAt)
			out := decide(k, f.request(), snap)
			check(t, out, f.want)
			if got, want := out.Decision.GetPolicyBundleDigest(), snap.Ref().GetDigest(); got != want || want == "" {
				t.Errorf("policy_bundle_digest %q, want the snapshot's %q", got, want)
			}
		})
	}
	checkCorpus(t, fixtures)
}

// checkCorpus holds the directory to what the exit criteria ask of it: all
// five verdicts, one INDETERMINATE per cause, and the direct READ on the
// every-field document held INDETERMINATE, so that a fixture relaxed to ALLOW
// fails here and not only in a review.
func checkCorpus(t *testing.T, fixtures []fixture) {
	t.Helper()
	verdicts := map[controlv1.Verdict]bool{}
	codes := map[string]bool{}
	for _, f := range fixtures {
		verdicts[f.want.verdict] = true
		for _, c := range f.want.codes {
			codes[c] = true
		}
	}
	for _, v := range []controlv1.Verdict{verdictAllow, verdictDeny, verdictApproval, verdictObligations, verdictIndeterminate} {
		if !verdicts[v] {
			t.Errorf("no fixture expects %s", v)
		}
	}
	for _, c := range []string{codeRequiredFieldAbsent, codeMalformedInput, codeInvalidFieldValue, codeRuleUndetermined,
		codeTenantUndetermined, codePolicyStale, codeFailOpenRead, codeTenantMismatch, codeDelegationExceeds, codeNoMatchingRule,
		codePDPTimeout, codePDPUnavailable, codePDPAnswerRefused, codePDPDeny, codePDPAllow} {
		if !codes[c] {
			t.Errorf("no fixture expects %s", c)
		}
	}
	i := slices.IndexFunc(fixtures, func(f fixture) bool { return f.name == "every-field-direct-read" })
	if i < 0 {
		t.Fatal("the every-field direct READ fixture is missing")
	}
	if f := fixtures[i]; f.want.verdict != verdictIndeterminate || f.want.action != core.Block {
		t.Errorf("every-field direct READ expects %s and %d, want INDETERMINATE and Block", f.want.verdict, f.want.action)
	}
}

// TestFixturesWithoutABundle is the cause no fixture can carry: every case's
// request decided with no snapshot is never ALLOW, names POLICY_UNAVAILABLE
// unless a refusal stopped it before the policy step, carries no bundle
// digest and no load time, and runs only as a READ the operator opened,
// marked as such.
func TestFixturesWithoutABundle(t *testing.T) {
	for _, f := range readFixtures(t) {
		t.Run(f.name, func(t *testing.T) {
			out := decide(kernel(t, f.opts, fixedClock(f.decidedAt)), f.request(), nil)
			d := out.Decision
			if d.GetVerdict() == verdictAllow || d.GetVerdict() == controlv1.Verdict_VERDICT_UNSPECIFIED {
				t.Errorf("no bundle gave %s", d.GetVerdict())
			}
			// A decision with no action digest stopped at step 1 or 2, on one
			// refusal; every other one reached step 5.
			stopped := d.GetActionDigest() == ""
			if unavailable := slices.Contains(d.GetReasonCodes(), codePolicyUnavailable); unavailable == stopped || (stopped && len(d.GetReasonCodes()) != 1) {
				t.Errorf("codes %q with action digest %q", d.GetReasonCodes(), d.GetActionDigest())
			}
			if d.GetPolicyBundleDigest() != "" || d.GetPolicyLoadedAt() != nil || d.GetPolicyFreshness() != stale {
				t.Errorf("no bundle: digest %q, loaded_at %v, freshness %s", d.GetPolicyBundleDigest(), d.GetPolicyLoadedAt(), d.GetPolicyFreshness())
			}
			checkEnforcement(t, f.opts.FailOpenRead, f.env.GetAction().GetEffect() == effectRead, out)
		})
	}
}

// checkEnforcement holds an outcome to what each verdict enforces (ADR-0012):
// DENY blocks; ALLOW, REQUIRE_APPROVAL and ALLOW_WITH_OBLIGATIONS have their
// fixed actions and never carry the fail-open marker; INDETERMINATE blocks, or
// runs a READ the operator opened with FAIL_OPEN_READ_CONFIGURED as its last
// code; nothing else is a verdict. The property and the fuzz target share it.
func checkEnforcement(t tb, failOpenRead, read bool, out core.Outcome) {
	t.Helper()
	d := out.Decision
	codes := d.GetReasonCodes()
	marked := len(codes) > 0 && codes[len(codes)-1] == codeFailOpenRead
	fixed := map[controlv1.Verdict]core.EnforcementAction{
		verdictDeny: core.Block, verdictAllow: core.Execute, verdictApproval: core.AwaitApproval, verdictObligations: core.ExecuteWithObligations,
	}
	if want, ok := fixed[d.GetVerdict()]; ok {
		if out.Action != want || marked {
			t.Errorf("%s enforced as %d with codes %q", d.GetVerdict(), out.Action, codes)
		}
		return
	}
	if d.GetVerdict() != verdictIndeterminate {
		t.Errorf("verdict %s is not one of the five", d.GetVerdict())
		return
	}
	switch {
	case out.Action == core.Block && !marked:
	case out.Action == core.Execute && marked && failOpenRead && read:
	default:
		t.Errorf("INDETERMINATE enforced as %d with codes %q under fail-open %v on a read %v", out.Action, codes, failOpenRead, read)
	}
}
