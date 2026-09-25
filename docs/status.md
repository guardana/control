---
title: Implementation status
summary: What exists in this repository today, component by component, the one inventory every other page points to.
type: project
covers: [adapters/**, api/proto/**, cmd/**, internal/**, pkg/**]
---

# Implementation status

What exists in this repository today, component by component. Every other
document points here rather than keeping its own list, so this is the only
inventory in the project and the one that has to be right.

What the labels mean:

- `implemented`: it exists and a command in this repository exercises it.
- `experimental`: it exists and can still change without a major version.
- `planned`: it is not built. `Where` names the path it will take, the record
  that defines it, or says that no path has been chosen yet. A path that
  already holds a package doc and a test holds nothing that decides.

Nothing here is a security boundary. Do not deploy it as one.

## Components

| Component | Status | Where |
| --- | --- | --- |
| Go module and pinned toolchain | `implemented` | `go.mod` (`go 1.27.1`), `scripts/bootstrap.sh` |
| Quality gate, including the documentation and dependency checks | `implemented` | `Makefile`, `.golangci.yml`, `scripts/`, `internal/docscheck/`, `internal/core/layering_test.go`. The dependency rule is an allowlist over five trees, all examined; `make check-imports-probe` plants a violation in each and fails unless every mechanism refuses it. The documentation check judges every page's frontmatter, type, `covers`, budget and diagrams and pins every rendered page and block to its generator ([ADR-0015](adr/0015-documentation-structure-and-checks.md)); `make docs-impact` names the pages a change makes suspect |
| Brand indirection and one-command rename | `implemented` | `internal/brand/`, `scripts/check-brand.sh`, `scripts/rename-product.sh` |
| Architecture decision records | `implemented` | `docs/adr/` |
| Command line: `policy lint`, `policy test`, `policy keygen` and `policy sign` | `implemented` | `cmd/guardana-control/`, [guides/write-and-test-a-policy.md](guides/write-and-test-a-policy.md). The same binary holds the `approvals` commands and the `console` page in the approval providers row below, and the page's loopback listener is its one server; `policy explain` and `policy diff` are `planned` |
| Policy keys and signed bundle files | `implemented` | `internal/policykey/` (the private key file and its reader, the public key line and its one parser, the derived key id, the bundle file), `internal/files/` (create without replacing, replace, bounded read of a regular file), `internal/keytext/` (the withholding and quoting both binaries and the approvals page print through, which reads no key), [ADR-0018](adr/0018-keys-and-bundles-on-disk.md). `policy keygen` and `policy sign` write them and the gateway parses `policy.public_key` through the same package. Unix-like systems only; nothing stops a bundle with a lower serial from being loaded after a restart, and the approvals store, the hold journal and the spool still carry their own file code |
| Continuous integration | `implemented` | `.github/workflows/`: `ci.yml` runs `make quality` on Linux for every push to `main` and every pull request; `security.yml` runs CodeQL and the vulnerability, advisory and secret scans, `pull-request.yml` checks a pull request's title and sign-offs, `workflow-lint.yml` checks the workflows, and `scorecard.yml` scores the repository's supply chain |
| Wire contracts, frozen at `1.0` | `implemented` | `api/proto/guardana/control/v1/` (8 files), generated Go in `api/gen/go/`. `make proto-check` regenerates and diffs; `buf breaking` runs against `main` in CI and in `make proto-breaking` |
| Bounded validation and strict decoding | `implemented` | `pkg/contract/`. Unknown fields and undeclared enum numbers refused on both wire paths, inside map entries too, which `Decode` reads before the runtime parses them ([ADR-0002](adr/0002-wire-contracts-and-versioning.md)) |
| Effect materiality, sensitivity floors, toxic-flow predicate | `implemented` | `pkg/contract/effect.go`, `labels.go`; the kernel's tenant rule and fail-closed table read materiality, the matcher reads the floors |
| Reason-code registry and its generated reference | `implemented` | `internal/policy/reasons/`, [reference/reason-codes.md](reference/reason-codes.md), which the registry generates. The kernel and the matcher emit codes as their own literals, each held to the registry by a test; nothing on the decision path imports the registry |
| Evidence event chain, in memory | `implemented` | `internal/evidence/`. Builder, chain validation and a JSONL codec. A `Sink` seam and a memory sink for tests; the package itself reads no clock and touches no file, and the spool that writes disk implements the seam from `internal/spool/` |
| Decision kernel: the fixed order, the fail-closed table, the delegation check | `implemented` | `internal/core/`, `internal/core/delegation/`, [ADR-0012](adr/0012-policy-kernel-semantics.md). `policy test` and 25 fixtures under `testdata/policy/fixtures/` exercise every verdict and cause, the external decision point's answers among them; a 10 000-case determinism property and `FuzzDecide` run in `make test` and `make fuzz-smoke`. `ENFORCE` only; the modes are the enforcement point's ([ADR-0013](adr/0013-mcp-interception-approvals-and-modes.md)), and `internal/gateway/` calls `Decide` on every call it admits |
| Canonical action digest | `implemented` | `internal/canon/`, goldens in `testdata/digest/`, [ADR-0005](adr/0005-canonical-action-digest.md) and [ADR-0010](adr/0010-digest-domain-separation.md). Cross-language parity is unproved until a second implementation exists |
| Policy documents, signed bundles and the matcher | `implemented` | `internal/policy/rules/` parses `agent-policy/v1alpha1` strictly, `internal/policy/bundle/` signs and verifies bundle bytes (Ed25519 over the DSSE PAE); `internal/policy/match/` evaluates three-valued; `internal/policy/` loads a bundle into a snapshot and installs it once, in order; [ADR-0003](adr/0003-policy-model-and-external-pdp.md), [ADR-0012](adr/0012-policy-kernel-semantics.md). A holder pinned to the bundle id it serves refuses every other; a signed expiry and a serial that survives a restart are `planned` |
| External policy decision point (PDP) over AuthZEN, able to veto | `experimental` | [ADR-0017](adr/0017-an-external-decision-point-can-veto.md). The kernel takes the answer and the policy format reads it (`internal/core/`, `internal/policy/`); `adapters/authzen/` asks and reads the answer strictly; `internal/gateway/` asks only when the answer can change the decision; `cmd/guardana-gateway` configures it from the `pdp.` keys, checks its published metadata in `doctor` and counts the asks on `/healthz`. The end-to-end tests reach a decision point on the loopback over plaintext; TLS is exercised in the adapter's own tests, against the roots a test hands it, and the command has no setting for a private certificate authority |
| Approval binding to one action digest and one bundle digest | `implemented` | `internal/core/approval/` (`Bind`), golden in `testdata/digest/binding.json`. `internal/gateway/` holds a request for approval, keeps the record of what it minted itself, and consumes the approval exactly once on the retry ([ADR-0013](adr/0013-mcp-interception-approvals-and-modes.md)). An approver outside the plane answers through the file provider below; a hold still does not survive a restart, and a plane that keeps a hold journal closes the trail of one it lost rather than leaving it open ([ADR-0016](adr/0016-approval-providers-and-the-lost-hold.md)) |
| Evidence spool | `experimental` | `internal/spool/` (segmented log with CRC32C per record, fsync policy, a budget with a reservation per open trail, a quarantine for what a collector refuses, an exclusive directory lock, recovery that truncates only a torn tail and refuses damage), the `Sink` seam in `internal/evidence/` with a memory sink for tests, [ADR-0014](adr/0014-evidence-spool-and-sinks.md). Its own tests and a crash-safety property cover it, and `cmd/guardana-gateway` wires it as the plane's sink |
| OpenTelemetry export | `experimental` | `adapters/otel/`, OTLP logs over HTTP in the protocol's JSON encoding on the standard library, pinned by a golden in `testdata/otlp/`, [ADR-0014](adr/0014-evidence-spool-and-sinks.md). It acknowledges only the protocol's own answer read strictly, follows no redirect, needs the operator's word for a plaintext endpoint, retries everything else, quarantines what a collector refuses, and drains one run at a time; `cmd/guardana-gateway` runs it |
| A collector and a trail file of the project's own | `experimental` | `adapters/otel/` also reads what the exporter sends, strictly, and answers only after the records are synced; `internal/trailfile/` appends to a trail file under an exclusive lock, cuts a torn last line at open, and reads a file back with each trail's chain checked; `guardana-gateway collect` (loopback only) and `guardana-gateway trail`, [guides/watch-a-plane-without-a-collector.md](guides/watch-a-plane-without-a-collector.md), [ADR-0020](adr/0020-a-trail-and-counters-without-a-collector.md). A trail file is at least once and never rotated, and its check proves each chain's shape, not its integrity |
| Model Context Protocol (MCP) gateway | `experimental` | `internal/gateway/` (the protocol-neutral pipeline: `Admit`, `Close`, `Abort`, `Preview`, the mode table with its start-time refusals, the executed-arguments digest, the plane-owned hold and its memory approval store), `adapters/mcp/` (stateless and stateful HTTP and stdio listeners, one upstream client per server, the manifest with fingerprints and the operator's overrides, `tools/call`, `tools/list` shaping, `resources/read` and `prompts/get`), `cmd/guardana-gateway/` (`run`, `doctor`, `collect` and `trail`, `GET /healthz`, `GET /metrics` in the Prometheus text format from `internal/metrics`, whose names [reference/metrics.md](reference/metrics.md) is rendered from, and `GET /brand`), `internal/gatewayconfig/` (the configuration file, its `GUARDANA_CONTROL_*` variables and the field table [reference/configuration.md](reference/configuration.md) is rendered from), `internal/pause/` and `pause init`, `add`, `remove` and `list` in `cmd/guardana-control/` (the operator's pause file, its strict reader, its writer under an exclusive lock, and the poller a plane decides every call under, [ADR-0019](adr/0019-an-operator-can-pause-calls.md)), [ADR-0013](adr/0013-mcp-interception-approvals-and-modes.md), [guides/run-the-gateway.md](guides/run-the-gateway.md), [reference/mcp-coverage.md](reference/mcp-coverage.md). `OBSERVE`, `APPROVE`, `ENFORCE` and `LOCKDOWN` run and `SHADOW` and `WARN` are refused at start; no configuration key wires an authenticator, so no call binds an end user; a run is the listener's principal and agent, one per plane without an authenticator, and a policy's `flow` constraints read what the run's executions declared they return ([ADR-0021](adr/0021-a-run-carries-what-it-took-in.md)); after untrusted input a flow rule blocks every call to an untrusted destination; `APPROVE` needs an approval provider that can answer and is refused at start with the in-memory one; a pause bites up to one poll interval after it is written, never on an execution already running, and cannot name a principal, an agent or a tenant; elicitation and the Tasks extension are `planned` |
| Approval providers and the lost hold | `experimental` | `internal/approvals/` (a directory of records an approver writes and the plane reads back, two openers kept apart by the compiler, `0600` records under an owner-only directory), `internal/holdjournal/` (the plane's own durable record of its own holds, under its own exclusive lock), the `HoldJournal` seam and the reconciliation in `internal/gateway/`, `approvals list`, `approve` and `reject` in `cmd/guardana-control/`, and `console`, a page on `127.0.0.1` that lists, approves, rejects and pauses through the same directory and pause file (`internal/console/`, [reference/console.md](reference/console.md), [ADR-0023](adr/0023-a-local-page-answers-through-the-directory.md)), the `approvals.provider` keys in `internal/gatewayconfig/`, [ADR-0016](adr/0016-approval-providers-and-the-lost-hold.md), [concepts/approvals-and-the-held-call.md](concepts/approvals-and-the-held-call.md). Write access to the approvals directory is the approval authority and `approver_id` is an unauthenticated claim; a plane configured with no hold journal directory does not close a lost hold; the page's printed token and the session it trades for are the only thing between it and another local account or a web page, and it cannot tell whether a plane reads its pause file; signed webhook callbacks are `planned` |
| Scenarios held against a live plane | `experimental` | `internal/scenario/` reads `agent-scenario/v1alpha1` strictly; `guardana-gateway scenario run` (`cmd/guardana-gateway/`) calls a running plane as an MCP client, waits for its spool to drain, reads the trail file its collector writes and compares each call's answer, decision and trail, and answers approvals and pauses through the `guardana-control` beside it; [reference/scenario-format.md](reference/scenario-format.md), [ADR-0022](adr/0022-scenarios-are-data.md). It must be the plane's only client, a scenario must finish within the approval lifetime, and it trusts the trail file beyond the ids, the bundle digest and the mode it checks |
| Dev mode: one command for a local plane and its page | `experimental` | `guardana-gateway dev` (`cmd/guardana-gateway/dev*.go`) lays out a new state directory, signs the demo's document under a key that lives only in its memory, runs the collector and the plane in its own process on loopback ports it bound, and runs `guardana-control console` beside them; `--scenario` runs each scenario on a plane of its own. `internal/loopback/` is the one rule for a loopback IP literal, which `collect` uses too; [reference/dev.md](reference/dev.md), [ADR-0023](adr/0023-a-local-page-answers-through-the-directory.md). A plane serving a bundle file decides with `POLICY_STALE` every call that reaches the freshness check once the smaller of the document's and the operator's staleness budget has passed, until it is restarted |
| Demo: two vulnerable MCP servers, their plane and seven scenarios | `experimental` | `examples/vulnerable-mcp-agent/`, [get-started/try-the-demo.md](get-started/try-the-demo.md). One binary serves the orders or the web server over standard input and output, and a decision point on a fixed loopback port that never answers, so one demo runs at a time; `demo.yaml` and `policy.json` are what `dev` takes. Its test builds the binaries, runs each scenario on its own plane through `dev`, reads what each server received, and fails one mutant of each scenario on the member it changed |
| Agent-to-agent (A2A) adapter | `planned` | `adapters/` |
| Framework SDKs and thin clients | `planned` | separate repositories, [ROADMAP.md](../ROADMAP.md) |
| Control plane API | `planned` | `internal/controlapi/`, [ADR-0009](adr/0009-open-core-boundary.md) |
| Storage | `planned` | `internal/storage/` |
| Ingest and normalizer | `planned` | `internal/ingest/` |
| Graph API | `planned` | no path chosen yet, [ROADMAP.md](../ROADMAP.md) |
| Web UI | `planned` | no path chosen yet, [ROADMAP.md](../ROADMAP.md) |
| Detectors | `planned` | `detectors/builtin/`, `pkg/detector` |
| Releases built, signed and attested from a tag | `experimental` | `.goreleaser.yaml`, `.github/workflows/release.yml`, [RELEASING.md](../RELEASING.md), [ADR-0025](adr/0025-public-repository-merges-and-releases.md). Archives for Linux and macOS on amd64 and arm64 with both binaries, a CycloneDX bill of materials per archive, `checksums.txt` signed without a stored key, and a build provenance attestation for every archive, all verified by the workflow before it publishes. No container image yet |
| Benchmarks | `implemented` | `bench/`: envelope validation, the digest and `Decide` at 10, 100 and 1000 rules and with obligations, with a guard test in `make test`. Results stay local under `bench/results/`; a session report carries the numbers with the machine named |

`make quality` names every check that runs today. Read the `Makefile` for the
exact set rather than a list in prose, which drifts.

## What it supervises today

The project means to supervise an organization's AI agents
([ROADMAP.md](../ROADMAP.md)). Against that aim, the components above add up to
this:

| Aim | Today |
| --- | --- |
| Setup for common agent stacks, a proxy for the rest | An MCP proxy only; no framework port and no proxy for other tool APIs |
| Oversight of what agents do and the data they reach | Each tool call, resource read and prompt through one plane is decided and recorded; a call that bypasses the plane is not seen, and there is no view across planes |
| Procedures, and deviations from them | Policy per call and one flow rule; no procedure an agent is held to |
| Attempts to gain access | Each refusal is recorded with its reason codes and counted in `/metrics`; nothing reports a pattern of attempts |
| Quality: finished, not degraded, nothing skipped | A result's status and hash only |
| Running beside the agents, reporting deviations | The plane writes evidence to a spool for export; no supervisor reads it yet |
| Stopping an agent | A pause of every call, of one upstream or of one tool, for calls not yet running; not of one agent, principal or run |
| A run drawn as a graph | None; `guardana-gateway trail` checks each trail's chain |

## Open source, and what may be commercial

Required by [ADR-0009](adr/0009-open-core-boundary.md). A security capability is
never withheld from the open build, whatever its status above.

| Part | Licensing |
| --- | --- |
| Enforcement kernel | Open, permanently |
| Policy matcher | Open, permanently |
| Adapters | Open, permanently |
| Approvals | Open, permanently |
| Evidence | Open, permanently |
| Detectors | Open, permanently |
| Control API | Open, permanently |
| Self-hosted deployment | Open, permanently |
| Managed hosting | May be commercial |
| Curated policy and detector packs | May be commercial |

This page is updated in the same change as the work it describes.
