# ADR-0089: Outside signed, every receiver holds its input to the plane's one tenant and project

Status: proposed
Date: 2026-11-27

Amends [ADR-0082](0082-a-signed-document-names-its-plane-and-a-signed-plane-serves-one-tenant.md)
on "directory and memory are unchanged".
Amends [ADR-0046](0046-a-finding-stops-one-run-through-a-signed-route.md)
on the route under `reaction.public_key`.
Amends [ADR-0083](0083-a-finding-may-stop-an-agent-or-a-principal.md)
on agent and principal scopes, which it opens outside `signed`.
Amends [ADR-0019](0019-an-operator-can-pause-calls.md) on the unsigned pause
file.

## Context

Under `approvals.authority: signed` each file or answer a plane accepts is
held to the plane's tenant and project (contracts.md "Signed files and their
project checks"). Under `directory` and `memory` they are not:

- an approval claim in `approvals.dir` is matched by request id and approval id
  only; the approvals record of format `2.1` already carries `authority`,
  `tenant` and `project`, written by the plane when it holds the call, but
  the plane never compares them; a `2.0` record names neither;
- the unsigned pause file (versions `1` and `2`) names no tenant, project or
  plane, so every plane that reads the path takes it;
- the route a plane takes under `reaction.public_key` (`reaction-route/v1alpha1`)
  names a tenant but no project, so one route key speaks for every project
  of that tenant; its stop lines (list version `1.0`) name no project and its
  scope is `run` only;
- run records already resolve only for the plane's project and the listener's
  tenant.

Outside `signed` a plane judges answers and pauses under `tenant_id` and runs
and stops under the listener's tenant (`listener.principal.tenant_id` where
set), so two tenants can meet on one plane. Outside `signed` there is no
operator identity: write access to the directory or the file is the
authority.

## Decision

- One tenant under every authority: a `listener.principal.tenant_id` that is
  set and differs from `tenant_id` is refused at load, naming both keys, under
  `directory` and `memory` as under `signed`.
- A claim counts only if its record names the hold's tenant, the hold's
  project and the `directory` authority. The plane compares them with its own
  record of the hold. A claim that differs, and a record of format `2.0`, which
  names neither, are no answer the plane can judge: the hold stands and the
  call is `INDETERMINATE`, as for an unreadable signed answer. The approvals CLI
  and the console change nothing: they already copy the record.
- The unsigned pause file moves to `schema_version` `5` and names `tenant`
  (the plane's `tenant_id`) and `project`. A plane reads a file of another
  tenant or project as unknown, as an unreadable file is. Versions `1` and
  `2` are refused, naming `pause init --tenant --project <file>`, the one
  command that reads a version `2` file and rewrites it as version `5`.
  Version numbers are not shared with the signed pause file (`3` is its
  carry-only version, `4` its current one).
- Outside `signed`, the route under `reaction.public_key` is
  `reaction-route/v1alpha4`, signed by that key. It names `route_id`, `serial`,
  `tenant_id`, `project_id`, `lift_public_key` and `rules`; each rule names a
  `scope` of `run`, `agent` or `principal`, and `agent` and `principal` require
  `expires_seconds` and are taken only for `REPEATED_DENIAL` version `2`, the
  one rule [ADR-0088](0088-a-verdict-that-compares-two-events-times-is-at-most-suspected.md)
  lets stop an agent or a principal. Its list is version `4.0`: the header keeps the
  members of `1.0`, since the route digest commits to the project; `stop` and
  `covered` lines add `project_id`, `scope` and `subject`; a lift line names
  `scope`, `subject` and `through_line` and holds a `reaction-lift/v1alpha4`
  envelope `{list_id, route_digest, scope, subject, through_line}` signed by
  the route's lift key. A plane refuses a route, stop or lift of another
  tenant or project. `v1alpha1` routes and `1.0` lists are refused, naming
  `route sign` and `stops init`; nothing is carried from them.
- Each format keeps one signer: `v1alpha3` stays the signed authority's,
  signed by roster operators.
- Reads: outside `signed` the read boundary is the file mode, with one
  directory per project. A receiver is a part of the plane that takes an
  input as authority; each refuses another tenant's or project's input.
  Operator listings add no filters: anyone who can read the directory can
  read its files.
- `contracts.md` gains the rows for these receivers outside `signed`, each
  with the test that refuses another project's input, pinned by the docs
  check.

## Security / compatibility impact

Every break fails closed and names its way forward. Two tenants under
`directory` or `memory`: refused at load; set the listener's tenant to
`tenant_id` or remove it. A `2.0` approval record: the hold is `INDETERMINATE`
until it expires within `approvals.ttl`.

Pause file `1` or `2`: pause state unknown, so material calls are blocked
until `pause init --tenant --project` rewrites it. A `v1alpha1` route or a
`1.0` list: stop state unknown, so material calls are blocked; with the plane
stopped, `route sign` a `v1alpha4` route, `stops init`, and `react` again; a
stop that was lifted under the old list is stopped again, which errs on the
blocking side, and the operator lifts it again.

What this does not do: under `directory` write access to the approvals
directory, the pause file or the route file is still the authority, and no
operator is identified; a comparison of tenant and project catches a claim
filed for another hold or a directory switched to another project, not a
writer of the directory, who can write any bytes. The boundary stays the
plane's exclusive lock on its approvals directory, its owner and its mode.

## Alternatives considered

- Reusing `v1alpha3` outside `signed` under the route key: it needs `plane_id`,
  which ADR-0082 refuses outside `signed`; it names no lift key; its header
  and lift are signed by roster operators, and one format would carry two
  trust models chosen by configuration.
- Keeping `v1alpha1` with a documented rule of one route key per project: no
  project or scope on a line; an agent stop would cross projects.
- Keeping two tenants and teaching every receiver and the restore both: every
  receiver would carry two tenants for good.
- Adding project filters to `approvals list`, `runs list` and the console
  (decoration: file access already decides who reads).

## Consequences

Operators of `directory` and `memory` planes migrate a pause file, a route and
a stop list once; the changelog names each break; an upgrade guide shows the
steps. The reaction package gains one route format, one list version and one
lift kind.

## Validation

A directory claim of another hold's tenant, project or authority, and a `2.0`
record, are unreadable, with a matching claim as the negative control; a
version `5` pause file of another project is unknown; a `v1alpha4` stop of
another project's agent is refused; versions `1` and `2` and `v1alpha1` are
refused naming their commands; the load refuses two tenants under every
authority; the pause file's versions are distinct across the unsigned and
the signed kinds; `contracts.md`'s rows are pinned by the docs check.
