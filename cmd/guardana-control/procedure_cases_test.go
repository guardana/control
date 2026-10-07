package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guardana/control/internal/brand"
)

const (
	rootRun  = "run-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	childRun = "run-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// editedCases copies the worked example's directory and writes file into
// the copy as edit leaves it, so its relative paths still name the example's
// procedure and export. It returns the edited document's path.
func editedCases(t *testing.T, file string, edit func(doc map[string]any)) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(procedureDir)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(procedureDir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(dir, e.Name()), string(body))
		if e.Name() == file {
			raw = body
		}
	}
	if raw == nil {
		t.Fatalf("%s holds no %s", procedureDir, file)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var doc map[string]any
	if err := dec.Decode(&doc); err != nil {
		t.Fatal(err)
	}
	edit(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, file)
	writeFixture(t, path, string(out))
	return path
}

// caseOf is the case at i of doc, as a member map.
func caseOf(t *testing.T, doc map[string]any, i int) map[string]any {
	t.Helper()
	cases, _ := doc["cases"].([]any)
	if i >= len(cases) {
		t.Fatalf("the document holds %d cases", len(cases))
	}
	c, _ := cases[i].(map[string]any)
	return c
}

// expectedOf is the expect member of case i.
func expectedOf(t *testing.T, doc map[string]any, i int) map[string]any {
	t.Helper()
	e, _ := caseOf(t, doc, i)["expect"].(map[string]any)
	return e
}

// findingOf is the first expected finding of case i.
func findingOf(t *testing.T, doc map[string]any, i int) map[string]any {
	t.Helper()
	fs, _ := expectedOf(t, doc, i)["findings"].([]any)
	if len(fs) == 0 {
		t.Fatalf("case %d expects no finding", i)
	}
	f, _ := fs[0].(map[string]any)
	return f
}

// failedCase runs the document and wants exit 1, the case named failing
// with a line holding each of want, and the summary counting one failure.
func failedCase(t *testing.T, path, name string, want ...string) {
	t.Helper()
	code, stdout, stderr := invoke(t, "procedure", "test", path)
	if code != exitFail || stderr != "" {
		t.Fatalf("exit %d, stderr %q; want 1 and nothing on stderr\n%s", code, stderr, stdout)
	}
	var line string
	fails := 0
	for _, l := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(l, "FAIL ") {
			fails++
		}
		if strings.HasPrefix(l, "FAIL "+name+": ") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no FAIL line for %q:\n%s", name, stdout)
	}
	for _, w := range want {
		if !strings.Contains(line, w) {
			t.Errorf("%q does not hold %q", line, w)
		}
	}
	if fails != 1 || !strings.HasSuffix(stdout, ": 3 cases, 2 passed, 1 failed\n") {
		t.Errorf("not one failure among three cases:\n%s", stdout)
	}
}

// TestAFindingThatDiffersFailsAndSaysWhat: an expected finding that differs
// from the one made in its verdict, its run, its rule or what it cites fails
// its case, and the line names the field, what was found and what was
// expected.
func TestAFindingThatDiffersFailsAndSaysWhat(t *testing.T) {
	for name, c := range map[string]struct {
		field string
		value any
		want  string
	}{
		"verdict":  {"verdict", "SUSPECTED", "verdict CONFIRMED, want SUSPECTED"},
		"run":      {"run", childRun, "run " + rootRun + ", want " + childRun},
		"rule":     {"rule", "STEP_OUTSIDE_PROCEDURE", "rule RESOURCE_OUTSIDE_RUN, want STEP_OUTSIDE_PROCEDURE"},
		"requests": {"requests", []any{"r1"}, "requests [r1 r2], want [r1]"},
	} {
		t.Run(name, func(t *testing.T) {
			path := editedCases(t, "cases-0.2.json", func(doc map[string]any) { findingOf(t, doc, 1)[c.field] = c.value })
			failedCase(t, path, "a resource outside the run", c.want)
		})
	}
}

