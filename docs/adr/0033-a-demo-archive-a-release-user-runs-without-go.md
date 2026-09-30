# ADR-0033: A demo archive a release user runs without Go

Status: accepted
Date: 2026-09-30

Builds on [ADR-0008](0008-branching-and-release-policy.md),
[ADR-0017](0017-an-external-decision-point-can-veto.md),
[ADR-0022](0022-scenarios-are-data.md) and
[ADR-0023](0023-a-local-page-answers-through-the-directory.md).

## Context

The demo needed a checkout and Go: its victim servers were built from
`examples/`, its configuration named them by a path inside the repository, and
no release carried either. Its decision point, a victim serving on the fixed
port `127.0.0.1:18213`, made two demos on one machine collide and made the
demo's tests fail under a second test run. `dev` builds only where process
groups exist, and on any other system nothing said so before it misbehaved.

## Decision

- **A demo archive beside the product's.** Each release adds
  `guardana-control-demo_<version>_<os>_<arch>.tar.gz` for every platform the
  product archive has. It is wrapped in one directory, holds the gateway,
  `guardana-control` and the victim servers under `bin/`, and the demo's
  configuration, policy, scenarios, `call.sh` (mode `0755`) and README at their
  repository paths, so the tutorial's commands are the same in a checkout and
  in the archive. The product archive keeps its layout and never carries the
  victim, which is deliberately vulnerable. The image does not carry the demo:
  it would need the approver's binary in the plane's image.
- **A decision point that never answers, bound by dev.** `dev
  --decision-point=silent` binds one more loopback listener, as it binds its
  others, and never accepts on it. The plane's question waits there until
  `pdp.timeout`, so a rule reading `external` is undetermined with
  `PDP_TIMEOUT`; no code in dev can answer, so none can lift a veto. With the
  flag dev owns every `pdp.` key; without it the demo file's own decision
  point on the loopback stands, as before. No port is fixed anywhere in the
  demo.
- **The archive is checked, not its configuration.**
  `scripts/check-demo-archive.sh` extracts this machine's demo archive from
  goreleaser's `dist/`, requires every non-Go file of the demo the repository
  holds to be there and equal, and `call.sh` to be executable, and runs every
  scenario from it with no Go on `PATH`. `release.yml` runs it after the build
  and before anything is attested or published, and `make check-demo-archive`
  runs it after `make release-snapshot`.
- **An unsupported platform is refused first.** `dev` refuses a system without
  process groups before it reads anything, naming it; a test pins the list to
  the build constraint that gives `dev` its process groups. Archives exist for
  Linux and macOS.

## Security / compatibility impact

The product archive's contents do not change; its binaries are built under
`bin/` and the directory is stripped. A release gains four archives and their
bills of materials; `checksums.txt` names them, so the existing attestation
and verification cover them, and the release's archive count goes from four
to eight. The victim binary is signed and attested as a demo asset. The silent
listener takes connections into its backlog and reads nothing, so another
account can fill the backlog and no more. The binaries are not notarized for
macOS: a browser download is quarantined and refused, which the tutorial
says.

## Alternatives considered

- **The victim in the product archive.** Every user of the product would
  download, verify and unpack a deliberately vulnerable server, and a `bin/`
  and an `examples/` would land wherever the archive is opened.
- **A decision point in dev that publishes its metadata and holds every
  question.** A decision point embedded in the plane's binary, with held
  requests nobody bounds.
- **A port bound, released and handed to the victim.** Another process could
  take it in between, and would then be asked.
- **A Go test reading `.goreleaser.yaml`.** No parser for it exists here, and
  a copy of goreleaser's rules can agree with itself while the archive
  differs.

## Consequences

The tutorial starts from a download; a checkout is the alternative. Two demos
run side by side. The check runs on the real archive, so it does not run on a
pull request: a missing asset is caught by the release dry run or at the tag,
before anything is published.

## Validation

- `scripts/check-demo-archive.sh` over a snapshot: every demo file present,
  `call.sh` `0755`, all seven scenarios passed with no Go on `PATH`; over a
  copy with a scenario removed or `call.sh` at `0644`, it fails.
- A unit test dials the silent listener and reads nothing before a deadline;
  the demo's `fail-closed` scenario decides its export with `PDP_TIMEOUT`.
- Dev's tests keep a file-set loopback decision point working without the
  flag, and refuse a file-set `pdp.` key with it.
- The platform test compares `dev`'s list with the build constraint for every
  system `go tool dist list` names.
