package mermaid

import (
	"math"
	"testing"
)

const head = "flowchart LR\n  accTitle: t\n  accDescr: d\n"

func TestFigureRefusesASourceOutsideTheSubset(t *testing.T) {
	cases := []struct{ name, source, want string }{
		{"empty source", " \n%% only a comment\n", "mermaid block is empty"},
		{"unknown header", "graph TD\n  A --> B\n", "line 1: expected `flowchart LR|TD|TB`, got 'graph TD'"},
		{"header with a trailing word", "flowchart LR extra\n", "line 1: expected `flowchart LR|TD|TB`, got 'flowchart LR extra'"},
		{"header without a space", "flowchartLR\n", "line 1: expected `flowchart LR|TD|TB`, got 'flowchartLR'"},
		{"missing accTitle", "flowchart LR\n  accDescr: d\n  A --> B\n", "a diagram needs `accTitle:` and `accDescr:`, its text alternative"},
		{"missing accDescr", "flowchart LR\n  accTitle: t\n  A --> B\n", "a diagram needs `accTitle:` and `accDescr:`, its text alternative"},
		{"empty accTitle", "flowchart LR\n  accTitle:\n  accDescr: d\n  A\n", "line 2: `accTitle:` is empty"},
		{"empty accDescr", "flowchart LR\n  accTitle: t\n  accDescr:   \n  A\n", "line 3: `accDescr:` is empty"},
		{"no node", head, "a diagram needs at least one node"},
		{"cycle", head + "  A --> B\n  B --> A\n", "the diagram has a cycle; draw a directed acyclic graph"},
		{"self loop", head + "  A --> A\n", "the diagram has a cycle; draw a directed acyclic graph"},
		{"two-way arrow", head + "  A <--> B\n", "line 4: unsupported syntax at '<--> B'; see internal/docscheck/sitedoc/mermaid"},
		{"ampersand", head + "  A & B --> C\n", "line 4: unsupported syntax at '& B --> C'; see internal/docscheck/sitedoc/mermaid"},
		{"dash label", head + "  A -- label --> B\n", "line 4: unsupported syntax at '-- label --> B'; see internal/docscheck/sitedoc/mermaid"},
		{"dangling edge", head + "  A -->\n", "line 4: expected a node id at ''"},
		{"missing node id", head + "  --> B\n", "line 4: expected a node id at '--> B'"},
		{"nested subgraph", head + "  subgraph G [g]\n    subgraph H [h]\n    end\n  end\n", "line 5: nested subgraphs are not drawn"},
		{"subgraph spanning ranks", head + "  subgraph G [g]\n    A --> B\n  end\n", "subgraph G spans several ranks; keep a group in one"},
		{"subgraph without end", head + "  subgraph G [g]\n    A\n", "subgraph G has no `end`"},
		{"subgraph without node", head + "  subgraph G [g]\n  end\n  A --> B\n", "subgraph G has no node of its own"},
		{"edge into a subgraph", head + "  subgraph G [g]\n    A\n  end\n  B --> G\n", "line 7: G is a subgraph; an edge cannot point at a subgraph"},
		{"subgraph id taken by a group", head + "  subgraph G [g]\n    A\n  end\n  subgraph G [h]\n    B\n  end\n", "line 7: subgraph id 'G' is already taken"},
		{"subgraph id taken by a node", head + "  G --> B\n  subgraph G [g]\n    A\n  end\n", "line 5: subgraph id 'G' is already taken"},
		{"subgraph without a title", head + "  subgraph G\n    A\n  end\n", "line 4: write a subgraph as `subgraph id [title]`"},
		{"end without subgraph", head + "  A\n  end\n", "line 5: `end` without a subgraph"},
		{"unknown inline class", head + "  A:::loud --> B\n", "line 4: class 'loud' is not drawn; use one of ['accent', 'cmd', 'muted']"},
		{"hyphenated class name", head + "  A:::my-class --> B\n", "line 4: class 'my-class' is not drawn; use one of ['accent', 'cmd', 'muted']"},
		{"subgraph title holding a quote", head + "  subgraph G [\"a\"b]\n    A\n  end\n", "line 4: write a subgraph as `subgraph id [title]`"},
		{"subgraph title holding a bracket", head + "  subgraph G [a]]\n    A\n  end\n", "line 4: write a subgraph as `subgraph id [title]`"},
		{"subgraph title of two quotes", head + "  subgraph G [\"\"]\n    A\n  end\n", "line 4: write a subgraph as `subgraph id [title]`"},
		{"subgraph title of one quote", head + "  subgraph G[\"]\n    A\n  end\n", "line 4: write a subgraph as `subgraph id [title]`"},
		{"unknown class line", head + "  A --> B\n  class A,B loud\n", "line 5: class 'loud' is not drawn; use one of ['accent', 'cmd', 'muted']"},
		{"class before declaration", head + "  class A accent\n  A --> B\n", "line 4: `class` names 'A' before it is declared"},
		{"class line without a node", head + "  A\n  class accent\n", "line 5: `class` names '' before it is declared"},
		{"slanted shape", head + "  A[/slanted/] --> B\n", "line 4: node A: shapes and entity codes in '/slanted/' are not drawn"},
		{"backslash shape", head + "  A[\\back\\] --> B\n", `line 4: node A: shapes and entity codes in '\\back\\' are not drawn`},
		{"shape label with a single quote", head + "  A[/it's/] --> B\n", `line 4: node A: shapes and entity codes in "/it's/" are not drawn`},
		{"entity code", head + "  A[say #quot;hi#quot;] --> B\n", "line 4: node A: shapes and entity codes in 'say #quot;hi#quot;' are not drawn"},
		{"non-ASCII entity code", head + "  A[#café;] --> B\n", "line 4: node A: shapes and entity codes in '#café;' are not drawn"},
		{"unclosed rectangle", head + "  A[open --> B\n", "line 4: node A: `[` is never closed"},
		{"unclosed document", head + "  A[(open] --> B\n", "line 4: node A: `[(` is never closed"},
		{"unclosed pill", head + "  A([open) --> B\n", "line 4: node A: `([` is never closed"},
		{"conflicting labels", head + "  A[one] --> B\n  A[two] --> C\n", "line 5: node A is declared twice with different labels"},
		{"conflicting shapes", head + "  A[x] --> B\n  A([x]) --> C\n", "line 5: node A is declared twice with different shapes"},
		{"unlabelled shape conflict", head + "  A --> B\n  A([]) --> C\n", "line 5: node A is declared twice with different shapes"},
		{"unclosed edge label", head + "  A -->|oops B\n", "line 4: an edge label `|…` is never closed"},
		{"control character", head + "  A\x01 --> B\n", `line 4: unsupported syntax at '\x01 --> B'; see internal/docscheck/sitedoc/mermaid`},
		{"unprintable characters", head + "  A\u00ad\u2003\U000e0001 --> B\n", "line 4: unsupported syntax at '\\xad\\u2003\\U000e0001 --> B'; see internal/docscheck/sitedoc/mermaid"},
		{"statement of semicolons", head + "  A\n  ;;\n", "line 5: expected a node id at ''"},
		{"invalid UTF-8", head + "  A[\xff]\n", "mermaid block is not valid UTF-8"},
		{"control character in a label", head + "  A[a\x01b]\n", `line 4: node A: 'a\x01b' holds a character XML cannot carry`},
		{"noncharacter in a title", "flowchart LR\n  accTitle: t\uffff\n  accDescr: d\n  A\n", "line 2: 't\\uffff' holds a character XML cannot carry"},
		{"control character in a group title", head + "  subgraph G [g\x02]\n    A\n  end\n", `line 4: 'g\x02' holds a character XML cannot carry`},
		{"control character in an edge label", head + "  A -->|x\x7fy\x0e| B\n", `line 4: 'x\x7fy\x0e' holds a character XML cannot carry`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Figure(c.source, 0)
			if err == nil {
				t.Fatalf("Figure accepted the source and drew %d bytes, want the error %q", len(got), c.want)
			}
			if err.Error() != c.want {
				t.Errorf("error\n got %q\nwant %q", err.Error(), c.want)
			}
		})
	}
}

