# Golden fixtures for the canonical action digest

An approval binds to one exact action digest and expires; it never covers a
similar action, a retried action or a later one. That only works if every
implementation computes the same digest from the same action, so the files here
are the contract, not examples. The Go implementation is `implemented`
(`internal/canon`, `go test ./internal/canon/`); implementations in other
languages are `planned` and will be checked against this directory.

The rules below are the record in
[ADR-0005](../../docs/adr/0005-canonical-action-digest.md),
[ADR-0010](../../docs/adr/0010-digest-domain-separation.md) and
[ADR-0011](../../docs/adr/0011-contract-corrections-before-publication.md).
Where this page and those records disagree, the records win and this page is
wrong.

## The three domain tags

Fixed byte strings. None contains a product name, so a rename changes no
digest. Each ends with a newline: the canonical form escapes every control
character, so a raw newline cannot occur in the body and the boundary between
tag and body is unambiguous.

    action digest:    agent-action-digest/v1\n
    approval binding: agent-approval-binding/v1\n
    arguments hash:   agent-arguments-hash/v1\n

## What is hashed

    digest = "sha256:" + lowercase_hex(sha256(tag || canonical_json(action)))

`action` is an object with exactly ten members. Nine are taken from the action
envelope, the tenth is the authorized arguments:

| member | source | notes |
|---|---|---|
| `projectId` | `projectId` | a string, the envelope's own |
| `tenantId` | `tenantId` | a string, the envelope's own, not `principal.tenantId` or `resource.tenantId` |
| `environment` | `environment` | a string, the envelope's own, not `resource.environment` |
| `principal` | `principal` | `id`, `type`, `authnStrength`, `tenantId`, `attributes` |
| `agent` | `agent` | `id`, `instanceId`, `framework`, `version`, `modelRef` |
| `delegation` | `delegation` | array, order preserved; each hop has `from`, `to`, `scopes`, `reason` |
| `action` | `action` | `kind`, `name`, `protocol`, `effect`, `provider` |
| `resource` | `resource` | `type`, `id`, `tenantId`, `environment`, `labels` |
| `destination` | `destination` | `trustZone`, `host` |
| `authorizedArguments` | the caller | the arguments document, at most 65536 bytes |

Keys are the protobuf JSON names, the lowerCamelCase form protojson emits.

**Every member is present, at its proto zero value.** An absent string is `""`,
an absent map is `{}`, an absent repeated field is `[]`, an absent enum is its
zero name: `EFFECT_CLASS_UNSPECIFIED` for `action.effect`,
`TRUST_ZONE_UNSPECIFIED` for `destination.trustZone`. An absent message is all
of its members at their zero values, never `null` and never `{}`. A member is
never omitted and `null` never appears. This is the rule that stops an
implementation building the object from protojson output, which omits empty
scalars, from disagreeing with one reading the message.

**Enums are their name**, `"EFFECT_CLASS_TRANSACT"`, never their number. A
number that no name exists for is refused rather than digested.

## What is excluded

- `schemaVersion`, `requestId`, `traceId`, `spanId`, `occurredAt`, and
  `issuedAt` and `expiresAt` inside a delegation hop: two attempts at one action
  differ in these, and an approval has to match across them.
- `data` and `context`, in their entirety. An approval is honoured only in the
  trail of the request it was requested for, where neither can change; a call
  submitted again is a new request and needs a new approval
  ([ADR-0011](../../docs/adr/0011-contract-corrections-before-publication.md)).
- `arguments`, the hash and the preview of the arguments. The digest carries
  the arguments themselves, as `authorizedArguments`.

## Ordering

- **`delegation` keeps its order.** It is the authority chain, root outwards,
  and reversing it inverts the "child exceeds parent" check.
- **`scopes` inside a hop is sorted**, because it is a set and two producers may
  list it either way. Repeats are kept: sorting is not deduplication, and
  dropping a repeat would give two documents one digest.
- **Object keys are sorted as arrays of UTF-16 code units** (RFC 8785 section
  3.2.3), which is the order JavaScript's `Array.prototype.sort` gives and is
  **not** UTF-8 byte order or code point order. `scopes` is sorted the same way.

The `delegated` fixture holds the trap on purpose. It carries two scopes that
differ only in their last character, written `sandbox:\ud83d\ude00` (U+1F600,
in the file as a literal) and `sandbox:\ue000` (U+E000, in the file as an
escape). U+1F600 sorts **below** U+E000, because its surrogate pair D83D DE00 is
below E000 as code units, while its UTF-8 bytes go the other way.
Python's `sorted()` and any sort by code point order those two the wrong way
round; sort on `key.encode("utf-16-be")` or an equivalent.

