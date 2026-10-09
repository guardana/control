# ADR-0031: The documentation is served on the website

Status: accepted
Date: 2026-09-30

Builds on [ADR-0015](0015-documentation-structure-and-checks.md) and
[ADR-0030](0030-a-static-website-drawn-from-the-repository.md). Ends ADR-0015's
deferral of a documentation site, and amends ADR-0030 on where the landing
page's links lead.

Amended by [ADR-0049](0049-the-site-names-the-published-release.md): each
page's foot also names the published release and links the documentation at
its tag.

## Context

The landing page sends a reader to GitHub's file view for every page: the
documentation, the status, the roadmap, the backlog. ADR-0015 deferred a site
until the first public release, and two releases have shipped. Guardana
renders its own pages into its site from its repository, with a check that
fails when the two drift. A second copy of the text kept by hand would drift
the way ADR-0030's context describes for the landing page.

## Decision

- `make docs-gen` renders into `site/docs/`, one XHTML file per page, every
  page under `docs/` that `docs/docs.json` does not exclude, every record,
  `ROADMAP.md` and `CHANGELOG.md`, and writes `site/sitemap.xml`, through
  `internal/docscheck/sitedoc/docsite` and `scripts/gen-site.go`. The Markdown
  stays the one source. The rendered files are committed, so the deploy keeps
  no build step and what Cloudflare serves is what a reviewer read in a diff.
- A test renders the pages again and compares them with the committed ones
  byte for byte, and `site/docs/` may hold nothing the renderer does not
  write. A page changed without `make docs-gen` fails `make docs-check`, as
  the index and the landing page's diagrams already do.
- A page's address follows its path: `docs/concepts/architecture.md` is
  `/docs/concepts/architecture`, the index `/docs/`, the roadmap and the
  changelog `/docs/roadmap` and `/docs/changelog`. A link to another rendered
  page points at its address; a link to anything else the repository holds
  points at it on GitHub at `main`; a link to a path the repository does not
  hold, or out of it, fails the render, and so does a link whose scheme is
  not `http`, `https` or `mailto`. A heading's anchor is the one GitHub
  gives it, by the rule the Markdown link check uses
  (`internal/docscheck/anchor`), so a fragment that resolves on GitHub
  resolves on the site.
- The navigation groups the pages as the index does, by page type; the records
  are one entry pointing at their list on the index.
- The Markdown is read by goldmark with tables, strikethrough and bare links.
  Raw HTML is refused, since the site would serve it unchecked, and an HTML
  comment renders as nothing when nothing else shares its lines. An image is
  refused; no page has one. A page that is not UTF-8, holds a character XML
  cannot carry, or would hold one id twice, a heading's anchor and the layout's
  or a figure's, is refused.
- Every Mermaid block carries a one-line `accTitle:` and `accDescr:`. A
  flowchart inside the drawer's subset is drawn as on the landing page. Any
  other block is a figure with its title, its description, a link to its page
  on GitHub, which draws it, and its source; never dropped.
- The pages describe `main`, not the last tag, and each says so at its foot
  with a link to its source. The landing page's links to the documentation,
  the status, the roadmap, the backlog and the demo point at the site; the
  security policy, the licence and the trademarks stay on GitHub, which shows
  them as the repository's own.
- The pages add one stylesheet, `site/assets/control/docs.css`. They run no
  script and load nothing from another host, under the landing page's
  `_headers`.

## Security / compatibility impact

The product does not change: no binary or image links the renderer, which
`docs/dependencies.md` records with the `go list -deps` that shows it. The
site's checks now read every page: no script, no event handler, no load from
another host, every local link and fragment resolved, every link into the
repository naming a path it holds, and each page's canonical address, and no
other, in the sitemap. A fuzz target holds the renderer to well-formed XHTML
with no script for any Markdown it accepts.

Nothing unpublished is rendered: the renderer reads what `docs/docs.json`
does not exclude, and the gate runs on an export that holds tracked files
only. The pages were already public on GitHub.

## Alternatives considered

- **Hugo with a theme**, ADR-0015's old default. A second toolchain in the
  gate, and a theme's markup the site's checks would have to learn.
- **Rendering in Cloudflare's build.** The served page would be one no reviewer
  read, and the deploy would gain a build step and its supply chain.
- **A Markdown renderer of our own**, as the Mermaid drawer is. Hundreds of
  lines to keep in step with CommonMark for text the pages already write; the
  drawer exists because no library draws static SVG without a browser.
- **Links to `.html` files.** `auto-trailing-slash` would redirect every click;
  the address without the extension is the one served.
- **Documentation per tag.** Deferred; a release's notes and its tree say what
  it holds.

## Consequences

A change to a page changes its rendered file in the same commit, and the diff
shows both. `site/docs/` holds about 1.4 MB, one file per page. A link to a
file the tree holds but git ignores renders on a machine that has the file and
fails on the export the gate runs on. A Mermaid block with
no text alternative, raw HTML or a link to a path the repository lacks now
fails the gate where GitHub would have shown it. Every flowchart of the concept
pages falls outside the drawer's subset today, through a cycle, a decision
shape or a nested group, so the site describes them and GitHub draws them
until the drawer grows or the diagrams change.

## Validation

Tests in `internal/docscheck/` and `internal/docscheck/sitedoc/docsite/` run in
`make test` and `make docs-check`:

- the committed pages and sitemap equal a fresh render, and `site/docs/` holds
  nothing else;
- each refusal names its file and line: a link to a path the repository lacks,
  a link out of it, an empty link, a link or autolink with the `javascript` or
  `data` scheme, raw HTML in a block, inline or after a comment, an image, a
  diagram without its text alternative, bytes that are not UTF-8, a character
  XML forbids and an id used twice;
- links, anchors and diagrams render as above, and a mutant that leaves a link
  unrewritten or lets raw HTML through fails them;
- `FuzzRenderBody`, whose three minimized failures are kept as seeds;
- ADR-0030's site checks, over every page: no script or remote load, every
  link and fragment resolved as the host serves extensionless paths, every
  repository link and its heading present, and each id once.
