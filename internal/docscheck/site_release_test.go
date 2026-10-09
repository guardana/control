package docscheck

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/docscheck/docsconfig"
	"github.com/guardana/control/internal/docscheck/sitedoc"
)

// releasedVersion reads the version docs/docs.json names as published.
func releasedVersion(t *testing.T) string {
	t.Helper()
	data, err := fs.ReadFile(repoFS(t), "docs/docs.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := docsconfig.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Released
}

// pillProblems holds a page to one release pill that names version and
// links that version's release page.
func pillProblems(doc *xnode, version string) []string {
	var pills []*xnode
	doc.walk(func(n *xnode) {
		if n.name == "a" && n.hasClass("vlink") {
			pills = append(pills, n)
		}
	})
	if len(pills) != 1 {
		return []string{fmt.Sprintf("holds %d release pills, want 1", len(pills))}
	}
	var problems []string
	if got, want := pills[0].text(), "v"+version; got != want {
		problems = append(problems, fmt.Sprintf("the release pill says %q, want %q, the released version in docs/docs.json", got, want))
	}
	if got, want := pills[0].attrs["href"], "https://"+brand.ModulePath+"/releases/tag/v"+version; got != want {
		problems = append(problems, fmt.Sprintf("the release pill links %q, want %q", got, want))
	}
	return problems
}

// footProblems holds a documentation page to one source line that says it
// describes main, names the released version and links the documentation
// at that version's tag.
func footProblems(doc *xnode, version string) []string {
	var feet []*xnode
	doc.walk(func(n *xnode) {
		if n.name == "p" && n.hasClass("src") {
			feet = append(feet, n)
		}
	})
	if len(feet) != 1 {
		return []string{fmt.Sprintf("holds %d source lines, want 1", len(feet))}
	}
	var problems []string
	text := feet[0].text()
	if !strings.Contains(text, " on main. ") {
		problems = append(problems, fmt.Sprintf("the source line %q does not say the page describes main", text))
	}
	if want := "The latest release is v" + version + ","; !strings.Contains(text, want) {
		problems = append(problems, fmt.Sprintf("the source line %q does not say %q", text, want))
	}
	tagDocs := "https://" + brand.ModulePath + "/tree/v" + version + "/docs"
	var tagLinks int
	feet[0].walk(func(n *xnode) {
		if n.name == "a" && strings.Contains(n.attrs["href"], "/tree/v") {
			tagLinks++
			if n.attrs["href"] != tagDocs {
				problems = append(problems, fmt.Sprintf("the source line links %q, want %q", n.attrs["href"], tagDocs))
			}
		}
	})
	if tagLinks != 1 {
		problems = append(problems, fmt.Sprintf("the source line holds %d links to a tag, want 1 to %s", tagLinks, tagDocs))
	}
	return problems
}

// The header's pill names the released version on the landing page and on
// every documentation page, and every documentation page's foot names it, so
// a change to docs/docs.json that lands without `make docs-gen` fails here.
func TestSiteShowsTheReleasedVersion(t *testing.T) {
	version := releasedVersion(t)
	if err := sitedoc.Published([]byte(strings.Join(readLines(t, repoFS(t), sitedoc.Changelog), "\n")), version); err != nil {
		t.Errorf("docs/docs.json: %v", err)
	}
	pages, docs := 0, 0
	for name, data := range loadSite(t) {
		if path.Ext(name) != ".html" {
			continue
		}
		pages++
		doc := loadPage(t, data)
		report(t, name, pillProblems(doc, version))
		if strings.HasPrefix(name, "docs/") {
			docs++
			report(t, name, footProblems(doc, version))
		}
	}
	if pages < minDocsPages || docs < minDocsPages-1 {
		t.Errorf("read %d pages and %d documentation pages, want at least %d and %d", pages, docs, minDocsPages, minDocsPages-1)
	}
}

func TestPillProblems(t *testing.T) {
	pill := func(text, href string) string {
		return `<a class="vlink" href="` + href + `"><span class="ver">` + text + `</span></a>`
	}
	tag := "https://" + brand.ModulePath + "/releases/tag/v1.2.3"
	page := func(body string) *xnode { return loadPage(t, []byte("<html><body>"+body+"</body></html>")) }
	wantNone(t, "the released version", pillProblems(page(pill("v1.2.3", tag)), "1.2.3"))
	for name, c := range map[string]struct{ body, want string }{
		"a newer dated section":  {pill("v1.2.4", "https://"+brand.ModulePath+"/releases/tag/v1.2.4"), `says "v1.2.4", want "v1.2.3"`},
		"an older release":       {pill("v1.2.2", tag), `says "v1.2.2", want "v1.2.3"`},
		"no leading v":           {pill("1.2.3", tag), `says "1.2.3"`},
		"the releases list":      {pill("v1.2.3", "https://"+brand.ModulePath+"/releases"), "the release pill links"},
		"another tag":            {pill("v1.2.3", "https://"+brand.ModulePath+"/releases/tag/v1.2.4"), "the release pill links"},
		"no pill":                {`<a href="/">x</a>`, "holds 0 release pills"},
		"two pills":              {pill("v1.2.3", tag) + pill("v1.2.3", tag), "holds 2 release pills"},
		"a pill of another kind": {`<span class="vlink">v1.2.3</span>`, "holds 0 release pills"},
	} {
		wantProblem(t, name, pillProblems(page(c.body), "1.2.3"), c.want)
	}
}

func TestFootProblems(t *testing.T) {
	tagDocs := "https://" + brand.ModulePath + "/tree/v1.2.3/docs"
	foot := func(version, href string) string {
		return `<p class="src">Rendered from <a href="x">x.md</a> on <code>main</code>. It describes the tree as it is now. ` +
			`The latest release is v` + version + `, and <a href="` + href + `">its documentation</a> is in its tag.</p>`
	}
	page := func(body string) *xnode { return loadPage(t, []byte("<html><body>"+body+"</body></html>")) }
	wantNone(t, "the released version", footProblems(page(foot("1.2.3", tagDocs)), "1.2.3"))
	for name, c := range map[string]struct{ body, want string }{
		"a newer dated section": {foot("1.2.4", "https://"+brand.ModulePath+"/tree/v1.2.4/docs"), `does not say "The latest release is v1.2.3,"`},
		"the tag of another":    {foot("1.2.3", "https://"+brand.ModulePath+"/tree/v1.2.4/docs"), "the source line links"},
		"no link to the tag":    {strings.Replace(foot("1.2.3", tagDocs), tagDocs, "https://"+brand.ModulePath+"/tree/main/docs", 1), "holds 0 links to a tag"},
		"not on main":           {strings.Replace(foot("1.2.3", tagDocs), "<code>main</code>", "<code>v1.2.3</code>", 1), "does not say the page describes main"},
		"no source line":        {`<p>Rendered.</p>`, "holds 0 source lines"},
		"two source lines":      {foot("1.2.3", tagDocs) + foot("1.2.3", tagDocs), "holds 2 source lines"},
	} {
		wantProblem(t, name, footProblems(page(c.body), "1.2.3"), c.want)
	}
}

// siteHeader returns the one `<header class="top">` element of a page, as
// written.
func siteHeader(page []byte) ([]byte, error) {
	const open, closing = `<header class="top">`, `</header>`
	if n := bytes.Count(page, []byte(open)); n != 1 {
		return nil, fmt.Errorf("holds %d site headers, want 1", n)
	}
	start := bytes.Index(page, []byte(open))
	end := bytes.Index(page[start:], []byte(closing))
	if end < 0 {
		return nil, fmt.Errorf("the site header is not closed")
	}
	return page[start : start+end+len(closing)], nil
}

// headerDifference quotes the first line where a page's header leaves the
// landing page's.
func headerDifference(landing, page string) string {
	a, b := strings.Split(landing, "\n"), strings.Split(page, "\n")
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return fmt.Sprintf("line %d is %q, the landing page's %q", i+1, b[i], a[i])
		}
	}
	return fmt.Sprintf("it holds %d lines, the landing page's %d", len(b), len(a))
}

// The landing page's header, its release slot's markers aside, is the bytes
// every documentation page carries, so the two kinds of page cannot drift.
func TestSiteHeaderIsOneOnEveryPage(t *testing.T) {
	site := loadSite(t)
	landing, err := siteHeader(site["index.html"])
	if err != nil {
		t.Fatalf("%s: %v", sitedoc.Page, err)
	}
	want := strings.NewReplacer("<!-- release -->", "", "<!-- /release -->", "").Replace(string(landing))
	if want == string(landing) {
		t.Errorf("%s: the header holds no release slot", sitedoc.Page)
	}
	pages := 0
	for _, name := range sortedKeys(site) {
		if !strings.HasPrefix(name, "docs/") || path.Ext(name) != ".html" {
			continue
		}
		pages++
		got, err := siteHeader(site[name])
		switch {
		case err != nil:
			t.Errorf("%s: %v", name, err)
		case string(got) != want:
			t.Errorf("%s: the header differs from %s's: %s", name, sitedoc.Page, headerDifference(want, string(got)))
		}
	}
	if pages < minDocsPages-1 {
		t.Errorf("read %d documentation pages, want at least %d", pages, minDocsPages-1)
	}
}
