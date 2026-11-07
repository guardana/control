---
title: Back up and restore a plane
summary: Copy a stopped plane's floors, runs and pause file, and after a loss raise them back so no floor ends lower, every run is closed and no hold resumes.
type: how-to
covers: [internal/recovery/**, cmd/guardana-control/state.go, internal/policystate/restore.go, internal/runs/restore.go, internal/pause/merge.go]
---

# Back up and restore a plane

A plane keeps authority state on disk: policy floors, opened runs and their
taint, its pause file, and under the signed authority the roster's and the
pause's floors. `state backup` copies it; after a loss, `state restore`
raises what survived to the backup and never lowers anything
([ADR-0078](../adr/0078-a-restore-only-raises.md)). Both are `experimental`;
[status.md](../status.md) is the inventory.

## Prerequisites

- The plane stopped. Both commands take the spool's and the hold journal's
  locks for their whole run and refuse while a plane holds either. On a
  system without a file lock, Windows among them, both refuse.
- The plane's account and environment: the commands read the configuration
  as the plane does, and a restore writes only what that account owns.
- `approvals.authority` set to `directory` or `signed`. Under `memory` there
  is nothing to restore, and both commands refuse.
- No `reaction.stops`. This build does not restore a stop list yet, and a
  restore refuses rather than leave one behind.

## Steps

1. Back up into a directory that does not exist yet:

   ```sh
   guardana-control state backup plane.yaml /backups/plane-1
   ```

   It prints `backed up <n> files into /backups/plane-1`, and one line for
   each configured path where nothing stood. The directory is made mode
   `0700`, each copy mode `0600`, and `backup.json` is written last: a
   directory without it is no backup.

2. After a loss, make the spool and the hold journal directories again,
   empty and owned by the plane's account, then restore:

   ```sh
   guardana-control state restore plane.yaml /backups/plane-1
   ```

   It prints what it did per configuration key, the residual below, and
   which signed files the backup holds a copy of.

3. Check the plane before it serves:

   ```sh
   guardana-gateway doctor --config plane.yaml
   ```

## What a backup holds

`backup.json`, schema `0.1`, names the authority, the tenant, the project,
when the backup was taken, and for every file its class, the configuration
key, its absolute source, its path in the backup, its SHA-256 and its size.
A restore refuses a backup of another tenant, project or authority, a file
whose source is not where the configuration puts it today, a size or hash
that differs, a file the manifest does not list, and a major other than `0`.

Copied, each under its class: `policy.state_dir`, `reaction.floor_dir`,
`runs.dir` and `pause.file`, and under `signed` also `trust.floor_dir` and
`pause.floor_dir`. Never copied: the spool, the hold journal and its ledger,
the approvals store and inbox, the trail, the signer's floors, the bundle
and its statement, the roster, the route and every key.

## What a restore does

- Plane floor, per bundle id: the higher serial of the backup's and the
  live one, with its digest, stamped issued and latest at the restore time.
  The bundle's own statement, issued before the restore, confirms nothing;
  the next renewal does. Two digests at one serial, or a clock behind
  either floor, refuse before that id is written. An id neither side holds
  a serial for is listed with no file, so the start refuses until
  `policy state reset`.
- Route, roster and pause floors: the higher serial of the two.
- Runs: every run closed at the restore time. Under `directory` each record
  either side holds is written closed, keeping an earlier close; under
  `signed`, `state/<id>.closed` is written for every run the backup or
  `records/` names, so an open record put back later is refused. Each
  root's taint is the join of both sides, and a state lost beside its
  record counts as untrusted with an unknown read.
- Pause: under `directory` the union of both files' entries; an id holding
  two different pauses refuses. Under `signed` only its floor is raised.

Nothing held resumes: the journal, the ledger and the approvals store are
never written, so a lost hold stays lost and a spent approval stays spent.
A missing floor directory is built under a temporary name and renamed into
place; a restore stopped at any write and run again ends where an unbroken
one does.

## Verify

`doctor` reads each signed file against the raised floors. Under `signed`
the restore never writes an operator's file, the pause file and `records/`
among them. An operator who wants the backup's copy copies it back to the
same path; it is bound to that path, and the plane takes it only where the
raised floor still does. A lower signed pause file blocks every call until
an operator signs a new one.

## Limits

- A statement at a higher serial than the restored floor, issued before
  the restore, still confirms, within `policy.max_stale`.
- A run neither the backup nor `records/` names reopens only through an
  operator's signed write.
- Copying a volume back by hand is unsupported: it lowers floors, reopens
  closed runs, resumes holds and lets spent approvals count again.

## Roll back

A restore only raises, so it has no undo. `policy state reset` lowers a
plane floor, recording why; a new run is opened with `runs open`.
