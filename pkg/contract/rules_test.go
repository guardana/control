package contract_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// TestValidateRefusesACrossTenantCheckOverTwoAbsences is ADR-0011's tenant
// rule. The cross-tenant check compares principal.tenant_id with
// resource.tenant_id, and with both absent "" == "" passes. A producer that
// omitted both would get what one that filled them honestly with two tenants
// is denied.
func TestValidateRefusesACrossTenantCheckOverTwoAbsences(t *testing.T) {
	for _, effect := range []controlv1.EffectClass{
		controlv1.EffectClass_EFFECT_CLASS_TRANSACT,
		controlv1.EffectClass_EFFECT_CLASS_IDENTITY_OR_ACCESS,
	} {
		t.Run(effect.String(), func(t *testing.T) {
			env := satisfying(effect, effectsUnderTest()[effect])
			env.Principal.TenantId, env.Resource.TenantId = "", ""
			assertRefused(t, contract.Validate(env), contract.ErrMissingField, "principal.tenant_id")
		})
	}
}

// TestValidateAnchorsTheDelegationChain is ADR-0011's chain rule: a chain that
// links can still describe two strangers. It has to start at principal.id and
// end at agent.id.
func TestValidateAnchorsTheDelegationChain(t *testing.T) {
	if err := contract.Validate(withDelegation(3)); err != nil {
		t.Fatalf("a three-hop chain from the principal to the agent is refused, so the cases below prove nothing: %v", err)
	}
	for _, tc := range []struct {
		name, field string
		mutate      func(*controlv1.ActionEnvelope)
	}{
		{"it starts somewhere other than the principal", "delegation[0].from",
			func(e *controlv1.ActionEnvelope) { e.Delegation[0].From = "attacker" }},
		{"it ends somewhere other than the agent", "delegation[2].to",
			func(e *controlv1.ActionEnvelope) { e.Delegation[2].To = "someone-else" }},
		{"the envelope names no agent to end at", "delegation[2].to",
			func(e *controlv1.ActionEnvelope) { e.Agent = nil }},
		{"one hop, from a stranger to the agent", "delegation[0].from",
			func(e *controlv1.ActionEnvelope) { e.Delegation = e.Delegation[2:] }},
		{"one hop, from the principal to a stranger", "delegation[0].to",
			func(e *controlv1.ActionEnvelope) { e.Delegation = e.Delegation[:1] }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := withDelegation(3)
			tc.mutate(env)
			assertRefused(t, contract.Validate(env), contract.ErrInvalidValue, tc.field)
		})
	}
}

// TestValidateRefusesAHopWithNoExpiryOrOneThatEndsFirst: an absent expiry is
// more permissive than any present one, which ADR-0002's absence rule forbids,
// and a window that closes before it opens is not a window.
func TestValidateRefusesAHopWithNoExpiryOrOneThatEndsFirst(t *testing.T) {
	issued := time.Date(2026, 9, 9, 11, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name            string
		want            error
		issued, expires *timestamppb.Timestamp
	}{
		{"no expiry", contract.ErrMissingField, timestamppb.New(issued), nil},
		{"no expiry and no issue time", contract.ErrMissingField, nil, nil},
		{"expires a nanosecond before it was issued", contract.ErrInvalidValue,
			timestamppb.New(issued), timestamppb.New(issued.Add(-time.Nanosecond))},
		{"expires a day before it was issued", contract.ErrInvalidValue,
			timestamppb.New(issued), timestamppb.New(issued.Add(-24 * time.Hour))},
		// Inside one second only the nanos order the two times, so this is
		// the row that fails when their comparison is gone.
		{"expires earlier within the second it was issued in", contract.ErrInvalidValue,
			timestamppb.New(issued.Add(500 * time.Millisecond)), timestamppb.New(issued.Add(200 * time.Millisecond))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := withDelegation(2)
			env.Delegation[1].IssuedAt, env.Delegation[1].ExpiresAt = tc.issued, tc.expires
			assertRefused(t, contract.Validate(env), tc.want, "delegation[1].expires_at")
		})
	}

	// The accepting side of both rules, so the rows above are refused for the
	// order and the absence and not for the hop being edited at all.
	for _, tc := range []struct {
		name            string
		issued, expires *timestamppb.Timestamp
	}{
		{"expires the instant it was issued", timestamppb.New(issued), timestamppb.New(issued)},
		{"expires later within the second it was issued in",
			timestamppb.New(issued.Add(200 * time.Millisecond)), timestamppb.New(issued.Add(500 * time.Millisecond))},
		{"an expiry and no issue time", nil, timestamppb.New(issued)},
	} {
		env := withDelegation(2)
		env.Delegation[1].IssuedAt, env.Delegation[1].ExpiresAt = tc.issued, tc.expires
		if err := contract.Validate(env); err != nil {
			t.Errorf("%s: refused with %v", tc.name, err)
		}
	}
}

