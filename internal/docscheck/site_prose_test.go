// The landing page's and the README's free prose, read sentence by sentence:
// a promise of later work and a `planned` label are claims no status or
// backlog tag carries, so they are checked here.
package docscheck

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// futurePhrase matches a sentence that promises work for later.
var futurePhrase = regexp.MustCompile(`(?i)\b(?:comes?\s+next|coming\s+soon|next\s+release|will\s+ship|is\s+next|are\s+next|up\s+next|soon)\b`)

// inlineElements continue a sentence; every other element ends one.
var inlineElements = map[string]bool{
	"a": true, "abbr": true, "b": true, "cite": true, "code": true, "dfn": true, "em": true,
	"i": true, "kbd": true, "mark": true, "q": true, "s": true, "samp": true, "small": true,
	"span": true, "strong": true, "sub": true, "sup": true, "time": true, "u": true, "var": true,
}

// proseSegment is a run of text no block boundary breaks, whitespace
// collapsed, with the offsets at which a `planned` code element starts.
type proseSegment struct {
	text    string
	planned []int
	backlog bool
}

type segmenter struct {
	segments []proseSegment
	b        strings.Builder
	space    bool
	planned  []int
	backlog  bool
}

func (s *segmenter) flush() {
	if s.b.Len() > 0 {
		s.segments = append(s.segments, proseSegment{text: s.b.String(), planned: s.planned, backlog: s.backlog})
	}
	s.b.Reset()
	s.space, s.planned = false, nil
}

func (s *segmenter) write(text string) {
	for _, r := range text {
		if unicode.IsSpace(r) {
			s.space = s.b.Len() > 0
			continue
		}
		if s.space {
			s.b.WriteByte(' ')
			s.space = false
		}
		s.b.WriteRune(r)
	}
}

func (s *segmenter) markPlanned() {
	at := s.b.Len()
	if s.space {
		at++
	}
	s.planned = append(s.planned, at)
}

func (s *segmenter) visit(n *xnode) {
	if n.name == "style" || n.name == "script" {
		return
	}
	_, backlog := n.attrs["data-backlog"]
	block := backlog || !inlineElements[n.name]
	if block {
		s.flush()
	}
	outer := s.backlog
	s.backlog = outer || backlog
	if t := n.text(); n.name == "code" && (t == "planned" || t == "Planned") {
		s.markPlanned()
	}
	for _, p := range n.parts {
		if p.node != nil {
			s.visit(p.node)
		} else {
			s.write(p.text)
		}
	}
	if block {
		s.flush()
	}
	s.backlog = outer
}

// pageSegments is the page's visible text, broken at every block element and
// at every data-backlog element.
func pageSegments(doc *xnode) []proseSegment {
	s := &segmenter{}
	s.visit(doc)
	s.flush()
	return s.segments
}

var (
	markdownTag       = regexp.MustCompile(`<[^>]*>`)
	markdownListStart = regexp.MustCompile(`^(?:[-*>]|[0-9]+\.)\s`)
)

// readmeSegments is the README's prose: one segment per paragraph, list
// item, heading or table row, with fenced blocks and HTML tags left out.
func readmeSegments(markdown []byte) ([]proseSegment, error) {
	s := &segmenter{}
	fenced, opened := false, 0
	for i, line := range strings.Split(string(markdown), "\n") {
		trimmed := strings.TrimSpace(markdownTag.ReplaceAllString(line, " "))
		switch {
		case !fenced && strings.HasPrefix(strings.TrimSpace(line), "```"):
			s.flush()
			fenced, opened = true, i+1
		case fenced:
			fenced = strings.TrimSpace(line) != "```"
		case trimmed == "":
			s.flush()
		case strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "|"):
			s.flush()
			s.write(strings.TrimLeft(trimmed, "#"))
			s.flush()
		case markdownListStart.MatchString(trimmed):
			s.flush()
			s.write(markdownListStart.ReplaceAllString(trimmed, ""))
		default:
			s.write(" " + trimmed)
		}
	}
	if fenced {
		return nil, fmt.Errorf("the fence opened on line %d is not closed", opened)
	}
	s.flush()
	return s.segments, nil
}

// sentenceSpans splits collapsed text after each `.`, `!` or `?` that a
// space follows.
func sentenceSpans(text string) [][2]int {
	var spans [][2]int
	start := 0
	for i := 0; i+1 < len(text); i++ {
		if strings.IndexByte(".!?", text[i]) >= 0 && text[i+1] == ' ' {
			spans = append(spans, [2]int{start, i + 1})
			start = i + 2
		}
	}
	if start < len(text) {
		spans = append(spans, [2]int{start, len(text)})
	}
	return spans
}

// promiseProblems refuses each sentence that promises later work outside a
// data-backlog element, and counts the sentences it read.
func promiseProblems(name string, segments []proseSegment) ([]string, int) {
	var problems []string
	read := 0
	for _, seg := range segments {
		for _, span := range sentenceSpans(seg.text) {
			read++
			sentence := seg.text[span[0]:span[1]]
			if phrase := futurePhrase.FindString(sentence); phrase != "" && !seg.backlog {
				problems = append(problems, fmt.Sprintf("%s: the sentence %q promises future work (%q) outside a data-backlog item; name the task in docs/backlog.md instead", name, sentence, phrase))
			}
		}
	}
	return problems, read
}

// deliveredRows lists the component rows labelled implemented or
// experimental, as the page shows them: Markdown code spans unwrapped.
func deliveredRows(status map[string]string) map[string]string {
	rows := map[string]string{}
	for row, label := range status {
		if label == "implemented" || label == "experimental" {
			rows[strings.ReplaceAll(row, "`", "")] = label
		}
	}
	return rows
}

// plannedProblems refuses each `planned` code element whose sentence names a
// delivered component row exactly, and counts the elements it read.
func plannedProblems(name string, segments []proseSegment, status map[string]string) ([]string, int) {
	delivered := deliveredRows(status)
	rows := sortedKeys(delivered)
	var problems []string
	read := 0
	for _, seg := range segments {
		spans := sentenceSpans(seg.text)
		for _, at := range seg.planned {
			read++
			i := slices.IndexFunc(spans, func(sp [2]int) bool { return at >= sp[0] && at < sp[1] })
			if i < 0 {
				problems = append(problems, fmt.Sprintf("%s: a `planned` label at byte %d of %q sits in no sentence", name, at, seg.text))
				continue
			}
			sentence := seg.text[spans[i][0]:spans[i][1]]
			for _, row := range rows {
				if namesRow(sentence, row) {
					problems = append(problems, fmt.Sprintf("%s: the sentence %q calls %q planned; docs/status.md labels it %s", name, sentence, row, delivered[row]))
				}
			}
		}
	}
	return problems, read
}

// namesRow reports whether sentence holds row as a whole phrase: the same
// case, with no letter or digit against either end.
func namesRow(sentence, row string) bool {
	for from := 0; ; {
		i := strings.Index(sentence[from:], row)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(row)
		before, _ := utf8.DecodeLastRuneInString(sentence[:start])
		after, _ := utf8.DecodeRuneInString(sentence[end:])
		if !isWordRune(before) && !isWordRune(after) {
			return true
		}
		from = start + 1
	}
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
