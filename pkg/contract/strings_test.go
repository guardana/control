package contract_test

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

type namedString struct{ name, value string }

// identifierRefusals holds one or more vectors for each class ADR-0011's rule
// names. Escapes rather than literals: a source file holding these bytes is a
// Trojan Source finding of its own, and the reader cannot see what it says.
func identifierRefusals() []namedString {
	return []namedString{
		{"NUL", "a\x00b"},
		{"escape", "a\x1bb"},
		{"delete U+007F", "a\x7fb"},
		{"next line U+0085", "a\U00000085b"},
		{"C1 U+009F", "a\U0000009fb"},
		{"soft hyphen U+00AD", "a\U000000adb"},
		{"arabic letter mark U+061C", "a\U0000061cb"},
		{"mongolian vowel separator U+180E", "a\U0000180eb"},
		{"zero width space U+200B", "a\U0000200bb"},
		{"left-to-right mark U+200E", "a\U0000200eb"},
		{"right-to-left override U+202E", "a\U0000202eb"},
		{"word joiner U+2060", "a\U00002060b"},
		{"left-to-right isolate U+2066", "a\U00002066b"},
		{"byte order mark U+FEFF", "a\U0000feffb"},
		{"interlinear annotation anchor U+FFF9, Cf and not default-ignorable", "a\U0000fff9b"},
		{"kaithi number sign U+110BD, Cf and not default-ignorable", "a\U000110bdb"},
		{"language tag U+E0001", "a\U000e0001b"},
		{"tag latin small a U+E0061", "a\U000e0061b"},
		{"line separator U+2028", "a\U00002028b"},
		{"paragraph separator U+2029", "a\U00002029b"},
		{"combining grapheme joiner U+034F", "a\U0000034fb"},
		{"hangul choseong filler U+115F", "a\U0000115fb"},
		{"hangul jungseong filler U+1160", "a\U00001160b"},
		{"khmer vowel inherent U+17B4", "a\U000017b4b"},
		{"mongolian free variation selector U+180B", "a\U0000180bb"},
		{"hangul filler U+3164", "a\U00003164b"},
		{"variation selector U+FE00", "a\U0000fe00b"},
		{"variation selector U+FE0F", "a\U0000fe0fb"},
		{"halfwidth hangul filler U+FFA0", "a\U0000ffa0b"},
		{"variation selector supplement U+E0100", "a\U000e0100b"},
		{"unassigned default-ignorable U+2065", "a\U00002065b"},
		{"unassigned default-ignorable U+FFF0", "a\U0000fff0b"},
		{"unassigned default-ignorable U+E0080", "a\U000e0080b"},
		{"unassigned default-ignorable U+E01F0", "a\U000e01f0b"},
		{"leading space", " ab"},
		{"trailing space", "ab "},
		{"leading tab", "\tab"},
		{"trailing line feed", "ab\n"},
		{"trailing no-break space U+00A0", "ab\U000000a0"},
		{"leading ogham space U+1680", "\U00001680ab"},
		{"trailing en quad U+2000", "ab\U00002000"},
		{"leading hair space U+200A", "\U0000200aab"},
		{"trailing narrow no-break space U+202F", "ab\U0000202f"},
		{"leading medium mathematical space U+205F", "\U0000205fab"},
		{"trailing ideographic space U+3000", "ab\U00003000"},
		{"only spaces", "   "},
		{"no-break space U+00A0 inside", "a\U000000a0b"},
		{"ogham space mark U+1680 inside", "a\U00001680b"},
		{"narrow no-break space U+202F inside", "a\U0000202fb"},
		{"ideographic space U+3000 inside", "a\U00003000b"},
		{"invalid UTF-8", "a\xffb"},
		{"a truncated sequence", "a\xc3"},
		{"an encoded surrogate", "a\xed\xa0\x80b"},
		{"an overlong encoding", "a\xc0\x80b"},
	}
}

// freeTextRefusals is what the two free-text fields still refuse: every control
// character but tab, line feed and carriage return, and invalid UTF-8.
func freeTextRefusals() []namedString {
	return []namedString{
		{"NUL", "a\x00b"},
		{"escape", "a\x1bb"},
		{"vertical tab", "a\x0bb"},
		{"delete U+007F", "a\x7fb"},
		{"next line U+0085", "a\U00000085b"},
		{"invalid UTF-8", "a\xffb"},
	}
}

// freeTextAllowances is text a person may write that no identifier may hold.
func freeTextAllowances() []namedString {
	return []namedString{
		{"padding", "  indented and trailing  "},
		{"tab, line feed and carriage return", "one\ttwo\r\nthree\n"},
		{"a zero width joiner in an emoji sequence", "\U0001F469\U0000200d\U0001F4BB"},
		{"a line separator", "one\U00002028two"},
		{"a right-to-left mark", "a\U0000200fb"},
		{"a no-break space, which only an identifier refuses", "a\U000000a0b"},
	}
}

