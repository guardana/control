# ADR-0017: An external decision point can veto what the signed policy allows

Status: accepted
Date: 2026-09-23

Builds on [ADR-0007](0007-repository-layout-and-dependency-rule.md),
[ADR-0011](0011-contract-corrections-before-publication.md),
[ADR-0012](0012-policy-kernel-semantics.md),
[ADR-0013](0013-mcp-interception-approvals-and-modes.md) and
[ADR-0014](0014-evidence-spool-and-sinks.md).
Amends [ADR-0003](0003-policy-model-and-external-pdp.md) on what an external
decision point's answer may do.

Amended by [ADR-0019](0019-an-operator-can-pause-calls.md): the decision point
is not asked about a call the plane has a cause of its own to block before the
ask (a pause that covers it, an unreadable pause state, or `LOCKDOWN` or a halt
on a material call), and such a call's recorded kernel decision holds no
answer.

## Context

ADR-0003 chose to reach an organization's own policy engine over the OpenID
AuthZEN Authorization API 1.0 rather than embed one, and to treat its timeout as
`INDETERMINATE`. ADR-0011 limited what this plane takes from it to `ALLOW`,
`DENY` or `INDETERMINATE`: an AuthZEN decision is a boolean, any obligation
rides in its context, and it names no policy version an approval could bind
to.

Neither record settles where the question is asked or what the answer may do.
The kernel in `internal/core` performs no I/O and reads only the clock it is
handed, so it cannot ask. Only the enforcement point may wait on a network, and
only `adapters/` may speak a protocol (ADR-0007).

AuthZEN 1.0 (final, 2026-01-11) evaluates over HTTPS `POST`, by default at
`/access/v1/evaluation`, and answers `{"decision": <boolean>, "context":
<object>}`; a denial is a `200`. A receiver MUST ignore members it does not
know, and a decision point MUST return the request identifier a caller sends,
for which the `X-Request-ID` header is the recommended carrier. The
obligations draft puts duties in `context.obligations` and says a caller that
cannot fulfil one MUST treat the answer as a denial.

## Decision

**The answer is an input the signed policy reads, and it can only veto.** The
policy format `agent-policy/v1alpha1` gains one constraint, `external`, which
holds when the external decision point denies the call. It is legal on a `DENY`
rule and refused on every other effect when the document is compiled. Every
grant comes from a signed local rule: the decision point can take one away and
never give one. Letting it decide within a scope takes two rules, an `ALLOW`
for the scope and a `DENY` for the same scope when the decision point denies.
An approval still binds one action digest under one bundle digest (ADR-0005,
ADR-0011), and `APPROVE` works unchanged.

**The kernel stays the only decider.** `core.Request` gains the answer beside
`Flow`: a sealed value whose zero is "not asked". Its states are allowed,
denied, denied because the answer carried an obligation, and unanswered with
one of three causes: a timeout, an unavailable decision point, or an answer
this plane refuses. The kernel turns the state into the matcher's three-valued
input and into its own reason codes; an adapter never spells a code. With the
same request, bundle, answer, clock reading and id source, `Decide` returns
the same decision, and the determinism property covers every state.

**Silence is a cause in the request.** An answer the kernel does not have
leaves the veto rule undetermined, which is `INDETERMINATE` with a cause in the
request, as every undetermined rule already is (ADR-0012). It blocks every
effect class in every mode that enforces, and `fail_open_read`, which covers
the bundle's availability only, never opens a read on it. A read survives an
outage of the decision point only where the bundle does not route it there.
`OBSERVE` enforces no decision, so there the call proceeds as any other does,
with the `INDETERMINATE` decision on its trail.

**The enforcement point asks only when the answer can change the decision.**
`internal/gateway` decides first without an answer. When the kernel reports
that an undetermined rule reads `external` and that first decision is not
already a `DENY`, which no answer can change, the gateway asks once, with a
deadline, and decides again with the answer; that second decision is the one
recorded. When the question cannot be asked, because the envelope names no
resource id, it decides again with that unanswered state instead. Without the
kernel's report no answer could change the verdict, so none is asked for.

- The rewrite path reuses the one answer: the question it would ask carries no
  arguments and is byte-identical.
- A retry on the resume path asks afresh, so a decision point that changed its
  mind between the hold and the retry blocks the held request.
- `Preview`, which shapes a tool list, never asks: a listing names no resource,
  and a tool the answer governs is listed as decided per call.
