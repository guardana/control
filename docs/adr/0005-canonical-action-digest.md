# ADR-0005: One canonical digest identifies an action

Status: accepted
Date: 2026-09-09

Amended by [ADR-0010](0010-digest-domain-separation.md) and
[ADR-0011](0011-contract-corrections-before-publication.md).

## Context

An approval, a replay and an SDK written in another language all have to agree
on what "the same action" means. If two implementations hash the same action
differently, an approval cannot be bound to it and evidence cannot be
correlated.

## Decision

One canonical hash identifies an action: `sha256` over a domain-separation
prefix followed by the RFC 8785 canonical JSON form of a fixed field set: the
envelope's `principal`, `agent`, `delegation`, `action`, `resource` and
`destination`, plus the authorized arguments.

Timestamps, request and trace identifiers, and previews are excluded, so the
same action hashes the same way twice.

An approval binds the action digest together with the policy bundle digest.

The domain-separation prefix is `agent-action-digest/v1\n` and contains no
product name. That is [ADR-0010](0010-digest-domain-separation.md), which amends
this record; the paragraph below about a rename changing every digest no longer
holds.

ADR-0011 adds the envelope's `projectId`, `tenantId` and `environment` to the
field set, once, before any digest was published, and keeps the tag. It also
refuses object keys that fold together.

The rules this record deferred, settled before the first golden was computed,
because a golden fixture computed under unstated rules is one no other language
can reproduce:

- **Every envelope field in the set is present**, materialized at its proto zero
  value. Such a field is never omitted, and `null` never stands in for one.
  Otherwise an SDK building the map from protojson output, which omits empty
  scalars, disagrees with one reading the struct, and the approval never
  matches. This rule is about the envelope, not about the authorized arguments:
  those are the caller's own JSON, where `null` is an ordinary value and is
  canonicalized as one.
- **Enums encode as their name**, `"EFFECT_CLASS_TRANSACT"`, not their number.
  A number changes meaning if the enum is ever renumbered, and the name is what
  every protojson implementation already emits.
- **`delegation` keeps its order**, because that order is the authority chain
  and reversing it inverts the "child exceeds parent" check. **`scopes` inside a
  delegation is sorted**, because it is a set and two producers may list it
  either way.
- **Floats are refused.** RFC 8785 specifies float serialization exactly, so
  this is a simplification rather than a necessity, and it has a real cost: a
  tool argument like `{"temperature": 0.7}` cannot be digested, so it cannot be
  approved, so a material effect carrying it is blocked. It is refused with a
  named JSON pointer and a reason code, never silently rounded. Accepting floats
  later would only add inputs that are refused today, so it changes no digest
  that already exists and invalidates no stored approval.
- **A string that is not valid UTF-8 is refused**, and so is an **unpaired
  surrogate escape**. This one is not tidiness. Measured while implementing:
  `"\ud800"` keeps U+D800 in Node 24 and Python 3.9 and becomes U+FFFD in Go,
  so the same document would have two canonical forms and two digests depending
  on which language hashed it. A valid surrogate pair is still accepted, and an
  escaped backslash followed by `ud800` is still an ordinary string.
- **Integers are limited to the JSON-safe range**, plus or minus 2^53 - 1.
  Beyond it there is no faithful IEEE-754 double, so an implementation reading
  the same document through `JSON.parse` would compute a different digest. A
  larger integer travels as a JSON string, which is what protobuf itself does
  for `int64`.

Digest strings are `sha256:` followed by lowercase hex, everywhere. They are
compared for equality to authorize a call, so the case is part of the contract.

## Security / compatibility impact

Binding both digests is what stops an approval from being replayed against
changed parameters, or against a different policy bundle than the one the
approver saw. Excluding volatile fields is what makes the binding usable at all:
two identical actions have to hash identically, or an approval could never match
the action it approved. Expiry is carried by its own field, with its own
semantics. The domain-separation prefix used to contain the product slug; it no
longer does, so a rename changes no digest, see
[ADR-0010](0010-digest-domain-separation.md).

## Alternatives considered

- Hashing the serialized protobuf. Field ordering and unknown-field handling are
  not stable enough across implementations to hash.
- A per-language JSON encoder without a canonical form. Small encoding
  differences produce silent mismatches that look like tampering.
- Including the timestamp or the request id. Two identical actions would then
  hash differently, so an approval could never match the action it approved.

## Consequences

Golden fixtures are shared across languages, so an SDK cannot drift unnoticed.
Adding a field to the digest input changes every existing digest, so it
invalidates stored approvals and needs its own record and a new package version,
see [ADR-0002](0002-wire-contracts-and-versioning.md).

## Validation

Runnable today: `go test ./internal/canon/` checks the canonical form itself —
key ordering by UTF-16 code unit, the escaping rules, the float and integer
refusals, the surrogate rule — with a fuzz target and property tests.

The digest, the domain tag and the golden fixtures in `testdata/digest/` are
`implemented`, and the same package's tests compute the digest over every
fixture. Cross-language agreement is not proved. An independent reimplementation
reproduced the fixtures during a review, which is evidence and not a maintained
second implementation; that is `planned`.
