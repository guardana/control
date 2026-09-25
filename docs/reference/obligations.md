---
title: Obligations
summary: The catalogue of obligation types a policy rule may name, and what the tree does with one.
type: reference
covers: [internal/policy/rules/catalogue.go, internal/gateway/rewrite.go, adapters/mcp/obligations.go, internal/docscheck/obligationdoc/**]
generated: scripts/gen-obligations.go
---

# Obligations

An obligation is a condition on a call that proceeds. It travels on a
decision in `Decision.obligations`, under `ALLOW_WITH_OBLIGATIONS` and
under `REQUIRE_APPROVAL` once the approval is given. A policy rule names an
obligation by its type, with string parameters and an `advisory` flag that
is false by default: false means the call proceeds only if the obligation is
applied ([ADR-0011](../adr/0011-contract-corrections-before-publication.md)).

The table below is the catalogue, the closed set of types a policy document
may name, and who in this build applies each type. Recognising a type is
`implemented`: the parser and `policy lint` refuse a document naming a type
outside it, and the kernel refuses a configuration whose applicable types
include one outside it. The gateway rewrites the arguments for its types
before the decision it records, and an adapter applies the types it declares
to the call it sends. A decision carrying a non-advisory obligation that
nothing here applies is `DENY` with `OBLIGATION_NOT_UNDERSTOOD`, and no
fail-open setting relieves that; an advisory one is skipped. The name is all
the tree holds about a type: a parameter's key is checked as a map key, and
no type has a parameter schema yet. What is built and what is not is in
[status.md](../status.md).

Rendered from the catalogue in `internal/policy/rules/catalogue.go` and the
types the gateway and the MCP adapter declare. Rebuild it with
`make docs-gen`; an edit made here does not survive the next run.

| Type | Applied by |
| --- | --- |
| `redact_fields` | the gateway |
| `read_only` | the MCP adapter |
| `restrict_resources` | the MCP adapter |
| `require_idempotency_key` | `planned`: nothing in this build |
| `cap_amount` | the gateway |
| `cap_rate` | `planned`: nothing in this build |
| `require_sandbox` | `planned`: nothing in this build |
| `second_approver` | `planned`: nothing in this build |
| `emit_alert` | `planned`: nothing in this build |
| `shorten_timeout` | the MCP adapter |
| `deny_external_sink` | the MCP adapter |
