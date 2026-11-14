# ADR-0084: The site is redrawn: one system for the landing page and the docs, a light and a dark theme, llms.txt

Status: accepted
Date: 2026-11-13

Amends [ADR-0030](0030-a-static-website-drawn-from-the-repository.md) on the
fonts and their licence, the visual system's path, the landing page's
diagrams and what its checks hold;
[ADR-0031](0031-the-documentation-is-served-on-the-website.md) on the pages'
look and the files the renderer writes;
[ADR-0009](0009-open-core-boundary.md) on which third-party fonts the site
ships under the SIL Open Font License; and
[ADR-0024](0024-control-and-guardana-are-independent.md) on the visual
system, which is no longer a copy of Guardana's brand.

## Context

The landing page described the 1.0 goal before what a reader can do today,
listed status rows by their inventory names, and drew its figures as cards
that a phone stacked into a long column. The documentation pages had a
different look over the same tokens, with no dark theme a reader could choose.
The maintainer approved a new page: a headline about deciding what agents run
and checking what they did, a scene of one agent's three calls, the three
steps, six questions a reader asks, the chain of what is signed, a concept of
a run drawn as a graph, a quickstart, the limits, and the questions before
connecting it, in a light and a dark theme on a phone and a desktop. The
public line moves with it, so README, the page and the social card change
together.

Some of the site's checks described the old page: a list of five to eight
status items titled with their rows' names, a list of backlog items titled
with their tasks, and at least three figures drawn as cards.

## Decision

- **One visual system.** The tokens, the fonts and their licence are
  `site/assets/control/v2/`, pinned by the digest of its `SHA256SUMS` as
  brand v1 was: a change is a v3 at a new path. The fonts are Geist and Geist
  Mono, one variable WOFF2 file each from the fonts' own v1.9.2 release, under
  the SIL Open Font License 1.1, its text served beside them. Nothing loads
  brand v1 any more, so `site/assets/brand/v1/` and its IBM Plex files are
  removed rather than kept unread. `control.css` holds what every page wears:
  the base, the header, the buttons and the footer; `home.css` the landing
  page; `docs.css` the documentation pages, with the names the drawn
  diagrams read mapped onto the v2 tokens. Only `tokens.css` sets a token,
  and no stylesheet writes words through `content`.
- **A light and a dark theme, with no script.** The tokens follow
  `prefers-color-scheme`. A checkbox in the shared header selects the other
  theme through `html:has(#theme:checked)`; the choice lasts for the page, not
  across pages, because nothing stores it.
- **One header and one footer.** Both are written by the renderer and carried
  byte for byte by the landing page, the header with the release pill in its
  slot.
- **The landing page draws its own illustrations.** An animated scene of
  three calls in SVG with SMIL, a three-row list in its place below 1180 px,
  where the scene's labels would draw under 12 px, and the concept graph;
  each is an element with `role="img"` and a text alternative. Every
  animation starts near its end and every element rests in its final state,
  so a still frame shows the outcome. With reduced motion the CSS animations
  stop, the hero shows the list, and the graph shows its complete state. It
  draws no README block, and README holds no diagram the page needs.
- **Every label is tied to its source.** The words implemented, experimental
  and planned appear on the page only as a `st` element inside an element
  whose `data-status` names a component row and shows that row's label, or
  as planned inside an element whose `data-backlog` names a task and shows its
  id, linked to the backlog. Each such element carries the row's name or the
  task's Work cell as its `title`, and a reviewed list in the test pairs each
  element's heading with its row or task, so a card cannot claim a row it
  does not describe. A sentence that calls something planned, or promises
  later work, sits inside such an element; in README it names its task.
- **llms.txt.** The renderer writes `site/llms.txt`: the product's name,
  README's lede as the description, the status in one paragraph, then every
  page under its type's heading with its address and summary, and the records
  under "Optional". The sitemap keeps listing pages only. A concatenated
  `llms-full.txt` is not written: the pages and records together are over
  1 MB of Markdown.
- **The public line.** The headline is "Decide what your AI agents run. Check
  what they did.", the lede the sentence under it, word for word in README,
  on the page and in `llms.txt`; the category phrase, AI agent supervision
  framework, stays in the page's title, its description and README.

## Security / compatibility impact

No product code changes. The page still runs no script and loads nothing from
another host under the same `_headers`; the theme switch and the quickstart
tabs are checkboxes and radio buttons that CSS reads. The removed brand v1
files were reachable at their addresses; nothing of this project links them.

## Alternatives considered

- **Keeping brand v1 and adding Geist beside it.** Two type systems in one
  tree, and files nobody loads kept as if they were in use.
- **A script that remembers the theme.** It would break `script-src 'none'`.
- **The release pill in the hero as well as the header.** Two pills on one
  page to keep equal; the header carries it on every page already.
- **Keeping the status list titled with the rows' names.** The inventory's
  wording is precise and long; the page's headings ask a reader's questions,
  and the row stays named in `data-status` and `title`.
- **llms-full.txt for the pages alone.** About as large as the 1 MB the file
  is meant to stay under, and a partial copy would read as the whole.

## Consequences

A row whose label changes, a task that leaves the backlog, or a card whose
heading changes fails the site check until the page or the reviewed list
follows. A theme a reader picks is forgotten on the next page.

## Validation

Tests in `internal/docscheck/` and `internal/docscheck/sitedoc/docsite/`, each
with fixtures that make it fail:

- the v2 directory matches its `SHA256SUMS`, whose digest is pinned, and the
  site holds exactly the pinned manifest and what the renderer writes;
- every `data-status` names a row, carries its name as `title` and shows its
  label once; every `data-backlog` names a task, carries its Work cell as
  `title` and shows its id once; each pairs with its heading as the reviewed
  list says, and the page ties at least three distinct tasks;
- a label word in any text of the page outside those elements is refused, and
  so is a sentence that calls something planned or promises later work
  outside one, on the page and in README;
- no page holds a script element or an event handler, and no attribute holds
  an address of another host other than a link, read from the parsed page; no
  SMIL element animates a reference;
- every page holds one theme checkbox with its label and the tokens
  stylesheet, and the switched palettes equal the other scheme's; no other
  stylesheet sets a token or writes words through `content`;
- every page holds one footer, and the landing page's header and footer are
  the bytes of every documentation page's;
- the page's illustrations each carry a text alternative, at least three;
- the page's lede is README's, and `llms.txt` quotes it, says alpha and not a
  security boundary, and lists every page under its type with its address and
  summary; a README with no lede stops the render.