// TestValidateDoesNotRuleOutACycle records what the chain rules leave out, so
// that adding one is a decision rather than a discovery: ADR-0011 fixes the
// ends, the links and the times, not the parties in between.
func TestValidateDoesNotRuleOutACycle(t *testing.T) {
	env := withDelegation(3)
	// user-1 -> party-1 -> user-1 -> agent-1
	env.Delegation[1].To, env.Delegation[2].From = "user-1", "user-1"
	if err := contract.Validate(env); err != nil {
		t.Errorf("Validate = %v; the recorded behaviour is that a chain may visit a party twice", err)
	}
}

// TestValidateRefusesAPreviewThatNamesNoProfile is ADR-0011's preview rule:
// an empty profile means no redaction ran, so a preview beside one is raw
// content in evidence with nothing in the record saying so.
func TestValidateRefusesAPreviewThatNamesNoProfile(t *testing.T) {
	const preview = `{"order":"[redacted]"}`
	for _, tc := range []struct {
		name    string
		args    *controlv1.Arguments
		refused bool
	}{
		{"a preview and no profile", &controlv1.Arguments{RedactedPreview: preview}, true},
		{"a preview and the profile that produced it", &controlv1.Arguments{RedactedPreview: preview, RedactionProfile: "default"}, false},
		{"a profile and no preview", &controlv1.Arguments{RedactionProfile: "default"}, false},
		{"neither", &controlv1.Arguments{SchemaRef: "orders.read/v1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := valid()
			env.Arguments = tc.args
			err := contract.Validate(env)
			if tc.refused {
				assertRefused(t, err, contract.ErrInvalidValue, "arguments.redacted_preview")
			} else if err != nil {
				t.Errorf("refused with %v", err)
			}
		})
	}
}

// stringMaps is every map of the envelope, each with a way to fill it.
func stringMaps() []struct {
	path string
	fill func(*controlv1.ActionEnvelope, ...string)
} {
	return []struct {
		path string
		fill func(*controlv1.ActionEnvelope, ...string)
	}{
		{"principal.attributes", func(e *controlv1.ActionEnvelope, keys ...string) {
			e.Principal.Attributes = map[string]string{}
			for _, k := range keys {
				e.Principal.Attributes[k] = "v"
			}
		}},
		{"resource.labels", func(e *controlv1.ActionEnvelope, keys ...string) {
			e.Resource.Labels = map[string]string{}
			for _, k := range keys {
				e.Resource.Labels[k] = "v"
			}
		}},
		{"context.budgets", func(e *controlv1.ActionEnvelope, keys ...string) {
			e.Context = &controlv1.RunContext{Budgets: map[string]int64{}}
			for _, k := range keys {
				e.Context.Budgets[k] = 1
			}
		}},
	}
}

