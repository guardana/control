# ADR-0022: A scenario is data a live plane is held to

Status: accepted
Date: 2026-09-24

Builds on [ADR-0013](0013-mcp-interception-approvals-and-modes.md),
[ADR-0016](0016-approval-providers-and-the-lost-hold.md),
[ADR-0019](0019-an-operator-can-pause-calls.md),
[ADR-0020](0020-a-trail-and-counters-without-a-collector.md) and
[ADR-0021](0021-a-run-carries-what-it-took-in.md). Amends ADR-0013 on what a
key under the gateway's `_meta` namespace means.

Amended on the review of its runner: a step also differs on events outside the
trail its answer names, on an approval id its trail did not request and on
arguments its trail's first proposal does not carry; pause entries are removed
after an interrupt and after a failed add; operator values refuse key text. The
paragraphs below say it.

## Context

`policy test` holds one decision of the kernel to what a case expects. Nothing
holds a live plane to a sequence of calls: that the agent got the answer it
should, that the plane decided what it should, that each trail holds the
events it should, and that an operator's answer or pause between calls did
what it should. The demo needs exactly that, as data a stranger can read, and a
lab that tests a pinned image of this plane needs the same statements without
importing this module.

Four facts of the plane shape a runner. The exporter keeps several requests in
flight, so a trail's later events can reach the trail file before its earlier
ones, and a trail held for an approval stays open until a retry. Pause
is read by polling, so a call made right after a pause can be decided under
the state before it. One plane process is one run (ADR-0021), so what one
scenario leaves, taint, a hold or a pause, is what the next one starts from.
And the answer an agent gets names no trail today: a pending answer carries
neither the request nor the decision, and an executed one carries nothing of
the gateway's.

A lab repository already writes scenarios as YAML: a trajectory of calls and,
apart from it, what each step's decision and each victim's journal must show.
Its identity lives in the file, where this plane takes identity from its
listener; its calls name a server, where this plane routes by tool name; it has
no place for an operator's action; and it expects an obligation this plane's
catalogue does not have.

## Decision

**One format, owned here, strict JSON.** A scenario is one JSON document whose
`kind` is `agent-scenario/v1alpha1`. The reader, `internal/scenario`, checks
`kind` before any rule about another member, whatever the member order, and
refuses a kind it does not know by name; only the file's name, the size bound
and the JSON syntax are judged before it. Any change to the format is a new
kind. It refuses a
duplicate member, an unknown one, `null` anywhere, bytes that are not UTF-8, an
escaped lone surrogate, anything after the document, and a document over its
bound. The scenario's id is its file name, which must match
`[a-z0-9][a-z0-9-]{0,63}\.json`. Every code must be in the reason registry,
every obligation type in the catalogue and every event kind named, when the
file loads, so an expectation this plane cannot meet fails before anything
runs. A scenario in which no call step expects an event is refused: it would
examine nothing.

**What a plane must be, checked and never sent.** `plane` names the mode and
the bundle id, and may name the bundle digest. Before the first step the runner
reads `/healthz`, refuses anything but a 200, and refuses a plane whose mode,
bundle, pause state or halt is not what the scenario starts from: nothing
paused and no halt. Every `POLICY_DECIDED` it later reads must carry that
bundle digest and every event that mode, or the step differs on
`plane.bundle.digest` or `plane.mode`. Identity is the listener's
(ADR-0013), so a scenario states none; the runner prints the principal and
agent the first `ACTION_PROPOSED` names, for a lab to map its own identity to.

**A scenario starts on a fresh run unless it says otherwise.** The first call's
`ACTION_PROPOSED` must carry `flow.v1.untrusted=false` and
`flow.v1.max_read=PUBLIC`, unless the scenario sets `"run": "continues"`, or
the step differs on `run`. The runner notes which request ids the trail file
already holds before the first step, and a step whose answer names one of them
differs on `trail.request`. It removes every pause entry it added, pass,
fail or interrupt: an interrupt that cancels the run does not stop the removal,
and each run of `guardana-control` is still bounded by its own timeout. The
runner ends the reason of each entry it adds with a marker drawn for that add,
and refuses a reason that leaves no room for it. A `pause add` that fails has
removed only the one entry new since before the add that carries the step's
scope and that marker; when no new entry or more than one carries both, it
removes nothing and names every new entry, since any of them may be an
operator's, an emergency stop among them. In this build a fresh run is a fresh plane.

