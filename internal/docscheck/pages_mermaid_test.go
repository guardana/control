package docscheck

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// maxDiagramNodes is the most nodes one diagram may hold; past it a reader
// no longer holds the picture in one look.
const maxDiagramNodes = 15

const sourcesPrefix = "Sources: "

var (
	identifier      = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	flowchartHeader = regexp.MustCompile(`^flowchart( (TB|TD|BT|RL|LR))?$`)
	// The pieces of a flowchart line the node counter strips, in order:
	// edge labels, quoted text, the shape around an identifier, a link
	// carrying text, and a bare link.
	edgeLabel  = regexp.MustCompile(`\|[^|]*\|`)
	quoted     = regexp.MustCompile(`"[^"]*"`)
	shape      = regexp.MustCompile(`\[[^\[\]]*\]|\([^()]*\)|\{[^{}]*\}`)
	flagShape  = regexp.MustCompile(`([A-Za-z0-9_]+)>[^\]]*\]`)
	textLink   = regexp.MustCompile(`-{2,}\s[^-]*\s-{2,}[>xo]?|-\.\s[^.]*\s\.-[>xo]?|={2,}\s[^=]*\s={2,}[>xo]?`)
	bareLink   = regexp.MustCompile(`(\s[xo]|<)?(-{2,}|={2,}|-\.+-|~{3,})(>|[xo](\s|$))?`)
	classMark  = regexp.MustCompile(`:::\w+`)
	sequenceOp = regexp.MustCompile(`-{1,2}(>>|>|x|\))`)
	stateOp    = "-->"
)

// diagram is one fenced mermaid block: the line it opens on, its lines and
// the paths its Sources paragraph names.
type diagram struct {
	line    int
	lines   []string
	sources []string
}

// diagramProblems judges every mermaid block of a page: its kind, its node
// count and the Sources paragraph that follows it, whose paths exist in the
// walk and fall inside the page's covers.
func diagramProblems(files []string, p parsedPage) []string {
	diagrams, problems := diagrams(p.body)
	for _, d := range diagrams {
		n, err := countNodes(d.lines)
		switch {
		case err != nil:
			problems = append(problems, fmt.Sprintf("diagram at line %d: %v", d.line, err))
		case n > maxDiagramNodes:
			problems = append(problems, fmt.Sprintf("diagram at line %d holds %d nodes, the most is %d", d.line, n, maxDiagramNodes))
		}
		for _, src := range d.sources {
			if !slices.Contains(files, src) {
				problems = append(problems, fmt.Sprintf("diagram at line %d: source %s is no file of the walk", d.line, src))
			}
			if !slices.ContainsFunc(p.meta.Covers, func(glob string) bool { return matchGlob(glob, src) }) {
				problems = append(problems, fmt.Sprintf("diagram at line %d: source %s is outside the page's covers", d.line, src))
			}
		}
	}
	return problems
}

// diagrams extracts the mermaid blocks and reads each one's Sources
// paragraph, the first non-blank lines after its closing fence.
func diagrams(body []byte) ([]diagram, []string) {
	var found []diagram
	var problems []string
	lines := strings.Split(string(body), "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "```mermaid" {
			continue
		}
		d := diagram{line: i + 1}
		end := i + 1
		for ; end < len(lines) && strings.TrimSpace(lines[end]) != "```"; end++ {
			d.lines = append(d.lines, lines[end])
		}
		if end == len(lines) {
			return found, append(problems, fmt.Sprintf("diagram at line %d has no closing fence", d.line))
		}
		sources, err := readSources(lines[end+1:])
		if err != nil {
			problems = append(problems, fmt.Sprintf("diagram at line %d: %v", d.line, err))
		}
		d.sources = sources
		found = append(found, d)
		i = end
	}
	return found, problems
}

// readSources reads the paragraph after a fence: `Sources: ` and backticked
// paths separated by a comma and a space, wrapped over lines until a blank
// one, with an optional full stop.
func readSources(after []string) ([]string, error) {
	start := 0
	for start < len(after) && strings.TrimSpace(after[start]) == "" {
		start++
	}
	if start == len(after) || !strings.HasPrefix(after[start], sourcesPrefix) {
		return nil, errors.New("the first line after the fence is not a Sources: line")
	}
	end := start
	for end < len(after) && strings.TrimSpace(after[end]) != "" {
		end++
	}
	text := strings.TrimSuffix(strings.TrimPrefix(strings.Join(after[start:end], " "), sourcesPrefix), ".")
	var sources []string
	for _, item := range strings.Split(text, ", ") {
		src, ok := strings.CutPrefix(item, "`")
		src, ok2 := strings.CutSuffix(src, "`")
		if !ok || !ok2 || src == "" || strings.ContainsAny(src, "` ") {
			return nil, fmt.Errorf("Sources item %q is not one backticked path", item)
		}
		sources = append(sources, src)
	}
	return sources, nil
}

