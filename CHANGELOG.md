# Changelog

Notable changes to this project are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[semantic versioning](https://semver.org/spec/v2.0.0.html).

The `0.x` series carries no compatibility promise between minor versions; see
[ADR-0008](docs/adr/0008-branching-and-release-policy.md). Each release is built,
signed and attested from its tag; [RELEASING.md](RELEASING.md) shows how to
verify one.

## [Unreleased]

### Fixed

- A decision point's answer that reaches the gateway after the call's
  deadline is a timeout (`PDP_TIMEOUT`), whatever its status and headers.
  Such an answer could be read as refused or as unavailable instead.

## [0.1.0-alpha] - 2026-09-25

The first public release. [docs/status.md](docs/status.md) says what is
`implemented` and what is `experimental`. Nothing here is a security boundary
yet.

### Added

- Wire contracts, frozen at `1.0`: eight protobuf files for the action
  envelope, the decision, approvals, results, events, findings, policy bundles
  and the shared types, with the generated Go. `pkg/contract` decodes them
  strictly, refusing unknown fields and undeclared enum numbers, and validates
  an envelope within stated bounds
  ([ADR-0002](docs/adr/0002-wire-contracts-and-versioning.md)).
- A canonical form for an action and the digest built on it, with golden
  fixtures ([ADR-0005](docs/adr/0005-canonical-action-digest.md),
  [ADR-0010](docs/adr/0010-digest-domain-separation.md)).
- Policy documents in `agent-policy/v1alpha1`, read as strict JSON; bundles
  signed with Ed25519 over the DSSE pre-authentication encoding; a three-valued
  matcher where `DENY` overrides; and the decision kernel with its fixed order
  and fail-closed table, under which `INDETERMINATE` never allows a material
  call ([ADR-0012](docs/adr/0012-policy-kernel-semantics.md)).
- `guardana-control`: `policy lint`, `test`, `keygen` and `sign`; `approvals
  list`, `approve` and `reject`; `pause init`, `add`, `remove` and `list`; and
  `console`, a page on the loopback that answers held calls and pauses
  ([ADR-0018](docs/adr/0018-keys-and-bundles-on-disk.md),
  [ADR-0019](docs/adr/0019-an-operator-can-pause-calls.md),
  [ADR-0023](docs/adr/0023-a-local-page-answers-through-the-directory.md)).
- `guardana-gateway`, an MCP gateway that decides each call before it reaches
  the server: `run` and `doctor`; the enforcement modes; approvals bound to one
  action digest and consumed once; obligations; a check that the executed
  arguments are the authorized ones; evidence kept in a local spool and exported
  over OTLP ([ADR-0013](docs/adr/0013-mcp-interception-approvals-and-modes.md),
  [ADR-0014](docs/adr/0014-evidence-spool-and-sinks.md),
  [ADR-0016](docs/adr/0016-approval-providers-and-the-lost-hold.md)).
- An external AuthZEN decision point that can veto what the signed policy
  allows, and never grant
  ([ADR-0017](docs/adr/0017-an-external-decision-point-can-veto.md)).
- `guardana-gateway collect` and `trail`, a trail file without a third-party
  collector, and `GET /metrics` in the Prometheus text format
  ([ADR-0020](docs/adr/0020-a-trail-and-counters-without-a-collector.md)).
- Run flow: the plane records whether a run took in untrusted input, so a
  policy's flow rule can block a later send to an untrusted destination
  ([ADR-0021](docs/adr/0021-a-run-carries-what-it-took-in.md)).
- `guardana-gateway scenario run`, which holds a live plane to
  `agent-scenario/v1alpha1` files, and `guardana-gateway dev`, one command for
  a local plane with its collector and approvals page
  ([ADR-0022](docs/adr/0022-scenarios-are-data.md),
  [ADR-0023](docs/adr/0023-a-local-page-answers-through-the-directory.md)).
- A demo, `examples/vulnerable-mcp-agent`, with seven scenarios, and
  [its tutorial](docs/get-started/try-the-demo.md).
- One quality gate, `make quality`, which CI runs on every push to `main` and
  every pull request, and a documentation structure the gate checks
  ([ADR-0015](docs/adr/0015-documentation-structure-and-checks.md)).
- Release archives for Linux and macOS on amd64 and arm64, with a CycloneDX
  bill of materials per archive, signed checksums and build provenance
  ([ADR-0025](docs/adr/0025-public-repository-merges-and-releases.md)).
- Governance: DCO sign-off, Conventional Commits, a security policy, a code of
  conduct, trademark terms and 25 architecture decision records, among them the
  independence of this project from Guardana
  ([ADR-0024](docs/adr/0024-control-and-guardana-are-independent.md)).

[Unreleased]: https://github.com/guardana/control/compare/v0.1.0-alpha...HEAD
[0.1.0-alpha]: https://github.com/guardana/control/releases/tag/v0.1.0-alpha
