# ADR-0010: The digest domain tag is fixed and carries no product name

Status: accepted
Date: 2026-09-09

Amends [ADR-0005](0005-canonical-action-digest.md) and
[ADR-0006](0006-product-name-switch.md). Amended by
[ADR-0011](0011-contract-corrections-before-publication.md): the field set
changed once under the same tag, because no digest under the earlier set was
published.

## Context

ADR-0005 defined the action digest as `sha256` over a domain-separation prefix
followed by the canonical JSON of a fixed field set, and made that prefix
contain the product slug. ADR-0006 accepted the consequence: a rename changes
every digest, so it invalidates every stored approval and every shared fixture.

That was affordable while no digest existed. This session computes the first
golden fixtures, which are the contract the Python and TypeScript implementations
will be checked against, so the prefix stops being a detail and becomes the
thing every language has to agree on.

There is also a failure the brand mechanism cannot see. `scripts/check-brand.sh`
finds the product name as a literal. A golden fixture holds a hash, and a hash
holds no literal, so a rename would silently invalidate every golden while the
brand gate stayed green. That is a check reporting a pass over something it
cannot examine.

## Decision

The domain tag is a fixed byte string chosen once, and no part of it is derived
from anything a rename would rewrite:

- action digest: `agent-action-digest/v1\n`
- approval binding: `agent-approval-binding/v1\n`

The approval binding had no tag at all before this record. It is the value an
approval is actually stored and compared against, so it needs one at least as
much as the action digest does.

The tag ends with a newline. RFC 8785 escapes every control character, so a raw
newline cannot occur inside the canonical body, which makes the boundary between
tag and body unambiguous. The separator is a newline for that reason and not for
readability.

The `v1` in the tag separates versions of the canonical form itself: a v2 form
that happened to produce identical bytes would still produce a different digest.

## Security / compatibility impact

A rename no longer invalidates a digest, an approval or a fixture. The
`Slug` constant in `internal/brand` keeps every other job it had.

Domain separation still buys what it is for. Two different purposes hashing with
the same primitive get different tags, so a digest computed for one purpose can
never be presented as a digest for another; and the version in the tag means a
later canonical form cannot collide with this one.

What is given up is that the tag no longer says which product produced a
digest. A fixture whose bytes are inspected in isolation is no longer
self-describing, which is why `testdata/digest/README.md` states the tag
explicitly rather than leaving a reader to infer it.

## Alternatives considered

- Keeping the slug in the tag and deciding the product name before the digest
  lands. It couples an unrelated decision to a technical one, and it leaves the
  brand gate unable to see a stale digest either way.
- An opaque random token as the tag. It is unforgeable by construction, and it
  is unreadable in forensic output. The precedents for fixing a tag per protocol
  version rather than per product, such as RFC 9380 domain separation tags, TLS
  1.3 label prefixes and SP 800-185 customization strings, all use readable
  strings.
- Deriving the tag from the proto package name. That name contains the product
  name too, so it is the same coupling by another route.

## Consequences

The tag is now a constant that must never change. Changing it is not a rename,
it is a new version of the canonical form, and it invalidates every stored
approval, so it needs its own record and a new package version.

Three statements that said the digest follows the brand are no longer true and
are corrected in the same change: `internal/brand/brand.go`, ADR-0005 and
ADR-0006.

## Validation

Runnable today: `go test ./internal/canon/` computes the digest over the golden
fixtures in `testdata/digest/`, which pin the tag by construction, and
`internal/brand/brand_test.go` no longer has a digest claim to pin.

`scripts/rename-product.sh` does not touch the tag. Proving that means running a
rename against a scratch copy and confirming the goldens still pass, which is a
step a maintainer takes rather than a gate that runs on every change.
