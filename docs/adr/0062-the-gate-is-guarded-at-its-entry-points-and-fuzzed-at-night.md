# ADR-0062: The gate is guarded at its entry points, and fuzzed at night

Status: accepted
Date: 2026-10-27

Amends [ADR-0052](0052-a-change-is-gated-on-its-pull-request.md): the nightly
run also fuzzes every target, and a pull request's gate checks only that each
target compiles.

## Context

`make quality` refuses what the environment can set before any recipe runs: a
dry run in `MAKEFLAGS`, `MAKEFILES`, `GOFLAGS`, the shell's startup files and
options, a stand-in for make or go. Three releases added such guards, with a
test that probes the make under test. Two ways around them remained, both on
the command line: `MAKEOVERRIDES=` emptied the list the guard read, so `make
quality MAKEOVERRIDES= SHELL=/usr/bin/true` ran nothing and exited 0; and
`make -f Makefile -f later.mk quality`, where `later.mk` holds its own
`quality` recipe, printed the pass line. No makefile can refuse a makefile
read after it. Whoever writes make's command line can as well not run the
gate.

The exposure a pull request can use was elsewhere. A workflow step can write
`SHELLOPTS=noexec` or `BASH_ENV` to `$GITHUB_ENV`, and the runner starts the
next step's bash with it, before make reads anything: with `SHELLOPTS=noexec`
the gate step runs nothing and succeeds. The workflow test read only the
quality job, and other required checks run in jobs that write `$GITHUB_ENV`
too.

Of the gate's 29 minutes on a pull request, 15.6 were `fuzz-smoke`: ten
seconds of fuzzing for each of 76 targets. Every target's seeds already run
in `test` and `test-race`. `docs-check` ran its packages a third time.

## Decision

- **The gate's result holds as its two entry points start it.** CI starts
  every run step of every workflow in `bash --noprofile --norc -p -e -o
  pipefail {0}`: with `-p`, bash reads neither `BASH_ENV` nor `ENV` and
  ignores `SHELLOPTS`, `BASHOPTS`, `CDPATH`, `GLOBIGNORE` and functions from
  the environment. The gate step runs `/usr/bin/env -u MAKEFLAGS -u
  GNUMAKEFLAGS -u MFLAGS -u MAKEFILES make quality GO=go`.
  `scripts/gate-commit.sh` starts make in an emptied environment, as before.
- **The Makefile keeps its guards on what an environment sets**, and refuses
  every command-line variable but `GO` by its `$(origin)`, which a command
  line cannot change. It states that a makefile read after it is beyond any
  guard, and stops there: no further guard is added for an invocation outside
  the two entry points.
- **Fuzzing runs at night and on the release commit.** `make quality` runs
  `fuzz-list`, which checks that every fuzz target declared in the source is
  compiled on the platform, and fuzzes nothing. The nightly workflow runs
  `make fuzz-smoke` on Linux, and a red night opens an issue as the race run
  does. `scripts/gate-commit.sh` runs `make fuzz-smoke` after `make quality`,
  so a release commit is fuzzed before it is tagged.
- `docs-check` stays a target and leaves `quality`, whose `test` and
  `test-race` run the same packages.

## Security / compatibility impact

No product code changes. A pull request is no longer fuzzed past its seeds: a
change that makes a target find a failing input within ten seconds is caught
by the next night's run or by the release commit's cold gate, not before it
merges. A step that writes a shell's startup state to `$GITHUB_ENV` no longer
changes what the next step's bash runs; a variable make reads still reaches
make, whose guards refuse it loudly. A caller who passes `-f` with another
makefile can still replace any recipe, and the page says the result does not
hold for such a run.

## Alternatives considered

- Keep extending the Makefile's guards: each make version reads its flags
  differently, the probes grow with each, and the makefile read after this
  one stays out of reach.
- Name `SHELLOPTS` and its kin in the workflow test's list of refused names:
  the allowed write to `$GITHUB_ENV` carries any text, and a name can be
  assembled from parts.
- `env -i` in the workflow's shell: the runner does not expand `$PATH` there,
  and the gate needs the tools a step put on it.
- Fuzz for three seconds on each pull request: still seven minutes, and the
  seeds already cover what a short run reaches.

## Consequences

A pull request's gate takes about half as long. The nightly workflow is the
first place a fuzz finding shows on `main`. Every workflow carries the same
shell line, and a step that needs another shell fails the workflow test until
this record is amended.

## Validation

`internal/docscheck`: `TestEveryWorkflowStartsItsShellClean` holds every
workflow with a run step to the shell line and refuses a `shell:` anywhere
else; `TestTheShellIgnoresTheStartupStateOfTheEnvironment` runs that line,
with the bash found on the path, over a script that exits 7 under
`SHELLOPTS=noexec`, `BASH_ENV` and an exported `echo`, after showing that
GitHub's plain bash line is changed by each; `TestFuzzingRunsOutsideTheGate`
holds the nightly step and the cold gate's `make fuzz-smoke`;
`TestTheGateRefusesALoosenedMake` refuses `MAKEOVERRIDES=` with a replaced
shell; `TestQualityRunsTheWholeGate` holds `fuzz-list` in quality.
`scripts/check-modules-probe.sh` runs `fuzz-list` clean and refuses a target a
build constraint leaves out.
