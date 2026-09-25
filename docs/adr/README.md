# Architecture decision records

A record states one decision already made and either the check that enforces it
or the session that brings one. Required for a change to authorization
semantics, a public contract, the layout rule, or release and licensing.

To add one: copy `0000-template.md` to the next free number, open the pull
request with `Status: proposed`, and set it to `accepted` when it merges.

| ADR | Decision |
| --- | --- |
| [0001](0001-language-and-toolchain.md) | Go, one compiler version pinned by the `go` directive |
| [0002](0002-wire-contracts-and-versioning.md) | Protobuf is the source of truth; generated Go is committed |
| [0003](0003-policy-model-and-external-pdp.md) | Structured predicates, deny-overrides, external PDP over AuthZEN |
| [0004](0004-evidence-and-privacy-defaults.md) | Metadata by default; content capture is opt-in |
| [0005](0005-canonical-action-digest.md) | One canonical digest identifies an action |
| [0006](0006-product-name-switch.md) | The name is a working name and can be switched |
| [0007](0007-repository-layout-and-dependency-rule.md) | The decision path imports no adapter, storage or server |
| [0008](0008-branching-and-release-policy.md) | Protected `main`, DCO, semver, releases built by CI |
| [0009](0009-open-core-boundary.md) | Everything here is Apache-2.0 and stays open |
| [0010](0010-digest-domain-separation.md) | The digest domain tag is fixed and carries no product name |
| [0011](0011-contract-corrections-before-publication.md) | The v1 contract is corrected once, before it is first published |
| [0012](0012-policy-kernel-semantics.md) | How the built-in policy kernel decides |
| [0013](0013-mcp-interception-approvals-and-modes.md) | The MCP gateway intercepts a call before it happens |
| [0014](0014-evidence-spool-and-sinks.md) | Evidence goes to a local spool first, and the spool decides what blocks |
| [0015](0015-documentation-structure-and-checks.md) | The documentation is a checked structure |
| [0016](0016-approval-providers-and-the-lost-hold.md) | An approver outside the plane answers, and a lost hold is closed |
| [0017](0017-an-external-decision-point-can-veto.md) | An external decision point can veto what the signed policy allows |
| [0018](0018-keys-and-bundles-on-disk.md) | A policy key and a signed bundle each have one format on disk |
| [0019](0019-an-operator-can-pause-calls.md) | An operator can pause calls, and a pause the plane cannot read blocks |
| [0020](0020-a-trail-and-counters-without-a-collector.md) | A plane's trail can land in a local file, and its counters in /metrics |
| [0021](0021-a-run-carries-what-it-took-in.md) | The plane keeps what each run took in, so a flow rule can fire |
| [0022](0022-scenarios-are-data.md) | A scenario is data a live plane is held to |
| [0023](0023-a-local-page-answers-through-the-directory.md) | A local page answers through the directory |
| [0024](0024-control-and-guardana-are-independent.md) | Guardana Control and Guardana are independent |
| [0025](0025-public-repository-merges-and-releases.md) | The admin pushes, maintainers merge and release, a release tag never moves |
