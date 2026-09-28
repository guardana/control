package mermaid

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// A left-to-right drawing gives way to the top-to-bottom one once its figure
// is too narrow to show it at switchScale; below minScale a drawing scrolls
// inside its figure rather than shrinking its text further.
var (
	switchScale = 0.8
	minScale    = 0.75
)

// variant is one drawing of a diagram: its id prefix, its CSS class and its
// orientation.
type variant struct {
	uid      string
	class    string
	vertical bool
	minScale float64
}

// Figure draws one diagram as a `<figure>` holding its SVG drawings and the
// style they need. index tells apart two figures of one source on a page.
func Figure(source string, index int) (string, error) {
	g, err := Parse(source)
	if err != nil {
		return "", err
	}
	rank, err := ranks(g)
	if err != nil {
		return "", err
	}
	laid, chains := layers(g, rank)
	digest := sha256.Sum256([]byte(source))
	uid := "dg" + hex.EncodeToString(digest[:])[:6] + strconv.Itoa(index)
	var drawings, narrow string
	if g.Direction == "TD" {
		tall, err := place(g, laid, true, false)
		if err != nil {
			return "", err
		}
		drawings = svg(g, tall, chains, variant{uid + "w", "dg-w", true, minScale})
	} else {
		wide, err := place(g, laid, false, false)
		if err != nil {
			return "", err
		}
		tall, err := place(g, laid, true, true)
		if err != nil {
			return "", err
		}
		drawings = svg(g, wide, chains, variant{uid + "w", "dg-w", false, 0}) +
			svg(g, tall, chains, variant{uid + "n", "dg-n", true, minScale})
		narrow = "<style>@container (max-width:" + num(float64(wide.width*switchScale)) + "px){#" + uid +
			" .dg-w{display:none}#" + uid + " .dg-n{display:block}}</style>"
	}
	return `<figure class="dg" id="` + uid + `">` + style + narrow + drawings + "</figure>", nil
}

// num writes a coordinate with one decimal, rounded half to even on the
// exact binary value, and drops a trailing ".0".
func num(value float64) string {
	return strings.TrimSuffix(strconv.FormatFloat(value, 'f', 1, 64), ".0")
}

func svg(g *Graph, d *drawing, chains [][]string, v variant) string {
	var b strings.Builder
	sizing := "width:100%;max-width:" + num(d.width) + "px"
	if v.minScale != 0 {
		sizing += ";min-width:" + num(float64(d.width*v.minScale)) + "px"
	}
	fmt.Fprintf(&b, `<svg class="%s" style="%s" viewBox="0 0 %s %s" width="%s" height="%s" role="img" `+
		`aria-labelledby="%s-t %s-d" xmlns="http://www.w3.org/2000/svg">`,
		v.class, sizing, num(d.width), num(d.height), num(d.width), num(d.height), v.uid, v.uid)
	fmt.Fprintf(&b, `<title id="%s-t">%s</title><desc id="%s-d">%s</desc><defs>`,
		v.uid, escape(g.Title), v.uid, escape(g.Description))
	for _, kind := range []string{"solid", "main"} {
		fmt.Fprintf(&b, `<marker id="%s-%s" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" `+
			`markerHeight="7" orient="auto-start-reverse"><path class="h %s" d="M0 0L10 5L0 10z"/></marker>`,
			v.uid, kind, kind)
	}
	b.WriteString("</defs>")
	for _, group := range g.Groups {
		gb := d.groups[group.ID]
		left, top := gb.x-gb.w/2, gb.y-gb.h/2
		fmt.Fprintf(&b, `<g class="g"><rect x="%s" y="%s" width="%s" height="%s" rx="12"/>`+
			`<text x="%s" y="%s">%s</text></g>`,
			num(left), num(top), num(gb.w), num(gb.h), num(left+groupPad), num(top+groupPad+4), escape(group.Title))
	}
	var labels strings.Builder
	for i, e := range g.Edges {
		points := make([]*box, 0, len(chains[i]))
		for _, item := range chains[i] {
			points = append(points, d.boxes[item])
		}
		path, mx, my := edgePath(points, v.vertical)
		marker := "solid"
		if e.Style == "main" {
			marker = "main"
		}
		fmt.Fprintf(&b, `<path class="e %s" d="%s" marker-end="url(#%s-%s)"/>`, e.Style, path, v.uid, marker)
		if e.Label != "" {
			width := textWidth(e.Label, true) + 12
			fmt.Fprintf(&labels, `<g class="l"><rect x="%s" y="%s" width="%s" height="%s" rx="5"/>`+
				`<text x="%s" y="%s">%s</text></g>`,
				num(mx-width/2), num(my-labelH/2), num(width), num(labelH), num(mx), num(my), escape(e.Label))
		}
	}
	b.WriteString(labels.String())
	for _, node := range g.Nodes {
		b.WriteString(shape(node, d.boxes[node.ID]))
	}
	b.WriteString("</svg>")
	return b.String()
}

