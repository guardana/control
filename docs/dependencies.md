---
title: Dependencies
summary: Every direct dependency and pinned tool of the module, why the standard library will not do, its licence and whether it sits in the request path.
type: project
covers: [go.mod, go.sum, scripts/tool-versions.env]
---

# Dependencies

A new direct dependency needs an entry on this page in the same change that
adds it, stating the problem it solves, why the standard library will not do,
its licence, a maintenance signal, and whether it sits in the request path. A
dependency in the request path gets a stricter review than one in tooling,
because it joins the set of code that has to be audited before a verdict can
be trusted.

`go.mod` is the source of truth. Where this page and `go.mod` disagree, this
page is wrong.

## In the request path

### `google.golang.org/protobuf` v1.36.12

- Problem it solves: encoding, decoding and validating the wire contracts. The
  generated Go under `api/gen/go/` is built against this runtime, and the JSON
  representation goes through `protojson` with unknown fields rejected, see
  [ADR-0002](adr/0002-wire-contracts-and-versioning.md).
- Why the standard library will not do: `encoding/json` carries no schema, no
  generated types, no breaking-change gate, and no rule for a field the
  receiver does not know. Quietly dropping a field the producer believed was
  security-relevant is the failure the contract exists to prevent.
- Licence: BSD-3-Clause, with a separate patent grant file, both in the module.
- Maintenance signal: it is the reference Go implementation of Protobuf,
  developed in `protocolbuffers/protobuf-go` alongside the format itself.
- Request path: yes. It parses input that has not been authorized yet, so a
  version bump is read rather than merged on a green gate.

### `github.com/modelcontextprotocol/go-sdk` v1.8.0

- Problem it solves: speaking the Model Context Protocol on both sides of the
  gateway, at both live revisions: the Streamable HTTP listener toward the
  agent, stateless for `2026-07-28` and stateful for `2025-11-25` and older,
  the stdio listener, and the client toward each upstream server. The
  receiving middleware it exposes is the one interception point of
  [ADR-0013](adr/0013-mcp-interception-approvals-and-modes.md).
- Why the standard library will not do: the protocol is JSON-RPC over three
  transports with per-revision negotiation, session handling, routing-header
  checks and result shapes that changed between revisions. Reimplementing two
  revisions of that and keeping them in step is what the module's authors do;
  it is the reference Go implementation of the protocol.
- Licence: read from the module's `LICENSE` file. The project is moving from
  MIT to Apache-2.0: contributions whose authors consented are Apache-2.0,
  contributions whose authors have not yet consented stay MIT, and the file
  carries both texts. The Go source files themselves still carry an MIT-style
  header. Both licences are permissive and compatible with this project's.
- Maintenance signal: the module is published from
  `modelcontextprotocol/go-sdk`, the organisation that publishes the protocol;
  v1.8.0 was released on 2026-09-14 as a copy of v1.8.0-pre.2, whose commit is
  dated 2026-09-04, and the module ships a security policy. Nothing else about
  the project's activity was checked.
- Request path: yes. It parses every request from the agent and every answer
  from an upstream before policy runs, so a version bump is read rather than
  merged on a green gate. It is imported by `adapters/mcp` and by
  `cmd/guardana-gateway`, which builds the upstream transports; a test in
  `internal/gateway` refuses the import there.
- What it links: the module pulls eight more into the binary that serves the
  adapter, none of them imported by this repository's own code. Each licence
  below was read in the module cache of the version `go.mod` pins:
  `github.com/google/jsonschema-go` v0.4.3 (MIT), which validates tool schemas;
  `github.com/segmentio/encoding` v0.5.4 (MIT) and `github.com/segmentio/asm`
  v1.1.3 (MIT), its JSON encoder and that encoder's assembly kernels;
  `github.com/yosida95/uritemplate/v3` v3.0.2 (BSD-3-Clause, the text in its
  `LICENSE` file), which expands resource templates; `golang.org/x/oauth2`
  v0.35.0, `golang.org/x/sync` v0.23.0, `golang.org/x/sys` v0.48.0 and
  `golang.org/x/time` v0.15.0 (all BSD-3-Clause), for the bearer-token
  middleware, the transports' concurrency helpers, system calls and its rate
  limiter. They sit in the request path with the module that brings them, and a
  bump to any of them is read the same way.

Beside those, code that ships imports only protobuf; `go list -deps
./adapters/mcp` names the set above and nothing more. The test-only entry below
is the only other module this repository imports itself. There is no database
driver and no logging library in the tree. The dependency rule of
[ADR-0007](adr/0007-repository-layout-and-dependency-rule.md) admits no module
but protobuf, and no standard library package it does not name, into the part
that decides; a new one there means an edit to that rule. The MCP module is
imported outside that part, by the adapter and by the gateway's command.

## Test-only

Imported by `_test.go` files. `go build ./...` does not link them, so none of
them reaches a released artifact.

### `pgregory.net/rapid` v1.3.0