// TestAMissingOrAnUnexpectedFindingFails: a finding expected and not made
// fails, and so does a finding made and not expected; an empty expectation
// is a claim that no finding is made, never a pass by default.
func TestAMissingOrAnUnexpectedFindingFails(t *testing.T) {
	path := editedCases(t, "cases-0.2.json", func(doc map[string]any) { expectedOf(t, doc, 2)["findings"] = []any{} })
	failedCase(t, path, "a denied refund retried with other arguments",
		"unexpected finding DENIED_ACTION_RETRIED_ARGUMENTS CONFIRMED "+rootRun+" [r2 r3]")

	path = editedCases(t, "cases-0.1.json", func(doc map[string]any) {
		expectedOf(t, doc, 0)["findings"] = []any{map[string]any{"rule": "REQUIRED_STEP_SKIPPED",
			"verdict": "CONFIRMED", "run": rootRun, "requests": []any{}}}
	})
	failedCase(t, path, "the procedure kept", "missing finding REQUIRED_STEP_SKIPPED CONFIRMED "+rootRun+" []")
}

// TestARuleStateMustBeExpectedExactly: a rule whose state differs fails,
// and so does a rule the report holds that the expectation leaves out.
func TestARuleStateMustBeExpectedExactly(t *testing.T) {
	path := editedCases(t, "cases-0.1.json", func(doc map[string]any) {
		rules, _ := expectedOf(t, doc, 2)["rules"].(map[string]any)
		rules["STEP_OUT_OF_ORDER"] = "CHECKED"
	})
	failedCase(t, path, "a run still open", "rule STEP_OUT_OF_ORDER: state NOT_CHECKED, want CHECKED")

	path = editedCases(t, "cases-0.2.json", func(doc map[string]any) {
		rules, _ := expectedOf(t, doc, 0)["rules"].(map[string]any)
		delete(rules, "EXCEPTION_TAKEN")
	})
	failedCase(t, path, "a waiver taken", "rule EXCEPTION_TAKEN: state CHECKED, not expected")
}

// TestACaseItsInputRefusesFails: a case supervise refuses to judge, here a
// tree whose child comes from another tenant, fails with the refusal.
func TestACaseItsInputRefusesFails(t *testing.T) {
	path := editedCases(t, "cases-0.2.json", func(doc map[string]any) {
		tree, _ := caseOf(t, doc, 0)["tree"].([]any)
		child, _ := tree[0].(map[string]any)
		child["tenant"] = "t2"
	})
	failedCase(t, path, "a waiver taken", "refused: ", "another tenant")
}

