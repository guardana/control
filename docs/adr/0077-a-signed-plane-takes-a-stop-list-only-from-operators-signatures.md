# ADR-0077: A signed plane takes a stop list only from operators' signatures

Status: accepted
Date: 2026-11-07

Amends [ADR-0046](0046-a-finding-stops-one-run-through-a-signed-route.md)
under the signed authority of
[ADR-0073](0073-an-approval-counts-only-as-the-plane-judges-it.md), over the
roster of [ADR-0071](0071-operators-are-named-by-a-signed-roster.md), as
[ADR-0075](0075-a-signed-plane-takes-a-pause-only-from-an-operators-signature.md)
does for the pause file.

## Context

Under ADR-0046 a route key signs the route and a lift key each lift, and
write access to the stops directory adds any stop the route allows and,
across a restart, can start a new list with none. Under
`approvals.authority: signed` that leaves two keys outside the roster and
the plane's own account able to replace the list with an empty one. A
signature on each line stops a forged lift; it does not stop an older list,
or another plane's, being put in place.

## Decision

- Under `signed` the route is `reaction-route/v1alpha2`, signed under the
  `route` act at level `admin`; it names a `project_id`, and each rule a
  `scope` of `run`. The stop list is version `2.0`: its header is a DSSE
  envelope of a `reaction-list-header/2.0` body signed under the `stops` act
  at level `operate`, naming a `serial` and, after a carry, the previous
  header's digest. A lift is `reaction-lift/v1alpha2`, signed under the `lift`
  act at level `operate`, and names the digest of the header it lifts from.
  Every stop and covered line names the project. No route or lift key exists
  under `signed`, and `reaction.public_key` is refused there.
- Each signed body names its key, its tenant and project and the absolute,
  cleaned path of the route file or the stop list it was signed for, links
  on the way to its directory resolved. A plane compares it with its own, so
  a copy to another plane of the same tenant and project is refused.
- A plane reads the route, the header and each lift through its operator
  ports. The stop list floor in `reaction.stops_floor_dir` is a serial floor
  of the kind the route and pause floors are: the header serial the plane
  took and the header's digest. A list below it, at its serial with another
  head, or above it naming another previous head is refused; the plane
  raises it when its first read is served. A restore raises it to the
  higher of its own and the backup's.
- The plane refuses to start when its own account may write the route file,
  the stops directory or either's directory, and refuses at the read a route
  file, stop list or directory that account owns; only a dev plane, one
  account, reads its own. `react` refuses to run as the account that owns
  the approvals directory. The console writes no stop and no lift.
- The commands that sign with `--key` judge what they start from on disk
  against the roster before they sign, and say on standard error what they
  signed.

## Security / compatibility impact

Breaking under `signed`: a `v1alpha1` route or a `1.0` list is refused, and
the plane needs `reaction.stops_floor_dir`. A `1.0` list is not carried into
`2.0`; operators start a new list and `react` stops again what may still
stop. Nothing changes under `memory` or `directory`. The floor is the plane's
to write, so its own account can lower it and put an older list back; the
placement checks and the signatures keep everyone else out.

## Alternatives considered

- Signing each stop line: `react` holds no operator key, and a missing line
  is an absence no line signature covers.
- A floor that keeps the list's length: a header is signed once and its list
  grows unsigned, so a length would hold only what the plane already read,
  which its accepted prefix holds within one life.

## Consequences

A plane takes the floor at its start: a carried list is served after a
restart, as before. `react` writes lines no operator signs; what it writes is
bounded by the route an admin signed and by the stops directory being out of
the plane's reach. A lift made against one list never ends a stop of a list
carried or restored after it.

## Validation

`internal/reaction` tests each refusal of a signed route, header, stop,
covered line and lift, and a lift for one head refused under another;
`internal/reaction/signedstops` tests both vouchers, the floor's chain and a
copy between two planes of one project; `internal/reaction/stopwrite` and
`internal/reaction/stoplist` test the writer's chain and the reader's owner
checks; `cmd/guardana-gateway` and `cmd/guardana-control` test the start
refusals, the floor raised at start and the commands through a roster.
