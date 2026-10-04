package sitedoc

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const readme = "# T\n\n" +
	"```mermaid\nflowchart LR\n    accTitle: One\n    accDescr: First.\n    A[a] --> B[b]\n```\n\n" +
	"```bash\n```mermaid\n```\n\n" +
	"```mermaid\nflowchart TD\n    accTitle: Two\n    accDescr: Second.\n    C[c] --> D[d]\n```\n"

// recorder stands in for the drawer: it records every call and draws a
// marker naming the source's title and the index it was given.
type recorder struct{ calls []string }

func (r *recorder) draw(source string, index int) (string, error) {
	title := strings.TrimSpace(strings.SplitN(strings.SplitN(source, "accTitle:", 2)[1], "\n", 2)[0])
	r.calls = append(r.calls, fmt.Sprintf("%s@%d", title, index))
	return fmt.Sprintf("<figure>%s@%d</figure>", title, index), nil
}

func TestBlocksSkipsAMermaidFenceInsideAnotherFence(t *testing.T) {
	blocks, err := Blocks([]byte(readme))
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 || !strings.Contains(blocks[0], "accTitle: One") || !strings.Contains(blocks[1], "accTitle: Two") {
		t.Errorf("Blocks = %q, want the two mermaid blocks in order", blocks)
	}
	if _, err := Blocks([]byte("```mermaid\nflowchart LR\n")); !errors.Is(err, ErrSlot) {
		t.Errorf("an unclosed fence: %v", err)
	}
}

func TestRenderDrawsEachSlotWithItsPosition(t *testing.T) {
	page := "<p>a</p><!-- diagram: README.md 2 -->old<!-- /diagram -->\n<!-- diagram: README.md 1 --><!-- /diagram -->"
	r := &recorder{}
	got, err := render([]byte(page), []byte(readme), r.draw)
	if err != nil {
		t.Fatal(err)
	}
	want := "<p>a</p><!-- diagram: README.md 2 -->\n<figure>Two@1</figure>\n<!-- /diagram -->\n" +
		"<!-- diagram: README.md 1 -->\n<figure>One@2</figure>\n<!-- /diagram -->"
	if string(got) != want {
		t.Errorf("render =\n%s\nwant\n%s", got, want)
	}
	again, err := render(got, []byte(readme), (&recorder{}).draw)
	if err != nil || string(again) != want {
		t.Errorf("a second render changed the page: %v\n%s", err, again)
	}
	slots, err := Slots(got)
	if err != nil || len(slots) != 2 || slots[0] != (Slot{"README.md", 2}) || slots[1] != (Slot{"README.md", 1}) {
		t.Errorf("Slots = %v, %v", slots, err)
	}
}

func TestAPageWithNoSlotRendersUnchanged(t *testing.T) {
	r := &recorder{}
	out, err := render([]byte("<p>a</p>"), []byte(readme), r.draw)
	if err != nil || string(out) != "<p>a</p>" {
		t.Errorf("render = %q, %v; want the page unchanged", out, err)
	}
}

func TestRenderRefusesABadSlot(t *testing.T) {
	for name, c := range map[string]struct{ page, want string }{
		"an unclosed slot":       {"<!-- diagram: README.md 1 -->", "not closed"},
		"a close with no open":   {"x<!-- /diagram -->", "closes and none is open"},
		"a nested slot":          {"<!-- diagram: README.md 1 --><!-- diagram: README.md 2 --><!-- /diagram --><!-- /diagram -->", "opens inside another"},
		"a malformed marker":     {"<!-- diagram: README.md -->x<!-- /diagram -->", "malformed"},
		"block zero":             {"<!-- diagram: README.md 0 -->x<!-- /diagram -->", "malformed"},
		"another file":           {"<!-- diagram: docs/other.md 1 -->x<!-- /diagram -->", "draws only from README.md"},
		"a block the file lacks": {"<!-- diagram: README.md 3 -->x<!-- /diagram -->", "holds 2 mermaid block(s)"},
	} {
		r := &recorder{}
		out, err := render([]byte(c.page), []byte(readme), r.draw)
		if !errors.Is(err, ErrSlot) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: render = %q, %v; want an error containing %q", name, out, err, c.want)
		}
		if len(r.calls) != 0 {
			t.Errorf("%s: the drawer ran %v before the refusal", name, r.calls)
		}
	}
}

// chain writes a block of n distinct nodes, each joined to the next.
func chain(n int) string {
	var b strings.Builder
	b.WriteString("```mermaid\nflowchart LR\n    accTitle: Many\n    accDescr: Many nodes.\n")
	for i := 1; i < n; i++ {
		fmt.Fprintf(&b, "    N%d --> N%d\n", i, i+1)
	}
	return b.String() + "```\n"
}

// The drawer costs about the cube of the node count, so a block over the
// bound is refused before it is handed to the drawer; one at the bound draws.
func TestRenderRefusesALargeBlockBeforeDrawing(t *testing.T) {
	page := []byte("<!-- diagram: README.md 1 --><!-- /diagram -->")
	r := &recorder{}
	if _, err := render(page, []byte(chain(15)), r.draw); err != nil || len(r.calls) != 1 {
		t.Fatalf("15 nodes: %v, drawer calls %v", err, r.calls)
	}
	r = &recorder{}
	_, err := render(page, []byte(chain(16)), r.draw)
	if !errors.Is(err, ErrSlot) || !strings.Contains(err.Error(), "16 nodes") {
		t.Errorf("16 nodes: %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("16 nodes: the drawer ran %v", r.calls)
	}
}

func TestRenderRefusesABlockThePortCannotRead(t *testing.T) {
	bad := "```mermaid\nsequenceDiagram\n    A->>B: x\n```\n"
	r := &recorder{}
	_, err := render([]byte("<!-- diagram: README.md 1 --><!-- /diagram -->"), []byte(bad), r.draw)
	if !errors.Is(err, ErrSlot) || len(r.calls) != 0 {
		t.Errorf("render = %v, drawer calls %v", err, r.calls)
	}
}

// Render goes through the real drawer: a figure whose id carries its index.
func TestRenderUsesTheDrawer(t *testing.T) {
	out, err := Render([]byte("<!-- diagram: README.md 1 --><!-- /diagram -->"), []byte(readme))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `<figure class="dg" id="dg`) || !strings.Contains(string(out), "<title id=") {
		t.Errorf("Render drew no figure:\n%s", out)
	}
}
