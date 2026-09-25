# ADR-0003: Structured predicates, deny-overrides, external PDP over AuthZEN

Status: accepted
Date: 2026-09-09

Amended by [ADR-0011](0011-contract-corrections-before-publication.md): a
fail-open read keeps its `INDETERMINATE` verdict, and an external decision point
returns `ALLOW`, `DENY` or `INDETERMINATE` only. Amended by
[ADR-0012](0012-policy-kernel-semantics.md), which fixes the fail-closed mapping
this record left open and narrows fail-open reads to the policy's
availability. Amended by
[ADR-0017](0017-an-external-decision-point-can-veto.md): an external decision
point's answer is an input the signed policy reads, it can only veto, and its
silence is a cause in the request.

## Context

A decision runs in front of a tool call, so it has to be cheap, reproducible and
readable by whoever audits it later. Rules that can execute arbitrary code are
none of those. The project also has to work with the policy engine an
organization already runs, without shipping that engine inside the binary.

## Decision

- The built-in matcher evaluates structured predicates only: identity, action,
  resource, context, effect class and bounds. No regular expressions and no
  scripting in the decision path.
- Precedence is deny-overrides. `DENY` beats `INDETERMINATE`, which beats
  `REQUIRE_APPROVAL`, which beats `ALLOW_WITH_OBLIGATIONS`, which beats `ALLOW`.
- When no rule matches, the verdict is `DENY` with reason `NO_MATCHING_RULE`.
  There is no implicit allow.
- Malformed or unknown security-relevant input is `INDETERMINATE`. A fail-closed
  table decides what `INDETERMINATE` enforces for each effect class: it blocks
  the action for every class with a material effect, and a read-only class may
  be allowed and recorded only where the operator has explicitly turned that on,
  with that setting visible in the decision evidence. The exact mapping is
  [ADR-0012](0012-policy-kernel-semantics.md)'s.
- External policy engines are reached over the OpenID AuthZEN Authorization API
  1.0 request shape (https://openid.net/wg/authzen/specifications/) instead of
  being embedded, which keeps the hot-path binary small. A PDP timeout is
  `INDETERMINATE`, not an allow.

## Security / compatibility impact

`INDETERMINATE` is not an allow, and no adapter gets to collapse it into one:
what it enforces comes from the fail-closed table, not from the caller. Keeping
regular expressions out of the decision path is not about evaluation cost: Go's
regexp engine is linear-time and does not backtrack. It is about review. A
structured predicate can be read by someone who did not write it, indexed, and
explained back in the decision it produced, and a policy author cannot express
something subtly wider than they intended. Keeping scripts out removes a
code-execution surface on top of that, in the one place the system trusts.
AuthZEN 1.0 is a published request shape, so an external engine integrates
against a documented contract rather than a private one.

## Alternatives considered

- Embedding a policy language such as Rego or CEL. Expressive, but it puts an
  evaluator with its own failure modes and its own supply chain into every
  enforcement point, and a rule stops being reviewable by reading it.
- Regular expressions for resource matching. Familiar, and a rule that looks
  narrow can match far more than its author meant, with nothing in the rule to
  show a reviewer that it does.
- Allow on evaluation error, or a default allow when nothing matches. The usual
  shortcut, and the one that turns an outage or a typo into an authorization.

## Consequences

Some policies cannot be expressed by the built-in matcher and have to be written
as explicit predicates or delegated to an external PDP. The fail-closed table
means a policy-source outage blocks actions with material effect; that
availability cost is the point of the decision, and the table is what keeps it
predictable. Adding a verdict or changing precedence is a wire change, so it
needs a new package version and its own record, see
[ADR-0002](0002-wire-contracts-and-versioning.md).

## Validation

Nothing in this record is runnable today. The matcher, the precedence table and
the fail-closed table are planned and land together, with fixtures covering all
five verdicts.
