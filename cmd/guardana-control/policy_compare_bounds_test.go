package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// padTo writes body with spaces after it to exactly n bytes, which a JSON
// reader skips.
func padTo(t *testing.T, path, body string, n int) {
	t.Helper()
	if len(body) > n {
		t.Fatalf("%d bytes do not fit in %d", len(body), n)
	}
	writeFixture(t, path, body+strings.Repeat(" ", n-len(body)))
}

func corpusCase(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(corpusPath("cases", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A directory of 4096 case files is read; one of 4097 is refused for the
// count before any file is read.
func TestPolicyCompareCaseCountBound(t *testing.T) {
	for _, n := range []int{4096, 4097} {
		dir := t.TempDir()
		for i := range n {
			writeFixture(t, filepath.Join(dir, fmt.Sprintf("c%04d.json", i)), "not a case")
		}
		report := filepath.Join(t.TempDir(), "r.json")
		code, _, stderr := invoke(t, compareArgsFor(report, "--cases", dir)...)
		bound := strings.Contains(stderr, "4097 case files, at most 4096")
		if code != exitUsage || bound != (n == 4097) {
			t.Errorf("%d files: exit %d, %q", n, code, stderr)
		}
	}
}

// A case file of 2 MiB is read; one byte more is refused for its size.
func TestPolicyCompareCaseFileBound(t *testing.T) {
	body := corpusCase(t, "read-order.json")
	for _, n := range []int{maxReadBytes, maxReadBytes + 1} {
		dir := t.TempDir()
		padTo(t, filepath.Join(dir, "read-order.json"), body, n)
		report := filepath.Join(t.TempDir(), "r.json")
		code, _, stderr := invoke(t, compareArgsFor(report, "--cases", dir)...)
		if n == maxReadBytes && code != exitOK {
			t.Errorf("%d bytes: exit %d, %q, want 0", n, code, stderr)
		}
		if n > maxReadBytes && (code != exitUsage || !strings.Contains(stderr, "over the size bound") || !strings.Contains(stderr, "read-order.json")) {
			t.Errorf("%d bytes: exit %d, %q, want the size refused", n, code, stderr)
		}
	}
}

// A labels file of 1 MiB is read; one byte more is refused for its size.
func TestPolicyCompareLabelsFileBound(t *testing.T) {
	body := `{"kind":"policy-case-labels/v1alpha1","labeller":"x","labels":[{"case":"case:read-order.json","label":"legitimate"}]}`
	cases := compareCasesDir(t, "read-order.json")
	for _, n := range []int{maxLabelsBytes, maxLabelsBytes + 1} {
		labels := filepath.Join(t.TempDir(), "labels.json")
		padTo(t, labels, body, n)
		report := filepath.Join(t.TempDir(), "r.json")
		code, _, stderr := invoke(t, compareArgsFor(report, "--cases", cases, "--labels", labels)...)
		if n == maxLabelsBytes && code != exitOK {
			t.Errorf("%d bytes: exit %d, %q, want 0", n, code, stderr)
		}
		if n > maxLabelsBytes && (code != exitUsage || !strings.Contains(stderr, "over the size bound")) {
			t.Errorf("%d bytes: exit %d, %q, want the size refused", n, code, stderr)
		}
	}
}

// An export one byte over the bound is refused for its size, before any of
// it is parsed; the file is sparse, so the test writes no such volume.
func TestPolicyCompareExportBound(t *testing.T) {
	export := filepath.Join(t.TempDir(), "export.jsonl")
	f, err := os.OpenFile(export, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600) //nolint:gosec // G304: the test's own file
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(maxCompareExportBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(t.TempDir(), "r.json")
	refused(t, report, compareArgsFor(report, "--evidence", export), "--evidence", "over the size bound")
}