## The canonical JSON form

RFC 8785, restricted so that a second implementation cannot silently differ.
Refused, never approximated:

- a number that is not an integer literal, including `1.0` and `1e3`
- an integer outside +/-(2^53 - 1), where no IEEE-754 double is faithful
- a string or key that is not valid UTF-8
- an unpaired surrogate escape such as `"\ud800"`, which three languages decode
  three different ways; a valid pair is fine
- a duplicate object key, the one input that gives two documents one digest
- two member names in one object that fold together, as the next section
  defines
- more than 32 nested containers in the arguments document, counted from the
  document's own root: `[]` is one container and `[[]]` two. The action object
  that holds the document is not one of them, so a document of 32 is accepted
  and sits 33 deep in the action; the rest of the action never nests more than
  four deep
- an arguments document that starts with a byte order mark, which most readers
  strip and this form does not
- an arguments document of whitespace only: only an empty byte string is
  absent, as the arguments section says
- the envelope, the five messages the digest reads or a delegation hop that
  carries a field the implementation does not know. It may be a field a
  later minor version added to the set, and digesting without it would give
  two actions one digest, so it is refused, never digested without the field

### Keys that fold together

Two member names in one object are refused when they are equal under simple
case folding: map every code point of each name through the mappings of status
C or S in `CaseFolding.txt` of Unicode 17.0.0, leave a code point that has no
such mapping as it is, and compare the two results code point by code point.
So `amount` and `AMOUNT` cannot both be members of one object, and neither can
`k` and U+212A KELVIN SIGN, `s` and U+017F LATIN SMALL LETTER LONG S, or U+00DF
and U+1E9E. The rule holds in every object of the action: the objects inside the
arguments, and the `attributes` and `labels` maps.

A consumer that matches member names without regard to case would otherwise
read one value where the digest covers another. The rule defends a consumer
that folds; it does not defend one that compares through case mapping, which
equates U+0131 and `I`. Full case folding (status F, which equates U+00DF and
`ss`) is a different relation. An object with the members U+00DF and `ss` is
accepted, and so is one with U+0131 and `I`. Nor does the rule defend a
consumer that ignores `_` or `-` when it matches names, as Go's
`encoding/json/v2` does under `case:ignore`: `amount` and `a_mount` are both
accepted as members of one object, so such a consumer keeps json/v2's
duplicate-name refusal on. A later Unicode version can add mappings, so an
implementation takes its table from 17.0.0, not from whatever its runtime
ships.

Strings are escaped as RFC 8785 section 3.2.2.2 requires: `\b \t \n \f \r \" \\`,
lowercase `\u00hh` for the remaining C0 controls, and every other character
literal, including the solidus, DEL and all non-ASCII. There is no whitespace
between tokens and no Unicode normalization: a key written as U+00E9 and the
same key written as U+0065 U+0301 are two different members.

## The arguments document

`authorizedArguments` holds the value of the arguments document, parsed and
canonicalized, not its bytes: whitespace and member order in the file do not
change the digest. The document is canonicalized on its own and then placed in
the action, which is why its depth is counted from its own root. Absent
arguments (an empty byte string, and nothing else) are the empty object: a
document of whitespace only is refused, and so is one that starts with a byte
order mark. A document that is the literal `null` is refused, because `null` is
a second spelling of "no arguments" that would hash differently. A `null`
**inside** the arguments is ordinary data and is kept, which `refund_prod`
exercises with `"memo": null`.

The limit is 65536 bytes of the arguments document, measured before parsing.

## The arguments hash

    arguments_hash = "sha256:" + lowercase_hex(sha256(
        "agent-arguments-hash/v1\n" || canonical_json(arguments)))

`arguments` is the arguments document under the rules of the previous section:
the same limit, the same refusals, and the empty object `{}` when the document
is absent. The hash therefore covers exactly the bytes the digest holds as
`authorizedArguments`. It is the value of `arguments.canonicalHash`, which
`EXECUTE` and `TRANSACT` envelopes must carry. Both computing it and the
receiver's recheck against the arguments it holds, which refuses a mismatch,
are `implemented`. It is not what an approval binds to: the action digest
is.

## The approval binding

    binding = "sha256:" + lowercase_hex(sha256(
        "agent-approval-binding/v1\n" || action_digest || "\n" || bundle_digest))