- Asks in flight are bounded, and one past the bound is unanswered at once.
- The enforcement point refuses to start with a bundle that reads `external`
  and no decision point configured, and with a decision point no rule
  consults.

**The seam is internal, and the protocol lives in `adapters/authzen`.**
`gateway.DecisionPoint` asks about one envelope and returns the kernel's input
type with no error: whatever it cannot do is an unanswered state that names its
cause. `adapters/authzen` implements it, imports `internal/core` for the type,
and is the only tree that knows AuthZEN. `internal/gateway` imports no adapter,
and a test already refuses that import. `pkg/` does not change (ADR-0009).

**The question carries only what a policy can read.** Mapping version 1:

| AuthZEN | From the envelope |
| --- | --- |
| `subject` | the principal's type and id; its tenant, authentication strength and attributes as properties |
| `action` | the action's name; its kind, provider, protocol and effect class as properties |
| `resource` | the resource's type and id; its tenant, environment and labels as properties |
| `context` | the agent's id and framework; the destination's trust zone and host; the data labels; the envelope's project, tenant, environment and request id; `supported_obligations: []` |

It never sends the argument bytes, their hash or preview, the run context,
trace identifiers, the delegation chain or the model reference. A call whose
envelope names no resource id is not asked about, and nothing stands in for
the id. The request identifier travels as `X-Request-ID`, and an answer that
does not return it in that header is refused.

**The answer is read strictly where it could grant.**

- Its structure is always strict: status `200`, `Content-Type:
  application/json`, one bounded JSON document, exact member names each once,
  and `decision` a JSON boolean. Anything else is refused.
- An answer that passes those checks and says `false` is a denial whatever
  else it carries: nothing added to a denial makes it more permissive. This
  plane fulfils no obligation, and ignores any a denial carries.
- An answer that says `true` counts only when the top level is exactly
  `decision` and an optional `context`, the context holds only members the
  operator listed as informational (none by default), and
  `context.obligations` is absent or empty. A non-empty `obligations` is a
  denial, the obligations draft's own rule. Any other member is refused.

This deviates on purpose from AuthZEN's "MUST ignore unknown members", and only
on an answer that would allow: a member this plane does not know may carry a
duty.

**The transport.** It keeps the exporter's protections (ADR-0014): no redirect
followed, bounded reads, header names and values checked, no credential in a
log or in evidence. It adds two the exporter does not have. Plain HTTP needs a
named risk setting and a loopback address, because a forged `true` removes a
veto. No proxy is used unless one is configured, so an environment variable
cannot send the question elsewhere. Discovery, `/.well-known/authzen-configuration`,
is a `doctor` check and never a start precondition. The evaluation endpoint is
configuration, and it must share the decision point identifier's scheme, host
and port; `doctor` checks that the published metadata agrees. `signed_metadata`
is not verified.

**The decision names the answer.** `pdp_type` stays `builtin`, because the
verdict is the kernel's. `pdp_instance` holds the configured decision point
identifier when the answer was consulted and is empty otherwise; the contract
page states that meaning when the seam lands. `pdp_version` stays empty, since
AuthZEN carries no version. `decision_latency_us` stays the kernel's own time,
and the enforcement point counts the ask's latency. When the answer was
consulted, the decision carries exactly one of the first five codes below,
or `OBLIGATION_NOT_UNDERSTOOD` in place of one when the decision point said
`true` and attached an obligation:

| Code | Number | Usual verdict | When |
| --- | --- | --- | --- |
| `PDP_TIMEOUT` | 17 | `INDETERMINATE` | no answer within the deadline |
| `PDP_UNAVAILABLE` | 39 | `INDETERMINATE` | no decision came back: a transport error, a status other than `200`, a redirect, the bound on asks in flight, or a question that could not be asked |
| `PDP_ANSWER_REFUSED` | 40 | `INDETERMINATE` | a `200` this plane will not read |
| `PDP_DENY` | 41 | `DENY` | the decision point denied the call |
| `PDP_ALLOW` | 42 | `ALLOW` | the decision point did not deny the call |
| `OBLIGATION_NOT_UNDERSTOOD` | 29 | `DENY` | the decision point said `true` and attached an obligation |

The numbers are fixed here, before any code takes one. The registry's summary
of `OBLIGATION_NOT_UNDERSTOOD` widens to an obligation an external answer
carries; the code still also marks a bundle obligation this plane cannot
apply, beside `PDP_ALLOW` when the answer allowed. No rule may name a `PDP_`
code as its reason, so a local denial never reads as the decision point's.

