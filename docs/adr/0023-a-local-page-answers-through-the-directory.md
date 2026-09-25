# ADR-0023: A local page answers through the directory

Status: accepted
Date: 2026-09-24

Builds on [ADR-0016](0016-approval-providers-and-the-lost-hold.md),
[ADR-0018](0018-keys-and-bundles-on-disk.md),
[ADR-0019](0019-an-operator-can-pause-calls.md),
[ADR-0020](0020-a-trail-and-counters-without-a-collector.md),
[ADR-0021](0021-a-run-carries-what-it-took-in.md) and
[ADR-0022](0022-scenarios-are-data.md).
Amends [ADR-0016](0016-approval-providers-and-the-lost-hold.md): a handle
judges its directory at every call, not only when it opens.

Amended on the reviews of its first implementation: the printed token is good
for one trade, an answer is bound to the digest the approver confirmed, the
store reaches every file through the directory it opened, and the page withholds
and quotes what it shows as the command line does. The paragraphs below say it.

## Context

A stranger's first plane should start with one command, and a call held for an
approval should be answerable without learning a command line. The approvals
directory and the pause file already carry the authority: whoever can write
them, as the plane's own user, can approve, reject and pause (ADR-0016,
ADR-0019). What is missing is a surface a person uses in a browser and the one
command that lays out a plane for it.

A page is a listening surface, and a browser on the same machine is reachable
by every site the operator visits. A web page can post to a loopback port, can
rebind a name to `127.0.0.1`, and can frame what it cannot read. Cookies are not
isolated by port, so any other server on the loopback that the operator's
browser visits receives them. Another local account can reach the port. An
agent's own tool, a fetch behind the plane, can reach it too. And what the page
shows comes partly from the agent: a resource id is the agent's text.

## Decision

**The page is an approver process.** `guardana-control console` serves it, from
the approver's binary, through `internal/approvals` and `internal/pause`, as the
`approvals` and `pause` commands write. It adds no authority. The plane's binary
gains no path that answers an approval, writes a pause or reads a private key,
and a check on the built binary says so. The page's package links no key reader,
no pipeline, no evidence, no spool, no journal, no trail and no process
launcher, and serves only files embedded in the binary.

**A printed token good for one trade.** The page binds an ephemeral port on
`127.0.0.1` and prints one line: its address with a 256-bit token in the URL's
fragment, which a browser never sends. The token is in no argument,
environment variable, file or log of the command, but the browser keeps the
link in its history and session files, so the token is worth one thing: the
page's script trades it once, at `/api/session` and within ten minutes of the
start on the wall clock and the monotonic one, for a session token of the same
strength. The session token lives in the
tab's session storage and goes as a header on every later request, compared in
constant time. After the trade, or ten minutes after the start, the printed
token opens nothing. A request whose `Host` is not exactly the bound address is
refused, so a rebound name reaches nothing. Every request to its API needs an
`Origin` and a `Sec-Fetch-Site` that are same-origin when the browser sends
them, and a write needs a JSON body. Every answer forbids framing, caching and
referrers, and the content security policy allows the page's own script only,
with no inline script.

**The page shows what the listing shows, and renders it as text.** It shows what
`approvals list` and `pause list` show, including that the readable fields are
not bound to the digest; no arguments, no preview, no trail. Every value is set
as text, never as markup, and withheld and quoted as the command line prints
it, through a package that reads no key.
The server makes every sentence the page says of a record, a listing or a pause
file, and the script only places them. Its words claim no more than the data:
an approved call can resume on the agent's retry before its expiry, when the
retry is the same call and nothing blocks it, and is never "ran"; an incomplete
listing is a banner, never
an empty list; a pause file that cannot be read says the plane blocks every
call.

**An answer names the call it answers.** Cards stand in the order their calls
were held, so a new hold never moves a button under the pointer, and no card
leaves the page while it is open. Answer buttons wait one second after any
change to the list. An answer takes two clicks, the second naming the tool,
the upstream and the first 12 hex digits of the action digest, and the answer
carries that digest: the page files it only for the record whose digest it is.

