# ADR-0030: A static website drawn from the repository

Status: accepted
Date: 2026-09-28

Builds on [ADR-0015](0015-documentation-structure-and-checks.md) and
[ADR-0024](0024-control-and-guardana-are-independent.md).

## Context

The project had no page of its own outside GitHub. A landing page has to say
what the plane does today and what is only planned, and a page kept by hand
drifts from the tree it describes: a status label changes, a roadmap item
ships, the architecture diagram moves on in the README and not on the page.
The organization's other project serves its site as static files from its own
repository, and a reader moving between the two should see one visual system.

## Decision

- The site is `site/` in this repository, served at `control.guardana.dev` by
  a Cloudflare Worker with static assets and no script, described by
  `wrangler.json` at the root: its name, the assets directory, how an address
  maps to a file, its one address as a Custom Domain, for which Cloudflare
  creates the DNS record and the certificate at deploy, and neither a
  `workers.dev` address nor preview addresses.
  Cloudflare's Git integration deploys it on every push to `main`. The file is
  strict JSON rather than JSONC, so a comment cannot hide a key from the check
  that reads it.
- The page runs no script and loads nothing from another host. `site/_headers`
  sets a Content-Security-Policy with `default-src 'none'`, `script-src
  'none'`, fonts, images and styles from the site only, and inline styles
  allowed, which the drawn diagrams carry. The fonts are served from the site.
- The page's diagrams are the README's first three Mermaid blocks.
  `make docs-gen` draws each into a marked slot of `site/index.html` as static
  SVG, light and dark, with a top-to-bottom drawing for narrow screens, through
  `internal/docscheck/sitedoc`. The drawer reads a subset of Mermaid flowcharts:
  every block carries `accTitle:` and `accDescr:`, its text alternative; there
  is no cycle and no two-way edge; a group's members share one level. A block
  over 15 nodes is refused before it is drawn.
- Each item in the page's "today" list names its row in `docs/status.md` and
  shows that row's label; each planned item names its id in `docs/backlog.md`.
  The hero's tagline is README's.
- The visual system is a copy of Guardana's brand v1 in
  `site/assets/brand/v1/`: tokens, IBM Plex fonts with their licence, and
  marks. From the day it was taken the copy is Control's own; a change is a v2
  at a new path, never an edit in place. Control's accent, mark and icon live in
  `site/assets/control/`, and the accent overrides the brand's colour tokens
  without touching the copy.
- The social card `site/og.png` is rendered by hand in a browser from
  `scripts/og-card.html`, which uses the site's fonts and mark.

## Security / compatibility impact

The site holds no secret and runs no code in the browser. It can still claim
something the tree lacks, or load something from another host. The checks
below refuse a load from another host, and a status or roadmap item that no
longer matches its source; the page's other sentences are held to the tree by
review.

Every push to `main` deploys, whatever CI says of it, so a commit that breaks
the page is live until the next one. The site's checks run in the same gate as
the code, and a red run on `main` is fixed before anything else lands
([ADR-0025](0025-public-repository-merges-and-releases.md)). The deploy command
in Cloudflare names a pinned version of `wrangler`.

`scripts/rename-product.sh` rewrites the Worker's name with the rest of the
tree. The next Cloudflare build then fails on the name it no longer knows until
the Cloudflare project is renamed as well. A rename also changes the text of
the drawn diagrams without redrawing them, so the slot check fails until
`make docs-gen` runs.

Nothing in the site, its drawer or its checks reads, runs or names Guardana's
code; ADR-0024 is unchanged.

## Alternatives considered

- A separate repository for the site. The page would drift from the code it
  describes, and a check comparing the two would have to span two
  repositories.
- GitHub Pages. It reads no `_headers` file, so the page could not send its
  Content-Security-Policy, and a deploy job would hold a write token.
- Mermaid's own renderer, with the SVG committed. It needs Node and a browser
  in the gate, and its output is neither styled by the site nor reproducible.
- Guardana's drawer, run from its repository. That would make Control's gate
  depend on Guardana, which ADR-0024 refuses.
- The documentation site now. ADR-0015's deferral stands: the landing page
  links to the pages on GitHub.

## Consequences

A change to the architecture is one edit to a README block and one
`make docs-gen`; the page follows. A change to a status label or a finished
roadmap item fails the site check until the page follows as well. The drawer
is a few hundred lines of Go the project now maintains; a Mermaid feature
outside its subset cannot be used in a block the page draws.

The page has one address, `control.guardana.dev`; the Worker answers on no
other.

## Validation

Tests in `internal/docscheck/` run in `make test` and `make docs-check`, each
with fixtures that make it fail:

- the site holds exactly the files its manifest pins, and the brand copy
  matches its `SHA256SUMS`, whose own digest is pinned;
- the slots are README blocks 1, 2 and 3 in order, and redrawing them gives the
  committed page;
- no page, stylesheet or SVG loads from another host, runs a script or carries
  an event handler, and `_headers` holds exactly the pinned policy;
- every local link and fragment resolves, and every link into this repository
  on GitHub names a file or directory the tree has;
- each status item's label equals its row's, each backlog id exists, and the
  page's and the card's tagline equal README's;
- `wrangler.json` holds exactly the keys above, once each;
- `internal/docscheck/sitedoc/mermaid` draws recorded inputs byte for byte and
  refuses each construct outside the subset, naming the line.
