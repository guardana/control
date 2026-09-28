// Package mermaid draws a subset of Mermaid flowcharts as static SVG, light and
// dark through CSS variables, with no script. A left-to-right diagram also gets
// a top-to-bottom drawing that replaces it on a narrow screen.
//
// The subset:
//
//   - `flowchart LR`, `flowchart TD` or `flowchart TB` on the first line;
//   - `accTitle: …` and `accDescr: …`, both required: they are the text
//     alternative;
//   - nodes `id`, `id[label]`, `id(label)`, `id([label])` (drawn as a pill) and
//     `id[(label)]` (a stored document), with an optional `:::class`;
//   - edges `-->`, `-.->` (optional) and `==>` (the main path), each with an
//     optional `|label|`, chained on one line;
//   - `subgraph id [title]` … `end`, not nested, whose members share one rank;
//   - `classDef` lines, which style other renderers and are ignored here, and
//     `class a,b name`. The classes drawn are `accent`, `cmd` and `muted`.
//
// Labels break at `<br>`. A source outside the subset is refused with an error
// naming its line, so a diagram is never published half drawn.
package mermaid

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// Graph is a parsed diagram. Nodes and groups keep the order they were first
// named in, which is the order they are drawn in.
type Graph struct {
	// Direction is "LR" or "TD"; a `flowchart TB` header reads as "TD".
	Direction   string
	Title       string
	Description string
	Nodes       []*Node
	Edges       []Edge
	Groups      []*Group
}

// Node is one box. Shape is "rect", "pill" or "doc"; Classes are sorted.
type Node struct {
	ID      string
	Lines   []string
	Shape   string
	Classes []string
	Group   string
}

// Edge joins two nodes. Style is "solid", "dot" or "main".
type Edge struct {
	From  string
	To    string
	Style string
	Label string
}

// Group is a subgraph: a titled frame around nodes of one rank.
type Group struct {
	ID      string
	Title   string
	Members []string
}

var drawnClasses = []string{"accent", "cmd", "muted"}

var shapes = []struct{ open, close, name string }{
	{"[(", ")]", "doc"}, {"([", "])", "pill"}, {"[", "]", "rect"}, {"(", ")", "rect"},
}

var arrows = []struct{ op, style string }{{"-.->", "dot"}, {"==>", "main"}, {"-->", "solid"}}

type parser struct {
	graph    *Graph
	nodes    map[string]*Node
	labelled map[string]bool
	group    *Group
}

// Parse reads a diagram in the subset, or returns an error naming the line.
func Parse(source string) (*Graph, error) {
	if !utf8.ValidString(source) {
		return nil, errors.New("mermaid block is not valid UTF-8")
	}
	type line struct {
		number int
		text   string
	}
	var lines []line
	for i, raw := range splitLines(source) {
		text := strip(raw)
		if text == "" || strings.HasPrefix(text, "%%") {
			continue
		}
		lines = append(lines, line{i + 1, strip(strings.TrimRight(text, ";"))})
	}
	if len(lines) == 0 {
		return nil, errors.New("mermaid block is empty")
	}
	direction, ok := header(lines[0].text)
	if !ok {
		return nil, fmt.Errorf("line %d: expected `flowchart LR|TD|TB`, got %s", lines[0].number, quote(lines[0].text))
	}
	p := &parser{graph: &Graph{Direction: direction}, nodes: map[string]*Node{}, labelled: map[string]bool{}}
	for _, l := range lines[1:] {
		if err := p.statement(l.text); err != nil {
			return nil, fmt.Errorf("line %d: %w", l.number, err)
		}
	}
	return p.finish()
}

func (p *parser) finish() (*Graph, error) {
	g := p.graph
	if p.group != nil {
		return nil, fmt.Errorf("subgraph %s has no `end`", p.group.ID)
	}
	if g.Title == "" || g.Description == "" {
		return nil, errors.New("a diagram needs `accTitle:` and `accDescr:`, its text alternative")
	}
	if len(g.Nodes) == 0 {
		return nil, errors.New("a diagram needs at least one node")
	}
	for _, group := range g.Groups {
		if len(group.Members) == 0 {
			return nil, fmt.Errorf("subgraph %s has no node of its own", group.ID)
		}
	}
	return g, nil
}