**The directory is the one opened.** Both handles of the approvals store open
the directory once, as an `os.Root`, and reach every file through it, the
plane's lock and the approver's probe of it included. At the start of every
call they judge that opened directory again (the same owner, no write bit for
the group or the world) and refuse a name that no longer names it, with its own
sentence, which the page shows and on which the plane blocks the call as on any
refusal of its store. A directory put at the name is never read or written.

**`approver_id` is the starter's claim.** It is a flag of `console`, not a form
field and not the account running it, which is the plane's account for every
approver and would only look authenticated.

**Dev lays out a plane and supervises it.** `guardana-gateway dev` takes the
demo's configuration and policy document and creates a fresh directory that
holds everything the plane writes. It signs the bundle with a key that exists
only in its memory, starts the collector and the plane in its own process on
loopback addresses it bound itself, and starts the page beside it at its own
version, over a pipe whose end stops the page. It refuses the plane's
environment variables, a configuration that sets what dev owns, and any
address it binds or reaches that is not an IP literal on the loopback: its
listeners and collector, every upstream endpoint, and the decision point's
identifier, evaluation endpoint and proxy. It builds the plane the way
`serve` does: no setting and no branch exists for dev alone. When any part
stops it stops them all, in the order that lets the trail drain. With
`--scenario`, each scenario runs on a plane of its own with no page.

The page's JSON is private to the page and carries no version: it is not a
contract.

## Security / compatibility impact

No wire contract or setting changes. The approval projection gained the
upstream (`provider`) under the same version before any release, so a reader of
an earlier build refuses a new projection as one it cannot decode. The page is reachable only
with a session traded from its printed token, only from this machine, only by
the tab that traded it and a copy of that tab. It
does not protect against a process of the plane's own user, which can already
write the directory and the file; a tool behind the plane that can write files
is such a process.

## Alternatives considered

- **The page in the plane's process.** It would link the approver's write path
  into the plane and make the plane its own approver.
- **A cookie and a token per form.** It needs no script, and it leaks to every
  other loopback port the browser visits.
- **The token in every request's query.** It leaks to logs, history and
  referrers.
- **No page; dev prints `approvals approve` lines.** Smallest, and it misses
  the goal.

## Consequences

A stranger answers a held call in a browser. The approver's binary serves HTTP
on the loopback. The gateway's binary signs a bundle in memory and starts a
sibling process.

## Declared limits

- A second tab needs a new link: the console has to be started again.
- The script's one-second wait, its kept cards and its two-click confirmation
  are checked in a browser, not by the gate.
- The page cannot tell whether a plane reads the pause file it writes.
- The fields the page shows are not bound to the digest the approval binds.
- A dev plane goes stale when its policy budget passes, and dev says when.

## Validation

- The page's package reaches the approvals and pause packages and none of the
  refused ones; it opens no file by path and serves no directory.
- The gateway's built binary holds none of the approver's answer, the pause
  writers or a private-key reader, and holds the in-memory signer.
- Each refusal with the input at which it bites: a foreign `Host`, a write
  without the token, a cross-origin `Origin`, a same-site fetch, a body that is
  not JSON, a read without the token, a path outside the embedded files; every
  answer forbids framing.
- A second trade of the printed token, the printed token on any other request,
  and a trade ten minutes after the start are refused.
- An answer whose digest is not the record's, a member spelled in another case,
  and a body that is not UTF-8 are refused, and the record stays pending.
- A directory loosened, or swapped for a link to another store, under an open
  handle is refused by the page and by the plane's handle; one swapped in
  between a call's judge and its file operations is never read or written: the
  answer lands in the opened directory, and the plane neither finds nor spends
  the other store's approval.
- Key text in a projection field or a pause reason is withheld, a lone surrogate
  escape is refused and nothing is written, and a trade whose wall clock is past
  ten minutes is refused though the monotonic clock is not.
- A resource id holding markup or U+202E is shown inert and quoted.
- The token is in no process argument, environment, file or log of dev.
- Dev refuses a plane variable in its environment before anything binds, a
  configuration setting a key dev owns, and an existing state directory;
  killing the page stops dev, and killing dev stops the page.
- A held call answered through the page resumes on its retry; with a pause in
  scope, the retry is blocked and the approval is not spent.
