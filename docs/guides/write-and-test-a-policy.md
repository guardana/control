---
title: Write and test a policy
summary: Write an agent-policy/v1alpha1 document, lint it, prove what it decides with cases, and sign it into the bundle a plane loads.
type: how-to
covers: [cmd/guardana-control/**, internal/policy/**, internal/policykey/**]
---

# Write and test a policy

## When to use this

You are about to give a gateway a policy, or to change the one it runs, and
you want to know what it will decide before a call depends on it. The
commands are `implemented`: `policy lint` says whether a document is one this
build reads, `policy test` decides a case the way the kernel would and
compares the result with what you expect, and `policy keygen` and `policy sign`
turn the document into a bundle a plane loads. Run them with
`go run ./cmd/guardana-control ...` from the repository root, or build the
binary once with `go build ./cmd/guardana-control`.

What a document may say is in
[reference/policy-format.md](../reference/policy-format.md); how the kernel
decides against it is [ADR-0012](../adr/0012-policy-kernel-semantics.md); the
obligation types a rule may name are in
[reference/obligations.md](../reference/obligations.md); what the commands print
is in [reference/cli.md](../reference/cli.md).

## Prerequisites

- This repository at a Go toolchain the `go.mod` pins.
- An idea of the calls the policy must allow, block or hold: the action's
  name, provider and effect class, the resource and the environment. A rule
  matches on what the envelope says, spelled exactly.
- A Unix-like system: the signing commands refuse where a file's permission
  bits cannot be read. Loading the bundle into a plane is
  [run-the-gateway.md](run-the-gateway.md).

## Steps

### 1. Write the document

Start from
[testdata/policy/documents/example.json](../../testdata/policy/documents/example.json).
Give the bundle an `id`, a `version` and a `serial`, and set `maxStaleSeconds`
to how long a plane may keep deciding on this document after its loader last
confirmed it. Then write the rules, each with an `id`, an `effect` and a
`when` that constrains something.

Two things an author meets early:

- A `DENY` keyed on a free-form value is only as strong as the producer's
  spelling. A host name has one spelling, because the contract refuses its
  upper-case, trailing-dot and Unicode forms; an identifier such as
  `principal.id` compares exactly as sent, so a `DENY` on `agent-7` does not
  meet `Agent-7`. An allow-list is the one construction a second spelling
  cannot pass: a call that names a value no `ALLOW` rule lists matches no rule,
  and nothing matched is `DENY`.
- `v1alpha1` cannot give delegated calls an obligation without blocking direct
  ones. A rule on `delegation.scopes` is unknown for a call that carries no
  chain, and an unknown rule of any effect but `ALLOW` makes the decision
  `INDETERMINATE`, which is `Block` for a material effect. A rule that
  should apply to delegated calls alone therefore turns every direct call into
  one the enforcement point blocks.

### 2. Lint it

```
$ go run ./cmd/guardana-control policy lint testdata/policy/documents/example.json
$ echo $?
0
```

Nothing is printed on a pass. On a refusal, the first refusal in document
order is one line on stderr and the exit status is 1; the parser stops at the
first refusal, so fix one per run:

```
$ go run ./cmd/guardana-control policy lint refused.json
guardana-control: policy lint: rule "cap-refunds" "rules[0].obligations[0].type": "rules: an obligation type outside the catalogue"
```

The refusal names the rule and, as a path, the member it refused, and never
repeats the value. `lint` runs the checks a bundle goes through when it
is loaded, as far as they concern the document itself; the signature, the key
and the fields outside the document are checked only when a bundle is loaded.

### 3. Write a case per decision you rely on

A case is one JSON file holding the document, an envelope, the run's flow, the
kernel's options, two timestamps, optionally a decision point's answer, and the
decision you expect; its members are
in [reference/policy-format.md](../reference/policy-format.md#a-test-case).
Copy one from
[cmd/guardana-control/testdata/cases](../../cmd/guardana-control/testdata/cases)
and change the envelope and `expect`. Write one case for each verdict the
policy must produce, and one for the call it must not let through: a policy
tested only on the calls it allows has not been tested.

### 4. Run the cases

```
$ go run ./cmd/guardana-control policy test cmd/guardana-control/testdata/cases
ok   allow-read.json: ALLOW Execute [RULE_ALLOW]
ok   approve-refund.json: REQUIRE_APPROVAL AwaitApproval [APPROVAL_REQUIRED]
ok   deny-unmatched-write.json: DENY Block [NO_MATCHING_RULE]
ok   obligated-read.json: ALLOW_WITH_OBLIGATIONS ExecuteWithObligations [OBLIGATIONS_ATTACHED]
ok   stale-read.json: INDETERMINATE Block [POLICY_STALE RULE_ALLOW]
```

Every `*.json` file of the directory runs as a case, in name order; other files
are not read and subdirectories are not entered. For each case the document is
signed in memory with a key derived from a fixed seed, loaded at `loaded_at`,
the envelope is decoded the way a receiver decodes JSON, and the kernel decides
at `decided_at`. The fixed key is not what a case tests: it is there so that a
run needs no key from the user.

### 5. Make a key and sign the document

```
$ go run ./cmd/guardana-control policy keygen --out keys
key_id: ed25519-<16 hex digits>
public_key: <44 characters of base64>
$ go run ./cmd/guardana-control policy sign --key keys/signing.key --out orders.bundle policy.json
bundle_id: orders-policy
version: 2026-09-24.1
serial: 3
digest: sha256:<64 hex digits>
key_id: ed25519-<16 hex digits>
out: orders.bundle
```

Run `keygen` once per policy authority. It creates `keys` with mode `0700`
and refuses a path that exists, so it never writes over a key. Its two lines
go under `policy:` in the gateway's configuration as they are. Whoever can
read `keys/signing.key` can sign a policy every plane pinning that public key
accepts, so keep the file off the plane's host; `sign` refuses it when
another account owns it or the group or others have any permission on it, and
a key mounted into a container needs mode `0400`. Give `--key` the path of the
file, never its text: an argument holding key text is refused without being
repeated.

`sign` refuses a document that does not parse before it opens the key, signs
under the id derived from the key, and refuses to write a bundle the loader
would refuse. The bundle is public and written with mode `0644`, replacing an
earlier bundle at `--out`; any other file there is refused and left as it
was. Give each new version a higher `serial`. What the files hold and why is
[ADR-0018](../adr/0018-keys-and-bundles-on-disk.md).

## Verify

- `policy lint` exits 0 and prints nothing.
- `policy test` prints `ok` on every line and exits 0. A case that decides
  otherwise than it expects is `FAIL name: got <decision>, want <expected>`;
  a case that cannot run, because its file does not parse as a case, its
  document does not sign or its options are refused by the kernel, is
  `FAIL name: <reason>`. The exit status is 1 when any line is `FAIL`, when
  the directory holds no case, and when it cannot be read; an empty directory
  is never a pass.
- The line a passing case prints spells the decision the way `expect` does,
  so the member to change in a failing case can be read off its line.
- `policy sign` exits 0 and prints the `key_id` `keygen` printed. A plane
  started with a key other than the one that signed the bundle refuses it and
  names both ids.
- The kernel's own fixtures in
  [testdata/policy/fixtures](../../testdata/policy/fixtures) are cases too,
  and the command's tests run them; run `go test ./cmd/guardana-control/` to
  see them pass on your toolchain.

## Roll back

A document is not deployed by these commands, so there is nothing to undo on
the plane: to go back, sign the previous document with a higher `serial` and
restart the plane on it. Keep the previous document and its cases in version control; a
change to the policy is a change to the cases in the same commit, and a case
that stops passing is the review's first question.
