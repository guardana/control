---
title: Command line
summary: The two binaries, what each prints as its help, and the exit statuses they share.
type: reference
covers: [cmd/guardana-control/**, cmd/guardana-gateway/**]
---

# Command line

Two binaries, built from the repository root with `go build ./cmd/<name>`;
[status.md](../status.md) says what each does today. Each fenced block below
is the help its binary prints on a usage error, byte for byte: the marker
around it names the test that diffs the block against the binary's text, so
a flag or a command that changes without this page fails the gate.

| Exit status | Means |
| --- | --- |
| 0 | The command did what it was asked. |
| 1 | The input was refused or a case failed: stderr says why, one line each; a scenario's report on stdout names what differed. |
| 2 | A usage error (stderr carries the help below or names the flag or argument), or a scenario that could not run. |

Without arguments either binary prints its version and one status line on
stdout and exits 0.

Every command of either binary refuses an argument holding a PEM marker, the
start of a key file's body line, a line break or another control character
before it reads a flag, with one line that does not repeat the argument and
status 2: such an argument is key text where a path belongs, or text that
would break the line reporting it. Only the body line's unbroken start is
matched, so a spaced or partial one passes. Key text read from a setting, a
file, the environment or a request is printed as `[key text withheld]`,
keeping the rest, logs included.
A message holding a control character, a line or paragraph separator, a
format character or bytes that are not UTF-8 is printed as a quoted Go string.

## guardana-control

`policy lint` and `policy test` read a document without a plane, and
`policy keygen` and `policy sign` make the key and the signed bundle a plane
loads: [guides/write-and-test-a-policy.md](../guides/write-and-test-a-policy.md)
is their page. The `approvals` commands read and answer the records under an
approvals directory, from outside the gateway process.

`policy keygen` creates the directory `--out` names, mode `0700`, and refuses a
path that exists. Into it go `signing.key`, the private key as one PKCS#8 PEM
block, mode `0600`, and `signing.pub`, the public key as one line of standard
base64 and a newline, which `policy.public_key` takes as it is. It prints exactly two lines, `key_id: <id>` and `public_key: <line>`,
the two members the gateway's `policy` group takes. The id is `ed25519-` and
the first 16 hex digits of SHA-256 over the public key's 32 bytes; nothing
lets an operator choose it.

`policy sign` prints one `name: value` line each for `bundle_id`, `version`,
`serial`, `digest`, `key_id` and `out`, and writes the bundle mode `0644`,
replacing an earlier bundle at `--out`. It writes nothing when it refuses: a
document that does not parse, refused before the key is opened; a key file
another account owns, one on which the group or others have any permission,
one that is not a regular file, and one that is not exactly one unencrypted
Ed25519 key in PKCS#8, with an OpenSSH, encrypted, EC, RSA or public key each
named; an `--out` that is the key or the document by any spelling or link, and
one that exists and is not a policy bundle, so no other file, another key
included, is replaced; and a bundle the loader would refuse. `--out` is
cleaned as text before anything reads it, so `a/link/../x` names `a/x`
whatever `a/link` points at. When syncing the directory fails after the
rename, `sign` reports the failure although the new bundle may already be in
place.

The key reaches the command only as a path. No refusal quotes the key file or prints
the `--key` value: a refusal about the key names the flag and the check that
failed. Both commands refuse
on a platform without permission bits
([ADR-0018](../adr/0018-keys-and-bundles-on-disk.md)).

Write access to that directory is the approval authority. `--approver-id` is
an unauthenticated claim: it is recorded beside the answer so that whoever
reads the evidence later has a name, and nothing checks it against anything.
Both answering commands require it, because an answer nobody claims tells a
reader nothing; flags come before the directory and the approval id.

`approvals list` prints, for each record, what the plane wrote — its state,
its resolution and both digests — and beside it the readable fields of the
projection the plane wrote at hold time. Those readable fields are not bound
to the action digest beside them: the binding is over the authorized argument
bytes, which no record holds, so a person reading a listing cannot check one
against the other. The listing says so above the records. A listing the store
reports incomplete exits 1 and names what it could not read, because what it
shows is then neither the whole directory nor an empty one.

`approvals approve` and `approvals reject` write the answer whether or not a
plane is running, and exit 0 either way, because the record is on disk and
the next plane to start reads it. Where no plane holds the directory they say
so on stderr: nothing waits for the answer and the held call will not run,
since a hold does not survive the plane stopping; the answer is written all
the same, and a plane that keeps a hold journal records it on that call's
trail as too late to resume it. Writing nothing there would close that trail as one nobody answered,
which is false about a person who did decide.

Both commands refuse, writing nothing, an approval id that is not there, one
an approver answered already, one the plane consumed, one the plane closed
without resuming, one past its expiry, and an approver id or a reason over
its bound — each on its own line, naming what to do about it.

The `pause` commands write and read the pause file a plane names in
`pause.file` ([ADR-0019](../adr/0019-an-operator-can-pause-calls.md)), and
[concepts/enforcement-modes.md](../concepts/enforcement-modes.md) says what a
pause does to a call. `pause init` creates the file, pausing nothing, and
refuses a name that is taken. `pause add` takes exactly one scope: `--global`,
every call; `--provider <p>`, every call to that upstream by its configured
name; or `--action <k>` with `--provider <p>`, where `<k>` is `tool`, `prompt`
or `resource` and a tool also takes `--name <n>`, the name the plane routes
by. A prompt or a resource takes no name, since the plane routes it by its
upstream alone. `<f>` is the pause file, a relative path read from the working
directory. It prints
the id it drew for the entry and nothing else, and `pause remove <file> <id>`
takes that id; removing the last entry leaves a file that pauses nothing,
because a missing file blocks every call. `pause list` prints one line per
entry, its reason included, or `no entries`.

Write access to the pause file is the authority to pause and to lift a pause,
as write access to the approvals directory is the authority to approve: any
process running as the plane's user can empty the file, so run the commands
as that user. They refuse, writing nothing, a file or a directory the group or
others may write or another account owns, a link, a file the plane would
refuse to read, a second scope or none, a name, a reason or a scope the file
cannot hold, and a 65th entry, each on one line that does not repeat the
value. A writer waits up to ten seconds for another writer's lock; a
running plane takes none. A pause blocks every call not yet handed out for
execution when the plane reads it, within one `pause.poll_interval`; it cancels
nothing already running,
and a hold outlives it: a held request whose retry was paused resumes after
the lift if its approval has not expired.

`console` serves a page that answers approvals and writes pauses:
[console.md](console.md).

<!-- generated: go test ./cmd/guardana-control -run TestCLIPage -->
```
usage:
  guardana-control policy lint <file>
  guardana-control policy test <dir>
  guardana-control policy keygen --out <dir>
  guardana-control policy sign --key <file> --out <file> <document>
  guardana-control approvals list <dir>
  guardana-control approvals approve --approver-id <id> [--reason <text>] <dir> <approval-id>
  guardana-control approvals reject --approver-id <id> [--reason <text>] <dir> <approval-id>
  guardana-control pause init <file>
  guardana-control pause add --provider <p> [--action <k> [--name <n>]]|--global [--reason <r>] <f>
  guardana-control pause remove <file> <id>
  guardana-control pause list <file>
  guardana-control console --approvals <dir> [--pause <f>] --approver-id <id> [--until-stdin-closes]

Write access to the approvals directory is the approval authority:
--approver-id is a claim recorded as given, never an identity.
Write access to the pause file is the authority to pause a call and to lift a pause.
```
<!-- /generated -->

## guardana-gateway

`run` serves one plane from a configuration file and `doctor` checks that
file without serving: [guides/run-the-gateway.md](../guides/run-the-gateway.md)
is their page and [configuration.md](configuration.md) lists every key.
`--config` is required: no default location is searched. A key naming a file or a
directory refuses key text, naming the key and not the value.

`collect` and `trail` read no configuration:
[guides/watch-a-plane-without-a-collector.md](../guides/watch-a-plane-without-a-collector.md)
is their page. `collect` listens on a loopback IP address only and appends
what a plane exports to one file until stopped; `--out` names a trail
file or a new path, and any other file is refused and left as it was. A last
line with no newline is removed only when it can be the start of an evidence
line, so a file of one line that cannot, such as a settings file, is refused
too. `trail` prints one line per request and exits 1 when a chain fails,
cannot be read either way, or the file holds no trail, when the file ends in a
partial line that no running `collect` holds, and when it holds more than
100,000 lines.

`scenario run` holds a running plane to scenario files
([scenario-format.md](scenario-format.md)); it exits 1 when one differed, and
2, a failure too, when one could not run.

`dev`: [dev.md](dev.md).

<!-- generated: go test ./cmd/guardana-gateway -run TestCLIPage -->
```
usage: guardana-gateway [run --config <file> | doctor --config <file> | collect --listen <addr> --out <file> | trail <file> | scenario run --config <file> --trail <file> [--control <file>] [--timeout <d>] <path>... | dev --config <file> --policy <file> [--state <dir>] [--scenario <file>]...]

run
  -config string
    	the configuration file to read

doctor
  -config string
    	the configuration file to read

collect
  -listen string
    	the loopback address to listen on, host:port; port 0 takes a free one
  -out string
    	the trail file to append what arrives to

trail

scenario
  -config string
    	the plane's configuration, which names its listener, its /healthz, its approvals directory and its pause file
  -control string
    	the guardana-control that answers approvals and pauses; by default the one beside this binary
  -timeout duration
    	the bound on each wait: the spool's drain, a pause read, a run of guardana-control, and a call past the upstream's own timeout (default 10s)
  -trail string
    	the trail file the plane's collector writes

dev
  -config string
    	the demo's configuration, without the keys dev sets
  -policy string
    	the policy document dev signs in memory
  -scenario file
    	a scenario file to run on a plane of its own; repeatable
  -state string
    	a new directory for the plane's state; by default a temporary one
```
<!-- /generated -->
