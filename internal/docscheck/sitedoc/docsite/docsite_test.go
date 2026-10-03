package docsite

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

const pageHead = "---\ntitle: A page\nsummary: What the page is for.\ntype: explanation\ncovers: [x/**]\n---\n\n"

// fixture is a repository with the pages Build requires, a code file, a
// directory and one page whose body the case supplies.
func fixture(body string) fstest.MapFS {
	return fstest.MapFS{
		"docs/index.md":          {Data: []byte("---\ntitle: Documentation\nsummary: Every page.\ntype: project\ncovers: [docs/**]\n---\n\n# Documentation\n\n## Decision records\n")},
		"docs/concepts/a.md":     {Data: []byte(pageHead + body)},
		"docs/concepts/b.md":     {Data: []byte(pageHead + "# B\n\n## Part\n\n## Part\n")},
		"docs/adr/0001-first.md": {Data: []byte("# ADR-0001: First\n\nStatus: accepted\n")},
		"ROADMAP.md":             {Data: []byte("# Roadmap\n\nPlanned.\n")},
		"CHANGELOG.md":           {Data: []byte("# Changelog\n\n## [Unreleased]\n\n## [0.2.0] - 2026-01-02\n\n## [0.1.0] - 2026-01-01\n")},
		"cmd/tool/main.go":       {Data: []byte("package main\n")},
		"examples/demo/x.txt":    {Data: []byte("x\n")},
	}
}

func build(t *testing.T, body string) (map[string][]byte, error) {
	t.Helper()
	return Build(fixture(body), func(string) bool { return false })
}

func renderA(t *testing.T, body string) string {
	t.Helper()
	files, err := build(t, body)
	if err != nil {
		t.Fatal(err)
	}
	out, ok := files[Dir+"/concepts/a.html"]
	if !ok {
		t.Fatalf("no %s/concepts/a.html among %d files", Dir, len(files))
	}
	return string(out)
}

func TestLinksPointAtThePageOrItsSource(t *testing.T) {
	out := renderA(t, "# A\n\n[b](b.md), [part](b.md#part-1), [here](#a), [code](../../cmd/tool/main.go), "+
		"[dir](../../examples/demo/), [roadmap](../../ROADMAP.md), [record](../adr/0001-first.md), [web](https://example.invalid/x)\n")
	for _, want := range []string{
		`href="/docs/concepts/b"`,
		`href="/docs/concepts/b#part-1"`,
		`href="#a"`,
		`href="https://github.com/guardana/control/blob/main/cmd/tool/main.go"`,
		`href="https://github.com/guardana/control/tree/main/examples/demo"`,
		`href="/docs/roadmap"`,
		`href="/docs/adr/0001-first"`,
		`href="https://example.invalid/x"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
}

func TestHeadingsTakeGitHubsAnchors(t *testing.T) {
	files, err := build(t, "# A\n")
	if err != nil {
		t.Fatal(err)
	}
	out := string(files[Dir+"/concepts/b.html"])
	for _, want := range []string{`<h1 id="b">B</h1>`, `<h2 id="part">Part</h2>`, `<h2 id="part-1">Part</h2>`} {
		if !strings.Contains(out, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
}

func TestRefusalsNameTheFileLine(t *testing.T) {
	// The body starts on line 8 of the file, after the frontmatter and a blank line.
	cases := map[string]struct{ body, want string }{
		"a link to a path the repository lacks": {"# A\n\n[gone](../../nowhere.md)\n", "line 10: link \"../../nowhere.md\" names nowhere.md, which the repository does not hold"},
		"a link out of the repository":          {"# A\n\n[out](../../../x.md)\n", "line 10: link \"../../../x.md\" escapes the repository"},
		"an empty link":                         {"# A\n\n[empty]()\n", "line 10: an empty link"},
		"raw HTML as a block":                   {"# A\n\n<div>x</div>\n", "line 10: raw HTML"},
		"raw HTML inline":                       {"# A\n\ntext <b>bold</b> text\n", "line 10: raw HTML"},
		"an image":                              {"# A\n\n![alt](b.md)\n", "line 10: an image"},
		"a diagram with no text alternative":    {"# A\n\n```mermaid\nflowchart LR\n    A --> B\n```\n", "line 10: diagram: a Mermaid block needs"},
		"a javascript: link":                    {"# A\n\n[x](javascript:alert(1))\n", "line 10: link \"javascript:alert(1)\": only http, https and mailto"},
		"a data: link by reference":             {"# A\n\n[x][r]\n\n[r]: data:text/html,x\n", "line 10: link \"data:text/html,x\": only http, https and mailto"},
		"a javascript: autolink":                {"# A\n\n<javascript:alert(1)>\n", "line 10: link \"javascript:alert(1)\": only http, https and mailto"},
		"text after a comment block":            {"# A\n\n<!-- c --><b>x</b>\n", "line 10: raw HTML"},
		"a heading that takes the layout's id":  {"# A\n\n## Main\n", `the id "main" is used twice`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := build(t, c.body)
			if err == nil {
				t.Fatal("rendered; want a refusal")
			}
			if !errors.Is(err, ErrSite) || !strings.Contains(err.Error(), "docs/concepts/a.md: "+c.want) {
				t.Errorf("err = %v, want it to name docs/concepts/a.md and %q", err, c.want)
			}
		})
	}
}

func TestAnHTMLCommentRendersAsNothing(t *testing.T) {
	out := renderA(t, "# A\n\n<!-- generated: x -->\n\ntext <!-- inline --> text\n")
	if strings.Contains(out, "generated: x") || strings.Contains(out, "inline") || strings.Contains(out, "omitted") {
		t.Errorf("a comment reached the page:\n%s", out)
	}
}

func TestADiagramIsDrawnOrDescribedNeverDropped(t *testing.T) {
	drawn := renderA(t, "# A\n\n```mermaid\nflowchart LR\n    accTitle: Two steps\n    accDescr: A leads to B.\n    A[One] --> B[Two]\n```\n")
	if !strings.Contains(drawn, "<svg") || strings.Contains(drawn, `class="dg-text"`) {
		t.Errorf("a flowchart in the subset was not drawn:\n%s", drawn)
	}
	described := renderA(t, "# A\n\n```mermaid\nsequenceDiagram\n    accTitle: One message\n    accDescr: A asks B.\n    A->>B: one & two\n```\n")
	for _, want := range []string{
		`class="dg-text"`, "<figcaption>One message</figcaption>",
		`<p>A asks B. <a href="https://github.com/guardana/control/blob/main/docs/concepts/a.md">Drawn on GitHub</a>.</p>`,
		"A-&gt;&gt;B: one &amp; two",
	} {
		if !strings.Contains(described, want) {
			t.Errorf("the described diagram lacks %s:\n%s", want, described)
		}
	}
}

func TestCodeIsEscaped(t *testing.T) {
	out := renderA(t, "# A\n\n```sh\necho '<script>' && x\n```\n")
	if !strings.Contains(out, `<pre><code class="language-sh">echo &#39;&lt;script&gt;&#39; &amp;&amp; x`) {
		t.Errorf("the code block is not escaped:\n%s", out)
	}
}

