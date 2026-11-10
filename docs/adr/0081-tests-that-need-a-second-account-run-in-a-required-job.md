# ADR-0081: Tests that need a second account run in a required job

Status: accepted
Date: 2026-11-10

Amends [ADR-0063](0063-a-skipped-test-fails-the-gate-unless-the-repository-expects-it.md),
whose gate runs no test as root. Builds on
[ADR-0052](0052-a-change-is-gated-on-its-pull-request.md) and
[ADR-0073](0073-an-approval-counts-only-as-the-plane-judges-it.md).

## Context

Several controls decide by the account on the other side: the health socket
answers its own account and `health.socket_uid`, the exporter sends the
credential only to a collector of `export.collector_uid`, a signed start
refuses a file the plane's own account could change, and a signed runs
directory's `records/` belongs to the operators while the plane only reads
it. `make quality` runs as one ordinary account. Its tests reach these checks
through an injected peer uid or a file of their own account, so the kernel's
answer for a second account, the socket's mode and a directory that account
cannot search went untested. Five tests that give a file away skip without
root; ADR-0063 lists them as expected skips, and no gate of record ran them.

## Decision

The CI job "Two accounts" in `ci.yml`, a required check, makes two system
accounts with `useradd`, sets their uids in `GUARDANA_TEST_OTHER_UID` and
`GUARDANA_TEST_THIRD_UID`, and runs `scripts/two-accounts.sh`. The script
builds each package `scripts/expected-runs.txt` names with the build tag
`twoaccounts` and runs only the listed tests, as root, through
`go tool test2json`. A test runs as root and hands each account its part
through a child process of its own binary started as that account, so the
same test sets the ownership up and judges it.

`scripts/check-runs.go`, over `internal/testreport.SummarizeRuns`, reads every
event and fails unless each listed test passed: a skip anywhere, a listed test
missing, a failed build, a stream with no event and a list that names nothing
each fail the job. The tagged files are not compiled by `make quality`, so the
expected skips need no entry for them; the root tests of those skips are in
the list too.

## Security / compatibility impact

No product code changes. The job runs repository code as root on a disposable
runner, with a read-only token, no secret and no cache write. A test of this
kind that stops running fails a required check rather than passing unseen.

## Alternatives considered

- A nightly job only: a regression lands, and is found a day later by whoever
  reads the nightly run.
- Ordinary tests that skip without root or the variables: the main gate would
  list each one as an expected skip, and nothing would refuse a run in which
  they all skipped.
- Running each test binary as the account it needs, with `sudo -u`: the files
  each case needs belong to other accounts, which only root can arrange, and a
  test split over three invocations cannot judge its own setup.

## Consequences

Changing the repository's required checks is the maintainer's step on GitHub;
`scripts/lib/github-settings.sh` declares the new one. A test added to the
list runs only on Linux; on macOS the tagged tests build and skip without the
variables.

## Validation

`internal/testreport`'s tests refuse a planted stream with a skip, a missing
test, no event, a failed build, a failed test and a pass from another package,
and refuse an empty or malformed list. The job's own transcript names each
listed test `PASSED`.
