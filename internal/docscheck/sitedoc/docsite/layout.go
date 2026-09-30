package docsite

import (
	stdhtml "html"
	"slices"
	"strings"

	"github.com/guardana/control/internal/brand"

	"github.com/guardana/control/internal/docscheck/frontmatter"
	"github.com/guardana/control/internal/docscheck/indexdoc"
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
// documentation's navigation with the current page marked, the page's source
// and the footer.
func layout(p *page, nav []navSection, body []byte) []byte {
	var b strings.Builder
	title := p.title + " · " + brand.Name
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
<header class="top"><div class="wrap bar">
  ` + mark + `
  <nav aria-label="Primary">
    <a href="/docs/">Docs</a>
    <a href="/docs/status">Status</a>
    <a href="/docs/roadmap">Roadmap</a>
    <a class="gh" href="` + repository + `">GitHub</a>
  </nav>
</div></header>
<div class="wrap shell">
<main id="main" class="prose">
`)
	b.Write(body)
	b.WriteString(`<p class="src">Rendered from <a href="` + sourceURL(p.src, false) + `">` + esc(p.src) +
		`</a> on <code>main</code>. It describes the tree as it is now; a release's notes say what a tag holds.</p>
</main>
<nav class="side" aria-label="Documentation">
`)
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
	b.WriteString(`</nav>
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