func TestEveryPageIsWrittenAndListed(t *testing.T) {
	files, err := build(t, "# A\n")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"index.html", "concepts/a.html", "concepts/b.html", "adr/0001-first.html", "roadmap.html", "changelog.html"}
	if len(files) != len(want)+1 {
		t.Errorf("Build wrote %d files, want %d pages and the sitemap", len(files), len(want))
	}
	sitemap := string(files[Sitemap])
	for _, name := range want {
		if _, ok := files[Dir+"/"+name]; !ok {
			t.Errorf("no %s/%s", Dir, name)
		}
	}
	for _, loc := range []string{Origin + "/", Origin + "/docs/", Origin + "/docs/concepts/a", Origin + "/docs/roadmap"} {
		if !strings.Contains(sitemap, "<loc>"+loc+"</loc>") {
			t.Errorf("the sitemap lacks %s", loc)
		}
	}
	a := string(files[Dir+"/concepts/a.html"])
	for _, want := range []string{
		`<link rel="canonical" href="` + Origin + `/docs/concepts/a"/>`,
		`<a href="/docs/concepts/a" aria-current="page">A page</a>`,
		`<h2>Concepts</h2>`,
		`<a href="/docs/#decision-records">Decision records</a>`,
	} {
		if !strings.Contains(a, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
}

// Every page wears the one header, with the newest release's pill, and
// carries the section list twice: the sidebar and the menu a narrow screen
// shows above the content, both marking the current page.
func TestThePageCarriesTheHeaderAndTheMenu(t *testing.T) {
	a := renderA(t, "# A\n")
	for _, want := range []string{
		`<a class="vlink" href="https://github.com/guardana/control/releases"><span class="ver">v0.2.0</span></a>`,
		`<a href="/docs/">Docs</a>`,
		`<a href="/docs/status">Status</a>`,
		`<a class="hide-s" href="/docs/roadmap">Roadmap</a>`,
		`<a class="gh" href="https://github.com/guardana/control" aria-label="GitHub"><svg viewBox="0 0 16 16"`,
		`<span class="hide-s">GitHub</span></a>`,
		"<details class=\"mnav\">\n<summary>Menu · <b>A page</b></summary>\n<nav class=\"inner\" aria-label=\"Documentation\">",
	} {
		if !strings.Contains(a, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	if n := strings.Count(a, `<a href="/docs/concepts/a" aria-current="page">A page</a>`); n != 2 {
		t.Errorf("the current page is marked %d times, want twice: in the sidebar and in the menu", n)
	}
	if menu, main := strings.Index(a, `<details class="mnav">`), strings.Index(a, `<main id="main"`); menu < 0 || main < 0 || menu > main {
		t.Errorf("the menu opens at byte %d and the content at %d; the menu must come first", menu, main)
	}
}

// A changelog whose newest release cannot be read stops the render: a
// header showing no version, or an older one, is never written.
func TestAChangelogWithNoReleaseIsRefused(t *testing.T) {
	fsys := fixture("# A\n")
	fsys["CHANGELOG.md"] = &fstest.MapFile{Data: []byte("# Changelog\n\n## [Unreleased]\n")}
	if _, err := Build(fsys, func(string) bool { return false }); !errors.Is(err, ErrSite) || !strings.Contains(err.Error(), "CHANGELOG.md holds no dated release section") {
		t.Errorf("err = %v, want the missing release named", err)
	}
}

func TestARootPageNeedsItsTitle(t *testing.T) {
	fsys := fixture("# A\n")
	fsys["ROADMAP.md"] = &fstest.MapFile{Data: []byte("Planned.\n")}
	if _, err := Build(fsys, func(string) bool { return false }); err == nil || !strings.Contains(err.Error(), "ROADMAP.md: the first line is not the page's title") {
		t.Errorf("err = %v, want the roadmap's missing title named", err)
	}
}

func TestTextXHTMLCannotCarryIsRefused(t *testing.T) {
	for body, want := range map[string]string{
		"# A\n\n\xa3\n":      "the page is not valid UTF-8",
		"# A\n\nbell \x07\n": "line 10: the character U+0007",
		"# A\n\n￿ here\n":    "line 10: the character U+FFFF",
	} {
		if _, err := build(t, body); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("body %q: err = %v, want %q", body, err, want)
		}
	}
}
