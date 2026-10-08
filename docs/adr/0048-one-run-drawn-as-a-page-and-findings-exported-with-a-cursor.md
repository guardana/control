# ADR-0048: One run drawn as a page, and findings exported with a cursor

Status: accepted
Date: 2026-10-07

Builds on [ADR-0035](0035-a-versioned-evidence-export-and-a-bounded-query.md),
[ADR-0039](0039-many-channels-into-one-core.md),
[ADR-0041](0041-coverage-per-declared-path.md),
[ADR-0045](0045-one-run-is-supervised-against-its-procedure.md) and
[ADR-0047](0047-a-procedure-states-its-exceptions-resources-and-children.md).

## Context

Milestone 2 ends with a user who sees one run: what was meant, what happened,
where a boundary was crossed, what was stopped and what was not seen
(backlog B22). `guardana-control supervise` prints a line per step and per
finding; nothing draws the run, and status.md says "a run drawn as a graph:
none". The roadmap's sentence also asks for each step "marked enforced,
observed or inferred", and ADR-0041 says "inferred" has no source and is not
printed until one exists.

Findings stay in a log that `notify` reads with a set of delivered keys. A
program of the operator's own, or a view kept elsewhere, has no way to resume
reading where it stopped.

## Decision

**A page per run, written once.** `supervise --view <file>` writes one HTML
page from the evaluation the command has just made, beside its findings. No
listener serves it, it fetches nothing, and it carries no script: a
`Content-Security-Policy` meta tag sets `default-src 'none'` and allows inline
style only. It is rewritten by running the command again; nothing on it reads
a clock. The file is created with mode `0600`.

**What it shows.** Expected steps in the procedure's order beside the observed
calls of the run, or of its tree under `inherit`, each call with its run, its
time and whether that time can be ordered, and its export. Each mark is a shape
and a line style as well as a colour, with a legend and a text equivalent:

- enforced: a plane decided and enforced the call;
- decided, not enforced: a plane in `OBSERVE` decided it;
- observed: a source reported it, with that source's trust;
- exception or approval: an exception was taken, or a person approved;
- blocked: denied by policy, blocked by the plane, or stopped;
- not seen: an expected step with no call, or a source silent or absent.

By default the page shows the neighbourhood of each finding (the call it
rests on and the calls just before and after it); `--view-all` shows the whole
run up to a fixed number of calls and says when it stopped short. No mark
moves or animates.

**No "inferred".** No deterministic source of an inferred step exists. The
roadmap's sentence reads "each marked enforced, observed with its source's
trust, or not seen", and "inferred" waits for a source that can be tested.

**Where the code lives.** `internal/supervise` returns, beside its findings, the
calls it judged as plain data: request, step or none, run, time and whether it
is told, export, an outcome whose zero value is unknown (open, completed,
failed, denied, blocked by the plane), the approval in three states, and the
resource. The page is drawn by `internal/runview` with `html/template`,
outside the guarded trees, so the supervisor stays free of output formats and
the view never decides a mark the supervisor did not. A string an agent chose
(a tool name, a resource id, a reason) is text content only, never an
attribute, a link or a style, and is cut at a fixed length with the cut shown.

**Findings exported with a cursor.** `guardana-control findings export
--findings <dir> [--after <cursor>] [--limit n]` writes ADR-0035's shape under
its own format, `guardana.control.findings-export` version `"0.1"`, unstable
as the records are. Its lines are `header`, `finding_record`,
`supervise_report`, `gap`, `duplicate` and `trailer`. It reads up to the last
write the log committed, never a line of a write still open. A log created
from this release starts with a header record holding a random `log_id`,
which its cursors bind to; a log made before it keeps an identity by its first
line's content, and two copies of one such log are the same log to a cursor.
`notify` keeps its own set of delivered keys, which does not depend on
position.

## Security / compatibility impact

Nothing here decides or stops a call; the page and the export read what
supervise wrote. The page holds tool names, resource ids and reasons the agent
chose, so it is built for hostile text: template escaping, no attribute or URL
built from it, no script allowed by its own policy, a bound on every string.
It may hold more than the operator meant to share, as the findings log does;
mode `0600` keeps it the operator's. The findings log gains a header record;
a 0.9 reader refuses it by name. The export's format is unstable and says so.

## Alternatives considered

- **A page in `console`.** Live, but gives a listener that also approves holds
  the procedure, the exports and the observation logs, and a second place a
  mark is decided.
- **A published JSON report with a separate viewer.** A new contract with one
  consumer; the findings export is the machine path.
- **A text view only.** Cannot show the neighbourhood of a deviation beside the
  expected steps in a form a reviewer can share.
- **Keep "inferred" with a heuristic.** A guess drawn as a fact.
- **The cursor on the first line alone for new logs.** Deterministic records
  let a copy and its original share a first line.

## Consequences

An operator runs `supervise` with `--view` and opens the file. Its contents
are as complete as the inputs, and the page says what it did not see. A
program reads findings in order and resumes after a restart. Renaming
"inferred" out of the roadmap sentence narrows a public promise until a
source exists.

## Validation

- Golden pages for the refund example, a tree under `inherit`, an exception
  taken, a resource outside the run and a retried denial; a change to a mark
  changes a golden.
- A fuzz test feeds hostile tool names, resource ids and reasons and finds no
  markup, attribute or URL from them in the page; a test finds the CSP tag and
  no `<script`.
- Every mark differs from the others without colour (shape or line style),
  and the legend's colours meet a contrast check against the background.
- `--view-all` on a run past the bound says it stopped short.
- An export after a cursor returns the next record; a cursor from another log,
  a copy with another `log_id`, or off a line boundary is refused; a write cut
  short is not exported; an old log without a header exports and says its
  identity is by content.
