package docscheck

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/guardana/control/internal/brand"
)

func mustParse(t *testing.T, page string) *xnode {
	t.Helper()
	doc, err := parseXHTML([]byte(page))
	if err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	return doc
}

func TestSiteLinkProblems(t *testing.T) {
	repo := fstest.MapFS{"docs/a.md": {Data: []byte("a")}, "docs/sub/b.md": {Data: []byte("b")}}
	blob := "https://" + brand.ModulePath + "/blob/main/"
	tree := "https://" + brand.ModulePath + "/tree/main/"
	site := map[string][]byte{
		"index.html": []byte(`<html><body id="top"><a href="/">home</a><a href="#top">up</a><a href="/sub/#s">s</a>` +
			`<a href="` + blob + `docs/a.md">a</a><a href="` + tree + `docs/sub">sub</a><a href="https://example.org/">x</a>` +
			`<svg><path marker-end="url(#top)"/></svg><link rel="stylesheet" href="assets/a.css"/></body></html>`),
		"sub/index.html":       []byte(`<html><body><p id="s">s</p><a href="../index.html#top">back</a></body></html>`),
		"assets/a.css":         []byte(`@font-face{src:url("fonts/f.woff2")}`),
		"assets/fonts/f.woff2": nil,
	}
	problems, resolved := linkProblems(site, repo)
	wantNone(t, "a site whose links resolve", problems)
	if resolved != 9 {
		t.Errorf("resolved %d links, want 9 (the host link is not one)", resolved)
	}
	for name, c := range map[string]struct{ link, want string }{
		"a missing file":            {`<a href="/nothing.html">x</a>`, "the site does not hold"},
		"a missing relative file":   {`<a href="sub/none.html">x</a>`, "the site does not hold"},
		"a missing fragment":        {`<a href="#nowhere">x</a>`, `holds no id "nowhere"`},
		"a missing fragment across": {`<a href="/sub/#top">x</a>`, `sub/index.html holds no id "top"`},
		"a missing marker target":   {`<svg><path marker-end="url(#gone)"/></svg>`, `holds no id "gone"`},
		"a blob of a missing file":  {`<a href="` + blob + `docs/none.md">x</a>`, "not in the repository"},
		"a blob of a directory":     {`<a href="` + blob + `docs/sub">x</a>`, "a blob link to a directory"},
		"a tree of a file":          {`<a href="` + tree + `docs/a.md">x</a>`, "a tree link to a file"},
		"an empty link":             {`<a href="">x</a>`, "an empty link"},
	} {
		files := maps.Clone(site)
		files["index.html"] = []byte(strings.Replace(string(site["index.html"]), "</body>", c.link+"</body>", 1))
		problems, _ := linkProblems(files, repo)
		wantProblem(t, name, problems, c.want)
	}
	files := maps.Clone(site)
	files["assets/a.css"] = []byte(`@font-face{src:url("fonts/none.woff2")}`)
	problems, _ = linkProblems(files, repo)
	wantProblem(t, "a stylesheet's missing font", problems, "assets/fonts/none.woff2")
	problems, _ = linkProblems(map[string][]byte{"index.html": []byte(`<html><a href="https://example.org/">x</a></html>`)}, repo)
	wantProblem(t, "no local link", problems, "no local link was resolved")
	problems, _ = linkProblems(map[string][]byte{"index.html": []byte(`<html><br></html>`)}, repo)
	wantProblem(t, "a page that does not parse", problems, "index.html:")
}

func TestIDProblemsAndFigureIndexes(t *testing.T) {
	doc := mustParse(t, `<html><figure class="dg" id="dgabc1231"/><p id="x"/><figure class="dg" id="dgabc1232"/></html>`)
	wantNone(t, "distinct ids", idProblems(doc))
	if got, problems := figureIndexes(doc); !slices.Equal(got, []int{1, 2}) || len(problems) != 0 {
		t.Errorf("figureIndexes = %v, %q", got, problems)
	}
	wantProblem(t, "a duplicate id", idProblems(mustParse(t, `<html><p id="x"/><svg><g id="x"/></svg></html>`)), `the id "x" is used twice`)
	wantProblem(t, "an empty id", idProblems(mustParse(t, `<html><p id=""/></html>`)), "empty id")
	if _, problems := figureIndexes(mustParse(t, `<html><figure class="dg" id="plain"/></html>`)); len(problems) == 0 {
		t.Error("a figure id with no index was accepted")
	}
}

