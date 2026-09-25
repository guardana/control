# ADR-0006: The product name is a working name and can be switched

Status: accepted
Date: 2026-09-09

Amended by [ADR-0025](0025-public-repository-merges-and-releases.md): the
first release fixes the name; renaming after it needs a record of its own.

The name was decided on 2026-09-10: Guardana Control, `guardana/control`, and
the tree was renamed with the script below. The record stays because the
mechanisms do: a rename before the first tagged release is still cheap, and
after it is not.

## Context

The name was a working name when this record was written. A name in this project
is not only a title: it is the Go module path, the proto package,
the binary names, the environment prefix, the telemetry namespace and part of
the action digest. Spread by hand across the tree, that becomes unrenameable.

## Decision

Two mechanisms, because Go code and everything else need different handling:

- Go code reads the name, slug, module path, proto package, telemetry namespace
  and environment prefix from one file, `internal/brand/brand.go`.
- Outside Go code a brand literal is allowed only in the files on the
  allowlist. `scripts/check-brand.sh` fails the quality gate when one appears
  anywhere else.
- `scripts/rename-product.sh` rewrites both in one command: the brand file, the
  module path, the proto package, the binaries and the documentation.
- The name decision was due before `v0.1.0`, and was made before anything was
  published.

## Security / compatibility impact

A rename does not change the action digest. It used to: the domain-separation
prefix contained the product slug, so a rename would have invalidated every
stored approval and every shared fixture, and the brand gate could not have seen
it, because a golden fixture holds a hash and a hash holds no literal. The
prefix is now fixed and name-free, see
[ADR-0010](0010-digest-domain-separation.md).

After the first tagged release the module path and the proto package are public
contracts, and changing them breaks every importer, see
[ADR-0002](0002-wire-contracts-and-versioning.md). That is what a rename still
costs.

## Alternatives considered

- Waiting for the final name before writing code. Blocks the whole project on a
  decision that does not need to be made yet.
- Hard-coding the name and renaming later by search and replace. It misses
  generated code, fixtures and the digest prefix, and nothing proves it worked.

## Consequences

Brand strings are allowed only in the files on the allowlist, which is a small
constant cost on every contribution. `internal/brand/brand_test.go` pins the
current defaults, so a real rename updates that test in the same change.
Renaming after the first tagged release or the first external importer stays
expensive whatever the script does: the script buys time, it does not make the
decision free.

## Validation

Runnable today: `go test ./internal/brand/` pins the current defaults and
compares `ModulePath` with the module directive in `go.mod` and `ProtoPackage`
with the `package` line of every `.proto` file, and `scripts/check-brand.sh`
runs in `make quality` and fails on a brand literal outside the allowlist.
`scripts/rename-product.sh` refuses to start while `go.mod` and `ModulePath`
disagree, and compares `go.mod` with the requested module again after its pass.

The script was run once, for the rename to Guardana Control: first on a scratch
copy, where the full gate stayed green and the four digest goldens stayed
byte-identical, then on the tree. It is a step a maintainer takes, not a gate
that runs on every change.
