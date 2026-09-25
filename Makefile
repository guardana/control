# The one quality gate. CI runs these target names, so do not rename them.
SHELL := /usr/bin/env bash
.SHELLFLAGS := -eu -o pipefail -c
GO ?= go
RANGE ?= HEAD~1..HEAD
PKGS := ./...
FUZZTIME ?= 10s

# This Makefile's own directory. Recipes use paths relative to it, so a recipe
# started from anywhere else changes there first rather than half-working on
# paths that happen to resolve.
ROOT := $(patsubst %/,%,$(dir $(abspath $(firstword $(MAKEFILE_LIST)))))

# make splits variables into whitespace-separated words, so $(dir) and
# $(abspath) above silently truncate a checkout path that contains a space and
# leave ROOT pointing at a parent directory. Refuse to run rather than run the
# gate somewhere else. A quote in the path is fine; a space is not fixable here.
ifeq ($(wildcard $(ROOT)/scripts/lib/repo-files.sh),)
$(error cannot locate the repository root: computed $(ROOT). A path containing \
        whitespace breaks GNU make word splitting; move the checkout)
endif

# The root reaches the recipes through the environment rather than being
# interpolated into the recipe text, so a checkout path containing a quote
# cannot break the shell line make generates.
export REPO_ROOT := $(ROOT)

# Empty in the normal case, where make already runs at the root. That keeps the
# echoed command lines short enough to read the gate as a transcript.
#
# CD uses && so a failed cd stops the command instead of running it in whatever
# directory make happened to start in; RUN gets the same protection from set -e.
ifeq ($(CURDIR),$(ROOT))
CD :=
RUN := set -euo pipefail;
else
CD := cd -- "$$REPO_ROOT" &&
RUN := set -euo pipefail; cd -- "$$REPO_ROOT";
endif

# The project's only file enumerator: git ls-files where a work tree exists,
# find otherwise.
REPO_FILES := scripts/lib/repo-files.sh

# Hand-written Go. Generated code under api/gen/ is never reformatted.
GO_SRC := . $(REPO_FILES); repo_files '*.go' | { grep -v '^api/gen/' || true; }

# The gate is read as a transcript; -j would interleave the targets' output
# and hide which one reported what.
.NOTPARALLEL:

# A bare `make` must not run bootstrap, which installs software.
.DEFAULT_GOAL := quality-quick

.PHONY: bootstrap fmt fmt-check vet lint test test-race fuzz-smoke security \
        proto proto-check proto-breaking docs-check docs-gen docs-impact tidy-check check-brand \
        release-snapshot \
        check-imports check-imports-probe check-sizes check-actions \
        check-shell quality-quick quality

bootstrap:
	$(CD) scripts/bootstrap.sh

fmt:
	@$(RUN) \
	files=$$($(GO_SRC)); \
	if [[ -z "$$files" ]]; then echo "fmt: no Go source found" >&2; exit 1; fi; \
	gofmt -w $$files; \
	$(GO) tool goimports -w $$files

# Both tools, because fmt runs both: gofmt alone accepts imports that goimports
# would regroup, which would let `quality-quick` pass on a file `make fmt` is
# about to rewrite.
fmt-check:
	@$(RUN) \
	files=$$($(GO_SRC)); \
	if [[ -z "$$files" ]]; then echo "fmt-check: no Go source found" >&2; exit 1; fi; \
	badfmt=$$(gofmt -l $$files); \
	badimports=$$($(GO) tool goimports -l $$files); \
	if [[ -n "$$badfmt$$badimports" ]]; then \
	  [[ -z "$$badfmt" ]] || { echo "fmt-check: not gofmt-clean:" >&2; echo "$$badfmt" >&2; }; \
	  [[ -z "$$badimports" ]] || { echo "fmt-check: not goimports-clean:" >&2; echo "$$badimports" >&2; }; \
	  exit 1; \
	fi; \
	echo "fmt-check: $$(echo "$$files" | wc -l | tr -d ' ') file(s) clean (gofmt, goimports)"

