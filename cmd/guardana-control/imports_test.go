package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/guardana/control/internal/brand"
)

// approverPlatforms are the systems the release builds the binaries for, and
// windows, which it does not: a file only one system compiles can import what
// the host's own listing never shows.
var approverPlatforms = []string{
	"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64",
}

// planeTrees are the plane's side of the approval seam.
var planeTrees = []string{brand.ModulePath + "/internal/gateway", brand.ModulePath + "/internal/evidence"}

// TestTheApproverLinksNeitherTheGatewayNorEvidence: this binary answers an
// approval by writing a record into a directory. Reading that record back,
// deciding on it and appending evidence are the plane's side of the seam, and
// a build that reached either from here would put both sides in one binary and
// make the compiler's separation of the two handles decoration. `go list
// -deps` walks the non-test build transitively, once per release platform.
func TestTheApproverLinksNeitherTheGatewayNorEvidence(t *testing.T) {
	t.Parallel()
	listings := platformDependencies(t, ".")
	for i, platform := range approverPlatforms {
		// A listing that does not reach the store this binary answers through
		// examined some other package, and would pass whatever it found.
		if !slices.Contains(listings[i], brand.ModulePath+"/internal/approvals") {
			t.Errorf("go list -deps for %s reached %d package(s) and not the approvals store; nothing was examined",
				platform, len(listings[i]))
			continue
		}
		for _, dep := range planeReach(listings[i]) {
			t.Errorf("this binary reaches %s on %s", dep, platform)
		}
	}
}

// TestTheApproverCheckFindsWhatTheGatewayLinks is the negative control: the
// gateway links both of the plane's trees, and the same listing and predicate
// report each of them there on every release platform.
func TestTheApproverCheckFindsWhatTheGatewayLinks(t *testing.T) {
	t.Parallel()
	listings := platformDependencies(t, "../"+brand.Gateway)
	for i, platform := range approverPlatforms {
		hits := planeReach(listings[i])
		for _, tree := range planeTrees {
			if !slices.Contains(hits, tree) {
				t.Errorf("the gateway links %s on %s, and the check did not report it", tree, platform)
			}
		}
	}
}

// planeReach is every dependency under one of the plane's trees.
func planeReach(deps []string) []string {
	var hit []string
	for _, dep := range deps {
		for _, tree := range planeTrees {
			if dep == tree || strings.HasPrefix(dep, tree+"/") {
				hit = append(hit, dep)
				break
			}
		}
	}
	return hit
}

// platformDependencies lists the non-test dependencies of pattern once for
// each of approverPlatforms, in that order, with cgo off as the release builds
// them. A listing that fails or names no package fails the test.
func platformDependencies(t *testing.T, pattern string) [][]string {
	t.Helper()
	listings := make([][]string, len(approverPlatforms))
	errs := make([]error, len(approverPlatforms))
	var wg sync.WaitGroup
	for i, platform := range approverPlatforms {
		wg.Go(func() { listings[i], errs[i] = listPlatform(t.Context(), platform, pattern) })
	}
	wg.Wait()
	for i, platform := range approverPlatforms {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if len(listings[i]) == 0 {
			t.Fatalf("go list -deps for %s named no package", platform)
		}
	}
	return listings
}

func listPlatform(ctx context.Context, platform, pattern string) ([]string, error) {
	goos, goarch, ok := strings.Cut(platform, "/")
	if !ok {
		return nil, fmt.Errorf("platform %q is not GOOS/GOARCH", platform)
	}
	//nolint:gosec // G204: the program and every argument are literals of this file.
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-f", "{{.ImportPath}}", pattern)
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list -deps for %s: %w: %s", platform, err, stderr.Bytes())
	}
	return strings.Fields(string(out)), nil
}
