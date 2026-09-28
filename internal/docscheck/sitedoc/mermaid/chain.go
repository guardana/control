package mermaid

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// chain reads nodes joined by edges on one line: `a --> b -.->|label| c`.
func (p *parser) chain(line string) error {
	position := 0
	previous := ""
	var pending *Edge
	for {
		id, next, err := p.node(line, position)
		if err != nil {
			return err
		}
		if pending != nil {
			pending.From, pending.To = previous, id
			p.graph.Edges = append(p.graph.Edges, *pending)
		}
		position = skip(line, next)
		if position == len(line) {
			return nil
		}
		pending, next, err = arrow(line, position)
		if err != nil {
			return err
		}
		previous = id
		position = skip(line, next)
	}
}

func (p *parser) node(line string, position int) (string, int, error) {
	end := identEnd(line, position, false)
	if end == position {
		return "", 0, fmt.Errorf("expected a node id at %s", quote(line[position:]))
	}
	id := line[position:end]
	shape, label, position, err := shapeAt(line, end, id)
	if err != nil {
		return "", 0, err
	}
	if p.isGroup(id) {
		return "", 0, fmt.Errorf("%s is a subgraph; an edge cannot point at a subgraph", id)
	}
	class := ""
	if tag, ok := strings.CutPrefix(line[position:], ":::"); ok {
		if name := identEnd(tag, 0, true); name > 0 {
			class = tag[:name]
			if err := drawn(class); err != nil {
				return "", 0, err
			}
			position += len(":::") + name
		}
	}
	node, err := p.register(id, shape, label)
	if err != nil {
		return "", 0, err
	}
	if class != "" {
		addClass(node, class)
	}
	if p.group != nil && node.Group == "" {
		node.Group = p.group.ID
		p.group.Members = append(p.group.Members, id)
	}
	return id, position, nil
}

// identEnd returns where an ASCII identifier starting at position ends, or
// position when none starts there. A class name may also hold hyphens.
func identEnd(line string, position int, hyphen bool) int {
	end := position
	for end < len(line) {
		c := line[end]
		letter := c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z')
		inner := c >= '0' && c <= '9' || hyphen && c == '-'
		if accepted := letter || end > position && inner; !accepted {
			break
		}
		end++
	}
	return end
}

// shapeAt reads an optional `[label]`-style shape after a node id and
// returns the shape, the label and where the shape ends.
func shapeAt(line string, position int, id string) (string, string, int, error) {
	for _, s := range shapes {
		if !strings.HasPrefix(line[position:], s.open) {
			continue
		}
		start := position + len(s.open)
		end := strings.Index(line[start:], s.close)
		if end < 0 {
			return "", "", 0, fmt.Errorf("node %s: `%s` is never closed", id, s.open)
		}
		label := strings.Trim(strip(line[start:start+end]), `"`)
		if strings.HasPrefix(label, "/") || strings.HasPrefix(label, `\`) || hasEntity(label) {
			return "", "", 0, fmt.Errorf("node %s: shapes and entity codes in %s are not drawn", id, quote(label))
		}
		if err := drawable(label); err != nil {
			return "", "", 0, fmt.Errorf("node %s: %w", id, err)
		}
		return s.name, label, start + end + len(s.close), nil
	}
	return "", "", position, nil
}

// register returns the node, declaring it on first sight and refusing a
// conflicting redeclaration. A node named before it is labelled takes the
// label and shape it is given later.
func (p *parser) register(id, shape, label string) (*Node, error) {
	text := label
	if text == "" {
		text = id
	}
	lines := breakLines(text)
	drawnShape := shape
	if drawnShape == "" {
		drawnShape = "rect"
	}
	node, ok := p.nodes[id]
	switch {
	case !ok:
		node = &Node{ID: id, Lines: lines, Shape: drawnShape}
		p.nodes[id] = node
		p.labelled[id] = label != ""
		p.graph.Nodes = append(p.graph.Nodes, node)
	case label != "" && !p.labelled[id]:
		node.Lines, node.Shape = lines, drawnShape
		p.labelled[id] = true
	case label != "" && !slices.Equal(lines, node.Lines):
		return nil, fmt.Errorf("node %s is declared twice with different labels", id)
	case shape != "" && shape != node.Shape:
		return nil, fmt.Errorf("node %s is declared twice with different shapes", id)
	}
	return node, nil
}

func arrow(line string, position int) (*Edge, int, error) {
	for _, a := range arrows {
		if !strings.HasPrefix(line[position:], a.op) {
			continue
		}
		position += len(a.op)
		edge := &Edge{Style: a.style}
		if strings.HasPrefix(line[position:], "|") {
			end := strings.IndexByte(line[position+1:], '|')
			if end < 0 {
				return nil, 0, errors.New("an edge label `|…` is never closed")
			}
			edge.Label = strip(line[position+1 : position+1+end])
			if err := drawable(edge.Label); err != nil {
				return nil, 0, err
			}
			position += end + 2
		}
		return edge, position, nil
	}
	return nil, 0, fmt.Errorf("unsupported syntax at %s; see internal/docscheck/sitedoc/mermaid", quote(line[position:]))
}

func skip(line string, position int) int {
	for position < len(line) && line[position] == ' ' {
		position++
	}
	return position
}
