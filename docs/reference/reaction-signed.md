---
title: Signed reaction
summary: How a signed plane takes its route, stop list header and lifts from roster operators, holds them to a stop list floor, and which commands sign them.
type: reference
covers: [internal/reaction/signed.go, internal/reaction/route2.go, internal/reaction/header2.go, internal/reaction/lift2.go, internal/reaction/signedstops/**, internal/policystate/stops.go, cmd/guardana-control/stops_signed.go, cmd/guardana-gateway/reaction_signed.go]
---

# Signed reaction

Under `approvals.authority: signed` an operator of the trust roster signs
each [reaction](reaction.md) document, and no route or lift key exists
([ADR-0077](../adr/0077-a-signed-plane-takes-a-stop-list-only-from-operators-signatures.md)).
[contracts.md](../contracts.md#under-the-signed-authority) gives each format.

| Document | Format | Signed under the act | Level |
| --- | --- | --- | --- |
| Route | `reaction-route/v1alpha2` | `route` | `admin` |
| Stop list header | stop list `2.0`, header `reaction-list-header/2.0` | `stops` | `operate` |
| Lift | `reaction-lift/v1alpha2` | `lift` | `operate` |

Each body is canonical JSON naming its `alg`, its `keyId`, the tenant, the
project and `file`: the absolute, cleaned path of the route file or of the
stop list, every link on the way to its directory resolved. A copy of a
signed document to another plane of the same tenant and project is refused
there for its `file`. The route names a `project_id` and a `scope` of `run`
on each rule, and every stop and covered line of a `2.0` list names the
project. The header names a `serial` and, after a carry, the `previous`
header's digest; a lift names the digest of the header it lifts from,
`list_head`, so a lift made against one list never verifies against a list
carried or restored after it.

A plane reads the route, the header and each lift through its operator ports
and refuses a document no operator with the act's grant over its tenant and
project signed. The stop list floor, which `stops floor init --tenant
--project` makes and `reaction.stops_floor_dir` names, keeps the highest
header serial the plane took and that header's digest. A plane refuses a list
below the floor's serial, one at that serial with another head, and one above
it whose `previous` is not the floor's head, and raises the floor when its
first read is served. It refuses `reaction.route`, `reaction.stops` or the
directory of either when its own account may write them, and a route file,
stop list or their directory its own account owns; only `dev`, on one
account, reads them. A `v1alpha1` route, `reaction.public_key` and a `1.0`
list are refused.

`route sign --key`, `stops init --key` and `stops lift --key` take the
operator's key, and `stops init`, `stops lift` and `stops list` take
`--roster` and `--root-key` in place of `--public-key`: each judges the route,
the list and its lifts on disk through the roster before it signs, and says
on standard error what it signed. `react` takes `--approvals`, the plane's
approvals directory, and refuses to run as the account that owns it. The
console writes no stop and no lift at any setting.

`doctor` judges the route and the header through the roster file, holds the
header to the stop list floor without raising it, and names who signed each.
`/healthz` gives a `2.0` list's header serial under `stops.signed`; only
`health.socket` names the operators who signed the header and the route.

## Limits

- Under the signed authority a plane takes the floor at its start only: a
  carried list is served after a restart, as any new list is. The floor keeps
  a serial and a head, not a length, so a list cut back within one serial is
  caught by the placement checks and, while a plane runs, by its accepted
  prefix.