// countNodes reads the diagram's kind off its first line and counts the
// distinct nodes the way that kind declares them. A line the counter cannot
// read is an error, never a smaller count.
func countNodes(lines []string) (int, error) {
	body := trimmedLines(lines)
	if len(body) == 0 {
		return 0, errors.New("the diagram is empty")
	}
	header, rest := body[0], body[1:]
	switch {
	case flowchartHeader.MatchString(header):
		return flowchartNodes(rest)
	case header == "sequenceDiagram":
		return sequenceNodes(rest)
	case header == "stateDiagram-v2":
		return stateNodes(rest)
	}
	return 0, fmt.Errorf("%q is not flowchart, sequenceDiagram or stateDiagram-v2", header)
}

// trimmedLines drops blank lines and comments and trims the rest.
func trimmedLines(lines []string) []string {
	var out []string
	for _, line := range lines {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "%%") {
			out = append(out, t)
		}
	}
	return out
}

func startsWithAny(line string, words ...string) bool {
	for _, w := range words {
		if line == w || strings.HasPrefix(line, w+" ") {
			return true
		}
	}
	return false
}

// flowchartNodes counts the distinct identifiers declared or used in edges,
// inside a subgraph too; the subgraph itself is a frame, not a node.
func flowchartNodes(lines []string) (int, error) {
	nodes := map[string]bool{}
	for _, line := range lines {
		if startsWithAny(line, "subgraph", "end", "direction", "classDef", "class", "style", "linkStyle", "click") {
			continue
		}
		s := quoted.ReplaceAllString(edgeLabel.ReplaceAllString(line, " "), " ")
		for prev := ""; prev != s; {
			prev, s = s, shape.ReplaceAllString(s, " ")
		}
		s = flagShape.ReplaceAllString(s, "$1")
		s = textLink.ReplaceAllString(s, " ")
		s = bareLink.ReplaceAllString(s, " ")
		s = classMark.ReplaceAllString(strings.ReplaceAll(s, "&", " "), "")
		tokens := strings.Fields(s)
		if len(tokens) == 0 {
			return 0, fmt.Errorf("line %q names no node", line)
		}
		for _, token := range tokens {
			if !identifier.MatchString(token) {
				return 0, fmt.Errorf("cannot read %q as a node in line %q", token, line)
			}
			nodes[token] = true
		}
	}
	return len(nodes), nil
}

// sequenceNodes counts the participants and actors, declared or first used
// in a message.
func sequenceNodes(lines []string) (int, error) {
	nodes := map[string]bool{}
	for _, line := range lines {
		line = strings.TrimPrefix(line, "create ")
		switch {
		case startsWithAny(line, "participant", "actor"):
			name, _, _ := strings.Cut(strings.TrimSpace(line[strings.Index(line, " ")+1:]), " as ")
			nodes[strings.TrimSpace(name)] = true
		case startsWithAny(line, "Note", "note", "alt", "else", "end", "loop", "opt", "par", "and", "rect", "critical", "option",
			"break", "box", "autonumber", "title", "activate", "deactivate", "destroy", "link", "links", "properties", "details"):
		default:
			from, to, err := sequenceMessage(line)
			if err != nil {
				return 0, err
			}
			nodes[from], nodes[to] = true, true
		}
	}
	return len(nodes), nil
}

func sequenceMessage(line string) (from, to string, err error) {
	loc := sequenceOp.FindStringIndex(line)
	if loc == nil {
		return "", "", fmt.Errorf("cannot read %q as a message or a declaration", line)
	}
	from = strings.TrimSpace(line[:loc[0]])
	rest, _, ok := strings.Cut(line[loc[1]:], ":")
	to = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(rest), "+-"))
	if !ok || from == "" || to == "" {
		return "", "", fmt.Errorf("cannot read %q as a message", line)
	}
	return from, to, nil
}

// stateNodes counts the distinct states; [*] is the start and end
// pseudo-state, not a node.
func stateNodes(lines []string) (int, error) {
	nodes := map[string]bool{}
	inNote := false
	for _, line := range lines {
		switch {
		case inNote:
			inNote = line != "end note"
		case startsWithAny(line, "note"):
			inNote = !strings.Contains(line, ":")
		case line == "}" || line == "--" || startsWithAny(line, "direction", "classDef", "class", "hide"):
		case strings.Contains(line, stateOp):
			from, to, _ := strings.Cut(line, stateOp)
			to, _, _ = strings.Cut(to, ":")
			for _, s := range []string{strings.TrimSpace(from), strings.TrimSpace(to)} {
				if err := addState(nodes, s); err != nil {
					return 0, err
				}
			}
		default:
			if err := addState(nodes, stateDeclaration(line)); err != nil {
				return 0, err
			}
		}
	}
	return len(nodes), nil
}

// stateDeclaration reads the state a non-transition line declares: `state X`,
// `state X {`, `state "text" as X` or `X: text`.
func stateDeclaration(line string) string {
	if rest, ok := strings.CutPrefix(line, "state "); ok {
		if _, alias, ok := strings.Cut(rest, " as "); ok {
			rest = alias
		}
		return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "{"))
	}
	name, _, _ := strings.Cut(line, ":")
	return strings.TrimSpace(name)
}

func addState(nodes map[string]bool, s string) error {
	if s == "[*]" {
		return nil
	}
	if !identifier.MatchString(s) {
		return fmt.Errorf("cannot read %q as a state", s)
	}
	nodes[s] = true
	return nil
}
