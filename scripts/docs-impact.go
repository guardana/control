//go:build ignore

// Command docs-impact names the documentation pages a change makes suspect.
//
//	go run scripts/docs-impact.go --range main..HEAD          # git diff --name-only
//	go run scripts/docs-impact.go --changed paths.txt         # one path per line, under the root
//	go run scripts/docs-impact.go --for internal/gateway/mode.go
//	go run scripts/docs-impact.go --stale
//
// It prints how many changed paths it read, then the pages whose covers hold
// a changed path, the changed paths under a documentable surface of
// docs/docs.json that no page covers, the changed paths under a frozen path
// of that file, which are a contract change, and the covers globs under
// which the tree holds no file, each with the count it examined. --changed -
// reads the paths from standard input; a producer that fails before the pipe
// gives an empty list, which the first line shows as zero paths read. The
// files are what `git ls-files` lists, tracked and untracked but not ignored
// and not deleted from disk; in a tree with no git a walk with the same
// exclusions stands in, and the count says which. --stale
// lists, per page, the commits touching its covers since the page's own last
// commit; without git, or in a shallow clone, that is NOT MEASURED, never
// "none". Git is only ever believed about the working directory itself: an
// export placed inside another repository is not measured against it.
//
// Everything this program does lives in internal/docscheck/impact, which the
// gate compiles, vets, lints and tests; this file hands it the process. It
// runs from the repository root. A page whose frontmatter does not parse is
// reported and makes the exit status 1; a run that could not be made exits 2.
package main

import (
	"fmt"
	"os"

	"github.com/guardana/control/internal/docscheck/impact"
)

func main() {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "docs-impact: %v\n", err)
		os.Exit(2)
	}
	os.Exit(impact.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, impact.Git(wd, os.Environ()), os.DirFS(wd), wd))
}