// richEnvelope sets every string the envelope can carry, so the table below can
// break one at a time. It is a valid READ.
func richEnvelope() *controlv1.ActionEnvelope {
	env := valid()
	env.TraceId, env.SpanId, env.Environment = "trace-1", "span-1", "prod"
	env.Principal.Type, env.Principal.AuthnStrength = "user", "mfa"
	env.Principal.Attributes = map[string]string{"team": "ops"}
	env.Agent = &controlv1.Agent{Id: "agent-1", InstanceId: "inst-1", Framework: "fw", Version: "1.0.0", ModelRef: "model/v1"}
	env.Delegation = []*controlv1.Delegation{{
		From: "user-1", To: "agent-1", Scopes: []string{"orders.read"}, Reason: "asked for a report",
		IssuedAt:  timestamppb.New(time.Date(2026, 9, 9, 11, 0, 0, 0, time.UTC)),
		ExpiresAt: timestamppb.New(time.Date(2026, 9, 9, 13, 0, 0, 0, time.UTC)),
	}}
	env.Action.Kind, env.Action.Protocol = "tool.call", "mcp"
	env.Resource.Environment = "prod"
	env.Resource.Labels = map[string]string{"region": "eu"}
	env.Destination = &controlv1.Destination{TrustZone: zoneInternal, Host: "orders.internal"}
	env.Data = &controlv1.DataLabels{Sensitivities: []controlv1.Sensitivity{internal}, Sources: []string{"orders-db"}}
	env.Arguments = &controlv1.Arguments{
		CanonicalHash: "sha256:" + strings.Repeat("ab", 32), RedactedPreview: `{"order":"[redacted]"}`,
		SchemaRef: "orders.read/v1", RedactionProfile: "default",
	}
	env.Context = &controlv1.RunContext{
		SessionId: "sess-1", RunId: "run-1", StepId: "step-1", Risk: "low",
		Budgets: map[string]int64{"calls": 3}, Tags: []string{"nightly"},
	}
	return env
}

type stringField struct {
	path     string // the path a refusal names
	freeText bool
	// accept is a value the field takes in this envelope, "" meaning "a b": the
	// row is then refused for the character and not for being set at all.
	accept string
	set    func(*controlv1.ActionEnvelope, string)
}

// stringFields names every string of the envelope and how to set it.
// TestStringFieldTableCoversTheSchema holds it to the descriptors.
func stringFields() []stringField {
	return []stringField{
		{"request_id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.RequestId = s }},
		{"trace_id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.TraceId = s }},
		{"span_id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.SpanId = s }},
		{"project_id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.ProjectId = s }},
		{"tenant_id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.TenantId = s }},
		{"environment", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Environment = s }},
		// The chain starts at the principal and ends at the agent, so those two
		// rows move the chain's ends with them. The walk reaches fields 9 and 10
		// before the chain's field 11, so the refusal still names them.
		{"principal.id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Principal.Id, e.Delegation[0].From = s, s }},
		{"principal.type", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Principal.Type = s }},
		{"principal.authn_strength", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Principal.AuthnStrength = s }},
		{"principal.tenant_id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Principal.TenantId = s }},
		{"principal.attributes[0].key", false, "", func(e *controlv1.ActionEnvelope, s string) {
			e.Principal.Attributes = map[string]string{s: "ops"}
		}},
		{"principal.attributes[0]", false, "", func(e *controlv1.ActionEnvelope, s string) {
			e.Principal.Attributes = map[string]string{"team": s}
		}},
		{"agent.id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Agent.Id, e.Delegation[0].To = s, s }},
		{"agent.instance_id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Agent.InstanceId = s }},
		{"agent.framework", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Agent.Framework = s }},
		{"agent.version", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Agent.Version = s }},
		{"agent.model_ref", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Agent.ModelRef = s }},
		{"delegation[0].from", false, "user-1", func(e *controlv1.ActionEnvelope, s string) { e.Delegation[0].From = s }},
		{"delegation[0].to", false, "agent-1", func(e *controlv1.ActionEnvelope, s string) { e.Delegation[0].To = s }},
		{"delegation[0].scopes[0]", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Delegation[0].Scopes = []string{s} }},
		{"delegation[0].reason", true, "", func(e *controlv1.ActionEnvelope, s string) { e.Delegation[0].Reason = s }},
		{"action.kind", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Action.Kind = s }},
		{"action.name", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Action.Name = s }},
		{"action.protocol", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Action.Protocol = s }},
		{"action.provider", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Action.Provider = s }},
		{"resource.type", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Resource.Type = s }},
		{"resource.id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Resource.Id = s }},
		{"resource.tenant_id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Resource.TenantId = s }},
		{"resource.environment", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Resource.Environment = s }},
		{"resource.labels[0].key", false, "", func(e *controlv1.ActionEnvelope, s string) {
			e.Resource.Labels = map[string]string{s: "eu"}
		}},
		{"resource.labels[0]", false, "", func(e *controlv1.ActionEnvelope, s string) {
			e.Resource.Labels = map[string]string{"region": s}
		}},
		// A host holds no space (ADR-0011), so the row accepts "ab" instead.
		{"destination.host", false, "ab", func(e *controlv1.ActionEnvelope, s string) { e.Destination.Host = s }},
		{"data.sources[0]", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Data.Sources = []string{s} }},
		{"arguments.canonical_hash", false, "sha256:" + strings.Repeat("0f", 32), func(e *controlv1.ActionEnvelope, s string) {
			e.Arguments.CanonicalHash = s
		}},
		{"arguments.redacted_preview", true, "", func(e *controlv1.ActionEnvelope, s string) { e.Arguments.RedactedPreview = s }},
		{"arguments.schema_ref", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Arguments.SchemaRef = s }},
		{"arguments.redaction_profile", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Arguments.RedactionProfile = s }},
		{"context.session_id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Context.SessionId = s }},
		{"context.run_id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Context.RunId = s }},
		{"context.step_id", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Context.StepId = s }},
		{"context.risk", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Context.Risk = s }},
		{"context.budgets[0].key", false, "", func(e *controlv1.ActionEnvelope, s string) {
			e.Context.Budgets = map[string]int64{s: 3}
		}},
		{"context.tags[0]", false, "", func(e *controlv1.ActionEnvelope, s string) { e.Context.Tags = []string{s} }},
	}
}

