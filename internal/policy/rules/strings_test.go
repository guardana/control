package rules

import (
	"strings"
	"testing"
)

// quote makes a JSON string of s, which holds no quote, backslash or control
// character.
func quote(s string) string { return `"` + s + `"` }

// position is a place in a document that holds a string: a builder taking the
// JSON string to put there, the field a refusal names, and the rule it names.
type position struct {
	name  string
	doc   func(value string) string
	field string
	rule  string
}

func inWhen(fragment string) func(string) string {
	return func(v string) string { return docWithWhen(strings.Replace(fragment, "%s", v, 1)) }
}

// identifierPositions is every place that holds an identifier: the bundle's
// id and version, a rule's id, and every value a constraint lists, except a
// destination host, which TestHostValues holds to its own rule as well.
func identifierPositions() []position {
	const w = "rules[0].when."
	return []position{
		{"bundle id", func(v string) string {
			return docWithBundle(`{"id":` + v + `,"version":"1","serial":1,"maxStaleSeconds":1}`)
		}, "bundle.id", ""},
		{"bundle version", func(v string) string {
			return docWithBundle(`{"id":"p","version":` + v + `,"serial":1,"maxStaleSeconds":1}`)
		}, "bundle.version", ""},
		{"rule id", func(v string) string {
			return docWithRules(`{"id":` + v + `,"effect":"DENY","when":{"action":{"name":["x"]}}}`)
		}, "rules[0].id", ""},
		{"principal id", inWhen(`{"principal":{"id":[%s]}}`), w + "principal.id[0]", "r"},
		{"principal type", inWhen(`{"principal":{"type":[%s]}}`), w + "principal.type[0]", "r"},
		{"authn strength", inWhen(`{"principal":{"authnStrength":[%s]}}`), w + "principal.authnStrength[0]", "r"},
		{"principal tenant", inWhen(`{"principal":{"tenantId":[%s]}}`), w + "principal.tenantId[0]", "r"},
		{"attribute value", inWhen(`{"principal":{"attributes":{"k":[%s]}}}`), w + "principal.attributes[0][0]", "r"},
		{"agent id", inWhen(`{"agent":{"id":[%s]}}`), w + "agent.id[0]", "r"},
		{"agent framework", inWhen(`{"agent":{"framework":[%s]}}`), w + "agent.framework[0]", "r"},
		{"action name", inWhen(`{"action":{"name":[%s]}}`), w + "action.name[0]", "r"},
		{"action provider", inWhen(`{"action":{"provider":[%s]}}`), w + "action.provider[0]", "r"},
		{"action protocol", inWhen(`{"action":{"protocol":[%s]}}`), w + "action.protocol[0]", "r"},
		{"action kind", inWhen(`{"action":{"kind":[%s]}}`), w + "action.kind[0]", "r"},
		{"resource type", inWhen(`{"resource":{"type":[%s]}}`), w + "resource.type[0]", "r"},
		{"resource id", inWhen(`{"resource":{"id":[%s]}}`), w + "resource.id[0]", "r"},
		{"resource tenant", inWhen(`{"resource":{"tenantId":[%s]}}`), w + "resource.tenantId[0]", "r"},
		{"resource environment", inWhen(`{"resource":{"environment":[%s]}}`), w + "resource.environment[0]", "r"},
		{"label value", inWhen(`{"resource":{"labels":{"k":[%s]}}}`), w + "resource.labels[0][0]", "r"},
		{"delegation scope", inWhen(`{"delegation":{"scopes":[%s]}}`), w + "delegation.scopes[0]", "r"},
	}
}

