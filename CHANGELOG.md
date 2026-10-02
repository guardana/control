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

- `runs list` and `runs close` no longer make an empty directory a runs
  directory; they refuse it, and only `runs open` makes one. A temporary file
  left by a crash while `runs open` made the directory no longer makes every
  later command and plane refuse it.

## [0.5.0-alpha] - 2026-10-02

An author can now see why a call got its decision: `policy explain` names the
rules that matched and the fields a rule could not read. Two starter policy
packs, read-only and approval-for-writes, come with cases that show what they
decide. A guide puts the plane between an MCP client and a server already in
use. An audit before the release fixed defects, among them: a call the agent
cancelled could lose its closing record, and enough of them blocked every later
call; other accounts could write the evidence directory; a configuration
refusal printed a header credential; and some MCP clients refused the plane's
answers as schema errors. [docs/status.md](docs/status.md) says what is
`implemented` and what is `experimental`. Nothing here is a security boundary
yet.

### Added

- `guardana-control policy explain <case>` decides one case as `policy test`
  does and says how: the decision's verdict, action, mode, policy digest and
  codes, what the kernel found before the policy, and a line per rule, matched,
  undetermined with the field it could not read and the input that field
  lacked, or not matched on the fields that failed. It reads the kernel's own
  evaluation, so it never describes a decision the kernel would not make for
  the same case
  ([ADR-0036](docs/adr/0036-policy-explain-reads-the-kernels-own-evaluation.md),
  [reference/policy-explain.md](docs/reference/policy-explain.md)).
- Two starter policy packs under `examples/starter-packs/`, read-only and
  approval-for-writes, each a document with allow, deny and unknown cases, and
  a page that says what each assumes about the plane and leaves unguarded
  ([reference/starter-packs.md](docs/reference/starter-packs.md)).
- A guide that puts the plane between an MCP client and a server already in
  use, and a profile for the MCP project's reference filesystem server that
  classifies its 14 tools and holds every write for approval
  ([guides/connect-an-existing-mcp-setup.md](docs/guides/connect-an-existing-mcp-setup.md)).

### Fixed

- A `tools/call` the plane blocked or held for a tool that declares an output
  schema reached the agent as a schema error from any client that checks
  structured content, the MCP project's own TypeScript client among them,
  instead of `APPROVAL_PENDING` or the block's codes. The plane's own answers
  now carry their fields in the result's `_meta` under its namespace and no
  structured content.
- A configuration value the reader refused (a flow character, an unknown
  escape, an unclosed quote) was printed in `doctor`'s and `run`'s refusal,
  credentials included: an `export.headers` or `pdp.headers` value, the
  `pdp.proxy`, an endpoint with userinfo. Those are now named without the
  value.
- The evidence spool took a directory other accounts could write, or another
  account owned, and appended through a link put at a segment's name. It now
  refuses both, as the approvals, runs and pause directories do, and opens
  every segment without following a link.
- The configuration reader kept a tag (`!!str x`) or a single-quoted value
  (`'acme'`) as text, marks included, so a tenant id written `'acme'` matched
  no run and no rule. Both are now refused; quote a value with double quotes.
- A call the agent cancelled after the plane handed it out lost its closing
  record: the trail stopped at `ACTION_STARTED`, material calls halted, and the
  spool kept the call's reservation, so enough cancelled calls blocked every
  call until a restart. The closing record is now written whatever the agent
  does.
- A stateful HTTP session was never ended, so sessions a client opened and
  abandoned held the plane's memory until it stopped. A session idle for 30
  minutes now ends.
- A `shorten_timeout` obligation whose `ms` a duration cannot hold wrapped
  when converted, and the call ran with no bound or with one nobody wrote. It
  is now refused like a non-positive `ms`.

## [0.4.0-alpha] - 2026-10-01

An operator can now open runs for an agent, and a plane with `runs.dir`
accepts a call only under a run token that resolves: two runs of one agent
keep their flow state apart, and a run's flow state survives a restart. A
plane without `runs.dir` behaves as before. A stdio plane no longer writes its
start lines into the protocol stream.
[docs/status.md](docs/status.md) says what is `implemented` and what is
`experimental`. Nothing here is a security boundary yet.

### Added

- Runs an operator opens
  ([ADR-0034](docs/adr/0034-a-run-the-operator-opens-has-an-identity-of-its-own.md)):
  `guardana-control runs open`, `close` and `list` over a runs directory, and
  `runs.dir` on the plane, under which every call needs a run token, in the
  `Run-Token` header on HTTP or from `run --run-token-file` on stdio. Two
  runs of one agent keep their flow state apart, a run opened under a parent
  shares the flow state of the parent's top-level run, the state survives a
  restart, and a held call resumes only from its own run. A refused token is
  answered with HTTP 401 or the JSON-RPC error `-31102`, and the refusals are
  counted by cause. A plane without `runs.dir` is unchanged.