- Problem it solves: property tests for the canonical JSON serializer and the
  action digest. A canonicalizer is a total function over a very large input
  space, and the inputs that break it are the ones nobody thought to write
  down. `rapid` generates them, and when one fails it shrinks the failure to
  the smallest input that still fails, which is what makes a property failure
  something a person can act on.
- Why the standard library will not do: `testing/quick` generates values but
  does not shrink, so a failure arrives as a random deep tree rather than the
  two-key object that actually breaks the ordering rule. Go's native fuzzing is
  used here too and covers byte inputs; it does not build typed trees of the
  shape the canonicalizer takes.
- Licence: **MPL-2.0**. File-level copyleft: modifying one of its source files
  would keep that file under MPL, while using the library unmodified does not
  affect this project's Apache-2.0 licensing. It is never linked into a
  released artifact.
- Maintenance signal: v1.3.0, published 2026-03-30, read from pkg.go.dev in the
  session that added this entry. Nothing else about the project's activity was
  checked, so this line says less than the entries above it.
- Request path: no.

## Developer tooling

Pinned as `tool` directives in `go.mod`, so they arrive with `go mod download`
at the version the module records. None of them is linked into a shipped
binary and none is in the request path.

| Tool | Module | Used by | Licence |
| --- | --- | --- | --- |
| `protoc-gen-go` | `google.golang.org/protobuf` | `make proto`, `make proto-check` | BSD-3-Clause |
| `goimports` | `golang.org/x/tools` | `make fmt`, `make fmt-check` | BSD-3-Clause |
| `govulncheck` | `golang.org/x/vuln` | `make security` | BSD-3-Clause |

Every other module in `go.mod` is marked `// indirect` and arrives through
those three or through the MCP module: `golang.org/x/mod`, `golang.org/x/sync`,
`golang.org/x/sys`, `golang.org/x/telemetry`, `golang.org/x/tools` and
`golang.org/x/vuln` through the tools; `github.com/google/jsonschema-go`,
`github.com/segmentio/asm`, `github.com/segmentio/encoding`,
`github.com/yosida95/uritemplate/v3`, `golang.org/x/oauth2` and
`golang.org/x/time` through the MCP module, and those six are linked into the
gateway with it. No code in this repository imports them.

## Tools that are not Go modules

`golangci-lint`, `buf`, `actionlint`, `zizmor`, `gitleaks`, `osv-scanner`,
`shellcheck`, `syft`, `goreleaser` and `cosign` are pinned in
`scripts/tool-versions.env` and checked by `make bootstrap`, which fails on a
version mismatch rather than continuing. The Go compiler is pinned separately,
by the `go` directive in `go.mod`, see
[ADR-0001](adr/0001-language-and-toolchain.md).

All but `goreleaser`, `syft` and `cosign` run targets of the gate. Those three
build a release, in `.github/workflows/release.yml` and in
`make release-snapshot`, and CI checks each download against the digest pinned
beside its version:

- `goreleaser` (MIT) builds the two binaries for Linux and macOS, packs one
  archive per platform, writes `checksums.txt` and drafts the GitHub release,
  from `.goreleaser.yaml`. Doing that by hand in the workflow would be a long
  script that nothing else tests.
- `syft` (Apache-2.0) writes a CycloneDX bill of materials for each archive,
  from the module information Go records in each binary.
- `cosign` (Apache-2.0) signs `checksums.txt` without a key, with a
  certificate for the release workflow's identity, and verifies that signature
  before the release is published.

None of these is a dependency of the product, and nothing in the tree imports
any of them.

## GitHub Actions used by the workflows

An action runs inside the workflows that decide whether a change may merge, so
it is a dependency of the release and review path even though it never ships.
Each is pinned to a commit SHA with the version in a trailing comment;
`make check-actions` fails on a reference that is not, and `zizmor` and
`actionlint` read the same files. None of them is in the request path.

| Action | Why it is here | Licence |
| --- | --- | --- |
| `actions/checkout` | Fetches the tree every job works on | MIT |
| `actions/setup-go` | Installs the compiler named by `go-version-file: go.mod` | MIT |
| `bufbuild/buf-action` | Installs the pinned `buf`, with `setup_only`; `make proto-check` and the breaking-change step drive it. It replaced the archived `bufbuild/buf-setup-action` | Apache-2.0 |
| `golangci/golangci-lint-action` | Installs the pinned linter, with `install-only`; `make lint` runs it | MIT |
| `github/codeql-action` | CodeQL static analysis, and the SARIF upload that publishes the supply-chain score to code scanning | MIT |
| `actions/dependency-review-action` | Blocks a pull request that adds a vulnerable dependency | MIT |
| `ossf/scorecard-action` | Scores the repository's own supply-chain settings | Apache-2.0 |
| `actions/upload-artifact` | Keeps the raw supply-chain result that code scanning would trim, and the dry run's release build | MIT |
| `actions/attest-build-provenance` | Records signed build provenance for every release asset before the release is published | MIT |

Licences were read from each action's repository metadata, not from a scanner.