Both inputs are the full `sha256:` strings, and both must be `sha256:` followed
by 64 lowercase hex digits or the call is refused. Every digest string in this
project is compared for equality to authorize a call, so the case is part of the
contract, not a formatting preference. `binding.json` pins one binding, from
the `refund_prod` digest and a placeholder bundle digest; an implementation
that drops the newline between the two inputs, hashes the bare hex or changes
the case of either input passes every digest fixture and fails this one.

## The fixture format

```json
{
  "envelope": { "...": "protojson of one action envelope" },
  "authorized_args": { "...": "the arguments document" },
  "expected_digest": "sha256:..."
}
```

To check an implementation: read `envelope` as protojson, read the raw bytes of
`authorized_args`, build the ten-member object above, canonicalize it, prepend
the action tag, hash, and compare the whole string against `expected_digest`.

`arguments_hash.json` pins the arguments hash alone, so it has no envelope:

```json
{
  "authorized_args": { "...": "the arguments document" },
  "expected_hash": "sha256:..."
}
```

To check it: read the raw bytes of `authorized_args`, canonicalize them as an
arguments document, prepend the arguments tag, hash, and compare the whole
string against `expected_hash`.

`binding.json` pins the approval binding, so it has no envelope either:

```json
{
  "action_fixture": "refund_prod.json",
  "action_digest": "sha256:...",
  "bundle_digest": "sha256:...",
  "expected_binding": "sha256:..."
}
```

To check it: bind `action_digest`, which is the `expected_digest` of the
fixture `action_fixture` names, to `bundle_digest` under the formula above and
compare the whole string against `expected_binding`.

Every envelope here also passes this repository's contract validation, so a
fixture is never an action that could not have been proposed.

## The fixtures

| file | value | what it is for |
|---|---|---|
| `minimal.json` | `sha256:423ea583eb1ce0e797ed73d1f6334063d37318890ba3ee7eb5cd8f1a489788d1` | the smallest envelope that validates; almost every member is at its zero value |
| `refund_prod.json` | `sha256:1f3edcddf8af7b6779d5e7e47ce6754924a213d2f68273786d5dbda87b42db88` | every field populated, every excluded field populated too, escapes and an astral character in the arguments |
| `delegated.json` | `sha256:ba4e8682bd8c6f1b53d0d1b775ec4305f1a57872064bcc0b6ea3db9c4591f171` | two delegation hops, so hop order and scope sorting are both exercised by a fixture |
| `mutated_amount.json` | `sha256:e72339465af022c09d3e5aa44b3f6a1bdcd10108fb9708364b3253bd44d0c4d2` | identical to `refund_prod.json` apart from one argument value, and it must produce a different digest |
| `escapes.json` | `sha256:4c12075d685dfd732ac18569a4c79ef58607355374c5b4ab7d8738eaa2b7f9a5` | what other encoders escape and this form does not: `/ < > & = '`, U+001F, U+007F, U+2028 and U+2029 in the arguments, beside the keys U+E000 and U+1F600; and three empty entries in the envelope, an attribute value, a label key and a scope, which are kept |
| `arguments_hash.json` | `sha256:27fc04404826fca53b45f651f49d557521bf94c051b68707e0ca4522e8f0abd0` | the arguments hash of a document written with whitespace, member order and escapes the canonical form removes |
| `binding.json` | `sha256:3105283da9f4d61d9fa9558a8375d0796bce39ee48cdb7fbdff8a4ee0f34b394` | the approval binding of the `refund_prod` digest to a placeholder bundle digest of 64 `1`s, which pins the separator and the tag |

`refund_prod.json` carries one value written two ways: `escaped` holds
`"caf\u00e9 \ud83d\ude00"` and `literal` holds the same two characters as
literal UTF-8. The two must canonicalize to identical bytes. An implementation
where they differ has an escape or surrogate bug that no other fixture shows.

`arguments.canonicalHash` in these envelopes is an obvious placeholder, not the
hash of the arguments beside it. The digest excludes it and these files pin the
digest; a receiver comparing it would refuse the mismatch. `arguments_hash.json`
pins the hash.

Each value was computed by the Go implementation and, before it was pinned, by
an independent implementation written from this page alone, which is not
maintained. The values are never edited by hand: changing one means changing
the canonical form, which is a new version of the tag and a new record.
ADR-0011 changed the four digests once under the same tag, before any was
published.