**Steps are calls and operator actions.** Each step is exactly one of `call`
(a tool and its arguments), `approve` or `reject` (the pending approval an
earlier call step was answered with, and an approver id), `pause` (an entry of
ADR-0019's format) and `unpause` (the entry an earlier pause step added).
Arguments are kept as the JSON they were written as, numbers included. A
reference `${step[n].output}` may stand only inside a string value, never in a
member name. `n` counts from zero and names an earlier call step whose answer
was a result, not an error, with exactly one text item of at most 64 KiB; any
other source refuses the step, and nothing is cut. Substitution is one pass,
left to right, over the scenario's own string: the text put in is never read
again, and only that string is encoded again, as canon encodes a string. A
`${` that is not a well-formed reference is refused at load, so a literal `${`
cannot be sent. A value an operator step puts on `guardana-control`'s command
line refuses key text at load.

**Every call states three things, all required.** `answer`: its `kind`,
`result`, `error`, `blocked` or `pending`, told apart by the gateway's
`<ns>/answer` marker alone, and its `codes`: the reason codes of a block, the
code of a pending answer, none otherwise. `decided`: the verdict, the reason
codes and the obligations (`type`, `params` and `advisory` each) of the
`POLICY_DECIDED` on the trail the answer names, or `"none"` where that trail
has none. `trail`: which request it is, `"new"` or `"step[n]"` for the held
request an earlier pending step started, and the kinds of every event that
request's trail then holds, in the order of their links, which may be none.
The hash of the arguments sent is held to the first `ACTION_PROPOSED` of the
trail the answer names, which for `step[n]` is the held call's, and differs on
`args`. Every list compares exactly and in order: the kernel's
codes follow its steps, its rules follow the document and the plane's causes
follow a fixed table, so each order is the plane's and not the runner's.

**The answer names its trail.** The adapter puts `<ns>/request_id` and
`<ns>/decision_id`, taken from the decision the pipeline returned, on every
`tools/call` answer: in the result's `_meta` for a block, a pending state and
an execution, and in the error's data where an upstream's error carries an
object there or nothing. They are added after the result is hashed, so the
recorded hash stays the upstream's. A resumed retry names the held request's
trail. The keys are stripped from everything an upstream sends, as every key
under the namespace already is, and the runner reads them only from those two
places. `<ns>/answer` alone marks an answer the gateway made; the two ids ride
on every answer (this amends ADR-0013). An answer that names no trail fails its
step.

**The runner reads a trail only once the plane has shipped it.** After each
step it polls `/healthz` until the spool holds nothing unacknowledged, within a
bound, and requires the spool's quarantined records and the exporter's
partially rejected ones unchanged since the run began; the collector
acknowledges only what the file holds. It then reads the trail file once. It
never waits for a closing event: a held trail stays open, and an absence is
proven only by a drained spool. It requires the adapter's admissions to have
grown by exactly one per call step, so no other client moved the run. Every
event the file gained since the previous read must be on the trail the answer
names: growth on any other trail, one an earlier step judged included, differs
on `trail.others`. After a
pause or unpause step it waits for two more completed reads of the pause file
and the state it wrote, since the read in progress may have begun before the
write. An approve step needs no wait: the plane reads the directory when the
retry comes.

**The runner holds no operator's authority of its own.** `guardana-gateway
scenario run` is an MCP client of the plane's listener and reads the trail file
ADR-0020's collector writes. For an approve, a reject, a pause or an unpause it
runs `guardana-control`, found beside its own executable or named by a flag and
never looked up on the search path (a bare name is made absolute before it is
checked and run), and refuses one whose version is not its own. A pending
answer's approval id must be the one its trail's last `APPROVAL_REQUESTED`
records, or the step differs on `answer.approval_id` and no operator step
answers it. The gateway's binary keeps no code that answers an approval, as ADR-0016
has it.

**Three exits.** 0 when every step compared every member. 1 when one differed,
named `step[i].member: want X, got Y`; the scenario stops at that step. 2 when
the scenario could not run: no scenario, a file the reader refuses, a
precondition, an answer that names no trail, a barrier past its bound, another
client, a `guardana-control` missing or of another version, or a trail file
that gained no event while the exporter's acknowledged records grew, which is
not the plane's. A gate treats 2 as a failure.

**What each side checks.** This repository checks the plane's own account: the
answers, the decisions and the trails. A lab checks the far side, what each
victim served, and reads this plane's trail with a reader of its own. A lab
scenario names a file of this format for its calls and keeps only what a lab
can see.

## Security / compatibility impact

No frozen wire contract moves: the ids travel in MCP `_meta` and error data,
which this plane already writes under its namespace, and `Decision.request_id`
exists. The ids are minted from the system's randomness and grant nothing; a
blocked answer already carries the decision id. An upstream cannot forge them,
since the adapter strips the namespace from what an upstream sends and writes
its own after. The runner is a test tool with the authority of whoever runs it:
it answers approvals and pauses through the approver's binary, whose
directories the plane already trusts to its own user. The format is versioned
and experimental, and a change is a new kind.

## Alternatives considered

- **The lab's YAML shapes as they stand.** YAML's typed decoding already cost
  this project a policy format; the shapes carry identity the plane does not
  take from a file, a server the plane does not route by, no operator steps,
  and a vocabulary the plane does not speak.
- **Waiting for a trail's closing event.** A held trail never closes before its
  retry, a runner that stops when the expected kinds appear passes a trail
  whose later events are still in flight, and an absence cannot be waited for.
- **The approver's write path in the gateway binary.** The smallest code, but
  the plane's binary would hold a path that answers approvals, and only review
  would keep the plane from reaching it.
- **A third binary for the runner.** The cleanest split, and a binary to ship
  and document for one test tool.
- **The runner in the approver's binary.** It would have to link evidence to
  read a trail, which ADR-0016 keeps out of that binary.
- **Expectations checked for inclusion.** A scenario that names some codes
  passes while the plane adds others; an exact list fails on the one nobody
  expected.
- **A step kind for a decision point that goes away.** The plane has no switch
  for it; a scenario about an outage runs against a plane configured with a
  decision point that does not answer, which its expectations show.

## Consequences

The demo's cases become files a reader can open, each run against its own
plane. Every `tools/call` answer carries two more members, and the coverage
page says the marker is `<ns>/answer` alone. A lab adopting this format drops
its own trajectory and decision expectations and keeps its victims'.

## Declared limits

- An upstream error whose data is neither an object nor absent names no trail,
  and neither does a call that fails with no answer at all, a timeout or a
  closed connection; neither can be a scenario step.
- The runner must be the plane's only client while it runs, and a scenario
  must finish within the plane's approval lifetime.
- The runner trusts the trail file it is pointed at to be the plane's, beyond
  the ids, the bundle digest and the mode it checks.
- A literal `${` cannot be sent.
- Two builds that both report `dev` pass the version check, whatever tree each
  comes from.

## Validation

- The reader: one refusal per rule with the input at which it bites, among them
  an unknown kind among other unknown members (the refusal names `kind`),
  `"codes": null`, a `\xff` byte, `"\ud800"`, a duplicate member, a reference
  to the same or a later step, one in a member name, an unterminated `${`, a
  file named `Foo.json`, an unregistered code, an obligation type outside the
  catalogue, an unnamed event kind, and a scenario in which no call step
  expects an event; a fuzz target.
- Substitution: an output holding `${step[0].output}` goes in literally; the
  arguments hash on `ACTION_PROPOSED` equals canon's hash of the arguments the
  runner sent; an error step or two text items as a source refuse.
- The adapter: an upstream result carrying a forged `<ns>/request_id` reaches
  the runner with the plane's; error data carrying one is stripped and
  replaced; error data that is a string leaves the step uncorrelated, exit 2;
  pending answers and waiting retries name the held trail; the recorded result
  hash does not move.
- The barrier: with several exports in flight and a collector that holds back
  the first batch, a step passes only once the gap fills; a quarantined record
  fails the run; a collector that never answers exits 2.
- Order: a toxic scenario and then one whose read is declared trusted, on one
  plane: the second fails the fresh-run check. A retry wrongly held anew fails
  on `trail.request`. A pause followed at once by a call, fifty times, never
  flakes.
- Every scenario of the runner's own live set: flipping a verdict, adding a
  code, dropping the last kind, swapping an answer's kind and swapping `request`
  each exit 1 naming that member; each of the demo's seven, mutated once, exits
  1 naming its member; a timeout or a precondition does not count as the mutant
  caught. An
  empty directory and a trail file that is not the plane's each exit 2.
- The gateway's built binary holds no symbol of the approver's handle, checked
  against its symbol table beside one that must be there.
- A plane that also writes a second trail, or grows one an earlier step judged,
  differs on `trail.others`; a pending answer naming an approval its trail did
  not request differs on `answer.approval_id` and runs no operator step; a
  retry sent with other arguments differs on `args`.
- A bare `--control` name runs the file in the working directory, never one on
  the search path.
- An interrupt, and a `pause add` that fails after writing, leave no entry the
  runner added, and a failed add never removes an entry an operator added
  meanwhile; a scenario that fails with a pause in force leaves the plane
  unpaused for the next one. A relative `--control` name is resolved by the
  kernel, `..` after a link included.