func TestTagProblems(t *testing.T) {
	status := map[string]string{"Kernel `x`": "implemented", "Spool": "experimental", "Web UI": "planned"}
	backlog := map[string]string{"B01": "Build the first thing", "B02": "Build the second thing"}
	good := `<html><p>An <code class="st" data-status="Spool">experimental</code> spool.</p><ul>` +
		`<li data-status="Kernel ` + "`x`" + `"><span class="st implemented">implemented</span><span class="t">Kernel ` + "`x`" + `</span></li>` +
		`<li data-status="Spool"><span class="st">experimental</span><span class="t">Spool</span></li>` +
		`<li data-backlog="B01"><span class="id">B01</span><span class="t">Build the first thing</span></li>` +
		`<li data-backlog="B02"><span class="id">B02</span><span class="t">Build the second thing</span></li></ul></html>`
	wantNone(t, "items that name their sources", tagProblems(mustParse(t, good), status, backlog, 2, 2, 2))
	for name, c := range map[string]struct{ from, to, want string }{
		"a label not the row's":          {`<span class="st">experimental</span><span class="t">Spool`, `<span class="st">implemented</span><span class="t">Spool`, `shows ["implemented"], want the row's label "experimental"`},
		"no label shown":                 {`<span class="st">experimental</span><span class="t">Spool`, `<span class="t">Spool`, "shows []"},
		"a title not the row's":          {`<span class="t">Spool</span>`, `<span class="t">Production-ready spool</span>`, `titled ["Production-ready spool"], want the row "Spool"`},
		"no title":                       {`<span class="t">Spool</span>`, ``, `titled [], want the row "Spool"`},
		"an unknown row":                 {`<li data-status="Spool">`, `<li data-status="Spool store">`, "names no row"},
		"an empty data-status":           {`<li data-status="Spool">`, `<li data-status="">`, "an empty data-status"},
		"a label with no data-status":    {`<li data-status="Spool">`, `<li>`, `a status label "experimental" outside a data-status item`},
		"an inline label not the row's":  {`data-status="Spool">experimental</code>`, `data-status="Spool">implemented</code>`, `shows ["implemented"], want the row's label "experimental"`},
		"an inline label with no row":    {`<code class="st" data-status="Spool">`, `<code class="st">`, `a status label "experimental" outside a data-status item`},
		"an unknown backlog id":          {`data-backlog="B02"`, `data-backlog="B99"`, `"B99" names no task`},
		"an empty data-backlog":          {`data-backlog="B02"`, `data-backlog=""`, "an empty data-backlog"},
		"a backlog title not the task's": {`<span class="t">Build the second thing</span>`, `<span class="t">Ship the second thing</span>`, `titled ["Ship the second thing"], want the task "Build the second thing"`},
		"too few status items":           {`<li data-status="Spool"><span class="st">experimental</span><span class="t">Spool</span></li>`, ``, "1 status items, want 2 to 2"},
		"too many status items":          {`</ul>`, `<li data-status="Web UI"><span class="st">planned</span><span class="t">Web UI</span></li></ul>`, "3 status items, want 2 to 2"},
		"too few backlog items":          {`<li data-backlog="B02"><span class="id">B02</span><span class="t">Build the second thing</span></li>`, ``, "1 backlog items, want at least 2"},
	} {
		page := strings.Replace(good, c.from, c.to, 1)
		if page == good {
			t.Fatalf("%s: the fixture edit changed nothing", name)
		}
		wantProblem(t, name, tagProblems(mustParse(t, page), status, backlog, 2, 2, 2), c.want)
	}
}

func TestStatusRowsAndBacklogTitles(t *testing.T) {
	status := statusRows(strings.Split("## Components\n\n| Component | Status | Where |\n| --- | --- | --- |\n| Kernel `x` | `implemented` | `a/` |\n\n## Other\n\n| Kernel `x` | `planned` | b |\n", "\n"))
	if len(status) != 1 || status["Kernel `x`"] != "implemented" {
		t.Errorf("statusRows = %v", status)
	}
	titles := backlogTitles(strings.Split("| ID | Work |\n| --- | --- |\n| B05 | Package a demo |\n| B5 | b |\n| X01 | c |\n", "\n"))
	if len(titles) != 1 || titles["B05"] != "Package a demo" {
		t.Errorf("backlogTitles = %v", titles)
	}
}

