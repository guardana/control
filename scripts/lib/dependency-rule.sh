#!/usr/bin/env bash
# shellcheck disable=SC2034 # every array is read by a script that sources this file
# The dependency rule's lists for the shell half of the gate. check-imports.sh
# enforces them; check-imports-probe.sh plants its fixture in every guarded
# tree. The depguard rules in .golangci.yml and internal/core/layering_test.go
# carry the same lists, and internal/core/layering_agreement_test.go fails when
# any two disagree.
#
# The rule is an allowlist. A package in a guarded tree may import a package of
# this module outside the denied trees, any package of a module in `modules`,
# and a standard library package named in `stdlib`. Nothing else.
#
# Each array holds one bare word per line: the agreement test reads this file
# as text.

# Trees that hold the decision path and must stay free of I/O.
guarded=(
  internal/core
  internal/policy
  internal/canon
  internal/evidence
  pkg/contract
)

# Trees of this module that a guarded tree must not import.
denied=(
  adapters
  internal/storage
  internal/controlapi
  internal/gateway
  internal/ingest
)

# Modules outside this one, every package of which a guarded tree may import.
modules=(
  google.golang.org/protobuf
)

# Trees of this module accepted whole, their own imports not examined.
# Generated code imports reflect and unsafe by construction, and proto-check
# keeps it equal to what the pinned generator writes.
generated=(
  api/gen
)

# Standard library packages a guarded tree may import, matched exactly: "io"
# does not admit "io/ioutil". None opens a connection, a file or a process,
# reads the environment or draws randomness, except through functions
# .golangci.yml refuses by name in the guarded trees (time.Now, time.Sleep,
# fmt.Scan, ed25519.GenerateKey and the like); fmt.Print is refused everywhere.
# A read that no name reveals is not refused: time.Unix(...).Format reads the
# local zone. crypto/ed25519 imports crypto/rand itself, which the rule does not
# examine.
stdlib=(
  bufio
  bytes
  cmp
  context
  crypto/ed25519
  crypto/sha256
  crypto/subtle
  encoding/binary
  encoding/hex
  encoding/json
  errors
  fmt
  io
  maps
  math
  math/bits
  slices
  sort
  strconv
  strings
  sync
  sync/atomic
  time
  unicode
  unicode/utf16
  unicode/utf8
)

# go list fields a package held to the rule may not hold a file in. The first
# two list what build constraints leave out on this platform, which a build for
# another platform compiles and the gate on this one never sees. The rest is
# every source the go command compiles or links besides plain Go, and any of
# those reaches the clock or the kernel with no import at all. A package held to
# the rule therefore builds from the same Go files on every platform.
refused_files=(
  IgnoredGoFiles
  IgnoredOtherFiles
  CgoFiles
  CFiles
  CXXFiles
  MFiles
  HFiles
  FFiles
  SFiles
  SwigFiles
  SwigCXXFiles
  SysoFiles
)
