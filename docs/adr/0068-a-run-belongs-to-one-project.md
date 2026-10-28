# ADR-0068: A run belongs to one project

Status: accepted
Date: 2026-10-28

Amends [ADR-0034](0034-a-run-the-operator-opens-has-an-identity-of-its-own.md)
on whose run a token names.

## Context

A run's record named a tenant, a principal and an agent, and a plane resolved
a token for the identity its listener gives. Two planes of different projects
that share a runs directory and a listener identity resolved each other's
runs. A token opened for one project's task then acted as a run of the other,
carried that project's flow state into it, and was recorded under the other
project.

## Decision

A run record carries `project_id`, and its `schema_version` moves from `1.0`
to `2.0`. The record is refused when it is written or read with an empty or
invalid project.

A plane resolves a token under its own `project_id`, never under one the
listener or the caller names. A token for another project's run is refused
as another identity: 401 with cause `identity`, and no trail.

`runs open` needs `--project`, and refuses a parent of another project.

A record of major 1 names no project, so it is refused with its version,
never read as any project's. The way to move is to open each run again: no
run lives past 720 hours.

The runs directory's marker and each root's state stay at `1.0` and name no
project. Planes of several projects may still share one directory, because
each record is bound on its own.

## Security / compatibility impact

A token can no longer cross projects. Every run opened by an earlier release
stops resolving on upgrade, and its agents need new tokens. A stdio plane given
such a token refuses to start. A plane that serves runs over HTTP answers 401
`unreadable` on each call until the token is replaced.

## Alternatives considered

- Bind the runs marker to one project as well. Planes of two projects could
  then not share a directory, and the record's own binding already refuses
  the token.
- Read a `1.0` record as the plane's project. A run opened for one project
  would then be served by whichever plane read it first.
- Take the project from the listener. A listener is configured per plane and
  can be copied between planes, so it proves nothing about the project.

## Consequences

Operators reopen live runs once at upgrade. `doctor` does not yet name a
`1.0` record in a runs directory; the plane's refusal does.

## Validation

- `internal/runs` keeps a literal `1.0` record and refuses it, and refuses a
  record with an empty project at encode and at decode.
- `cmd/guardana-gateway`'s `TestARunOfAnotherProjectIsRefusedOnASharedDirectory`
  serves two projects' planes from one directory and listener identity and
  expects 401 with no trail.
- `TestARecordOfMajorOneResolvesOnNoPlane` expects a `1.0` record to resolve on
  no plane.
