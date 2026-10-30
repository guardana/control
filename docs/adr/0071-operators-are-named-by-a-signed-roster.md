# ADR-0071: Operators are named by a signed roster, and authorized by its grants

Status: proposed
Date: 2026-10-30

Builds on [ADR-0066](0066-each-decision-about-authority-is-a-port.md), which
makes trust, operator authentication and operator authorization ports, on
[ADR-0038](0038-a-signed-freshness-statement-and-a-serial-floor.md) and
[ADR-0046](0046-a-finding-stops-one-run-through-a-signed-route.md) for the
serial floor, and on
[ADR-0017](0017-an-external-decision-point-can-veto.md) for the veto.

## Context

Today an approver is whoever can write the approvals directory, and the
approver id is a claim. ADR-0066 says operators are authenticated against a
signed roster by default and authorized by the roles it states, and that a
remote authorization only vetoes. Three ports need a first kind each, a
format for the roster, and a rule for what an operator signs.

## Decision

**The trust roster port** (`internal/trust.Roster`, kind `file`, the
default). One file holds a DSSE envelope of payload type
`application/vnd.agent-trust-roster+json`, signed once by the pinned root key.
Its body is the canonical JSON object of kind `trust-roster/v1alpha1`:
`rosterId`, `serial`, `issuedAt`, `expiresAt` and `keys`, each required, no
other. Each key carries `keyId` (its own id), `alg` (`ed25519` in this
version), `role` (`operator` in this version), `subject`, `publicKey` and one
to 32 `grants` of `tenant`, `project` and `level` (`read`, `operate`,
`admin`). A tenant or a project is never empty and never a pattern. No key is
named twice; none is the root, nor a key the plane pins for policy,
freshness, routes or lifts; the root is none of those either. The lifetime is
at most 7 days; `operators roster sign` signs for 24 hours unless told
otherwise. A roster dated after the clock, expired, of another id than the
one held, below the serial held, or at it with another digest, is not taken.
Each taken roster raises a floor of the new `authority` kind in
`internal/policystate`, so a restart never takes a lower one. The plane keeps
the last roster it verified until its own expiry, then answers unavailable.
Any other role or algorithm is refused until a change adds its consumer.

**The operator authenticator port** (`internal/operators.Authenticator`,
kind `roster`, the default). An act has one payload type: approving or
rejecting a hold is `application/vnd.agent-approval-answer+json`. A statement
is one signature over a canonical JSON object of at most 16 KiB that names
`alg` and `keyId`. The key is selected by the signature's key id from the
roster, for role `operator`, and the signature is verified under the
algorithm that key carries; then the body's `alg` and `keyId` must be that
key's. The answer is unavailable, refused or authenticated, never a `bool`,
and an authenticated one names the issuer `roster/<rosterId>`, the subject
and the key id. Only this package builds an authenticated answer.

**The operator authorizer port** (`internal/operators.Authorizer`, kind
`roster`, the default). It asks the roster again for the signing key, so a
key removed, given to another subject, or lowered after it signed permits
nothing, and permits an act over exactly one tenant's one project when a
grant there is at the act's level or above (`approve` needs `operate`). An
empty tenant or project is denied, never read as a wildcard. A veto may
follow it: it is asked only about what the roster permitted, its answer can
only be no objection or an objection, and no answer in time is unavailable
unless `operators.authorizer.on_unavailable` is `roster_only`.

**Bounds.** Each call to another port runs under a timeout, 2 s unless set
and at most 30 s, and at most 16 at once unless set, 256 at most; a call over
either is unavailable, counted per cause.

**Conformance.** `testdata/ports/trust-roster`, `operator-authenticator` and
`operator-authorizer` hold one case file each, every rule with a negative
control, and each kind's test runs them. `scripts/ports.txt` lists the three
ports.

This change adds no reason code, no configuration key and no wire field. The
plane does not read a roster yet: the approval authority's change wires
these ports, builds the `authn:` approver id from an authenticated answer,
and amends the approval records.

## Security / compatibility impact

No v1 wire bytes, digest or approval binding change. The roster and the
answer get payload types of their own, so no signature made for another
signed file verifies as either. Selecting the algorithm by the key keeps a
statement from choosing how it is checked. The roster's expiry bounds how
long a revoked operator keeps authority while a newer roster is withheld,
and the plane's account can withhold it for the file kind; the floor bounds
how far back a restart can go, and write access to the floor directory can
lower it, as for every floor.

## Alternatives considered

- One pinned key per operator in the configuration: no expiry, no grants,
  and a restart to revoke.
- An authorizer that reads the grants carried in the authentication: a key
  revoked between the two would still be permitted.
- Grants with a project wildcard: a mistyped empty project would grant
  every project.
- An expiry with no ceiling: a withheld roster would keep a revoked operator
  for as long as the signer chose.

## Consequences

An operator is added, revoked or regranted by signing a new roster, with no
restart. The root key becomes the authority over every operator, and its
signer must re-sign at least once a week. A second kind of each port needs
its own cases to pass.

## Validation

- `internal/trust`: an envelope of every other payload type never verifies
  as a roster and a roster never as another file; a root equal to the
  policy, freshness, route or lift key is refused; a lower, forked, expired or
  future roster is refused; the last roster is kept until its expiry, then
  unavailable; the floor lowered by hand does not lower the roster held.
- `internal/operators`: a statement naming another algorithm or key, signed
  by another key, or of another payload type, is refused; two operators act
  only where their grants reach; an empty project is denied; a revoked key
  permits nothing; a veto only denies; an unavailable, late or zero answer
  from the roster or the veto blocks.
- The three conformance files, each rule with a negative control.
