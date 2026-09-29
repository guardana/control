// The landing page and its deploy files, read the way the host serves them.
package docscheck

import (
	"bytes"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/docscheck/sitedoc"
)

const (
	siteDir       = "site"
	brandDir      = "site/assets/brand/v1"
	ogCard        = "scripts/og-card.html"
	siteOrigin    = "https://control.guardana.dev/"
	brandV1Digest = "6d177eb80cb233c8fce3b613f11788f548ca0cbfc7bd573e2fada5debcb64e4c"
	brandV1Files  = 10
	minSiteLinks  = 20
)

// siteManifest is every file under site/.
var siteManifest = []string{
	".assetsignore",
	"_headers",
	"assets/brand/v1/SHA256SUMS",
	"assets/brand/v1/fonts/OFL.txt",
	"assets/brand/v1/fonts/ibm-plex-mono-latin-400-normal.woff2",
	"assets/brand/v1/fonts/ibm-plex-mono-latin-500-normal.woff2",
	"assets/brand/v1/fonts/ibm-plex-sans-latin-400-normal.woff2",
	"assets/brand/v1/fonts/ibm-plex-sans-latin-500-normal.woff2",
	"assets/brand/v1/fonts/ibm-plex-sans-latin-600-normal.woff2",
	"assets/brand/v1/fonts/ibm-plex-sans-latin-700-normal.woff2",
	"assets/brand/v1/icon.svg",
	"assets/brand/v1/mark.svg",
	"assets/brand/v1/tokens.css",
	"assets/control/control.css",
	"assets/control/icon.svg",
	"assets/control/mark-dark.svg",
	"assets/control/mark.svg",
	"favicon.svg",
	"index.html",
	"og.png",
	"robots.txt",
	"sitemap.xml",
}

func loadSite(t *testing.T) map[string][]byte {
	t.Helper()
	sub, err := fs.Sub(repoFS(t), siteDir)
	if err != nil {
		t.Fatal(err)
	}
	files, err := siteFiles(sub)
	if err != nil {
		t.Fatalf("reading %s: %v", siteDir, err)
	}
	return files
}

func loadPage(t *testing.T, data []byte) *xnode {
	t.Helper()
	doc, err := parseXHTML(data)
	if err != nil {
		t.Fatalf("the page does not parse as XML: %v", err)
	}
	return doc
}

func report(t *testing.T, what string, problems []string) {
	t.Helper()
	for _, p := range problems {
		t.Errorf("%s: %s", what, p)
	}
}

func TestSiteHoldsThePinnedManifest(t *testing.T) {
	report(t, siteDir, manifestProblems(loadSite(t), siteManifest))
}

func TestBrandFilesMatchThePinnedSums(t *testing.T) {
	sub, err := fs.Sub(repoFS(t), brandDir)
	if err != nil {
		t.Fatal(err)
	}
	report(t, brandDir, brandProblems(sub, brandV1Digest, brandV1Files))
}

// The page draws README blocks 1, 2 and 3 in that order, each figure indexed
// by its slot's position, and a re-render leaves the page as committed.
func TestSiteSlotsAreTheReadmesBlocks(t *testing.T) {
	fsys := repoFS(t)
	page, err := fs.ReadFile(fsys, sitedoc.Page)
	if err != nil {
		t.Fatal(err)
	}
	readme, err := fs.ReadFile(fsys, readmeName)
	if err != nil {
		t.Fatal(err)
	}
	slots, err := sitedoc.Slots(page)
	if err != nil {
		t.Fatal(err)
	}
	want := []sitedoc.Slot{{Path: "README.md", Block: 1}, {Path: "README.md", Block: 2}, {Path: "README.md", Block: 3}}
	if !slices.Equal(slots, want) {
		t.Errorf("slots = %v, want %v", slots, want)
	}
	report(t, sitedoc.Page, slotTitleProblems(page, readme, []string{
		"How a tool call is decided today",
		"Where " + brand.Name + " is going",
		"Where Guardana and " + brand.Name + " sit in a system's life",
	}))
	rendered, err := sitedoc.Render(page, readme)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rendered, page) {
		t.Errorf("%s lags README.md; run `make docs-gen`", sitedoc.Page)
	}
	doc := loadPage(t, page)
	indexes, problems := figureIndexes(doc)
	report(t, sitedoc.Page, problems)
	if !slices.Equal(indexes, []int{1, 2, 3}) {
		t.Errorf("figure indexes = %v, want [1 2 3]", indexes)
	}
	report(t, sitedoc.Page, idProblems(doc))
}