### Changed

- The release job waits for a maintainer to approve it in the `release`
  environment before it builds; [RELEASING.md](RELEASING.md) has the step.

### Fixed

- A plane with a stdio listener wrote its start lines, the mode and the count
  of held calls lost to a restart among them, to standard output, which is the agent's protocol stream,
  so a client's first read failed; they now go to standard error.

## [0.3.0-alpha] - 2026-10-01

A configuration must now name its enforcement mode: one that relied on the
`OBSERVE` default no longer loads. The demo now starts with
`dev --decision-point=silent` and needs a system with process groups. A
resource prefix now refuses the dot segments, percent escapes, queries and
fragments that let an id leave it; evidence no longer carries previews; and a
result that cannot be hashed is recorded as `UNKNOWN`, not as a success. New
are a demo archive that runs without Go, an evidence export another program
resumes by cursor, and the documentation on the website.
[docs/status.md](docs/status.md) says what is `implemented` and what is
`experimental`. Nothing here is a security boundary yet.

### Changed

- `mode` has no default: a configuration that names no enforcement mode is
  refused at load instead of starting a plane in `OBSERVE`, which stops nothing
  its policy decides. Add `mode:` to a configuration that relied on the
  default ([ADR-0032](docs/adr/0032-the-enforcement-mode-has-no-default.md)).
- The demo's decision point no longer listens on the fixed port
  `127.0.0.1:18213`, so demos run side by side: start the demo with
  `dev --decision-point=silent`, which the old command lacks and now needs.
  `dev` refuses a system without process groups before it starts anything.

### Added

- A website for `control.guardana.dev` in `site/`: one static page with
  no script and nothing loaded from another host, whose diagrams
  `make docs-gen` draws from README's, and the project's mark in the
  README header ([ADR-0030](docs/adr/0030-a-static-website-drawn-from-the-repository.md)).
- The documentation on the website: every page, record, the roadmap and
  the changelog rendered under `control.guardana.dev/docs/` by
  `make docs-gen`, with links, anchors and diagrams that mean there what
  they mean on GitHub, and a check that fails when a page and its rendering
  drift ([ADR-0031](docs/adr/0031-the-documentation-is-served-on-the-website.md)).
- A demo archive per platform, `guardana-control-demo_<version>_<os>_<arch>.tar.gz`,
  that runs the tutorial and every scenario without Go or a checkout; each
  release runs its scenarios from the linux/amd64 archive before it is
  published
  ([ADR-0033](docs/adr/0033-a-demo-archive-a-release-user-runs-without-go.md)).
- `dev --decision-point=silent`: a decision point on a port `dev` binds and
  never answers, so a rule reading `external` is undetermined with
  `PDP_TIMEOUT`.
- `guardana-gateway trail export`: a trail file in a versioned format another
  program reads, with a cursor to resume from, a record for every line it
  cannot read, and a trailer; `examples/evidence-report` rebuilds each call's
  lifecycle from it without importing the project's internals
  ([ADR-0035](docs/adr/0035-a-versioned-evidence-export-and-a-bounded-query.md)).

### Fixed

- A `restrict_resources` obligation with a `prefix` let a resource id such
  as `/srv/data/../../etc/passwd`, `/srv/data/%2e%2e/etc` or
  `https://api.example/pub/..?x` through; under a prefix, an id holding a
  segment of dots, an empty segment, a backslash, a `%`, a `;`, a `?`, a `#`
  or a byte outside printable ASCII is now refused.
- The plane recorded whatever preview an adapter put on a call's arguments or
  its result; every event now reaches the sink without previews, as capture
  is off.
- A tool result that could not be encoded was recorded as a success with no
  hash; it is now `UNKNOWN`, with the protocol status `unhashable`.
- An approver's answer succeeded beside a consumed or not-resumed record it
  could not read; the store now refuses it and takes the answer back.
- An approver whose answer the plane spent between the approver's write and
  its check was told the approval had been consumed; it is now told the
  answer was filed, and any other consumed record still refuses it.
- The collector kept the argument and result previews another producer sent;
  it now clears them before the trail file holds an event. A trail file that
  already holds such an event reads a copy sent again as other content.
- A pending call told the agent to retry after 0 seconds when
  `approvals.retry_after` was under a second; the hint now rounds up to whole
  seconds.