## Security / compatibility impact

The decision point cannot authorize a call the signed bundle does not allow. A
compromised, misconfigured or forged answer can deny, leave a call undecided,
which blocks it, or, by saying `true` falsely, remove the veto it would have
made. Its silence never becomes an allow in any decision. The signed bundle
remains the one statement of what may happen, and approvals keep binding to
it. The wire contract does not change: `pdp_instance` gains a stated meaning,
and four reason codes join the registry, a registry change and not a contract
change. The policy format gains one constraint; a bundle that does not use it
decides exactly as before.

The enforcement point sends a remote party the identity, action, resource and
destination of each call whose decision depends on the answer, never its
content (invariant 9).

## Alternatives considered

- **The answer as an input on any rule, with any value.** A bundle could make
  the decision point its sole grantor, its silence would become `DENY
  NO_MATCHING_RULE` or `INDETERMINATE` depending on how the bundle is written,
  and list shaping would hide every tool the answer governs.
- **The answer instead of the bundle, for calls a configuration routes there.**
  The routing table would be unsigned configuration deciding which calls
  escape the signed policy, and a routed decision has no bundle digest an
  approval can bind to.
- **The enforcement point asks instead of the kernel.** It skips the kernel's
  own checks, which ADR-0012 runs whatever the policy says.
- **Silence as a cause in the policy's availability.** A read the decision point
  would have vetoed would run on a local allow under `fail_open_read`: the
  alternative ADR-0012 already rejected.
- **Ask on every call.** It sends every call's metadata to a party that governs
  only some of them.
- **Honour "MUST ignore unknown members" everywhere.** Ignoring a member that
  carries a duty, on an answer that allows, would let an unapplied duty pass as
  permission.
- **Discovery at start.** An outage of the decision point would stop the plane,
  including every call the bundle never routes there.

## Consequences

An operator who wants the decision point to decide a scope writes that scope
twice, once to allow and once to veto; the bundle stays the one place that says
what may happen. The decision point's latency is added to every call whose
decision depends on it, and its outages block exactly those calls. A decision
point that decorates its allows with context members needs those members
listed. The roadmap's "instead of the built-in matcher" becomes "can veto what
the signed policy allows". OPA, which has no AuthZEN endpoint, needs a shim or
a native adapter of its own, which is later work.

## Declared limits

1. The decision point never grants; a forged allow can remove its own veto.
2. An approval binds the bundle digest, not the decision point's policy. A
   retry asks again and a changed answer blocks; a policy that changed and
   answers the same is invisible here.
3. Silence blocks every effect class the bundle routes to the decision point in
   every mode that enforces; `fail_open_read` never opens a read on it.
4. The decision point never sees arguments, their hash or a digest; its answer
   covers the call's identity, and the rewrite path reuses it.
5. `Preview` never asks, so a governed tool is listed as decided per call.
6. The question carries what mapping version 1 lists and nothing else; the
   delegation chain is not sent, and a call with no resource id is not asked
   about.
7. Strict on an allow, lenient on a denial: a stated deviation from AuthZEN.
8. A decision point that does not return `X-Request-ID` is refused on every
   call.
9. `pdp_version` is empty, and `decision_latency_us` excludes the ask.
10. No answer is cached and no batch evaluation is used; either would reuse a
    decision across calls and needs its own record.
11. Discovery is checked by `doctor` only, and `signed_metadata` is not verified.
12. No obligation is fulfilled, and the access-request and approval profile and
    the MCP binding are not implemented: a denial from the decision point
    cannot become a request for approval.
13. The bound on asks in flight and the deadline both fail closed.

## Validation

Planned with the lanes that build it:

- The compiler refuses `external` on every effect but `DENY`, one boundary
  case per effect.
- Property: deciding with an allowing answer equals deciding with every
  `external` rule deleted, in verdict, action, rule ids and obligations, so the
  decision point never grants.
- The determinism property over every state of the answer, the zero value
  included.
- Property: when the kernel does not report the answer as needed, every state
  decides the same.
- For every unanswered cause and every effect class, with `fail_open_read` on
  and off, an enforcing mode blocks the call.
- The adapter against an in-process decision point, one case per refusal
  above, and a fuzz target on the answer's reader.
- A golden of mapping version 1, and a test that no field it never sends
  reaches the request bytes.
- End to end: a case per effect class and cause, a retry the decision point now
  denies, and a list shaping that asks nothing.
