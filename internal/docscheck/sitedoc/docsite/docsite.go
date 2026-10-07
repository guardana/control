// Package docsite renders the documentation as pages of the website: every
// page and record under docs/, the roadmap and the changelog, each an XHTML file
// under site/docs/ whose links, anchors and diagrams mean on the site what they
// mean on the repository's host. The pages are committed, and a test renders
// them again and compares, so a page edited without `make docs-gen` fails the
// gate.
package docsite

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/docscheck/frontmatter"
	"github.com/guardana/control/internal/docscheck/indexdoc"
	"github.com/guardana/control/internal/docscheck/sitedoc"
)

const (
	// Dir is the directory the renderer owns: every file in it is written by
	// Build, and a file Build does not write is stale.
	Dir = "site/docs"
	// Sitemap lists the landing page and every rendered page.
	Sitemap = "site/sitemap.xml"
	// Origin is the site's one address.
	Origin = "https://control.guardana.dev"

	roadmap   = "ROADMAP.md"
	changelog = sitedoc.Changelog
)

// ErrSite is wrapped by every refusal.
var ErrSite = errors.New("docsite")

// page is one Markdown source and where it is served.
type page struct {
	src     string // repository path of the Markdown
	out     string // repository path of the rendered file
	url     string // the path it is served at, without an extension
	title   string
	summary string
	section string
	body    []byte // the Markdown after any frontmatter
	// bodyLine is the file's line body starts on, so a refusal names the
	// line an editor shows.
	bodyLine int
}

// Build renders every page from fsys, rooted at the repository, and returns
// what it writes keyed by repository path: each page under Dir and the
// sitemap. excluded names the paths the docs check skips: what docs/docs.json
// lists and what the repository does not hold.
func Build(fsys fs.FS, excluded func(string) bool) (map[string][]byte, error) {
	pages, err := collect(fsys, excluded)
	if err != nil {
		return nil, err
	}
	bySource := make(map[string]*page, len(pages))
	for _, p := range pages {
		bySource[p.src] = p
	}
	notes, err := fs.ReadFile(fsys, changelog)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSite, err)
	}
	release, err := sitedoc.Release(notes)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSite, err)
	}
	pill := sitedoc.Pill(release)
	nav := buildNav(pages)
	out := make(map[string][]byte, len(pages)+1)
	for _, p := range pages {
		body, err := renderBody(p, bySource, fsys)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrSite, p.src, err)
		}
		page := layout(p, nav, pill, body)
		if err := uniqueIDs(page); err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrSite, p.src, err)
		}
		out[p.out] = page
	}
	out[Sitemap] = sitemap(pages)
	return out, nil
}

// collect reads the index, every page and record the index lists, the roadmap
// and the changelog.
func collect(fsys fs.FS, excluded func(string) bool) ([]*page, error) {
	docs, records, err := indexdoc.Collect(fsys, excluded)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSite, err)
	}
	var pages []*page
	index, err := documentPage(fsys, indexdoc.Index, "")
	if err != nil {
		return nil, err
	}
	pages = append(pages, index)
	for _, d := range docs {
		p, err := documentPage(fsys, d.Path, indexdoc.Section(d.Meta.Type))
		if err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	for _, r := range records {
		body, err := fs.ReadFile(fsys, r.Path)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrSite, err)
		}
		p := newPage(r.Path, r.Title, "Decision record, "+strings.ToLower(r.Status)+".", sectionRecords, body)
		pages = append(pages, p)
	}
	for _, src := range []string{roadmap, changelog} {
		p, err := rootPage(fsys, src)
		if err != nil {
			return nil, err
		}
		pages = append(pages, p)
	}
	slices.SortFunc(pages, func(a, b *page) int { return strings.Compare(a.out, b.out) })
	return pages, nil
}

func documentPage(fsys fs.FS, src, section string) (*page, error) {
	data, err := fs.ReadFile(fsys, src)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSite, err)
	}
	meta, body, err := frontmatter.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrSite, src, err)
	}
	p := newPage(src, meta.Title, meta.Summary, section, body)
	p.bodyLine = bytes.Count(data[:len(data)-len(body)], []byte("\n")) + 1
	return p, nil
}

// rootPage reads a page at the repository's root, which carries no
// frontmatter: its title is its first line's heading.
func rootPage(fsys fs.FS, src string) (*page, error) {
	body, err := fs.ReadFile(fsys, src)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSite, err)
	}
	first, _, _ := strings.Cut(string(body), "\n")
	title, ok := strings.CutPrefix(first, "# ")
	if !ok || title == "" {
		return nil, fmt.Errorf("%w: %s: the first line is not the page's title", ErrSite, src)
	}
	summaries := map[string]string{
		roadmap:   "What has to become true, in the order it is planned.",
		changelog: "What each release changed, and what is unreleased on main.",
	}
	return newPage(src, title, summaries[src], sectionProject, body), nil
}

func newPage(src, title, summary, section string, body []byte) *page {
	p := &page{src: src, title: title, summary: summary, section: section, body: body, bodyLine: 1}
	switch {
	case src == indexdoc.Index:
		p.out, p.url = Dir+"/index.html", "/docs/"
	case strings.HasPrefix(src, "docs/"):
		rel := strings.TrimSuffix(strings.TrimPrefix(src, "docs/"), ".md")
		p.out, p.url = Dir+"/"+rel+".html", "/docs/"+rel
	default:
		name := strings.ToLower(strings.TrimSuffix(path.Base(src), ".md"))
		p.out, p.url = Dir+"/"+name+".html", "/docs/"+name
	}
	return p
}

var idAttr = regexp.MustCompile(` id="([^"]*)"`)

// uniqueIDs refuses a page that holds one id twice, such as a heading whose
// anchor is the layout's own id or a figure's: a fragment would land on
// whichever comes first.
func uniqueIDs(page []byte) error {
	seen := map[string]bool{}
	for _, m := range idAttr.FindAllSubmatch(page, -1) {
		id := string(m[1])
		if seen[id] {
			return fmt.Errorf("the id %q is used twice; rename the heading that takes it", id)
		}
		seen[id] = true
	}
	return nil
}

// sourceURL is where the repository's host shows a tracked file.
func sourceURL(rel string, dir bool) string {
	kind := "blob"
	if dir {
		kind = "tree"
	}
	return "https://" + brand.ModulePath + "/" + kind + "/main/" + rel
}