- A scenario stopped as one that could not run when the plane's read of its
  pause file ran late on a busy machine and `/healthz` briefly reported the
  pause state stale; the runner now reads it again until its timeout, and any
  other unknown cause still stops it.

## [0.2.0-alpha] - 2026-09-28

The second release. It fixes how an approval's expiry is kept, how much
of the plane a stdio upstream is handed, where a plaintext export may go,
and how numbers and uncertain results are recorded. It adds an image of
the gateway beside the archives, and pages that say what the plane
protects, what it records and which failures it survives.
[docs/status.md](docs/status.md) says what is `implemented` and what is
`experimental`. Nothing here is a security boundary yet.

### Added

- Reason code `APPROVAL_STATE_UNKNOWN` (44, `INDETERMINATE`): a lost hold
  whose approval the plane could not read or trust closes with it, where
  it closed as `APPROVAL_EXPIRED` before
  ([ADR-0027](docs/adr/0027-expiry-at-hand-out-and-an-unreadable-lost-hold.md)).
- A benchmark of the gateway round trip, and `scripts/bench.sh --publish`,
  which records a run from a clean tree, with its hardware and Go
  settings, in a tracked file under `bench/results/`
  ([docs/reference/benchmarks.md](docs/reference/benchmarks.md)).
- Each release is to publish `ghcr.io/guardana/control-gateway:<version>`
  for linux/amd64 and linux/arm64, signed keyless and with build
  provenance, its digest named in the release notes;
  [RELEASING.md](RELEASING.md) says how to check it and
  [docs/guides/run-in-a-container.md](docs/guides/run-in-a-container.md)
  how to run it.
- A page of the failures the plane is built to survive: the collector, the
  spool, the decision point, an upstream dying mid-call, the pause file
  and a restart with calls held, each with a test that runs the built
  binary against a real failure
  ([docs/reference/failure-modes.md](docs/reference/failure-modes.md)).
- A threat model and a privacy page: what the plane protects, from whom
  and where it stops; what a record holds, where it goes and how long it
  stays ([docs/concepts/threat-model.md](docs/concepts/threat-model.md),
  [docs/concepts/privacy.md](docs/concepts/privacy.md)).

### Changed

- A stdio upstream no longer inherits the plane's environment. It gets
  `PATH`, `HOME`, `LANG`, `LC_ALL`, `TMPDIR` and `USER`, each where the
  plane has it, and the variables its new `upstreams.N.env` list names;
  a name under `GUARDANA_CONTROL_` is refused, so the exporter's and the
  decision point's header credentials are no longer handed to it. An
  upstream that read any other inherited variable starts without it:
  list each one it needs, and `doctor` marks a listed name the plane's
  environment lacks
  ([ADR-0028](docs/adr/0028-a-stdio-upstream-gets-only-the-environment-it-is-given.md)).
- `export.allow_plaintext` admits a plaintext collector on a loopback IP
  literal only, as `pdp.allow_plaintext` already did and as
  [ADR-0020](docs/adr/0020-a-trail-and-counters-without-a-collector.md)
  states. A plaintext endpoint on any other host, a name such as `localhost`
  included, is refused at start.

### Fixed

- A tool whose definition holds a fractional number, such as
  `"default": 0.7`, is fingerprinted and can be classified, and a call
  whose arguments hold a fraction a double represents exactly is decided.
  The digest binds a number's value, so `1.0` and `1` digest alike, and
  every digest of arguments accepted before is unchanged
  ([ADR-0029](docs/adr/0029-exact-fractions-in-the-canonical-form.md)).
- A resumed call whose approval expired by the plane's clock before it
  was handed to the upstream is not sent: it is blocked with
  `APPROVAL_EXPIRED`, and the approval stays spent.
- A call whose answer the gateway could not read, or could not complete
  over HTTP (a malformed answer, a connection the upstream closed after
  reading the call, a 429 or 5xx status), is recorded with an `UNKNOWN`
  result rather than as the upstream's failure: whether it took effect
  is unknown.
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

[Unreleased]: https://github.com/guardana/control/compare/v0.5.0-alpha...HEAD
[0.5.0-alpha]: https://github.com/guardana/control/releases/tag/v0.5.0-alpha
[0.4.0-alpha]: https://github.com/guardana/control/releases/tag/v0.4.0-alpha
[0.3.0-alpha]: https://github.com/guardana/control/releases/tag/v0.3.0-alpha
[0.2.0-alpha]: https://github.com/guardana/control/releases/tag/v0.2.0-alpha
[0.1.0-alpha]: https://github.com/guardana/control/releases/tag/v0.1.0-alpha
