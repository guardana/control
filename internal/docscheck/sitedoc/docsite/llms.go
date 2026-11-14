package docsite

import (
	"bytes"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/docscheck/frontmatter"
	"github.com/guardana/control/internal/docscheck/indexdoc"
)

const (
	// LLMs is the documentation's map in the llms.txt shape, for a reader's
	// tools: every page with its address and summary, grouped as the index
	// groups them.
	LLMs = "site/llms.txt"
	// Readme holds the lede the map quotes as the project's description.
	Readme = "README.md"
)

// llmsOptional is the llms.txt section a reader's tools may skip.
const llmsOptional = "Optional"

const llmsStatus = "This is alpha software and not a security boundary: do not deploy it as one. " +
	"What exists today is listed on the status page, each capability with its label, " +
	"implemented, experimental or planned:"

var boldLine = regexp.MustCompile(`^\*\*(.+)\*\*$`)

// readmeLede is the README's first paragraph after its bold tagline, joined
// onto one line: the sentence the landing page sets under its heading.
func readmeLede(fsys fs.FS) (string, error) {
	data, err := fs.ReadFile(fsys, Readme)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for i, line := range lines {
		if !boldLine.MatchString(strings.TrimSpace(line)) {
			continue
		}
		var lede []string
		for _, next := range lines[i+1:] {
			next = strings.TrimSpace(next)
			switch {
			case next == "" && len(lede) == 0:
			case next == "":
				return strings.Join(lede, " "), nil
			default:
				lede = append(lede, next)
			}
		}
		if len(lede) > 0 {
			return strings.Join(lede, " "), nil
		}
		return "", fmt.Errorf("%s: no paragraph follows the tagline on line %d", Readme, i+1)
	}
	return "", fmt.Errorf("%s: no line is set wholly in bold, the tagline the lede follows", Readme)
}

// llms writes the map: the product's name, the lede, the status, then one
// section per page type in the index's order and the records as the optional
// section, each page a link to its address with its summary.
func llms(pages []*page, lede string) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# %s\n\n> %s\n\n%s %s/docs/status\n", brand.Name, lede, llmsStatus, Origin)
	section := func(heading string, of func(*page) bool) {
		first := true
		for _, p := range pages {
			if !of(p) {
				continue
			}
			if first {
				fmt.Fprintf(&b, "\n## %s\n\n", heading)
				first = false
			}
			fmt.Fprintf(&b, "- [%s](%s%s): %s\n", p.title, Origin, p.url, p.summary)
		}
	}
	for _, typ := range frontmatter.Types {
		heading := indexdoc.Section(typ)
		section(heading, func(p *page) bool { return p.section == heading })
	}
	section(llmsOptional, func(p *page) bool { return p.section == sectionRecords })
	return b.Bytes()
}