vet:
	$(CD) $(GO) vet $(PKGS)
	@$(RUN) \
	gens=$$(. $(REPO_FILES); repo_files 'scripts/*.go'); \
	if [[ -z "$$gens" ]]; then \
	  echo "vet: SKIP generators, none under scripts/"; \
	else \
	  for g in $$gens; do \
	    $(GO) build -o /dev/null "$$g" || exit 1; \
	  done; \
	  echo "vet: $$(echo "$$gens" | wc -l | tr -d ' ') generator(s) compile"; \
	fi

# The configuration is named, not discovered: golangci-lint prefers a
# .golangci.yaml, .golangci.toml or .golangci.json beside .golangci.yml, so a
# file dropped at the root would replace the one the security maintainers own
# and the agreement tests read.
lint:
	$(CD) golangci-lint run --config .golangci.yml ./...

test:
	$(CD) $(GO) test -count=1 -shuffle=on $(PKGS)

test-race:
	$(CD) $(GO) test -count=1 -race $(PKGS)

fuzz-smoke:
	$(CD) scripts/fuzz-smoke.sh $(FUZZTIME)

# actionlint reads workflows only: handed a composite action it parses it as a
# workflow and reports five syntax errors that are not there. zizmor reads
# both, so it is the one that audits .github/actions.
#
# Neither takes a setting from the tree, since each would silence a finding:
# actionlint reads an empty configuration instead of .github/actionlint.yaml,
# and the shellcheck it runs over each run: block reads no rc file; zizmor
# loads no zizmor.yml and honours no "zizmor: ignore" comment.
security:
	$(CD) $(GO) tool govulncheck ./...
	$(CD) scripts/scan-repo-files.sh
	@$(RUN) \
	workflows=$$(. $(REPO_FILES); repo_files '.github/workflows/*.yml' '.github/workflows/*.yaml'); \
	actions=""; \
	if [[ -d .github/actions ]]; then \
	  actions=$$(. $(REPO_FILES); repo_files '.github/actions/*action.yml' '.github/actions/*action.yaml'); \
	fi; \
	if [[ -z "$$workflows" && -d .github/workflows ]]; then \
	  echo "security: .github/workflows exists but no workflow file was listed; refusing to report a pass" >&2; \
	  exit 1; \
	fi; \
	if [[ -z "$$actions" && -d .github/actions ]]; then \
	  echo "security: .github/actions exists but no action file was listed; refusing to report a pass" >&2; \
	  exit 1; \
	fi; \
	if [[ -n "$$workflows" ]]; then \
	  echo "$$workflows" | SHELLCHECK_OPTS=--norc xargs actionlint -config-file /dev/null; \
	else \
	  echo "security: SKIP actionlint, no .github/workflows directory"; \
	fi; \
	if [[ -n "$$workflows$$actions" ]]; then \
	  printf '%s\n' $$workflows $$actions | xargs zizmor --no-config --no-ignores; \
	else \
	  echo "security: SKIP zizmor, no workflow or composite action file"; \
	fi

proto:
	$(CD) buf generate
	$(CD) buf lint

# The regeneration check compares a fresh tree against the committed one
# rather than asking git for a diff, so it gives the same answer in an export
# with no history.
proto-check:
	$(CD) buf lint
	$(CD) buf format -d --exit-code
	@$(RUN) \
	tmp=$$(mktemp -d); \
	trap 'rm -rf "$$tmp"' EXIT; \
	buf generate --output "$$tmp"; \
	diff -r "$$tmp/api/gen" api/gen

# Wire compatibility against main, as CI checks a pull request. Outside
# quality: an export has no history to compare with, and the script then prints
# UNKNOWN and fails, never a pass.
proto-breaking:
	$(CD) scripts/proto-breaking.sh