// header reads `flowchart` and a direction, with any Unicode space between.
func header(text string) (string, bool) {
	rest, ok := strings.CutPrefix(text, "flowchart")
	if !ok {
		return "", false
	}
	direction := strings.TrimLeftFunc(rest, isSpace)
	if direction == rest {
		return "", false
	}
	switch direction {
	case "LR":
		return "LR", true
	case "TD", "TB":
		return "TD", true
	}
	return "", false
}

func (p *parser) statement(line string) error {
	keyword := line
	if end := strings.IndexFunc(line, isSpace); end >= 0 {
		keyword = line[:end]
	}
	rest := strip(line[len(keyword):])
	switch keyword {
	case "accTitle:", "accDescr:":
		return p.accessible(keyword, rest)
	case "class":
		return p.classLine(rest)
	case "subgraph":
		return p.openGroup(rest)
	case "end":
		if p.group == nil {
			return errors.New("`end` without a subgraph")
		}
		p.group = nil
		return nil
	case "classDef":
		return nil
	}
	return p.chain(line)
}

func (p *parser) accessible(keyword, text string) error {
	if text == "" {
		return fmt.Errorf("`%s` is empty", keyword)
	}
	if err := drawable(text); err != nil {
		return err
	}
	if keyword == "accTitle:" {
		p.graph.Title = text
	} else {
		p.graph.Description = text
	}
	return nil
}

func (p *parser) classLine(rest string) error {
	names, class := "", rest
	if cut := strings.LastIndexByte(rest, ' '); cut >= 0 {
		names, class = rest[:cut], rest[cut+1:]
	}
	for _, name := range strings.Split(names, ",") {
		node, ok := p.nodes[strip(name)]
		if !ok {
			return fmt.Errorf("`class` names %s before it is declared", quote(strip(name)))
		}
		if err := drawn(class); err != nil {
			return err
		}
		addClass(node, class)
	}
	return nil
}

func (p *parser) openGroup(rest string) error {
	if p.group != nil {
		return errors.New("nested subgraphs are not drawn")
	}
	id, title, ok := subgraphHead(rest)
	if !ok {
		return errors.New("write a subgraph as `subgraph id [title]`")
	}
	if _, taken := p.nodes[id]; taken || p.isGroup(id) {
		return fmt.Errorf("subgraph id %s is already taken", quote(id))
	}
	if err := drawable(title); err != nil {
		return err
	}
	p.group = &Group{ID: id, Title: title}
	p.graph.Groups = append(p.graph.Groups, p.group)
	return nil
}

// subgraphHead reads `id [title]`: space may surround the title, which may be
// quoted and holds neither `]` nor `"`. A bracket holding only space, or space
// and one quote, reads as an empty title.
func subgraphHead(rest string) (string, string, bool) {
	end := identEnd(rest, 0, false)
	if end == 0 {
		return "", "", false
	}
	inner, open := strings.CutPrefix(strings.TrimLeftFunc(rest[end:], isSpace), "[")
	inner, closed := strings.CutSuffix(inner, "]")
	if !open || !closed || strings.Contains(inner, "]") {
		return "", "", false
	}
	body := strings.TrimLeftFunc(inner, isSpace)
	if body != inner && (body == "" || body == `"`) {
		return rest[:end], "", true
	}
	body = strings.TrimPrefix(body, `"`)
	title, after, _ := strings.Cut(body, `"`)
	if title == "" || strings.TrimLeftFunc(after, isSpace) != "" {
		return "", "", false
	}
	return rest[:end], strip(title), true
}

func (p *parser) isGroup(id string) bool {
	return slices.ContainsFunc(p.graph.Groups, func(g *Group) bool { return g.ID == id })
}

func drawn(class string) error {
	if !slices.Contains(drawnClasses, class) {
		return fmt.Errorf("class %s is not drawn; use one of ['accent', 'cmd', 'muted']", quote(class))
	}
	return nil
}

func addClass(node *Node, class string) {
	if at, found := slices.BinarySearch(node.Classes, class); !found {
		node.Classes = slices.Insert(node.Classes, at, class)
	}
}
