package notify

import (
	"strconv"
	"strings"
	"testing"
)

// FuzzParseDelivered: whatever the bytes, the parser refuses them or returns
// keys that, each quoted on a line of its own, spell exactly the bytes before
// the cut, with only a tail a torn mark could leave after it.
func FuzzParseDelivered(f *testing.F) {
	for _, seed := range []string{
		"",
		keyA,
		keyA + `"` + idB + `:FINDING_VERDICT_SUSPECTED"` + "\n",
		keyA + `"` + idB[:7],
		keyA + `"` + idB + `:FINDING_VERDICT_SUSPECTED"`,
		"\n",
		`"` + idB + `:FINDING_VERDICT_MAYBE"` + "\n",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		keys, end, err := parseDelivered(data)
		if err != nil {
			return
		}
		var spelled strings.Builder
		for _, k := range keys {
			spelled.WriteString(strconv.Quote(k) + "\n")
		}
		if string(data[:end]) != spelled.String() {
			t.Fatalf("parsed %q as keys %q", data[:end], keys)
		}
		if tail := data[end:]; len(tail) >= len(`"`+idA+`:FINDING_VERDICT_INDETERMINATE"`+"\n") || strings.Contains(string(tail), "\n") {
			t.Fatalf("kept a tail no mark leaves: %q", tail)
		}
	})
}