// A node drawn inside a group it does not belong to, or two groups drawn over
// each other, is refused; boxes that only touch are not.
func TestOverlappingPlacementsAreRefused(t *testing.T) {
	g := &Graph{
		Nodes:  []*Node{{ID: "A", Group: "G"}, {ID: "B"}, {ID: "C", Group: "H"}},
		Groups: []*Group{{ID: "G", Members: []string{"A"}}, {ID: "H", Members: []string{"C"}}},
	}
	boxes := map[string]*box{"A": {50, 50, 64, 38}, "B": {300, 50, 64, 38}, "C": {300, 300, 64, 38}}
	cases := []struct {
		name   string
		groups map[string]*box
		want   string
	}{
		{"apart", map[string]*box{"G": {50, 50, 100, 80}, "H": {300, 300, 100, 80}}, ""},
		{"touching edges", map[string]*box{"G": {50, 50, 100, 80}, "H": {150, 50, 100, 80}}, ""},
		{"node inside another group", map[string]*box{"G": {50, 50, 100, 80}, "H": {300, 80, 100, 100}}, "node B would be drawn inside subgraph H"},
		{"groups over each other", map[string]*box{"G": {50, 50, 100, 80}, "H": {149, 50, 100, 80}}, "subgraphs G and H would overlap"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := refuseOverlaps(g, boxes, c.groups)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != c.want {
				t.Errorf("refuseOverlaps = %q, want %q", got, c.want)
			}
		})
	}
}

// A coordinate keeps the sign of a value that rounds to zero, and a tie
// on the exact binary value rounds to even.
func TestNumFormatsLikeTheGoldens(t *testing.T) {
	cases := map[float64]string{
		0: "0", 4: "4", 114.5: "114.5", 0.25: "0.2", 0.35: "0.3", 0.75: "0.8",
		-0.04: "-0", -1.25: "-1.2", 82.49999999999999: "82.5", 1e21: "1000000000000000000000",
	}
	for value, want := range cases {
		if got := num(value); got != want {
			t.Errorf("num(%v) = %q, want %q", value, got, want)
		}
	}
	if got := num(math.Copysign(0, -1)); got != "-0" {
		t.Errorf("num(-0.0) = %q, want %q", got, "-0")
	}
}
