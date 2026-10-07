package runview_test

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var labels = []string{"enforced", "decided, not enforced", "observed", "exception or approval", "blocked", "not seen"}

var (
	legendRow = regexp.MustCompile(`(?s)<tr class="legend">.*?</tr>`)
	shapeEl   = regexp.MustCompile(`<(rect|circle|polygon|path|line|ellipse|polyline)\b([^>]*?)/?>`)
	attr      = regexp.MustCompile(`([a-z-]+)="([^"]*)"`)
)

// shapeOf is a mark's drawing without its colour: each element with its
// geometry, and apart the line style of its strokes.
func shapeOf(svg string) (geometry, dash string) {
	var g []string
	dash = "solid"
	for _, el := range shapeEl.FindAllStringSubmatch(svg, -1) {
		parts := []string{el[1]}
		for _, a := range attr.FindAllStringSubmatch(el[2], -1) {
			switch a[1] {
			case "class", "fill", "stroke", "stroke-width", "fill-opacity", "stroke-linecap":
			case "stroke-dasharray":
				dash = a[2]
			default:
				parts = append(parts, a[1]+"="+a[2])
			}
		}
		g = append(g, strings.Join(parts, " "))
	}
	return strings.Join(g, "; "), dash
}

// TestEveryMarkDiffersWithoutColour: the legend draws the six marks, each
// with its text, and no two share a shape or a line style once colour is
// taken away. Every mark is hidden from a reader of text, which reads its
// label instead.
func TestEveryMarkDiffersWithoutColour(t *testing.T) {
	page := draw(t, goldens[1])
	rows := legendRow.FindAllString(page, -1)
	if len(rows) != len(labels) {
		t.Fatalf("the legend has %d rows, want %d", len(rows), len(labels))
	}
	shapes, dashes := map[string]string{}, map[string]string{}
	for i, row := range rows {
		if got := markLabel.FindStringSubmatch(row); got == nil || got[1] != labels[i] {
			t.Errorf("legend row %d is labelled %v, want %q", i, got, labels[i])
		}
		g, d := shapeOf(row)
		if g == "" {
			t.Fatalf("legend row %d draws no shape", i)
		}
		if other, ok := shapes[g]; ok {
			t.Errorf("%q and %q share the shape %s", other, labels[i], g)
		}
		if other, ok := dashes[d]; ok {
			t.Errorf("%q and %q share the line style %s", other, labels[i], d)
		}
		shapes[g], dashes[d] = labels[i], labels[i]
	}
	for _, svg := range regexp.MustCompile(`<svg[^>]*>`).FindAllString(page, -1) {
		if !strings.Contains(svg, `aria-hidden="true"`) {
			t.Errorf("a mark is not hidden from a reader of text: %s", svg)
		}
	}
	if n, m := strings.Count(page, "<svg"), strings.Count(page, `<span class="mk-label">`); n != m {
		t.Errorf("%d marks and %d labels", n, m)
	}
}

// luminance is a colour's relative luminance as WCAG 2 defines it.
func luminance(hex string) float64 {
	v, err := strconv.ParseUint(strings.TrimPrefix(hex, "#"), 16, 32)
	if err != nil || len(hex) != 7 {
		return math.NaN()
	}
	var l [3]float64
	for i, shift := range []uint{16, 8, 0} {
		c := float64((v>>shift)&0xff) / 255
		if c <= 0.04045 {
			l[i] = c / 12.92
		} else {
			l[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*l[0] + 0.7152*l[1] + 0.0722*l[2]
}

func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	return (math.Max(la, lb) + 0.05) / (math.Min(la, lb) + 0.05)
}

// TestContrastIsComputedAsWCAGDoes: the grey that just meets 4.5:1 on white
// does, and one step lighter does not.
func TestContrastIsComputedAsWCAGDoes(t *testing.T) {
	if c := contrast("#767676", "#ffffff"); c < 4.5 || c > 4.6 {
		t.Errorf("#767676 on white is %.3f, want 4.54", c)
	}
	if c := contrast("#777777", "#ffffff"); c >= 4.5 {
		t.Errorf("#777777 on white is %.3f, want under 4.5", c)
	}
	if c := contrast("#000000", "#ffffff"); math.Abs(c-21) > 1e-9 {
		t.Errorf("black on white is %.3f, want 21", c)
	}
}

var (
	cssVar    = regexp.MustCompile(`--([a-z-]+):\s*(#[0-9a-f]{6});`)
	darkBlock = regexp.MustCompile(`(?s)@media \(prefers-color-scheme: dark\) \{\s*:root \{(.*?)\}\s*\}`)
	lightRoot = regexp.MustCompile(`(?s)<style>\s*:root \{(.*?)\}`)
)

func colours(block string) map[string]string {
	out := map[string]string{}
	for _, m := range cssVar.FindAllStringSubmatch(block, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// TestEveryColourMeetsContrastInBothSchemes: in the light scheme and the
// dark, every colour the page names, each mark's included, has at least
// 4.5:1 against the background, and the dark scheme sets every colour the
// light one does.
func TestEveryColourMeetsContrastInBothSchemes(t *testing.T) {
	page := draw(t, goldens[1])
	light, dark := lightRoot.FindStringSubmatch(page), darkBlock.FindStringSubmatch(page)
	if light == nil || dark == nil {
		t.Fatal("the page states no light or no dark colours")
	}
	for name, scheme := range map[string]map[string]string{"light": colours(light[1]), "dark": colours(dark[1])} {
		bg, ok := scheme["bg"]
		if !ok || len(scheme) < 9 {
			t.Fatalf("%s: %d colours and background %q", name, len(scheme), bg)
		}
		for v, c := range scheme {
			if v == "bg" || strings.HasSuffix(v, "-bg") {
				continue
			}
			if r := contrast(c, bg); !(r >= 4.5) {
				t.Errorf("%s: --%s %s on %s is %.2f:1, under 4.5:1", name, v, c, bg, r)
			}
		}
	}
	for v := range colours(light[1]) {
		if _, ok := colours(dark[1])[v]; !ok {
			t.Errorf("the dark scheme leaves --%s as the light one sets it", v)
		}
	}
}
