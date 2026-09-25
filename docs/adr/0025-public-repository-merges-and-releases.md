# ADR-0025: The public repository: who pushes, who merges, who releases

Status: accepted
Date: 2026-09-25

Amends [ADR-0008](0008-branching-and-release-policy.md): the repository admin
pushes to `main` directly, only the maintain role merges, and a release is an
immutable tag that CI builds, signs and attests. Amends
[ADR-0006](0006-product-name-switch.md): the first release fixes the name.

## Context

ADR-0008 set the rules before the repository existed. Every change would reach
`main` through a pull request with one approving review, code-owner review on
the authorization paths and the required checks, and releases would be built
and signed by CI. The repository is now public at `github.com/guardana/control`
and has one maintainer. GitHub does not let an author approve their own pull
request, so for a sole maintainer that rule adds a bypass click to every change
and no review.
The project still needs to say who may merge other people's work and who may
publish a version, and GitHub has to enforce it.

## Decision

- The public history starts at one commit: the tree as it stood when it was
  published.
- `main` takes changes by squash-merged pull request. A pull request needs an
  approving review from someone other than its last pusher, the code owners'
  review where `.github/CODEOWNERS` names them, two approvals from the security
  maintainers on the paths that carry authorization meaning, resolved
  conversations, the required status checks on an up-to-date branch, and no
  CodeQL alert of high severity or above. The two-approval paths are listed in
  `scripts/github-bootstrap.sh`; beside ADR-0008's list they hold the
  enforcement pipeline, the approvals store, the policy keys, the release
  configuration and the tool digests the release job trusts.
- The repository admin, today Konrad Karauda, may push to `main` directly and
  bypasses the pull-request rules. No role bypasses the history rules: nobody
  force-pushes or deletes `main`, and its history stays linear.
- Only the maintain role and the admin can merge into `main`. A collaborator
  with write access opens pull requests like everyone else.
- A release is a tag `vX.Y.Z`, with an optional pre-release suffix, on a commit
  of `main`. Only the maintain role and the admin can create one, and no role
  can move or delete one. Immutable releases keep a published release's tag and
  assets as they were published.
- The release workflow builds every asset from the tag, in an environment that
  only `v*` tags can deploy to. It signs the checksums, attests every archive,
  verifies both, and only then publishes. A broken release is followed by the
  next version and never replaced.
- Pull request titles and commit subjects follow Conventional Commits:
  `type(scope): summary`, imperative, at most 72 characters on `main`. Every
  commit carries a DCO sign-off. A workflow checks every pull request's title
  and the sign-off of each of its commits; the admin's direct pushes are held
  to the same rules by the admin alone.
- The name is fixed from the first release. Renaming after it breaks the module
  path and the proto package, and needs a record of its own.
- `scripts/github-bootstrap.sh` holds the repository's settings, teams and
  rulesets. A change goes into the script first, then GitHub.

## Security / compatibility impact

The admin's direct pushes skip review by design. Each of them runs the full
gate in CI, as every push to `main` does, and a red run is fixed before
anything else lands. A stolen admin session could push to `main`, but it could
not rewrite history or move a release tag without first changing a ruleset, and
GitHub's audit log records that change.

A release tag is the root of every signature and attestation that follows from
it. Keeping its creation to the maintain role, and forbidding any change to a
tag once created, is what makes a verified download mean something. The
workflow that runs is the one in the tagged commit, so whoever creates the tag
vouches for that commit: the workflow refuses a commit that is not on `main`,
and the provenance of every asset names the commit it was built from.

A pull request's checks run from its own tree, so a change to a workflow is
held by the review of `.github/workflows/`, which needs two security
maintainers, and not by the checks it changes. An approval never counts from a pull request's own
author, so until the security maintainers are three, a maintainer's own change
to a two-approval path, and until they are two, any change there, reaches
`main` only by the admin's bypass. Dependency updates that touch the workflows
are among them.

## Alternatives considered

- Pull requests for everyone, the admin included, as ADR-0008 wrote it. With
  one maintainer that means a bypass on every change, which is the rule below
  with extra clicks.
- Direct pushes for the maintain role as well. Maintainers would then land
  unreviewed work, where the point of the role is to merge reviewed work.
- Required signed commits. The maintainer's machines do not sign commits today,
  and the DCO sign-off plus the release signature carry the provenance that
  matters. Revisit it when a second maintainer joins.
- The SLSA generator workflow instead of GitHub's artifact attestations. Both
  give build provenance at SLSA Build L2 here; the attestations need fewer
  moving parts and verify with `gh attestation verify`.

## Consequences

Contributors fork and open pull requests. Maintainers merge them and cut
releases with a tag. Settings live in a reviewed script rather than in the web
interface. A mistake in a release costs a version number, never a replaced
download.

## Validation

`scripts/github-bootstrap.sh` applies the rulesets, and
`gh api repos/guardana/control/rulesets` shows what is live. The pull request
workflow refuses a non-conforming title or a commit without a sign-off. The
release workflow refuses to publish an asset whose signature or attestation
does not verify. [RELEASING.md](../../RELEASING.md) shows how anyone checks a
download.
