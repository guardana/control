# ADR-0004: Evidence defaults to metadata; content capture is opt-in

Status: accepted
Date: 2026-09-09

Amended by [ADR-0011](0011-contract-corrections-before-publication.md): a result
names its redaction profile as the arguments do, and `Event` field 31 is declared
for the digest link.

## Context

The evidence record is what makes a decision reviewable months later. It is also
the place where prompts, tool arguments and model output would leak if the
default were wrong. A default that captures everything turns an audit log into
the most sensitive store an operator runs.

## Decision

Two different rules, and the record states both because they are often confused:

- Prompt and tool content capture is off by default and opt-in. An operator can
  turn it on. The default record is metadata plus hashes.
- Hidden model reasoning is never recorded, at any setting. There is no opt-in
  for it.

The rest:

- Redaction runs before anything is persisted or exported, never on read.
- Evidence is append-only. Tamper evidence, meaning a hash chain over records
  and signed checkpoints, is planned.
- Records are written to a local spool first and exported from there.

## Security / compatibility impact

Content that was never written cannot leak from a backup, an export or a support
bundle, and reasoning is never written at any setting. Redacting before
persistence means a bug in a read path cannot expose raw content, because the
raw content is not there.

Append-only is a write discipline, not tamper evidence. It stops the running
system from rewriting a record; it does not let a reader prove that nothing was
changed underneath it. That proof needs the hash chain and the signed
checkpoints, which are planned, and until those exist the record must not be
described as tamper-evident.

The spool keeps the exporter off the request path, so an exporter outage does
not block a decision. It does not make loss impossible. What it buys is that
loss becomes bounded and visible: the spool has a size, and its depth and its
drops are counted, instead of a decision disappearing silently.

## Alternatives considered

- Capture on by default with redaction applied on read. Convenient for
  debugging, and one bug exposes content that was already stored.
- Synchronous export. The exporter becomes part of the request path, and its
  outage becomes either an availability incident or a reason to stop enforcing.
- Recording model reasoning for debuggability. It is the least controlled text
  in the system and the hardest to redact.

## Consequences

An operator who needs full content has to turn it on deliberately and own the
retention. The spool needs a disk budget, and what happens when it fills is part
of that decision rather than an afterthought. Reconstructing a decision from
metadata and hashes alone is harder than reading a transcript, which is the
trade being made.

## Validation

Nothing in this record is runnable today. The capture setting and the redaction
it applies are planned, with the spool, its counters and the exporter; the hash
chain and the signed checkpoints are planned after them.