// TestACasesDocumentItCannotReadIsRefused: exit 2, nothing on stdout and
// one line naming what was refused, for a member the format does not have
// at each level, a version it does not read, a missing expectation, no case,
// a case named twice, and a procedure or an export it cannot read.
func TestACasesDocumentItCannotReadIsRefused(t *testing.T) {
	for name, c := range map[string]struct {
		file string
		edit func(doc map[string]any)
		want string
	}{
		"unknown at the top":    {"cases-0.2.json", func(d map[string]any) { d["extra"] = true }, "extra: a member the cases format does not have"},
		"unknown in a case":     {"cases-0.2.json", func(d map[string]any) { caseOf(t, d, 1)["expected"] = true }, "cases.2.expected: a member"},
		"unknown in an expect":  {"cases-0.2.json", func(d map[string]any) { expectedOf(t, d, 0)["verdict"] = "CONFIRMED" }, "cases.1.expect.verdict: a member"},
		"unknown in a finding":  {"cases-0.2.json", func(d map[string]any) { findingOf(t, d, 0)["anchor"] = "x" }, "cases.1.expect.findings.1.anchor: a member"},
		"version 0.2":           {"cases-0.2.json", func(d map[string]any) { d["schema_version"] = "0.2" }, "schema_version: not 0.1"},
		"version as a number":   {"cases-0.2.json", func(d map[string]any) { d["schema_version"] = json.Number("0.1") }, "schema_version: want a string"},
		"no expect":             {"cases-0.2.json", func(d map[string]any) { delete(caseOf(t, d, 0), "expect") }, "cases.1.expect: missing"},
		"no expected findings":  {"cases-0.2.json", func(d map[string]any) { delete(expectedOf(t, d, 0), "findings") }, "cases.1.expect.findings: missing"},
		"no expected rules":     {"cases-0.2.json", func(d map[string]any) { delete(expectedOf(t, d, 0), "rules") }, "cases.1.expect.rules: missing"},
		"null findings":         {"cases-0.2.json", func(d map[string]any) { expectedOf(t, d, 0)["findings"] = nil }, "cases.1.expect.findings: want a list"},
		"unknown verdict":       {"cases-0.2.json", func(d map[string]any) { findingOf(t, d, 0)["verdict"] = "PROBABLE" }, "cases.1.expect.findings.1.verdict: want"},
		"unspecified verdict":   {"cases-0.2.json", func(d map[string]any) { findingOf(t, d, 0)["verdict"] = "UNSPECIFIED" }, "cases.1.expect.findings.1.verdict: want"},
		"unknown rule state":    {"cases-0.2.json", func(d map[string]any) { rulesOf(t, d, 0)["EXCEPTION_TAKEN"] = "DONE" }, "cases.1.expect.rules.EXCEPTION_TAKEN: want"},
		"a request twice":       {"cases-0.2.json", func(d map[string]any) { findingOf(t, d, 1)["requests"] = []any{"r1", "r1"} }, "cases.2.expect.findings.1.requests: r1 twice"},
		"no case":               {"cases-0.2.json", func(d map[string]any) { d["cases"] = []any{} }, "cases: no case"},
		"a name twice":          {"cases-0.2.json", func(d map[string]any) { caseOf(t, d, 1)["name"] = "a waiver taken" }, "cases.2.name: given to two cases"},
		"an unknown event key":  {"cases-0.2.json", func(d map[string]any) { firstEvent(t, d)["eventID"] = "x" }, "cases.1.evidence.1.events.1: "},
		"whole left out":        {"cases-0.2.json", func(d map[string]any) { delete(evidenceOf(t, d), "whole") }, "cases.1.evidence.1.whole: missing"},
		"a path and events":     {"cases-0.2.json", func(d map[string]any) { evidenceOf(t, d)["path"] = "x.jsonl" }, "cases.1.evidence.1: a path or events, not both"},
		"a closed run unstated": {"cases-0.2.json", func(d map[string]any) { delete(caseOf(t, d, 0)["run"].(map[string]any), "closed") }, "cases.1.run.closed: missing"},
		"a local last_heard":    {"cases-0.2.json", func(d map[string]any) { sourceOf(t, d, 2)["last_heard"] = "2026-10-05T11:00:00+01:00" }, "cases.3.sources.1.last_heard: want a UTC time"},
		"a missing procedure":   {"cases-0.2.json", func(d map[string]any) { d["procedure"] = "none.json" }, "none.json"},
		"a refused procedure":   {"cases-0.2.json", func(d map[string]any) { d["procedure"] = "cases-0.1.json" }, "procedure refused"},
		"a missing export":      {"cases-0.1.json", func(d map[string]any) { evidenceOf(t, d)["path"] = "none.jsonl" }, "none.jsonl"},
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := invoke(t, "procedure", "test", editedCases(t, c.file, c.edit))
			line := oneStderrLine(t, stderr)
			if code != exitUsage || stdout != "" {
				t.Fatalf("exit %d, stdout %q, stderr %q; want 2 and nothing printed", code, stdout, stderr)
			}
			if !strings.HasPrefix(line, brand.CLI+": procedure test: ") || !strings.Contains(line, c.want) {
				t.Errorf("stderr %q, want a line under the command holding %q", line, c.want)
			}
		})
	}
}

func rulesOf(t *testing.T, doc map[string]any, i int) map[string]any {
	t.Helper()
	r, _ := expectedOf(t, doc, i)["rules"].(map[string]any)
	return r
}

// evidenceOf is the first export of the first case.
func evidenceOf(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	ev, _ := caseOf(t, doc, 0)["evidence"].([]any)
	x, _ := ev[0].(map[string]any)
	return x
}

func firstEvent(t *testing.T, doc map[string]any) map[string]any {
	t.Helper()
	evs, _ := evidenceOf(t, doc)["events"].([]any)
	e, _ := evs[0].(map[string]any)
	return e
}

func sourceOf(t *testing.T, doc map[string]any, i int) map[string]any {
	t.Helper()
	ss, _ := caseOf(t, doc, i)["sources"].([]any)
	if len(ss) == 0 {
		t.Fatalf("case %d names no source", i)
	}
	s, _ := ss[0].(map[string]any)
	return s
}
