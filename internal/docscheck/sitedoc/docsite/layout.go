package docsite

import (
	stdhtml "html"
	"slices"
	"strings"

	"github.com/guardana/control/internal/brand"

	"github.com/guardana/control/internal/docscheck/frontmatter"
	"github.com/guardana/control/internal/docscheck/indexdoc"
	"github.com/guardana/control/internal/docscheck/sitedoc"
)

// The two sections a page type does not name: the records, listed on the
// index rather than one by one, and the project pages the roadmap and the
// changelog join.
var (
	sectionRecords = "Decision records"
	sectionProject = indexdoc.Section("project")
)

// mark is the landing page's logotype, so both kinds of page wear one header:
// the product name with its last word set bold.
var mark = func() string {
	first, last, _ := strings.Cut(brand.Name, " ")
	return `<a class="mark" href="/"><svg viewBox="0 0 24 24" aria-hidden="true" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="M12 2.8 4.5 5.6v6.1c0 4.6 3.2 8.2 7.5 9.5 4.3-1.3 7.5-4.9 7.5-9.5V5.6z"/><path d="M10.2 8.4v7.8M10.2 12.3h5.3"/></svg><span>` +
		esc(first) + ` <b>` + esc(last) + `</b></span></a>`
}()

// repository is the project's home on its host.
var repository = "https://" + brand.ModulePath

// githubIcon is the host's mark, drawn inline because the site loads no image
// from another host.
const githubIcon = `<svg viewBox="0 0 16 16" aria-hidden="true" fill="currentColor"><path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0 0 16 8c0-4.42-3.58-8-8-8z"/></svg>`

// header is the site's one header, the landing page's as well: the mark, the
// released version's pill and the primary navigation. The landing page holds
// the same bytes with the pill inside its release slot.
func header(pill string) string {
	return `<header class="top"><div class="wrap bar">
  ` + mark + `
  ` + pill + `
  <nav aria-label="Primary">
    <a href="/docs/">Docs</a>
    <a href="/docs/status">Status</a>
    <a class="hide-s" href="/docs/roadmap">Roadmap</a>
    <a class="gh" href="` + repository + `" aria-label="GitHub">` + githubIcon + `<span class="hide-s">GitHub</span></a>
  </nav>
</div></header>
`
}

type navEntry struct{ label, url string }

type navSection struct {
	heading string
	entries []navEntry
}

// buildNav groups the pages the way docs/index.md groups them, in the order of
// the page types, with the records as one entry pointing at their list.
func buildNav(pages []*page) []navSection {
	var nav []navSection
	add := func(heading string, e navEntry) {
		for i := range nav {
			if nav[i].heading == heading {
				nav[i].entries = append(nav[i].entries, e)
				return
			}
		}
		nav = append(nav, navSection{heading: heading, entries: []navEntry{e}})
	}
	for _, typ := range frontmatter.Types {
		heading := indexdoc.Section(typ)
		for _, p := range pages {
			if p.section == heading {
				add(heading, navEntry{p.title, p.url})
			}
		}
	}
	add(sectionProject, navEntry{sectionRecords, "/docs/#decision-records"})
	return nav
}

// layout wraps a rendered body in the site's chrome: the header, the
// documentation's navigation with the current page marked, once as the menu a
// narrow screen shows above the content and once as the sidebar, the page's
// source with the released version, and the footer.
func layout(p *page, nav []navSection, released string, body []byte) []byte {
	var b strings.Builder
	title := p.title + " · " + brand.Name
	sections := sectionList(p, nav)
	b.WriteString(`<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" lang="en" xml:lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>` + esc(title) + `</title>
<meta name="description" content="` + esc(p.summary) + `"/>
<link rel="canonical" href="` + Origin + p.url + `"/>
<meta name="theme-color" content="#0B8F80"/>
<link rel="icon" href="/favicon.svg" type="image/svg+xml"/>
<link rel="stylesheet" href="/assets/brand/v1/tokens.css"/>
<link rel="stylesheet" href="/assets/control/control.css"/>
<link rel="stylesheet" href="/assets/control/docs.css"/>
</head>
<body class="doc">
<a class="skip" href="#main">Skip to content</a>
` + header(sitedoc.Pill(released)) + `<div class="wrap shell">
<details class="mnav">
<summary>Menu · <b>` + esc(p.title) + `</b></summary>
<nav class="inner" aria-label="Documentation">
` + sections + `</nav>
</details>
<main id="main" class="prose">
`)
	b.Write(body)
	b.WriteString(`<p class="src">Rendered from <a href="` + sourceURL(p.src, false) + `">` + esc(p.src) +
		`</a> on <code>main</code>. It describes the tree as it is now; a release's notes say what a tag holds. ` +
		`The latest release is v` + esc(released) + `, and <a href="` + esc(sitedoc.TagDocs(released)) + `">its documentation</a> is in its tag.</p>
</main>
<nav class="side" aria-label="Documentation">
` + sections + `</nav>
</div>
<footer class="foot"><div class="wrap row">
  <p>` + esc(brand.Name) + ` · Apache-2.0</p>
  <ul>
    <li><a href="` + repository + `">GitHub</a></li>
    <li><a href="` + repository + `/releases">Releases</a></li>
    <li><a href="` + repository + `/blob/main/SECURITY.md">Security</a></li>
    <li><a href="` + repository + `/blob/main/LICENSE">Licence Apache-2.0</a></li>
    <li><a href="` + repository + `/blob/main/TRADEMARKS.md">Trademarks</a></li>
  </ul>
</div></footer>
</body>
</html>
`)
	return []byte(b.String())
}

// sectionList is the documentation's sections as lists of links, the
// current page marked.
func sectionList(p *page, nav []navSection) string {
	var b strings.Builder
	current := func(url string) string {
		if url == p.url {
			return ` aria-current="page"`
		}
		return ""
	}
	b.WriteString(`<ul class="home"><li><a href="/docs/"` + current("/docs/") + `>Overview</a></li></ul>
`)
	for _, s := range nav {
		b.WriteString("<h2>" + esc(s.heading) + "</h2><ul>\n")
		for _, e := range s.entries {
			b.WriteString(`<li><a href="` + e.url + `"` + current(e.url) + `>` + esc(e.label) + "</a></li>\n")
		}
		b.WriteString("</ul>\n")
	}
	return b.String()
}

// sitemap lists the landing page and every rendered page, in path order.
func sitemap(pages []*page) []byte {
	urls := []string{Origin + "/"}
	for _, p := range pages {
		urls = append(urls, Origin+p.url)
	}
	slices.Sort(urls)
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
`)
	for _, u := range urls {
		b.WriteString("  <url><loc>" + esc(u) + "</loc></url>\n")
	}
	b.WriteString("</urlset>\n")
	return []byte(b.String())
}

func esc(s string) string { return stdhtml.EscapeString(s) }
