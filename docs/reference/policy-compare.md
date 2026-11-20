---
title: Policy compare
summary: What `policy compare` reads, how it decides each case under two policies, its report and labels file, its counts, bounds and exit statuses.
type: reference
covers: [cmd/guardana-control/policy_compare.go, cmd/guardana-control/policy_compare_input.go, internal/policycompare/**, testdata/policy/compare/**]
---

# Policy compare

```
guardana-control policy compare --current <document> --candidate <document> [--cases <dir>]
      [--evidence <export>] [--labels <file>] --report <file>
```

Decides each case under the policy in force and under a candidate, through
the kernel's own evaluation, and writes a report of what changed. It is
`experimental`
([ADR-0087](../adr/0087-a-candidate-policy-is-compared-through-the-kernels-own-evaluation.md)).
Every result is simulated: the command sends no call, reads or consumes no
approval, writes no policy, floor or statement and reaches no plane. Both
documents are signed in memory with the fixed key `policy test` uses, without
its obligation parameter check, as `policy explain` signs.

## What it reads

| Flag | Reads |
| --- | --- |
| `--current`, `--candidate` | A policy document ([policy-format.md](policy-format.md#the-document)). |
| `--cases` | A directory of [test cases](policy-format.md#a-test-case), read as `policy test` reads one, `expect` optional and ignored. Each case's `document` must have the current document's canonical digest, or the run is refused naming the file. A case's id is `case:` and its file name. |
| `--evidence` | An [evidence export](../contracts.md#the-evidence-export), owned by this account and not writable by others. Each request it names is a case, `request:` and its id. |
| `--labels` | A labels file, below. |

At least one of `--cases` and `--evidence` is given.

## An evidence case

A request becomes a case with exactly one `ACTION_PROPOSED` and one
`POLICY_DECIDED`. Its envelope is the proposal's, its arguments `{}` with
their hash recomputed, since the evidence holds only a hash. It holds no flow
and no external answer. It is loaded at the decision's `policy_loaded_at` and
decided at its `decided_at`. The kernel runs no read open and applies every
obligation type, and no operator budget narrows the author's.

## Unknown

An unknown case has one reason, both sides' verdicts withheld, and is never
counted unchanged. The reasons, in the order they are judged:

| Reason | The case |
| --- | --- |
| `export_not_whole` | Lacks its proposal or decision, in an export that is not whole. |
| `incomplete_request` | Lacks one proposal or its decision, holds two proposals, or a decision without both times. |
| `several_decisions` | Holds more than one `POLICY_DECIDED`. |
| `historic_policy_missing` | Was decided on a policy whose digest is not `--current`'s. |
| `recorded_stale` | Was decided on a stale policy. |
| `not_evaluated` | A side did not load or the kernel refused the case's options. |
| `external_answer_needed` | A rule on either side waits on the decision point's answer. |
| `missing_facts` | A rule on either side is undetermined for a field or a flow input the case lacks. |
| `freshness_not_held` | Evidence: a side is stale at the recorded times. |
| `obligation_applicability_unknown` | Evidence: a side names a non-advisory obligation type the recorded decision does not carry. |
| `disagrees_with_record` | Evidence: the current side's verdict or codes differ from the recorded ones. |

## The change

The actions are ordered `Execute`, `ExecuteWithObligations`, `AwaitApproval`,
`Block`. An after further along is `stricter`; further back, `looser`. The
same action with the same verdict, codes and obligations, in order, is
`unchanged`; with any of them different, `other`.

## The report

`--report` names a file that does not exist; the report is forced to disk
under a temporary name beside it and linked there, mode `0600`. It is one
JSON object:

| Member | Holds |
| --- | --- |
| `kind` | `policy-comparison/v1alpha1`. |
| `simulated` | `true`. |
| `build` | This build's version. |
| `current`, `candidate` | `id`, `version` and `digest` of each policy. |
| `provenance.cases` | `dir` as given, `files`, and `digest`: SHA-256 over one line per file, in name order, of the file's SHA-256 in hex, two spaces and its name. `null` without `--cases`. |
| `provenance.evidence` | `path`, `sha256`, `whole`, `not_whole` (why not, or empty) and `requests`. `null` without `--evidence`. |
| `provenance.labels` | `path`, `sha256` and `labeller`. `null` without `--labels`. |
| `scope.evaluation` | How each case was decided. |
| `scope.not_seen` | What no case shows: live traffic, an evidence case's arguments and flow, a decision point's answer, approvals, the plane's own settings. |
| `scope.labeller_independence` | `declared, not verified`, or `no labels`. |
| `counts` | Below. |
| `cases` | One row per case, case files first, in name order, then requests as the export first names them. |

A row holds `id`, `source` (`case` or `evidence`), `simulated` (`true`),
`label` (`attack`, `legitimate` or `null`), `before` and `after`, `change`
(the four above or `unknown`), `unknown_reason` and `unknown_detail` (`null`
unless unknown). A side holds `evaluated`, then `verdict`, `action`, `codes`
and `obligations` (each `type`, `params`, `advisory`), all `null` on an
unknown case, and `missing_facts`: each input an undetermined rule lacked, in
the wire spelling [policy-explain.md](policy-explain.md#a-rules-line) uses.

## Counts

| Member | Counts | Out of |
| --- | --- | --- |
| `read` | Every case. | |
| `by_source` | `case` and `evidence`. | `read` |
| `evaluated_both_sides` | Cases both policies decided. | `read` |
| `unknown` | Unknown cases, and `by_reason`, every reason listed. | `read` |
| `compared` | Cases not unknown. | `read` |
| `change` | `unchanged`, `stricter`, `looser`, `other`. | `compared` |
| `by_label` | Per `attack`, `legitimate` and `unlabelled`: each change and `unknown`. | that label's cases |

Each count carries its denominator as `of`. Standard output repeats the
counts on one line each, after `report: <path>`.

## The labels file

| Member | Holds |
| --- | --- |
| `kind` | `policy-case-labels/v1alpha1`, the first member, checked before any other. |
| `labeller` | Who labelled, 1 to 200 bytes with no control character. |
| `labels` | 1 to 4096 entries of `case` (a case id of this run) and `label` (`attack` or `legitimate`), a case at most once. |

Strict JSON: an unknown, missing, repeated or `null` member, bytes that are
not UTF-8, and anything after the object are refused. A policy document names
no author, so whether the labeller is not the author is declared, never
checked.

## Bounds

| Bound | Value |
| --- | --- |
| Cases in a run, case files and requests together | 4096 |
| A case file, or a policy document | 2 MiB |
| A labels file | 1 MiB |
| An evidence export | 32 MiB |

## Exit status

| Status | Means |
| --- | --- |
| 0 | Every case was evaluated on both sides and none is unknown. |
| 1 | The report is written and holds an unknown case; or standard output failed after it was written. |
| 2 | Usage, a refused document, case, export or labels file, or a report not written. |