// TestValidateKeepsTheReservedNamespaceOutOfEveryMap: ADR-0011 extends the
// principal.attributes rule to every map and folds it. A key whose first nine
// code points fold to "reserved." is refused, because a consumer that folds
// or upper-cases keys reads it as reserved. Code points and not bytes: U+017F
// folds to s and takes two bytes, so a nine-byte window misses "re" U+017F
// "erved.".
func TestValidateKeepsTheReservedNamespaceOutOfEveryMap(t *testing.T) {
	for _, m := range stringMaps() {
		t.Run(m.path, func(t *testing.T) {
			for _, key := range []string{
				"reserved.", "reserved.x", "reserved.owner", "Reserved.x", "RESERVED.x", "rEsErVeD.",
				"re\U0000017Ferved.x", "RE\U0000017FERVED.x",
			} {
				t.Run(strconv.QuoteToASCII(key), func(t *testing.T) {
					env := valid()
					m.fill(env, key)
					assertRefused(t, contract.Validate(env), contract.ErrInvalidValue, m.path+"[0]")
				})
			}
			// Nine code points that do not fold to the prefix, and eight that
			// fold to all of it but the dot.
			for _, key := range []string{"reserved", "Reserved", "re\U0000017Ferved", "reservedx", "reserve.x", "x.reserved.y"} {
				env := valid()
				m.fill(env, key)
				if err := contract.Validate(env); err != nil {
					t.Errorf("key %+q is refused: %v", key, err)
				}
			}
		})
	}
}

// TestValidateRefusesKeysThatFoldTogether is ADR-0011's fold rule, in every
// map: two keys equal under simple case folding are one key to a consumer that
// folds, as Go's encoding/json does for U+017F and U+212A.
func TestValidateRefusesKeysThatFoldTogether(t *testing.T) {
	for _, m := range stringMaps() {
		t.Run(m.path, func(t *testing.T) {
			for _, tc := range []struct {
				keys []string
				at   string // the later key in byte order is the one refused
			}{
				{[]string{"Team", "team"}, "[1].key"},
				{[]string{"TEAM", "team"}, "[1].key"},
				{[]string{"k", "\U0000212a"}, "[1].key"},
				{[]string{"s", "\U0000017f"}, "[1].key"},
				{[]string{"\U000000df", "\U00001e9e"}, "[1].key"},
				// Not neighbours in byte order: a check of adjacent keys only
				// passes this one.
				{[]string{"Team", "other", "team"}, "[2].key"},
			} {
				env := valid()
				m.fill(env, tc.keys...)
				assertRefused(t, contract.Validate(env), contract.ErrInvalidValue, m.path+tc.at)
			}
			// Simple folding keeps these apart: full folding would join the
			// first pair, case mapping the second. ADR-0011 defends neither.
			for _, keys := range [][]string{
				{"\U000000df", "ss"},
				{"\U00000131", "I"},
				{"team", "teams"},
			} {
				env := valid()
				m.fill(env, keys...)
				if err := contract.Validate(env); err != nil {
					t.Errorf("keys %+q are refused: %v", keys, err)
				}
			}
		})
	}
}

// TestValidateRefusesALabelThatSaysNothing: SENSITIVITY_UNSPECIFIED inside
// data.sensitivities is a label that says nothing, and ToxicFlow would have to
// read it as unknown.
func TestValidateRefusesALabelThatSaysNothing(t *testing.T) {
	env := valid()
	env.Data = &controlv1.DataLabels{Sensitivities: []controlv1.Sensitivity{public, unset}}
	assertRefused(t, contract.Validate(env), contract.ErrInvalidValue, "data.sensitivities[1]")

	env.Data.Sensitivities = []controlv1.Sensitivity{public, secret}
	if err := contract.Validate(env); err != nil {
		t.Errorf("two declared labels are refused: %v", err)
	}
}