func TestTaglineProblem(t *testing.T) {
	want, err := readmeTagline([]string{"# T", "", "**Watch the calls your *agents* make.**", "**Later.**"})
	if err != nil || want != "Watch the calls your *agents* make." {
		t.Fatalf("readmeTagline = %q, %v", want, err)
	}
	if _, err := readmeTagline([]string{"# T", "plain"}); err == nil {
		t.Error("a README with no bold line gave a tagline")
	}
	const tagline = "Watch the tool calls your AI agents make."
	good := `<html><p class="tagline">Watch the tool calls your <span>AI agents</span>
	make.</p></html>`
	if p := taglineProblem(mustParse(t, good), tagline); p != "" {
		t.Errorf("a matching tagline: %s", p)
	}
	for name, c := range map[string]struct{ page, want string }{
		"an older tagline": {`<html><p class="tagline">Gate the tools your agents call.</p></html>`, "want the README's"},
		"no tagline":       {`<html><p>Watch the tool calls your AI agents make.</p></html>`, "0 tagline elements"},
		"two taglines":     {`<html><p class="tagline">a</p><p class="big tagline">b</p></html>`, "2 tagline elements"},
	} {
		if p := taglineProblem(mustParse(t, c.page), tagline); !strings.Contains(p, c.want) {
			t.Errorf("%s: %q, want a problem containing %q", name, p, c.want)
		}
	}
}

const goodWrangler = `{
  "name": "` + brand.Slug + `",
  "compatibility_date": "2026-09-28",
  "assets": {"directory": "./site", "html_handling": "auto-trailing-slash"},
  "workers_dev": false,
  "preview_urls": false
}
`

func TestWranglerProblems(t *testing.T) {
	wantNone(t, "the pinned configuration", wranglerProblems([]byte(goodWrangler)))
	for name, c := range map[string]struct{ from, to, want string }{
		"a comment":          {`{`, "{\n  // deployed from here", "invalid character"},
		"a key twice":        {`"workers_dev": false,`, `"workers_dev": false, "workers_dev": true,`, `"workers_dev" appears twice`},
		"a nested key twice": {`"./site",`, `"./site", "directory": "./",`, `$.assets: the key "directory" appears twice`},
		"an extra key":       {`"preview_urls": false`, `"preview_urls": false, "main": "worker.js"`, "main is worker.js, want <nil>"},
		"workers.dev on":     {`"workers_dev": false`, `"workers_dev": true`, "workers_dev is true, want false"},
		"a missing key": {`,
  "preview_urls": false`, ``, "preview_urls is <nil>, want false"},
		"another directory":       {`"./site"`, `"."`, "assets is"},
		"another name":            {`"` + brand.Slug + `"`, `"site"`, "name is site"},
		"a value after the value": {"}\n", "}\n{}\n", "something follows"},
		"not an object":           {goodWrangler, `[]`, "not an object"},
	} {
		wantProblem(t, name, wranglerProblems([]byte(strings.Replace(goodWrangler, c.from, c.to, 1))), c.want)
	}
}

func titledBlock(title string) string {
	return "```mermaid\nflowchart LR\n    accTitle: " + title + "\n    accDescr: d\n    A --> B\n```\n\n"
}

// A block inserted before a drawn one moves every later slot onto another
// diagram under its unchanged number; the pinned title catches it.
func TestSlotTitleProblems(t *testing.T) {
	page := []byte("<!-- diagram: README.md 1 --><!-- /diagram --><!-- diagram: README.md 2 --><!-- /diagram -->")
	readme := []byte(titledBlock("First") + titledBlock("Second"))
	wantNone(t, "slots on their diagrams", slotTitleProblems(page, readme, []string{"First", "Second"}))
	inserted := []byte(titledBlock("First") + titledBlock("New") + titledBlock("Second"))
	wantProblem(t, "a block inserted", slotTitleProblems(page, inserted, []string{"First", "Second"}), `slot 2 draws "New", want "Second"`)
	wantProblem(t, "a slot missing", slotTitleProblems(page, readme, []string{"First", "Second", "Third"}), "2 slots, want 3")
	wantProblem(t, "a block the README lacks", slotTitleProblems(page, []byte(titledBlock("First")), []string{"First", "Second"}), "slot 2 asks for block 2")
}