// TestIdentifierPositions crosses every identifier position with the string
// rules: never empty, at most 1024 bytes counted in bytes, and the identifier
// rule of ADR-0011.
func TestIdentifierPositions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, value string
		want        error
	}{
		{"1024 bytes", quote(strings.Repeat("a", 1024)), nil},
		{"1024 bytes in two-byte characters", quote(strings.Repeat("é", 512)), nil},
		{"a space inside", `"a b"`, nil},
		{"1025 bytes", quote(strings.Repeat("a", 1025)), ErrTooLarge},
		{"1025 bytes in 513 characters", quote(strings.Repeat("é", 512) + "a"), ErrTooLarge},
		{"empty", `""`, ErrEmpty},
		{"white space first", `" a"`, ErrIdentifier},
		{"white space last", `"a "`, ErrIdentifier},
		{"a no-break space", quote("a" + noBreakSpace + "b"), ErrIdentifier},
		{"a zero-width space", quote("a" + zeroWidthSpace + "b"), ErrIdentifier},
		{"a line separator", quote("a" + lineSeparator + "b"), ErrIdentifier},
		{"an escaped line feed", `"a\nb"`, ErrIdentifier},
		{"an escaped tab", `"a\tb"`, ErrIdentifier},
	}
	for _, pos := range identifierPositions() {
		for _, tc := range cases {
			t.Run(pos.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				if tc.want == nil {
					expectAccepted(t, pos.doc(tc.value))
					return
				}
				expectRefusal(t, pos.doc(tc.value), refusal{tc.want, pos.field, pos.rule})
			})
		}
	}
}

// TestMapKeyPositions holds a key of a map constraint, and of params, to the
// rules the envelope holds its own map keys to (ADR-0011): the string bound,
// the identifier rule and the reserved namespace, whose first nine code points
// fold to "reserved.". An empty key is one the envelope may carry, so a
// policy may name it.
func TestMapKeyPositions(t *testing.T) {
	t.Parallel()
	positions := []position{
		{"attribute key", func(k string) string {
			return docWithWhen(`{"principal":{"attributes":{` + k + `:["v"]}}}`)
		}, "rules[0].when.principal.attributes[0].key", "r"},
		{"label key", func(k string) string {
			return docWithWhen(`{"resource":{"labels":{` + k + `:["v"]}}}`)
		}, "rules[0].when.resource.labels[0].key", "r"},
		{"param key", func(k string) string {
			return docWithRules(`{"id":"r","effect":"REQUIRE_APPROVAL","obligations":[{"type":"cap_amount","params":{` + k + `:"v"}}],"when":{"action":{"name":["x"]}}}`)
		}, "rules[0].obligations[0].params[0].key", "r"},
	}
	cases := []struct {
		name, key string
		want      error
	}{
		{"1024 bytes", quote(strings.Repeat("k", 1024)), nil},
		{"empty", `""`, nil},
		{"reserved without the dot", `"reserved"`, nil},
		{"reserved further in", `"x.reserved.y"`, nil},
		{"reserved with an underscore", `"reserved_x"`, nil},
		{"reserved run on", `"reservedx"`, nil},
		{"eight code points of it", `"Reserved"`, nil},
		{"1025 bytes", quote(strings.Repeat("k", 1025)), ErrTooLarge},
		{"the reserved namespace", `"reserved.x"`, ErrReservedKey},
		{"the bare prefix", `"reserved."`, ErrReservedKey},
		{"reserved in title case", `"Reserved.x"`, ErrReservedKey},
		{"reserved in upper case", `"RESERVED.x"`, ErrReservedKey},
		{"reserved with a long s", quote("re" + longS + "erved.x"), ErrReservedKey},
		{"white space first", `" k"`, ErrIdentifier},
		{"a zero-width space", quote("k" + zeroWidthSpace), ErrIdentifier},
	}
	for _, pos := range positions {
		for _, tc := range cases {
			t.Run(pos.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				if tc.want == nil {
					expectAccepted(t, pos.doc(tc.key))
					return
				}
				expectRefusal(t, pos.doc(tc.key), refusal{tc.want, pos.field, pos.rule})
			})
		}
	}
}