// TestValidateHoldsEveryStringToOneRule: ADR-0011 names no list of fields. A
// policy keyed on a string the rule skipped matches a spelling nobody can see,
// so every string of the envelope is a row, and the free-text pair is held to
// its own narrower rule.
func TestValidateHoldsEveryStringToOneRule(t *testing.T) {
	if err := contract.Validate(richEnvelope()); err != nil {
		t.Fatalf("the base envelope is refused, so no case below proves anything: %v", err)
	}
	for _, f := range stringFields() {
		t.Run(f.path, func(t *testing.T) {
			accept := f.accept
			if accept == "" {
				accept = "a b"
			}
			if err := validateWith(f, accept); err != nil {
				t.Fatalf("%q is refused at %s, so a refusal below may be about the field and not the character: %v", accept, f.path, err)
			}
			refused, allowed := identifierRefusals(), []namedString(nil)
			if f.freeText {
				refused, allowed = freeTextRefusals(), freeTextAllowances()
			}
			for _, v := range refused {
				t.Run(v.name, func(t *testing.T) {
					assertRefused(t, validateWith(f, v.value), contract.ErrInvalidValue, f.path)
				})
			}
			for _, v := range allowed {
				if err := validateWith(f, v.value); err != nil {
					t.Errorf("%s: free text holding %+q is refused: %v", v.name, v.value, err)
				}
			}
		})
	}
}

func validateWith(f stringField, value string) error {
	e := richEnvelope()
	f.set(e, value)
	return contract.Validate(e)
}

// TestStringFieldTableCoversTheSchema reads the strings off the descriptors, so
// a string a later minor adds fails here until the table above names it, and a
// row naming a path the contract does not have fails too.
func TestStringFieldTableCoversTheSchema(t *testing.T) {
	// schema_version is held to the version rule, which runs before the walk.
	want := map[string]bool{"schema_version": true}
	for _, f := range stringFields() {
		want[f.path] = true
	}
	got := stringPaths((&controlv1.ActionEnvelope{}).ProtoReflect().Descriptor(), "")
	if len(got) < 10 {
		t.Fatalf("found %d string paths in the envelope; the descriptor walk is broken: %v", len(got), got)
	}
	for _, path := range got {
		if !want[path] {
			t.Errorf("%s is a string in the contract and stringFields has no row for it", path)
		}
		delete(want, path)
	}
	for path := range want {
		t.Errorf("stringFields names %s, which is not a string in the contract", path)
	}
}

// stringPaths lists every string below md in the spelling a refusal uses: a
// list element and a map entry at position 0, a map key with ".key".
func stringPaths(md protoreflect.MessageDescriptor, prefix string) []string {
	var paths []string
	fields := md.Fields()
	for i := range fields.Len() {
		fd := fields.Get(i)
		name := prefix + string(fd.Name())
		switch {
		case fd.IsMap():
			if fd.MapKey().Kind() == protoreflect.StringKind {
				paths = append(paths, name+"[0].key")
			}
			if fd.MapValue().Kind() == protoreflect.StringKind {
				paths = append(paths, name+"[0]")
			}
		case fd.IsList() && fd.Kind() == protoreflect.StringKind:
			paths = append(paths, name+"[0]")
		case fd.IsList() && fd.Message() != nil:
			paths = append(paths, stringPaths(fd.Message(), name+"[0].")...)
		case fd.Kind() == protoreflect.StringKind:
			paths = append(paths, name)
		case fd.Message() != nil:
			paths = append(paths, stringPaths(fd.Message(), name+".")...)
		}
	}
	return paths
}
