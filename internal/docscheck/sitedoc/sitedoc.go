// Package sitedoc rewrites the diagram slots of the site's landing page from
// the README's Mermaid blocks, so the README stays the one source of each
// diagram and a page that lags it is a generated file out of date.
//
// A slot is `<!-- diagram: README.md <n> -->…<!-- /diagram -->`; its body is
// replaced with the n-th Mermaid block drawn as a static figure. The one
// `<!-- release -->…<!-- /release -->` slot takes the header's link to the
// newest release CHANGELOG.md dates.
package sitedoc

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/guardana/control/internal/docscheck/sitedoc/mermaid"
)

const (
	// Script is the generator that rewrites the page.
	Script = "scripts/gen-site.go"
	// Page is the one file the generator writes.
	Page = "site/index.html"
	// Source is the one file a slot may draw from.
	Source = "README.md"
	// MaxNodes bounds a drawn block. The drawer's cost grows with about the
	// cube of the node count, so a larger block is refused before drawing.
	MaxNodes = 15
)

// ErrSlot is wrapped by every refusal.
var ErrSlot = errors.New("sitedoc")

// Slot names the block one slot draws: a file and a 1-based block number.
type Slot struct {
	Path  string
	Block int
}

const (
	openPrefix = "<!-- diagram:"
	closeMark  = "<!-- /diagram -->"
)

var openMark = regexp.MustCompile(`^<!-- diagram: (\S+) ([1-9][0-9]{0,2}) -->`)

// span is a slot and the byte range of its body in the page.
type span struct {
	Slot
	start, end int
}

// Slots lists the page's slots in page order.
func Slots(page []byte) ([]Slot, error) {
	spans, err := slotSpans(page)
	if err != nil {
		return nil, err
	}
	slots := make([]Slot, 0, len(spans))
	for _, s := range spans {
		slots = append(slots, s.Slot)
	}
	return slots, nil
}

func slotSpans(page []byte) ([]span, error) {
	var spans []span
	open := -1
	var current span
	for pos := 0; pos < len(page); {
		next, isOpen := nextMarker(page[pos:])
		if next < 0 {
			break
		}
		at := pos + next
		if !isOpen {
			if open < 0 {
				return nil, fmt.Errorf("%w: byte %d: a diagram slot closes and none is open", ErrSlot, at)
			}
			current.end = at
			spans = append(spans, current)
			open = -1
			pos = at + len(closeMark)
			continue
		}
		if open >= 0 {
			return nil, fmt.Errorf("%w: byte %d: a diagram slot opens inside another", ErrSlot, at)
		}
		m := openMark.FindSubmatch(page[at:])
		if m == nil {
			end := bytes.Index(page[at:], []byte("-->"))
			if end < 0 {
				end = len(page) - at
			}
			return nil, fmt.Errorf("%w: byte %d: malformed marker %q, want `%s <file> <n> -->`", ErrSlot, at, page[at:at+end], openPrefix)
		}
		if string(m[1]) != Source {
			return nil, fmt.Errorf("%w: byte %d: a slot names %s; the page draws only from %s", ErrSlot, at, m[1], Source)
		}
		n, err := strconv.Atoi(string(m[2]))
		if err != nil {
			return nil, fmt.Errorf("%w: byte %d: %w", ErrSlot, at, err)
		}
		open = at
		current = span{Slot: Slot{Path: string(m[1]), Block: n}, start: at + len(m[0])}
		pos = current.start
	}
	if open >= 0 {
		return nil, fmt.Errorf("%w: byte %d: a diagram slot is not closed", ErrSlot, open)
	}
	if len(spans) == 0 {
		return nil, fmt.Errorf("%w: the page holds no diagram slot", ErrSlot)
	}
	return spans, nil
}

// nextMarker returns the offset of the next open or close marker in b, and
// whether it opens; -1 when there is none.
func nextMarker(b []byte) (int, bool) {
	o, c := bytes.Index(b, []byte(openPrefix)), bytes.Index(b, []byte(closeMark))
	switch {
	case o < 0 && c < 0:
		return -1, false
	case o < 0:
		return c, false
	case c < 0 || o < c:
		return o, true
	}
	return c, false
}

// Blocks returns the bodies of the Markdown file's `mermaid` fences, in
// order. A fence of another language hides whatever it holds.
func Blocks(markdown []byte) ([]string, error) {
	var blocks []string
	var body strings.Builder
	fenced, mermaidFence, start := false, false, 0
	for i, line := range strings.Split(string(markdown), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case !fenced && strings.HasPrefix(trimmed, "```"):
			fenced, start = true, i+1
			mermaidFence = strings.TrimSpace(strings.TrimPrefix(trimmed, "```")) == "mermaid"
			body.Reset()
		case fenced && trimmed == "```":
			if mermaidFence {
				blocks = append(blocks, body.String())
			}
			fenced = false
		case fenced && mermaidFence:
			body.WriteString(line + "\n")
		}
	}
	if fenced {
		return nil, fmt.Errorf("%w: the fence opened on line %d is not closed", ErrSlot, start)
	}
	return blocks, nil
}

// Render returns the page with every slot's body replaced by its block,
// drawn with the slot's 1-based position on the page as the figure index.
func Render(page, readme []byte) ([]byte, error) {
	return render(page, readme, mermaid.Figure)
}

func render(page, readme []byte, draw func(string, int) (string, error)) ([]byte, error) {
	spans, err := slotSpans(page)
	if err != nil {
		return nil, err
	}
	blocks, err := Blocks(readme)
	if err != nil {
		return nil, err
	}
	sources := make([]string, len(spans))
	for i, s := range spans {
		if s.Block > len(blocks) {
			return nil, fmt.Errorf("%w: slot %d asks for block %d; %s holds %d mermaid block(s)", ErrSlot, i+1, s.Block, Source, len(blocks))
		}
		g, err := mermaid.Parse(blocks[s.Block-1])
		if err != nil {
			return nil, fmt.Errorf("%w: %s block %d: %w", ErrSlot, Source, s.Block, err)
		}
		if len(g.Nodes) > MaxNodes {
			return nil, fmt.Errorf("%w: %s block %d has %d nodes, over the %d a drawn block may hold", ErrSlot, Source, s.Block, len(g.Nodes), MaxNodes)
		}
		sources[i] = blocks[s.Block-1]
	}
	var out bytes.Buffer
	last := 0
	for i, s := range spans {
		figure, err := draw(sources[i], i+1)
		if err != nil {
			return nil, fmt.Errorf("%w: %s block %d: %w", ErrSlot, Source, s.Block, err)
		}
		out.Write(page[last:s.start])
		out.WriteString("\n" + figure + "\n")
		last = s.end
	}
	out.Write(page[last:])
	return out.Bytes(), nil
}
