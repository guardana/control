package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
)

// releasePlatforms are the systems the release builds the binaries for, and
// windows, which it does not: a file only one system compiles can import what
// the host's own listing never shows.
var releasePlatforms = []string{
	"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64",
}

// planePatterns are the plane's binary, its five guarded trees and the
// adapters, named from this package.
var planePatterns = []string{
	".", "../../internal/core/...", "../../internal/policy/...", "../../internal/canon/...",
	"../../internal/evidence/...", "../../pkg/contract/...", "../../adapters/...",
}

// platformListing is the non-test dependency closure of a set of patterns as
// one platform builds it.
type platformListing struct {
	platform string
	deps     []string
}

// dependencies lists the non-test dependencies of patterns once for each
// release platform, with cgo off as the release builds them. A listing that
// fails or names no package fails the test.
func dependencies(t *testing.T, patterns ...string) []platformListing {
	t.Helper()
	listings := make([]platformListing, len(releasePlatforms))
	errs := make([]error, len(releasePlatforms))
	var wg sync.WaitGroup
	for i, platform := range releasePlatforms {
		wg.Go(func() {
			listings[i].platform = platform
			listings[i].deps, errs[i] = listDependencies(t.Context(), platform, patterns)
		})
	}
	wg.Wait()
	for i, l := range listings {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if len(l.deps) == 0 {
			t.Fatalf("go list for %s named no package", l.platform)
		}
	}
	return listings
}

func listDependencies(ctx context.Context, platform string, patterns []string) ([]string, error) {
	goos, goarch, ok := strings.Cut(platform, "/")
	if !ok {
		return nil, fmt.Errorf("platform %q is not GOOS/GOARCH", platform)
	}
	//nolint:gosec // G204: the program and every argument are literals of this file.
	cmd := exec.CommandContext(ctx, "go", append([]string{"list", "-deps", "-f", "{{.ImportPath}}"}, patterns...)...)
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list for %s: %w: %s", platform, err, stderr.Bytes())
	}
	return strings.Fields(string(out)), nil
}

// reached is every dependency under one of trees.
func reached(deps, trees []string) []string {
	var hit []string
	for _, dep := range deps {
		for _, tree := range trees {
			if within(dep, tree) {
				hit = append(hit, dep)
				break
			}
		}
	}
	return hit
}

// unreached is every tree that no package of hits lies under.
func unreached(hits, trees []string) []string {
	var miss []string
	for _, tree := range trees {
		found := false
		for _, hit := range hits {
			found = found || within(hit, tree)
		}
		if !found {
			miss = append(miss, tree)
		}
	}
	return miss
}

func within(pkg, tree string) bool {
	return pkg == tree || strings.HasPrefix(pkg, tree+"/")
}
