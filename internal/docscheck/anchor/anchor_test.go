package anchor

import "testing"

// The expected anchors are written out as GitHub renders them, not computed.
func TestOf(t *testing.T) {
	cases := map[string]string{
		"Refusals":                        "refusals",
		"Layers and the dependency rule":  "layers-and-the-dependency-rule",
		"Note 7 — aside — The rule":       "note-7--aside--the-rule",
		"`make quality` runs the gate":    "make-quality-runs-the-gate",
		"What is `INDETERMINATE`?":        "what-is-indeterminate",
		"ADR-0007: Repository layout":     "adr-0007-repository-layout",
		"A [linked](other.md) word":       "a-linked-word",
		"snake_case stays":                "snake_case-stays",
		"Ärger über Öl":                   "ärger-über-öl",
		"1. Scope examined":               "1-scope-examined",
		"Closing hashes ##":               "closing-hashes",
		"C#":                              "c",
		"Tabs, commas; and (parentheses)": "tabs-commas-and-parentheses",
	}
	for heading, want := range cases {
		if got := Of(heading); got != want {
			t.Errorf("Of(%q) = %q, want %q", heading, got, want)
		}
	}
}

func TestNumberingNumbersRepeatsInPageOrder(t *testing.T) {
	n := Numbering{}
	var got []string
	for _, h := range []string{"Part", "Other", "Part", "part", "Part"} {
		got = append(got, n.Next(h))
	}
	want := []string{"part", "other", "part-1", "part-2", "part-3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("anchors = %q, want %q", got, want)
		}
	}
}
