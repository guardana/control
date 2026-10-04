// The landing page and its deploy files, read the way the host serves them.
package docscheck

import (
	"bytes"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/docscheck/docsconfig"
	"github.com/guardana/control/internal/docscheck/sitedoc"
	"github.com/guardana/control/internal/docscheck/sitedoc/docsite"
)

const (
	siteDir       = "site"
	brandDir      = "site/assets/brand/v1"
	ogCard        = "scripts/og-card.html"
	siteOrigin    = "https://control.guardana.dev/"
	brandV1Digest = "6d177eb80cb233c8fce3b613f11788f548ca0cbfc7bd573e2fada5debcb64e4c"
	brandV1Files  = 10
	minSiteLinks  = 20
	// minDocsPages is a floor under the rendered set: a render that found
	// no page would otherwise compare nothing with nothing.
	minDocsPages = 60
	// minProseSentences is a floor under the sentences read from the page
	// and from the README, so an extraction that found none fails.
	minProseSentences = 20
)

// siteManifest is every file under site/ that no generator writes; the
// documentation pages and the sitemap are what docsite.Build writes.
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
	"assets/control/docs.css",
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

// docsFiles renders the documentation as `make docs-gen` does, keyed by the
// path under site/.
func docsFiles(t *testing.T) map[string][]byte {
	t.Helper()
	fsys := repoFS(t)
	data, err := fs.ReadFile(fsys, "docs/docs.json")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := docsconfig.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	built, err := docsite.Build(fsys, cfg.Excludes)
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string][]byte, len(built))
	for name, data := range built {
		files[strings.TrimPrefix(name, siteDir+"/")] = data
	}
	if len(files) < minDocsPages {
		t.Fatalf("the renderer wrote %d files, fewer than the %d pages that exist", len(files), minDocsPages)
	}
	return files
}

func TestSiteHoldsThePinnedManifest(t *testing.T) {
	want := slices.Clone(siteManifest)
	for name := range docsFiles(t) {
		if !slices.Contains(want, name) {
			want = append(want, name)
		}
	}
	report(t, siteDir, manifestProblems(loadSite(t), want))
}

// Every documentation page and the sitemap are what the renderer writes from
// the Markdown now, and site/docs/ holds nothing it does not write.
func TestDocsPagesAreRendered(t *testing.T) {
	want := docsFiles(t)
	site := loadSite(t)
	for _, name := range sortedKeys(want) {
		got, ok := site[name]
		switch {
		case !ok:
			t.Errorf("%s/%s is missing; run `make docs-gen`", siteDir, name)
		case !bytes.Equal(got, want[name]):
			t.Errorf("%s/%s lags its source; run `make docs-gen`\n%s", siteDir, name, firstDifferingLine(want[name], got))
		}
	}
	for _, name := range sortedKeys(site) {
		if _, ok := want[name]; !ok && strings.HasPrefix(name, "docs/") {
			t.Errorf("%s/%s is rendered from no page; run `make docs-gen`", siteDir, name)
		}
	}
}

func TestBrandFilesMatchThePinnedSums(t *testing.T) {
	sub, err := fs.Sub(repoFS(t), brandDir)
	if err != nil {
		t.Fatal(err)
	}
	report(t, brandDir, brandProblems(sub, brandV1Digest, brandV1Files))
}

// The home page draws its own diagrams in HTML, so a phone reflows them, and
// draws no README block: the README's Mermaid is for GitHub. A re-render
// leaves the page as committed.
func TestTheHomePageDrawsNoReadmeBlock(t *testing.T) {
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
	if len(slots) != 0 {
		t.Errorf("slots = %v, want none", slots)
	}
	rendered, err := sitedoc.Render(page, readme)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rendered, page) {
		t.Errorf("%s changes when rendered; run `make docs-gen`", sitedoc.Page)
	}
	doc := loadPage(t, page)
	figures := 0
	doc.walk(func(n *xnode) {
		if n.name == "figure" && n.hasClass("fig") {
			figures++
		}
	})
	if figures < 3 {
		t.Errorf("%s holds %d diagrams, want at least three", sitedoc.Page, figures)
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

// Every page holds each id once, so a fragment or a drawing's reference lands
// on the one element it names.
func TestSitePagesHoldEachIDOnce(t *testing.T) {
	pages := 0
	for name, data := range loadSite(t) {
		if path.Ext(name) == ".html" {
			pages++
			report(t, name, idProblems(loadPage(t, data)))
		}
	}
	if pages < minDocsPages {
		t.Errorf("read %d pages, want at least %d", pages, minDocsPages)
	}
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

// A free sentence that promises later work lags the moment the work ships;
// only a backlog item, which the test above holds to docs/backlog.md, may.
func TestSiteAndReadmePromiseNoUnlistedWork(t *testing.T) {
	fsys := repoFS(t)
	page, err := fs.ReadFile(fsys, sitedoc.Page)
	if err != nil {
		t.Fatal(err)
	}
	readme, err := fs.ReadFile(fsys, readmeName)
	if err != nil {
		t.Fatal(err)
	}
	segments, err := readmeSegments(readme)
	if err != nil {
		t.Fatalf("%s: %v", readmeName, err)
	}
	for name, segments := range map[string][]proseSegment{sitedoc.Page: pageSegments(loadPage(t, page)), readmeName: segments} {
		problems, read := promiseProblems(name, segments)
		for _, p := range problems {
			t.Error(p)
		}
		if read < minProseSentences {
			t.Errorf("%s: read %d sentences, want at least %d; the prose was not parsed", name, read, minProseSentences)
		}
	}
}

func TestSitePlannedNamesNoDeliveredRow(t *testing.T) {
	fsys := repoFS(t)
	status := statusRows(readLines(t, fsys, "docs/status.md"))
	if len(status) < minComponentRows {
		t.Fatalf("read %d status rows; the source was not parsed", len(status))
	}
	page, err := fs.ReadFile(fsys, sitedoc.Page)
	if err != nil {
		t.Fatal(err)
	}
	problems, read := plannedProblems(sitedoc.Page, pageSegments(loadPage(t, page)), status)
	for _, p := range problems {
		t.Error(p)
	}
	if read == 0 {
		t.Errorf("%s: no `planned` label was read", sitedoc.Page)
	}
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

// Each page names the address it is served at, under the site's one origin,
// as canonical; the sitemap lists exactly those; robots.txt points at the
// sitemap.
func TestSiteNamesItsOneAddress(t *testing.T) {
	files := loadSite(t)
	var want []string
	for _, name := range sortedKeys(files) {
		if path.Ext(name) != ".html" {
			continue
		}
		address := siteOrigin + strings.TrimSuffix(strings.TrimSuffix(name, ".html"), "index")
		var canonical []string
		loadPage(t, files[name]).walk(func(n *xnode) {
			if n.name == "link" && n.attrs["rel"] == "canonical" {
				canonical = append(canonical, n.attrs["href"])
			}
		})
		if !slices.Equal(canonical, []string{address}) {
			t.Errorf("%s: canonical links = %q, want [%s]", name, canonical, address)
		}
		want = append(want, address)
	}
	slices.Sort(want)
	var locs []string
	for _, m := range sitemapLoc.FindAllStringSubmatch(string(files["sitemap.xml"]), -1) {
		locs = append(locs, m[1])
	}
	if len(want) < minDocsPages || !slices.Equal(locs, want) {
		t.Errorf("sitemap locations = %q, want the %d page addresses %q", locs, len(want), want)
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