func TestSiteLoadsNothingFromAnotherHost(t *testing.T) {
	report(t, siteDir, hostProblems(loadSite(t)))
	data, err := fs.ReadFile(repoFS(t), siteDir+"/_headers")
	if err != nil {
		t.Fatal(err)
	}
	report(t, siteDir+"/_headers", headerProblems(data))
}

func TestSiteLinksResolve(t *testing.T) {
	problems, resolved := linkProblems(loadSite(t), repoFS(t))
	report(t, siteDir, problems)
	if resolved < minSiteLinks {
		t.Errorf("%d links resolved, want at least %d", resolved, minSiteLinks)
	}
}

func TestSiteItemsNameTheirSources(t *testing.T) {
	fsys := repoFS(t)
	status := statusRows(readLines(t, fsys, "docs/status.md"))
	backlog := backlogTitles(readLines(t, fsys, "docs/backlog.md"))
	if len(status) < minComponentRows || len(backlog) < 5 {
		t.Fatalf("read %d status rows and %d backlog ids; the sources were not parsed", len(status), len(backlog))
	}
	report(t, sitedoc.Page, tagProblems(loadPage(t, loadSite(t)["index.html"]), status, backlog, 5, 8, 5))
}

func TestTaglinesAreTheReadmes(t *testing.T) {
	fsys := repoFS(t)
	want, err := readmeTagline(readLines(t, fsys, readmeName))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{ogCard, sitedoc.Page} {
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			t.Fatal(err)
		}
		if problem := taglineProblem(loadPage(t, data), want); problem != "" {
			t.Errorf("%s: %s", name, problem)
		}
	}
}

func TestWranglerConfigIsExact(t *testing.T) {
	data, err := fs.ReadFile(repoFS(t), "wrangler.json")
	if err != nil {
		t.Fatal(err)
	}
	report(t, "wrangler.json", wranglerProblems(data))
}

var sitemapLoc = regexp.MustCompile(`<loc>([^<]*)</loc>`)

// The page names its one address as canonical, the sitemap lists it alone,
// and robots.txt points at the sitemap.
func TestSiteNamesItsOneAddress(t *testing.T) {
	files := loadSite(t)
	var canonical []string
	loadPage(t, files["index.html"]).walk(func(n *xnode) {
		if n.name == "link" && n.attrs["rel"] == "canonical" {
			canonical = append(canonical, n.attrs["href"])
		}
	})
	if !slices.Equal(canonical, []string{siteOrigin}) {
		t.Errorf("canonical links = %q, want [%s]", canonical, siteOrigin)
	}
	var locs []string
	for _, m := range sitemapLoc.FindAllStringSubmatch(string(files["sitemap.xml"]), -1) {
		locs = append(locs, m[1])
	}
	if !slices.Equal(locs, []string{siteOrigin}) {
		t.Errorf("sitemap locations = %q, want [%s]", locs, siteOrigin)
	}
	if !strings.Contains(string(files["robots.txt"]), "\nSitemap: "+siteOrigin+"sitemap.xml\n") {
		t.Errorf("robots.txt does not name %ssitemap.xml", siteOrigin)
	}
}

// docs-gen writes the page through the registered generator, so a page
// edited by hand around its slots still gets redrawn on the next run.
func TestSiteIsRenderedByDocsGen(t *testing.T) {
	runs, problems := docsGenRuns(readLines(t, repoFS(t), "Makefile"))
	report(t, "Makefile", problems)
	run := genRun{sitedoc.Script, sitedoc.Page}
	if !slices.Contains(runs, run) || !slices.Contains(renderedFiles, run) {
		t.Errorf("docs-gen does not run %s -o %s, or the run is not registered", run.script, run.page)
	}
}
