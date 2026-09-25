# ADR-0024: Guardana Control and Guardana are independent

Status: accepted
Date: 2026-09-25

## Context

Two open-source projects share the Guardana name and organization.
[Guardana](https://github.com/guardana/guardana) verifies: it scans artifacts,
probes deployed systems and reads recorded traces, and it never sits in a
request path. Guardana Control sits in the request path and decides each call.

Early plans tied the two together: a trace format Guardana reads as a milestone
of this project, a converter from Guardana's contract files to policies, a CI
recipe that ran Guardana against this project's demo, and pages saying the two
share the wire contract and its fixtures. None of that was built, and Guardana
does not name this project anywhere. Left in the plans and the pages, it would
make Control look incomplete without Guardana, and it would let one project's
release wait on the other's.

## Decision

- Each project is complete on its own. Control builds, tests, runs and releases
  with no Guardana installed, configured or reachable, and loses nothing when
  Guardana is absent.
- Control depends on nothing from Guardana: no module, no binary, no service,
  no schema version and no fixture, in the product, its tests, the gate or the
  workflows.
- The two work together only through formats each one publishes for anyone.
  Here those are the v1 wire contracts, the OTLP log records and the trail
  file. Each project may describe the other and link to it.
- A bridge to Guardana is an optional module. That covers an exporter writing
  Control's evidence in a format Guardana reads, and a converter from a
  Guardana contract to a Control policy. Such a module is off unless an
  operator configures it, lives in its own package under `adapters/`, stays off
  the decision path and is imported by nothing else. Removing it changes
  nothing else in the tree.
- No release, milestone or roadmap item of either project waits on the other.
  Their versions are independent.

## Security / compatibility impact

A bridge is an adapter and translates, as every adapter does
([ADR-0007](0007-repository-layout-and-dependency-rule.md)). It adds no
verdict, and a policy it produces is signed and loaded like any other. No
guarantee this project makes depends on a verifier having run.

## Alternatives considered

- One product in two halves, sharing a schema repository. That couples two
  release trains in two languages and makes each half incomplete by definition.
- A bridge in the core, or on by default. It breaks invariant 8 and the layering
  rule of ADR-0007, and adds an optional tool to what has to be audited.
- Shared fixtures as a release condition. A change in Guardana could then fail
  this project's gate.

## Consequences

The roadmap keeps the bridge as a separate, optional item. The pages mention
Guardana as a related project and never as a prerequisite. Once a bridge is
built, someone who runs both projects can configure it; someone who runs one
needs nothing from the other.

## Validation

The workflows install no Guardana package and call no Guardana binary, and they
run the whole gate on a fresh runner, so a test that needed Guardana would fail
there. `go.mod` requires no Guardana module. Review covers the rest: a change
that makes Control need Guardana is refused under this record.
