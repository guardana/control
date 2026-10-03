package docscheck

import (
	"bytes"
	"fmt"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/docscheck/sitedoc"
)

var versionShape = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+`)

// newestRelease reads the changelog's first section heading that is not
// the unreleased one, and returns what its brackets hold.
func newestRelease(lines []string) string {
	for _, line := range lines {
		if strings.HasPrefix(line, "## [") && line != "## [Unreleased]" {
			version, _, _ := strings.Cut(strings.TrimPrefix(line, "## ["), "]")
			return version
		}
	}
	return ""
}

// pillProblems holds a page to one release pill that names version and
// links the releases page.
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
		problems = append(problems, fmt.Sprintf("the release pill says %q, want %q from %s", got, want, sitedoc.Changelog))
	}
	if got, want := pills[0].attrs["href"], "https://"+brand.ModulePath+"/releases"; got != want {
		problems = append(problems, fmt.Sprintf("the release pill links %q, want %q", got, want))
	}
	return problems
}

// The header's pill names the newest dated section of the changelog on the
// landing page and on every documentation page, so a release that lands
// without `make docs-gen` fails here.
func TestSiteShowsTheNewestRelease(t *testing.T) {
	version := newestRelease(readLines(t, repoFS(t), sitedoc.Changelog))
	if !versionShape.MatchString(version) {
		t.Fatalf("%s: the newest release heading names %q, not a version", sitedoc.Changelog, version)
	}
	pages := 0
	for name, data := range loadSite(t) {
		if path.Ext(name) == ".html" {
			pages++
			report(t, name, pillProblems(loadPage(t, data), version))
		}
	}
	if pages < minDocsPages {
		t.Errorf("read %d pages, want at least %d", pages, minDocsPages)
	}
}

func TestPillProblems(t *testing.T) {
	pill := func(text, href string) string {
		return `<a class="vlink" href="` + href + `"><span class="ver">` + text + `</span></a>`
	}
	releases := "https://" + brand.ModulePath + "/releases"
	page := func(body string) *xnode { return loadPage(t, []byte("<html><body>"+body+"</body></html>")) }
	wantNone(t, "the newest release", pillProblems(page(pill("v1.2.3", releases)), "1.2.3"))
	for name, c := range map[string]struct{ body, want string }{
		"an older release":       {pill("v1.2.2", releases), `says "v1.2.2", want "v1.2.3"`},
		"no leading v":           {pill("1.2.3", releases), `says "1.2.3"`},
		"another target":         {pill("v1.2.3", releases+"/tag/v1.2.3"), "the release pill links"},
		"no pill":                {`<a href="/">x</a>`, "holds 0 release pills"},
		"two pills":              {pill("v1.2.3", releases) + pill("v1.2.3", releases), "holds 2 release pills"},
		"a pill of another kind": {`<span class="vlink">v1.2.3</span>`, "holds 0 release pills"},
	} {
		wantProblem(t, name, pillProblems(page(c.body), "1.2.3"), c.want)
	}
}

func TestNewestReleaseSkipsTheUnreleasedSection(t *testing.T) {
	lines := []string{"# Changelog", "## [Unreleased]", "### [9.9.9]", "## [0.6.0-alpha] - 2026-10-03", "## [0.5.0-alpha] - 2026-10-02"}
	if got := newestRelease(lines); got != "0.6.0-alpha" {
		t.Errorf("newestRelease = %q, want 0.6.0-alpha", got)
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
