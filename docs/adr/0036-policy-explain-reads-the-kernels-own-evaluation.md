# ADR-0036: `policy explain` reads the kernel's own evaluation

Status: accepted
Date: 2026-10-02

Builds on [ADR-0012](0012-policy-kernel-semantics.md) and
[ADR-0026](0026-first-value-and-external-extension-paths.md).

## Context

A decision names its verdict, its reason codes, the policy's digest and the
ids of the rules that matched or were undetermined, in one list. An author
whose call came out `INDETERMINATE` or `DENY` cannot tell from it which rule
did what, nor which field of the call a rule could not read. The kernel's
evaluation is three-valued: a rule that reads a field the call leaves out is
unknown, an unknown `ALLOW` is no match, and an unknown rule of any other
effect makes the decision `INDETERMINATE`. Without seeing which field was
unknown, an author guesses.

An explanation computed beside the decision is a second evaluator, and two
three-valued evaluators drift. One computed on every decision costs the
request path an allocation per rule for a reader who is not there.

## Decision

**One evaluation, two entry points.** `match.Program` evaluates through one
unexported function. `Evaluate` passes it no trace; `Explain` passes one, which
records each rule's value as the evaluation gave it and, in a second reading
of the rule's pure constraints, the fields that did not hold and those that
read unknown. The kernel is built the same way: `Decide` and `Explain` run one
decision, the explanation nil in `Decide`. An explanation therefore describes
the decision `Decide` makes for the same request, and the tests below hold it
there.

**What "missing" means.** For a rule left undetermined, each constraint that
read unknown is named by the document's field and by the input it lacked, in
the envelope's wire spelling: `resource.environment`;
`principal.attributes["team"]`; `delegation` when no chain came; `external`
when the decision point gave no answer; for a toxic-flow constraint `flow` when
the run was not tracked, else `flow.max_sensitivity_read` or
`data.sensitivities`, whichever is unknown. Beside the rules the kernel's own
findings are named: the field a refusal named (never its message, which can
quote the input), the tenant a material call left out, and what became of a
delegation chain. A value the build cannot read, an undeclared enum number or
a label off the scale, is a refusal before evaluation and never a missing input.

**Names, not values.** An explanation names fields and never repeats a value
from the call. What it repeats is the author's own text from the document:
rule ids, the bundle's id and version, and a key of a constraint map inside a
field's name, each printed quoted so that it cannot read as another field.

**The command.** `guardana-control policy explain <case>` reads the case format
`policy test` reads, with `expect` optional, decides through `Kernel.Explain`
and prints the decision's fields and one line per rule, the ones that decided
first. It prints at most 32 rule lines and 8 constraints per line and counts
the rest. Its output carries no version and is not a contract: a script reads
`policy test`. The plane never calls `Explain`.

## Security / compatibility impact

No wire contract, reason code, digest or `pkg/` surface changes. `Evaluate`'s
allocations are unchanged and pinned. The explanation can name a field the
author's policy reads but cannot widen what a decision allows: it is computed
after the value it describes and feeds nothing back.

## Alternatives considered

- A trace on every `Result`: allocations on every call for a reader who is
  rarely there.
- A second evaluator in the command: two three-valued logics that only tests
  would hold together.
- Explaining a recorded decision from the trail: the inputs are not captured by
  default ([ADR-0004](0004-evidence-and-privacy-defaults.md)), so it could not
  name what was missing.
- Computing the plane's mode in the command: the command may not link the
  gateway, so the decision's own `enforcement_mode` is printed and the page
  says the plane's mode acts after.

## Consequences

An author sees which rule left a call undetermined and which input to supply,
in the shipped starter packs and in their own cases. The matcher keeps a field
name beside each constraint, and the plane's binary carries the trace code
behind the branch only `Explain` opens. The command reads a case, not a live call; an
operator asking about a call on a plane writes one from the call's fields.

## Validation

- `internal/policy/match`: over drawn documents and calls, `Explain`'s result
  is `Evaluate`'s, and every trace agrees with its value (no when a field
  failed, unknown when none failed and one was unknown, matched when none is
  listed); `Evaluate`'s allocation count is pinned.
- `internal/core`: on every fixture and in `FuzzDecide`, `Explain`'s decision
  equals `Decide`'s field for field, id and latency included.
- `cmd/guardana-control`: on every shipped case, explain's verdict, action and
  codes are `policy test`'s line; the bounds and the refusal's silence on the
  input are tested; `cmd/guardana-gateway` fails if a package the plane links
  calls `Explain`.
