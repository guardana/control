# ADR-0078: A restore only raises

Status: accepted
Date: 2026-11-07

Amends [ADR-0038](0038-a-signed-freshness-statement-and-a-serial-floor.md)
with a floor a restore stamps, and
[ADR-0076](0076-a-signed-plane-takes-a-run-only-from-an-operators-signature.md)
with the closes a restore carries into `state/`. Builds on
[ADR-0034](0034-a-run-the-operator-opens-has-an-identity-of-its-own.md),
[ADR-0073](0073-an-approval-counts-only-as-the-plane-judges-it.md) and
[ADR-0075](0075-a-signed-plane-takes-a-pause-only-from-an-operators-signature.md).

## Context

A plane's authority state lives on disk: floors that keep an older policy,
roster or pause from being taken again, runs and what each took in, and a
pause file. Losing that disk and copying an old volume back lowers every
floor, reopens closed runs, resumes holds and lets a spent approval count
again. Under the signed authority the plane's account cannot write the
operators' files, so a restore that merged them would need an operator's
signature over a union nobody judged.

## Decision

- `guardana-control state backup <config> <dir>` copies, per class, the
  plane floors, the route floors, the runs directory and the pause file,
  and under `signed` the roster's and the pause's floors, into a new
  owner-only directory with `backup.json` (schema `0.1`) written last. The
  spool, the hold journal and its ledger, the approvals store and inbox, the
  trail, the signer's floors and every key are never copied. A test holds
  every configuration key naming a file to one side or the other.
- `state restore <config> <dir>` runs as the plane's account, holding the
  spool's and the journal's locks for its whole run, and writes only what
  that account owns: floors, runs and, under `directory`, the pause file. It
  refuses another tenant, project or authority, a source the configuration
  does not name, a tampered or unlisted file and another major, before it
  writes.
- The locks are the directory locks the spool and the journal take, taken
  without linking either, so the approver's binary still reaches neither
  the gateway nor evidence. Where the system has no file lock, both
  commands refuse.
- Every floor ends at the higher serial of the backup's and the live one. A
  plane floor is stamped with issued and latest at the restore time, a raise
  in the floor's own order, so no statement issued before the restore
  confirms that serial again. The floor format does not change.
- Every run closes at the restore time. Under `signed` the restore writes
  `state/<id>.closed` for every run the backup or `records/` names, the
  close ADR-0076 carries, so an open record put back later is refused.
  Taint is joined: untrusted where either side is, the higher read, unknown
  above all.
- Under `directory` the pause file is the union of both; under `signed`
  only the pause floor is raised, and `doctor` names what an operator puts
  back.
- A stop list is refused until its list floor exists, never skipped.

## Security / compatibility impact

A restore cannot grant authority: it only raises floors, closes runs and
adds pauses, and never writes a hold, an approval or an operator's file. A
statement at a higher serial than the restored floor, issued before the
restore, still confirms within `policy.max_stale`. Under `signed` the floors
are only as strong as the plane's account. A raw volume copy stays
unsupported. No wire contract changes; `backup.json` is a new format.

## Alternatives considered

- Union the signed files and have an operator re-sign them: puts a key in
  recovery and asks for a signature over a union nobody judged.
- A floor format `1.1` with a `not_before`: the stamp is already a raise in
  the floor's order, so a new format buys nothing.
- Check once that no lock is held: a plane could start halfway through.

## Consequences

An operator stops the plane, makes the spool and journal directories again
and restores; a lost hold is lost, and an operator decides again. A plane
with a stop list cannot be restored until the list floor exists.

## Validation

`internal/recovery`'s tests restore after a loss and over surviving state,
refuse each bad backup and a held lock with the tree unchanged, stop a
restore before each write and rerun it to the same tree, and draw random
backups per class to show nothing ends lower (`TestRestoreNeverLowers`).
