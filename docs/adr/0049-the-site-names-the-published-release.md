# ADR-0049: The site names the published release

Status: proposed
Date: 2026-10-09

Amends [ADR-0030](0030-a-static-website-drawn-from-the-repository.md) on where
the header's version comes from, and
[ADR-0031](0031-the-documentation-is-served-on-the-website.md) on what a
page's foot says about the release.

## Context

The site's header named the version of the newest dated section of
`CHANGELOG.md`. `RELEASING.md` dates that section in a commit on `main` before
the tag is pushed, and Cloudflare deploys `main` on every push. So the site
named a release before it existed, and kept naming it if the release workflow
then failed and published nothing. The pages describe `main` too, so a reader
who took the header's version at its word could look for a command that the
downloadable release does not have.

## Decision

- `docs/docs.json` holds `released`: the version the site names as published.
  A maintainer sets it in a commit after the release is published, never in
  the commit that dates its section. The reader of `docs/docs.json` requires
  it and refuses a value that is not a version as a `CHANGELOG.md` heading
  writes it, such as `1.2.3` or `1.2.3-alpha`.
- The header's pill, on the landing page and on every documentation page,
  shows `v<released>` and links that version's release page on GitHub. The
  render fails when `released` does not head exactly one section of
  `CHANGELOG.md` read as `## [<version>] - <YYYY-MM-DD>`. A newer dated section
  above it, the release being prepared, does not change what the site names.
- Each documentation page's foot still says it is rendered from `main` and
  links its source, and adds one sentence naming the release and linking the
  `docs/` directory at its tag. It links the directory, not the page, because
  a page added after the tag has no copy there.

## Security / compatibility impact

No product code changes. A release whose publication fails changes nothing on
the site, and preparing the next version's section does not announce it. The
cost is one more commit per release: until a maintainer makes it, the site
names the previous release, which is out of date and still true.

## Alternatives considered

- **The newest dated section, as before.** It announces a release before its
  tag exists and after a failed publication.
- **Reading git tags in the generator.** The output would depend on which tags
  the clone has, and the gate runs on an export of the tree that holds none.
- **Deploying the site only from tags.** A fix to a page would wait for the
  next release, and Cloudflare's setup would change.
- **Documentation per tag rendered on the site.** Deferred, as ADR-0031
  deferred it; the foot links the tag's tree on GitHub instead.

## Consequences

`RELEASING.md` gains a step after publication: set `released` and run
`make docs-gen`. A `released` that names a version with no dated section, or
one that heads two sections, fails `make docs-gen` and the gate. Forgetting
the step leaves the site on the older release, which no check catches.

## Validation

Tests in `internal/docscheck/` run in `make test` and `make docs-check`:

- `docsconfig` refuses a missing, empty, null or malformed `released`;
- `sitedoc.Published` refuses a version with no section, an undated or
  malformed section, a section given twice and a malformed version, and
  accepts one with a newer dated section above it;
- `docsite.Build`, from a changelog whose newest dated section is newer than
  `released`, writes on every page one pill and one foot naming `released`,
  and nothing naming the newer section;
- over the committed site, every page's pill and every documentation page's
  foot name the `released` of `docs/docs.json` and link its tag, and that
  version heads a dated section of `CHANGELOG.md`.