// TestValidateGivesAHostOneSpelling is ADR-0011's host rule. A DENY keyed on
// destination.host would otherwise be dodged by a second spelling of the same
// host beside a broader ALLOW: upper case, a trailing dot, a Unicode name set
// against its A-label, a port after the name. Every refused host below passes
// the identifier rule, so what refuses it is the host rule and nothing else.
func TestValidateGivesAHostOneSpelling(t *testing.T) {
	for _, host := range []string{
		"PASTE.EXAMPLE.NET",
		"Paste.Example.Net",
		"paste.example.net.",
		".paste.example.net",
		"paste..example.net",
		"b\U000000fccher.example",
		"paste example.net",
		"paste_bin.example.net",
		"[2001:db8::1]",
		// A colon belongs to an IPv6 literal, which holds at least two: a port
		// after a name or an IPv4 address has one, and so has a pair of hex
		// groups that is no address.
		"paste.example.net:443",
		"1.2.3.4:443",
		"dead:beef",
	} {
		t.Run(strconv.QuoteToASCII(host), func(t *testing.T) {
			if err := contract.CheckIdentifier(host); err != nil {
				t.Fatalf("the identifier rule refuses it already (%v), so this says nothing about the host rule", err)
			}
			env := valid()
			env.Destination = &controlv1.Destination{Host: host}
			assertRefused(t, contract.Validate(env), contract.ErrInvalidValue, "destination.host")
		})
	}

	// The accepting side: one spelling of each kind of host, "::1" holding the
	// fewest colons an IPv6 literal can. The last two are recorded rather than
	// endorsed, as the page states: an IP literal keeps its second spellings,
	// 0x7f.0.0.1 beside 127.0.0.1 and an IPv6 address with and without zero
	// compression.
	for _, host := range []string{
		"paste.example.net",
		"xn--bcher-kva.example",
		"192.0.2.10",
		"2001:db8::1",
		"::1",
		"::ffff:1.2.3.4",
		"0x7f.0.0.1",
		"2001:db8:0:0:0:0:0:1",
	} {
		env := valid()
		env.Destination = &controlv1.Destination{Host: host}
		if err := contract.Validate(env); err != nil {
			t.Errorf("host %q is refused: %v", host, err)
		}
	}
}

// TestValidateHoldsAHostToTheBytesTheRuleNames puts every printable ASCII byte
// in the middle of a name, against the set ADR-0011 names for one: a-z, 0-9,
// the dot and the hyphen. The colon is not in it, because one colon makes no
// IPv6 literal. Every edge of the set is then an input: "`" and
// "{" beside the letters, "/" beside the dot and the digits, ":" beside the
// digits, "," beside the hyphen.
func TestValidateHoldsAHostToTheBytesTheRuleNames(t *testing.T) {
	assertHostBytes(t, "abcdefghijklmnopqrstuvwxyz0123456789.-", func(b byte) string { return "a" + string([]byte{b}) + "a" })
}

// TestValidateHoldsAnIPv6LiteralToTheBytesTheRuleNames is ADR-0011's literal
// rule: a host holding two colons is an IPv6 literal, held to hex digits, dots
// and colons. Every printable ASCII byte goes in after the colons, so "g"
// beside "f", and the hyphen a name may hold, are inputs. The three refused
// spellings of the test above each hold one colon, so none of them reaches
// this set.
func TestValidateHoldsAnIPv6LiteralToTheBytesTheRuleNames(t *testing.T) {
	assertHostBytes(t, "0123456789abcdef:.", func(b byte) string { return "::" + string([]byte{b}) + "1" })
}

// assertHostBytes sends every printable ASCII byte through spell, which puts it
// where no dot rule reaches it and the identifier rule refuses none, and
// requires Validate to accept exactly the bytes in named. The set is written
// out by the caller rather than read from the code.
func assertHostBytes(t *testing.T, named string, spell func(byte) string) {
	t.Helper()
	for b := byte(' '); b <= '~'; b++ {
		host := spell(b)
		t.Run("0x"+strconv.FormatUint(uint64(b), 16), func(t *testing.T) {
			if err := contract.CheckIdentifier(host); err != nil {
				t.Fatalf("%q: the identifier rule refuses it already (%v), so this says nothing about the host rule", host, err)
			}
			env := valid()
			env.Destination = &controlv1.Destination{Host: host}
			err := contract.Validate(env)
			if strings.IndexByte(named, b) >= 0 {
				if err != nil {
					t.Errorf("%q is refused: %v", host, err)
				}
				return
			}
			assertRefused(t, err, contract.ErrInvalidValue, "destination.host")
		})
	}
}