// edgePath draws an edge through its chain as cubic curves and returns the
// middle of its first segment, where a label sits.
func edgePath(points []*box, vertical bool) (string, float64, float64) {
	first, last := points[0], points[len(points)-1]
	type point struct{ x, y float64 }
	coords := make([]point, 0, len(points))
	if vertical {
		coords = append(coords, point{first.x, first.y + first.h/2})
	} else {
		coords = append(coords, point{first.x + first.w/2, first.y})
	}
	for _, p := range points[1 : len(points)-1] {
		coords = append(coords, point{p.x, p.y})
	}
	if vertical {
		coords = append(coords, point{last.x, last.y - last.h/2})
	} else {
		coords = append(coords, point{last.x - last.w/2, last.y})
	}
	var b strings.Builder
	b.WriteString("M" + num(coords[0].x) + " " + num(coords[0].y))
	for i := 1; i < len(coords); i++ {
		a, z := coords[i-1], coords[i]
		c1, c2 := point{a.x, a.y}, point{z.x, z.y}
		if vertical {
			bend := (z.y - a.y) / 2
			c1.y, c2.y = a.y+bend, z.y-bend
		} else {
			bend := (z.x - a.x) / 2
			c1.x, c2.x = a.x+bend, z.x-bend
		}
		fmt.Fprintf(&b, "C%s %s %s %s %s %s", num(c1.x), num(c1.y), num(c2.x), num(c2.y), num(z.x), num(z.y))
	}
	return b.String(), (coords[0].x + coords[1].x) / 2, (coords[0].y + coords[1].y) / 2
}

func shape(node *Node, b *box) string {
	classes := strings.Join(append([]string{"n"}, node.Classes...), " ")
	left, top := b.x-b.w/2, b.y-b.h/2
	var outline string
	if node.Shape == "doc" {
		lip := docLip
		curve := top + float64(lip*2.2)
		outline = `<path d="M` + num(left) + " " + num(top+lip) +
			"C" + num(left) + " " + num(top-lip/3) + " " + num(left+b.w) + " " + num(top-lip/3) + " " +
			num(left+b.w) + " " + num(top+lip) + "V" + num(top+b.h-lip) +
			"C" + num(left+b.w) + " " + num(top+b.h+lip/3) + " " + num(left) + " " +
			num(top+b.h+lip/3) + " " + num(left) + " " + num(top+b.h-lip) + `Z"/>` +
			`<path class="lip" d="M` + num(left) + " " + num(top+lip) +
			"C" + num(left) + " " + num(curve) + " " + num(left+b.w) + " " +
			num(curve) + " " + num(left+b.w) + " " + num(top+lip) + `"/>`
	} else {
		radius := 10.0
		if node.Shape == "pill" {
			radius = b.h / 2
		}
		outline = `<rect x="` + num(left) + `" y="` + num(top) + `" width="` + num(b.w) +
			`" height="` + num(b.h) + `" rx="` + num(radius) + `"/>`
	}
	offset := 0.0
	if node.Shape == "doc" {
		offset = docLip / 2
	}
	first := b.y + offset - float64(float64(len(node.Lines)-1)*lineHeight)/2
	var text strings.Builder
	for index, line := range node.Lines {
		fmt.Fprintf(&text, `<text x="%s" y="%s">%s</text>`,
			num(b.x), num(first+float64(float64(index)*lineHeight)), escape(line))
	}
	return `<g class="` + classes + `">` + outline + text.String() + "</g>"
}
