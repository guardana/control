# ADR-0076: A signed plane takes a run only from an operator's signature

Status: accepted
Date: 2026-11-06

Amends [ADR-0034](0034-a-run-the-operator-opens-has-an-identity-of-its-own.md)
under the signed authority of
[ADR-0073](0073-an-approval-counts-only-as-the-plane-judges-it.md), over the
roster of [ADR-0071](0071-operators-are-named-by-a-signed-roster.md), as
[ADR-0075](0075-a-signed-plane-takes-a-pause-only-from-an-operators-signature.md)
does for the pause.

## Context

Under ADR-0034 write access to the runs directory is the authority to open
and to close a run, and the plane's own account owns that directory. Under
`approvals.authority: signed` that account could open a run, reopen a closed
one or move a run under another root by writing a file. A signature stops a
forged record; it does not stop the open record being put back after the
close that replaced it.

## Decision

- Under `signed` `runs.dir` is a signed layout: `records/`, the operators'
  account's, which the plane only reads, and `state/`, the plane's, which no
  operator command writes. The plane refuses an unsigned directory, refuses
  to start when its account may write `records/` or a directory on its path,
  and refuses a `records/` or a record its own account owns, as ADR-0075
  refuses such a pause file.
- A record there is version `3.0`: a DSSE envelope of
  `application/vnd.agent-run-record+json` (`operators.ActRun`, level
  `operate`) over a canonical body holding every member of a `2.0` record,
  the signing key and the absolute path, links resolved, of the `records/`
  it was signed for, which binds it to one plane's directory. A close is a
  signed record too, which replaces the open one.
- A record counts only once it names the plane's own `records/`, its run is
  of the plane's tenant and project, and the authenticator and the
  authorizer take it. Any other record, a `2.0` one included, refuses the
  calls it would cover as `unreadable`, counted by cause.
- The replay of an open record after its close is held by the close carried
  into `state/`, not by a floor: when the plane takes a closed record, at a
  call or at its start, it writes the run's close there, and an open record
  of a run whose close it holds is refused as closed.
- `runs open` and `runs close` with `--key`, `--roster` and `--root-key`
  sign; they verify any record they sign over, a parent's or the one a close
  replaces, against the roster, the plane's tenant and project (`runs close`
  takes them as `--tenant` and `--project`) and the directory, refuse one
  that fails naming why, verify the record they write, and say on standard
  error what they signed. Without `--key` they refuse a signed layout.

## Security / compatibility impact

Breaking under `signed`: an unsigned runs directory refuses the start, so the
runs are opened again into a new, empty directory. Nothing changes under
`memory` or `directory`. `state/` is the plane's to write, so its own account
can still remove a carried close; a close the plane neither resolved nor
found at its start can be undone by whoever may write `records/`. Records are
group-readable, so the plane's account belongs to the records' group.

## Alternatives considered

- A serial floor over the directory: records are written one run at a time,
  so no single serial orders them, and a floor per run is the carried close.
- The operator command writing the close into `state/`: it would need write
  access to the plane's own state.

## Consequences

A root's state and lock are made by the plane at the root's first raise; a
missing state reads as the start. `runs prune` removes records only, and
the plane's state of a pruned tree stays in `state/`.

## Validation

`internal/runs` tests each refusal, the replay after a close seen at a call
and at a start, a record copied from another directory, and prune over the
split; `internal/runs/signedruns` tests both judges against one roster;
`cmd/guardana-control` tests the signed commands and a close over a record of
a key the roster lacks, another tenant, project or directory, or unsigned;
`cmd/guardana-gateway` tests the start refusals, the carry across a restart
and doctor's counts.
