# ADR-0043: A refused over-long name is recorded by its digest

Status: accepted
Date: 2026-10-05

Builds on [ADR-0011](0011-contract-corrections-before-publication.md) and
[ADR-0013](0013-mcp-interception-approvals-and-modes.md).

## Context

The contract bounds every string an envelope carries. The MCP adapter
records a call it refused as too large: it takes the strings the agent chose
off the envelope, so the trail can still say a call was refused and why. It
clears the action's name and the resource's id. Every envelope requires the
name, and the mutating, transacting and access classes require the id, so the
recorded proposal is one that a reader validating the trail refuses a second
time. Nothing in the record ties it to what the agent sent either. On a
`resources/read` or `prompts/get`, the name is the agent's own URI or prompt
name, so an agent chooses its length.

## Decision

When the adapter takes an action's name or a resource's id past the bound off
a refused envelope, it writes in its place `overlong:sha256:` followed by the
lower-case hex SHA-256 of the original bytes. That is 80 bytes, within the
bound. Run-context tags past the bound are still dropped, since no envelope
requires them. An envelope refused whole, which strings within their bound
cannot cause, loses its tags and keeps its name and resource.

The placeholder is written only beside the size refusal. That refusal stops
the decision before any policy reads the envelope, so no rule ever matches a
placeholder. An agent that sends the same text as its own name is decided like
any other, and the refusal on the record is what tells the two apart.
`docs/contracts.md` names the format.

## Security / compatibility impact

No wire field changes. The digest lets someone who holds the original tie it
to the record without the record holding it. It reveals the original only to
someone who can guess it, which an over-long string an agent chose rarely
allows. A recorded proposal that validated before still validates.

## Alternatives considered

- **Truncate to the bound.** It keeps the agent's text in the record,
  partly, and a truncated name can match a real one.
- **Leave the fields empty.** That is today's record, which a validating
  reader refuses.
- **A fixed word without a digest.** It validates, but nothing ties it to the
  original.

## Consequences

A trail reader no longer meets a second refusal for a call the plane
refused as too large. A reader that wants the original needs it from
elsewhere.

## Validation

- A refused tool call with an over-long resource id records a proposal that
  validates, carrying `overlong:sha256:` and the digest of the original id.
- A `resources/read` with an over-long URI keeps a name and an id, each the
  placeholder.
- An envelope refused whole keeps its name and resource as they were.
- No placeholder appears without the size refusal.