// TestHostValues holds every value of destination.host to the identifier
// rule, as every listed value is, and then to the one spelling a host has
// (ADR-0011): the contract refuses what no envelope may carry there, and a
// rule naming such a spelling would match no call. The second value of the
// list is checked as the first is.
func TestHostValues(t *testing.T) {
	t.Parallel()
	positions := []position{
		{"first host", inWhen(`{"destination":{"host":[%s]}}`), "rules[0].when.destination.host[0]", "r"},
		{"second host", inWhen(`{"destination":{"host":["paste.example.net",%s]}}`), "rules[0].when.destination.host[1]", "r"},
	}
	cases := []struct {
		name, value string
		want        error
	}{
		{"a name", `"paste.example.net"`, nil},
		{"a hyphen", `"paste-1.example.net"`, nil},
		{"one label", `"localhost"`, nil},
		{"digits", `"1.2.3.4"`, nil},
		{"an IPv6 loopback", `"::1"`, nil},
		{"an IPv6 mapped address", `"::ffff:1.2.3.4"`, nil},
		{"an IPv6 address", `"2001:db8::1"`, nil},
		{"1024 bytes", quote(strings.Repeat("a", 1024)), nil},
		{"empty", `""`, ErrEmpty},
		{"1025 bytes", quote(strings.Repeat("a", 1025)), ErrTooLarge},
		{"white space first", `" paste.example.net"`, ErrIdentifier},
		{"a zero-width space", quote("paste" + zeroWidthSpace + ".example.net"), ErrIdentifier},
		{"upper case", `"PASTE.EXAMPLE.NET"`, ErrIdentifier},
		{"one upper-case byte", `"paste.Example.net"`, ErrIdentifier},
		{"a trailing dot", `"paste.example.net."`, ErrIdentifier},
		{"a leading dot", `".paste.example.net"`, ErrIdentifier},
		{"an empty label", `"paste..example.net"`, ErrIdentifier},
		{"a port", `"paste.example.net:443"`, ErrIdentifier},
		{"a port on an address", `"1.2.3.4:443"`, ErrIdentifier},
		{"one colon", `"dead:beef"`, ErrIdentifier},
		{"a bracketed IPv6 address", `"[::1]"`, ErrIdentifier},
		{"a hex digit above f in an IPv6 literal", `"dead::beeg"`, ErrIdentifier},
		{"an underscore", `"paste_example.net"`, ErrIdentifier},
		{"a space inside", `"paste example.net"`, ErrIdentifier},
		{"a Unicode name", `"pästé.example.net"`, ErrIdentifier},
	}
	for _, pos := range positions {
		for _, tc := range cases {
			t.Run(pos.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				if tc.want == nil {
					expectAccepted(t, pos.doc(tc.value))
					return
				}
				expectRefusal(t, pos.doc(tc.value), refusal{tc.want, pos.field, pos.rule})
			})
		}
	}
}

// TestParamValues: a value in params meets the envelope's rule for a map
// value, which admits the empty string and nothing the identifier rule
// refuses.
func TestParamValues(t *testing.T) {
	t.Parallel()
	doc := func(v string) string {
		return docWithRules(`{"id":"r","effect":"REQUIRE_APPROVAL","obligations":[{"type":"cap_amount","params":{"max":` + v + `}}],"when":{"action":{"name":["x"]}}}`)
	}
	const field = "rules[0].obligations[0].params[0]"
	if got := expectAccepted(t, doc(`""`)).Rules[0].Obligations[0].Params; len(got) != 1 || got["max"] != "" {
		t.Errorf("params = %q, want max holding the empty string", got)
	}
	expectAccepted(t, doc(quote(strings.Repeat("v", 1024))))
	expectAccepted(t, doc(`"a b"`))
	expectRefusal(t, doc(quote(strings.Repeat("v", 1025))), refusal{ErrTooLarge, field, "r"})
	expectRefusal(t, doc(`" v"`), refusal{ErrIdentifier, field, "r"})
	expectRefusal(t, doc(quote("v"+zeroWidthSpace)), refusal{ErrIdentifier, field, "r"})
	expectRefusal(t, doc(`"a\nb"`), refusal{ErrIdentifier, field, "r"})
}
