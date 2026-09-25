# ADR-0015: The documentation is a checked structure

Status: accepted
Date: 2026-09-20

Builds on [ADR-0007](0007-repository-layout-and-dependency-rule.md) and
[ADR-0008](0008-branching-and-release-policy.md).

## Context

The pages under `docs/` were written one at a time as the code landed, each
in its own shape, with no rule that tied a page to the code it described. A
page could name a capability the tree did not hold, keep a number no test
produced, or fall out of step with a package that moved, and every gate
stayed green. The project law says a user-visible change carries its
documentation in the same change, and `docs/status.md` is the one inventory;
nothing enforced either beyond review. Two references were already rendered
from the code and pinned by tests, which showed the shape that works: a
renderer the gate compiles, called directly by its pin, never through a
subprocess a shim could stand in for.

## Decision

**Pages are typed and live where their type says.** Every page under `docs/`
but the records carries a frontmatter block with a title that equals its one
H1, a summary, a type from a fixed set (tutorial, how-to, explanation,
reference, spec, extending, project), the globs of the code whose change
makes the page suspect (`covers`), and, for spec and extending pages only, a
stability level. The type is the type `docs/docs.json` lists for the page's
own directory, or that file's override for one page; a page in a directory
the file does not list is refused rather than inheriting a parent's type.
The block is a strict subset of YAML with one spelling, read and written by
`internal/docscheck/frontmatter`, so a generator writes exactly what the
check reads and a site generator reads the block as YAML.

**Budgets are data, and a page over its budget is pinned to its count.**
`docs/docs.json` holds the words each type may hold, the two README budgets,
the directories and their types, the paths that are frozen, the files
outside the documentation, the code surfaces that need a page, and for each
page over budget its exact word count as a ceiling: a count that may only
fall, and whose rise is a visible edit to the file. Words are white-space
tokens holding a letter or a digit, outside fenced and generated blocks.

**Diagrams are Mermaid, bounded, and name their sources.** A block is a
flowchart, a sequence diagram or a state diagram of at most fifteen nodes,
counted by a counter that fails on a line it cannot read rather than count
fewer, and the first line after its fence is `Sources:` naming files that
exist and fall inside the page's `covers`.

**What can be rendered from the code is.** The reason codes, the
obligations, the gateway's configuration, the wire messages and the index
are whole pages rendered by `scripts/gen-*.go` through renderers the gate
compiles, lints and tests, each pinned by a test that calls the renderer
directly and diffs the committed page; the page names its generator and the
`docs-gen` recipe has to run that generator onto that page. The
enforcement-mode table, the kernel's fail-closed table and the evidence
chain's state diagram are blocks inside hand-written pages, rendered from
listings the code exports and derived from its own functions (`Requirements`,
`FailClosedRows`, `ChainSteps`), never from a second table; a block is
claimed in a registry the check reads, and a marker nothing claims fails.
The command-line page is pinned the same way from inside each command, which
diffs its own help text against the page.

**A change names the pages it makes suspect.** `scripts/docs-impact.go`
reads the changed paths of a range, a file or standard input and reports how
many it read, the pages whose `covers` match, the changed documentable
surfaces no page covers, the `covers` globs matching no file, and the changed
paths under a frozen path, which are a contract change; a pull request pastes
its output.
Whatever git could not measure is NOT MEASURED and a failure, never "no
pages": an absent git, a shallow clone, or git answering for another
repository, which it does for an export placed under one, so the tool holds
git's top level to its working directory by real path.

**The check is part of `make quality`.** `internal/docscheck` judges every
page on every run: the block, the type, the globs, the budgets, the
directories that may not be empty, the diagrams, the generated pages and
blocks, the links and the status labels it already judged. It walks the
directory and never asks git, so an export without `.git` is judged the same
as a work tree; a walk that finds fewer pages than exist is fatal.

## Security / compatibility impact

None on the wire, the digest or the decision path. The two listings the
kernel and the evidence package export add no import and no effect, and the
layering test judges them as before. The gateway's configuration reader and
loader moved to `internal/gatewayconfig` unchanged so the configuration
page could be rendered; every refusal they make is the one the command made.
The documentation check reads the tree and nothing else.

## Alternatives considered

- **A YAML library for the frontmatter.** A dependency in the gate for seven
  keys, and a reader that accepts what a strict block refuses.
- **A manifest file instead of frontmatter.** Cleaner pages on GitHub and a
  second file to keep in step with every move.
- **A hand-kept table beside each switch for the rendered blocks.** It would
  drift with every gate green; a listing that calls the function cannot.
- **A hidden `docs` subcommand in the gateway binary for its configuration
  page.** An undocumented capability in a production binary; the move of the
  reader to a package costs nothing at run time.
- **Prose lint in the gate.** Deferred: a house style for a linter is written
  after the pages exist in their shape, and a second model family reads the
  public prose until then.
- **The site now.** It waits for the first public release; every page stays
  readable on GitHub until then.

## Consequences

A page cannot be added without a type, a place, a budget and the code it
covers; a diagram cannot be drawn without saying where it comes from; a
rendered page cannot drift from its source; and a change to a covered path
names the pages to reread. The cost is a stricter edit: a page over budget is
split or pinned, a page in a new directory needs the directory listed first,
and a generated page is regenerated, never edited. The tutorial and the
threat model wait for the parts of the system they describe.

## Validation

`internal/docscheck` and its packages: one fixture per rule, among them the
sixteenth node per diagram type, a ceiling one word off either way, a
`Sources:` path outside `covers`, a generator the recipe never runs, an
unclaimed block; the frontmatter grammar's every refusal, a round-trip
property and a fuzz target that found and now refuses a second spelling; the
configuration's strict reader with a fuzz target; the pins going red on a
changed default, a field added to a descriptor, a removed table row and a
block the code no longer produces; `docs-impact` proved NOT MEASURED on an
export entered through a symlink under another repository. Not verified: the
checks against a tutorial or a spec page, which do not exist yet.
