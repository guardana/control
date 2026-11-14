// What the landing page and every documentation page must hold besides their
// links: illustrations with a text alternative, no script, and a theme switch
// that needs none.
package docscheck

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// illustrationProblems refuses an element drawn as an image, `role="img"`,
// that carries no text alternative, and counts the ones that do.
func illustrationProblems(doc *xnode) ([]string, int) {
	var problems []string
	drawn := 0
	doc.walk(func(n *xnode) {
		if n.attrs["role"] != "img" {
			return
		}
		if strings.TrimSpace(n.attrs["aria-label"]) == "" {
			problems = append(problems, fmt.Sprintf("a <%s role=\"img\"> has no aria-label", n.name))
			return
		}
		drawn++
	})
	return problems, drawn
}

// scriptProblems refuses a script element and an event handler attribute,
// whatever their case.
func scriptProblems(doc *xnode) []string {
	var problems []string
	doc.walk(func(n *xnode) {
		if strings.EqualFold(n.name, "script") {
			problems = append(problems, "a script element")
		}
		for _, name := range sortedKeys(n.attrs) {
			if strings.HasPrefix(strings.ToLower(name), "on") {
				problems = append(problems, fmt.Sprintf("the event handler %s on a <%s>", name, n.name))
			}
		}
	})
	return problems
}

// themeSwitchProblems holds a page to one checkbox with the id the tokens'
// :has() rules read, one label for it, and the tokens stylesheet.
func themeSwitchProblems(doc *xnode, tokens string) []string {
	var themed, boxes, labelled, sheets int
	doc.walk(func(n *xnode) {
		themed += count(n.attrs["id"] == "theme")
		boxes += count(n.attrs["id"] == "theme" && n.name == "input" && n.attrs["type"] == "checkbox")
		labelled += count(n.name == "label" && n.attrs["for"] == "theme")
		sheets += count(n.name == "link" && n.attrs["rel"] == "stylesheet" && n.attrs["href"] == tokens)
	})
	var problems []string
	if themed != 1 || boxes != 1 {
		problems = append(problems, fmt.Sprintf("%d elements carry the id theme and %d of them are a checkbox, want one checkbox", themed, boxes))
	}
	if labelled != 1 {
		problems = append(problems, fmt.Sprintf("%d labels for the theme switch, want 1", labelled))
	}
	if sheets != 1 {
		problems = append(problems, fmt.Sprintf("%d links to %s, want 1", sheets, tokens))
	}
	return problems
}

func count(match bool) int {
	if match {
		return 1
	}
	return 0
}

var themeBlock = regexp.MustCompile(`(?m)(?:^@media \(prefers-color-scheme:(light|dark)\)\{)?(:root|html:has\(#theme:checked\))\{([^}]*)\}`)

// themeTokenProblems reads the tokens' four palettes, the default light one,
// the dark one the system asks for, and the two a checked switch selects,
// and holds each switched palette to the other scheme's, so the switch
// selects exactly the theme the system did not ask for.
func themeTokenProblems(css []byte) []string {
	blocks := map[string]map[string]string{}
	for _, m := range themeBlock.FindAllStringSubmatch(string(css), -1) {
		key := m[1] + " " + m[2]
		if _, seen := blocks[key]; seen {
			return []string{fmt.Sprintf("the palette %q is set twice", key)}
		}
		blocks[key] = palette(m[3])
	}
	light, dark := blocks[" :root"], blocks["dark :root"]
	toDark, toLight := blocks["light html:has(#theme:checked)"], blocks["dark html:has(#theme:checked)"]
	var problems []string
	for name, p := range map[string]map[string]string{"the light palette": light, "the dark palette": dark, "the switch to dark": toDark, "the switch to light": toLight} {
		if len(p) < 10 {
			problems = append(problems, fmt.Sprintf("%s holds %d colours, want the whole palette", name, len(p)))
		}
	}
	if light["color-scheme"] != "light" || dark["color-scheme"] != "dark" {
		problems = append(problems, "the default palettes do not name their colour schemes")
	}
	if !maps.Equal(toDark, dark) {
		problems = append(problems, fmt.Sprintf("the switch to dark differs from the dark palette in %q", differing(toDark, dark)))
	}
	if !maps.Equal(toLight, light) {
		problems = append(problems, fmt.Sprintf("the switch to light differs from the light palette in %q", differing(toLight, light)))
	}
	return problems
}

// palette reads a block's declarations, the font families aside.
func palette(body string) map[string]string {
	decls := map[string]string{}
	for _, d := range strings.Split(body, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(d), ":")
		if !ok || name == "--sans" || name == "--mono" {
			continue
		}
		decls[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	return decls
}

func differing(a, b map[string]string) []string {
	var names []string
	for _, name := range append(sortedKeys(a), sortedKeys(b)...) {
		if a[name] != b[name] && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names
}
