package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/pkg/contract"
)

// signingSeed is the fixed seed of the key every case is signed with. The
// signature only has to verify against the key the same run loads; a fixed
// key keeps two runs of a directory byte-identical, which a key drawn from
// the system's randomness would not.
const signingSeed = "fixed seed for policy test cases"

const signingKeyID = "policy-test"

// policyTest runs every case of dir through the kernel and prints one line
// per case: `ok   name: VERDICT Action [CODES]`, or `FAIL name: ...` with the
// mismatch or the reason the case could not run. It exits 1 when any case
// fails and when the directory holds no case, so an empty directory is
// never a pass.
func policyTest(dir string, stdout, stderr io.Writer) int {
	names, err := caseFiles(dir)
	if err != nil {
		return fail(stderr, "policy test", err)
	}
	status := exitOK
	for _, name := range names {
		line, ok := runCase(filepath.Join(dir, name))
		mark := "ok  "
		if !ok {
			mark = "FAIL"
			status = exitFail
		}
		if _, err := fmt.Fprintf(stdout, "%s %s: %s\n", mark, oneLine(name), line); err != nil {
			return fail(stderr, "policy test", fmt.Errorf("writing to standard output: %w", err))
		}
	}
	return status
}

// caseFiles lists the regular files of dir named *.json, in name order, and
// refuses a directory that holds none. Regularity is decided through Stat,
// which follows a link, so a linked case is run rather than skipped without
// a word; a link that leads nowhere is a refusal.
func caseFiles(dir string) ([]string, error) {
	dir = filepath.Clean(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if info.Mode().IsRegular() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%s: no *.json case in the directory", dir)
	}
	return names, nil
}

// runCase reads, signs, loads and decides one case, and reports its line and
// whether the decision was the one expected. The line is one line whatever
// the case's own text holds.
func runCase(path string) (string, bool) {
	raw, err := readBounded(path)
	if err != nil {
		return oneLine(err.Error()), false
	}
	c, err := readCase(raw)
	if err != nil {
		return oneLine(err.Error()), false
	}
	got, err := decide(c)
	if err != nil {
		return oneLine(err.Error()), false
	}
	if got.verdict != c.expect.verdict || got.action != c.expect.action || !slices.Equal(got.codes, c.expect.codes) {
		return oneLine(fmt.Sprintf("got %s, want %s", got, c.expect)), false
	}
	return got.String(), true
}

// decide signs the case's document in memory, loads it at loaded_at and
// decides the envelope at decided_at. The envelope goes through the JSON
// decoder a receiver would use, and what that decoder refuses is handed to
// the kernel as its refusal rather than stopping the case.
func decide(c *testCase) (outcome, error) {
	key := ed25519.NewKeyFromSeed([]byte(signingSeed))
	signed, err := policy.Sign(c.document, key, signingKeyID)
	if err != nil {
		return outcome{}, err
	}
	keys := bundle.Keyring{signingKeyID: key.Public().(ed25519.PublicKey)}
	snap, err := policy.Load(signed, keys, c.loadedAt)
	if err != nil {
		return outcome{}, err
	}
	kernel, err := core.New(c.options, func() time.Time { return c.decidedAt }, func() string { return "policy-test" })
	if err != nil {
		return outcome{}, err
	}
	env, refusal := contract.DecodeJSON(c.envelope)
	out := kernel.Decide(context.Background(), core.Request{
		Envelope:       env,
		Refusal:        refusal,
		AuthorizedArgs: c.authorizedArgs,
		Flow:           c.flow,
		External:       c.external,
	}, snap)
	if out.Decision == nil {
		return outcome{}, errors.New("the kernel returned no decision")
	}
	return outcome{
		verdict: out.Decision.GetVerdict(),
		action:  out.Action,
		codes:   out.Decision.GetReasonCodes(),
	}, nil
}

// String spells an outcome the way a case's expect member does, so a line
// and the member an author would write to make it pass read the same.
func (o outcome) String() string {
	verdict := strings.TrimPrefix(controlv1.Verdict_name[int32(o.verdict)], "VERDICT_")
	if verdict == "" {
		verdict = fmt.Sprintf("VERDICT(%d)", o.verdict)
	}
	action := fmt.Sprintf("EnforcementAction(%d)", o.action)
	for name, a := range actions {
		if a == o.action {
			action = name
		}
	}
	return fmt.Sprintf("%s %s [%s]", verdict, action, strings.Join(o.codes, " "))
}