docs-check:
	$(CD) $(GO) test -count=1 ./internal/docscheck/...

# Rewrites the generated reference pages. Deliberately outside `quality`: a gate
# that regenerates a page cannot also report that the committed page had
# drifted. TestReasonCodesDocIsCurrent and TestObligationsDocIsCurrent report
# that.
# Names the pages a diff makes suspect. Not part of quality: it needs git and
# a range, and says NOT MEASURED when it has neither.
docs-impact:
	$(CD) $(GO) run scripts/docs-impact.go --range $(RANGE)

docs-gen:
	$(CD) $(GO) run scripts/gen-reason-codes.go -o docs/reference/reason-codes.md
	$(CD) $(GO) run scripts/gen-obligations.go -o docs/reference/obligations.md
	$(CD) $(GO) run scripts/gen-diagrams.go -o docs/concepts/enforcement-modes.md
	$(CD) $(GO) run scripts/gen-diagrams.go -o docs/concepts/how-a-call-is-decided.md
	$(CD) $(GO) run scripts/gen-diagrams.go -o docs/concepts/evidence-and-the-spool.md
	$(CD) $(GO) run scripts/gen-metrics.go -o docs/reference/metrics.md
	$(CD) $(GO) run scripts/gen-index.go -o docs/index.md
	$(CD) $(GO) run scripts/gen-config.go -o docs/reference/configuration.md
	$(CD) $(GO) run scripts/gen-wire.go -o docs/reference/wire/action_envelope.md action_envelope.proto
	$(CD) $(GO) run scripts/gen-wire.go -o docs/reference/wire/approval.md approval.proto
	$(CD) $(GO) run scripts/gen-wire.go -o docs/reference/wire/bundle.md bundle.proto
	$(CD) $(GO) run scripts/gen-wire.go -o docs/reference/wire/common.md common.proto
	$(CD) $(GO) run scripts/gen-wire.go -o docs/reference/wire/decision.md decision.proto
	$(CD) $(GO) run scripts/gen-wire.go -o docs/reference/wire/event.md event.proto
	$(CD) $(GO) run scripts/gen-wire.go -o docs/reference/wire/finding.md finding.proto
	$(CD) $(GO) run scripts/gen-wire.go -o docs/reference/wire/result.md result.proto

check-brand:
	$(CD) scripts/check-brand.sh

# The archives, bills of materials and checksums a tag would publish, built
# into dist/ without a signature. Not part of quality: it builds eight binaries
# and needs goreleaser and syft. --parallelism=1 keeps each archive's digest
# the same from run to run.
release-snapshot:
	$(CD) goreleaser release --snapshot --clean --skip=sign --parallelism=1

check-imports:
	$(CD) scripts/check-imports.sh

# The dependency rule's negative control: plants the rule's fixture in every
# package of every guarded tree of a staged copy of the repository, and fails
# unless every mechanism refuses it.
check-imports-probe:
	$(CD) scripts/check-imports-probe.sh

# go.mod and go.sum exactly as `go mod tidy` would write them, so a module a
# change starts importing, or stops needing, shows up here and not in review.
# The pass line is part of the command it reports on, so a recipe that ignored
# the command's failure would lose the line with it.
tidy-check:
	$(CD) $(GO) mod tidy -diff && echo "tidy-check: go.mod and go.sum are tidy"

check-sizes:
	$(CD) scripts/check-file-sizes.sh

check-actions:
	$(CD) scripts/check-actions-pinned.sh

# Every shell script repo_files lists; scripts/check-shell.sh says what it
# refuses besides shellcheck's own findings.
check-shell:
	$(CD) scripts/check-shell.sh

quality-quick: fmt-check vet test check-imports

quality: fmt-check vet lint test test-race fuzz-smoke security proto-check \
         docs-check tidy-check check-imports check-imports-probe check-brand \
         check-sizes check-actions check-shell
	@echo "quality: green"
