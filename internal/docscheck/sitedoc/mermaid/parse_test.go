package mermaid

import (
	"fmt"
	"reflect"
	"testing"
)

func TestParseReadsTheStructure(t *testing.T) {
	cases := []struct {
		name   string
		source string
		want   *Graph
	}{
		{
			name: "a node labelled after it is named",
			source: "flowchart LR\n  accTitle: Parse one\n  accDescr: A node named before it is labelled.\n" +
				"  A --> B -.->|maybe| C\n  B([Later<br/>label]):::cmd\n  class A,C accent\n  class C muted\n" +
				"  classDef accent fill:#fff\n",
			want: &Graph{
				Direction:   "LR",
				Title:       "Parse one",
				Description: "A node named before it is labelled.",
				Nodes: []*Node{
					{ID: "A", Lines: []string{"A"}, Shape: "rect", Classes: []string{"accent"}},
					{ID: "B", Lines: []string{"Later", "label"}, Shape: "pill", Classes: []string{"cmd"}},
					{ID: "C", Lines: []string{"C"}, Shape: "rect", Classes: []string{"accent", "muted"}},
				},
				Edges: []Edge{
					{From: "A", To: "B", Style: "solid"},
					{From: "B", To: "C", Style: "dot", Label: "maybe"},
				},
			},
		},
		{
			name: "a group, a document and a main edge",
			source: "flowchart TB\n  accTitle: Parse two\n  accDescr: A group and a stored document.\n" +
				"  %% a comment\n  subgraph IN [\"Quoted title\"]\n    X[(Store)]\n    Y[\"Say: hi\"];\n  end\n" +
				"  X ==>|main path| Z\n  Y --> Z\n  Q\n",
			want: &Graph{
				Direction:   "TD",
				Title:       "Parse two",
				Description: "A group and a stored document.",
				Nodes: []*Node{
					{ID: "X", Lines: []string{"Store"}, Shape: "doc", Group: "IN"},
					{ID: "Y", Lines: []string{"Say: hi"}, Shape: "rect", Group: "IN"},
					{ID: "Z", Lines: []string{"Z"}, Shape: "rect"},
					{ID: "Q", Lines: []string{"Q"}, Shape: "rect"},
				},
				Edges: []Edge{
					{From: "X", To: "Z", Style: "main", Label: "main path"},
					{From: "Y", To: "Z", Style: "solid"},
				},
				Groups: []*Group{{ID: "IN", Title: "Quoted title", Members: []string{"X", "Y"}}},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.source)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Parse\n got %s\nwant %s", describe(got), describe(c.want))
			}
		})
	}
}

func TestSubgraphTitles(t *testing.T) {
	cases := map[string]string{
		"G [   ]":                     "",
		"G\u00a0[\u00a0\"]":           "",
		"G [\t\"\t]":                  "",
		"G  [ \" Quoted title \"  ]":  "Quoted title",
		"G [ \"open title ]":          "open title",
		"G [close title\" ]":          "close title",
		"G\u2003[\u3000spaced\u2003]": "spaced",
	}
	for line, want := range cases {
		g, err := Parse(head + "  subgraph " + line + "\n    A\n  end\n")
		if err != nil {
			t.Errorf("subgraph %q: %v", line, err)
			continue
		}
		if g.Groups[0].Title != want {
			t.Errorf("subgraph %q: title %q, want %q", line, g.Groups[0].Title, want)
		}
	}
}

func describe(g *Graph) string {
	s := fmt.Sprintf("%s %q / %q\n", g.Direction, g.Title, g.Description)
	for _, n := range g.Nodes {
		s += fmt.Sprintf("  node %+v\n", *n)
	}
	for _, e := range g.Edges {
		s += fmt.Sprintf("  edge %+v\n", e)
	}
	for _, group := range g.Groups {
		s += fmt.Sprintf("  group %+v\n", *group)
	}
	return s
}
