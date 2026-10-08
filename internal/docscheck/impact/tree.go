package impact

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// TreeFiles lists the files of the tree fsys holds: git's list where git
// answers, and a walk only for a tree with no .git, which holds nothing the
// repository does not track. A tree holding .git that git cannot list is an
// error: the walk would judge local files git ignores.
func TreeFiles(run Runner, fsys fs.FS) (Files, error) {
	return ListFiles(run, func() ([]string, error) {
		switch _, err := fs.Stat(fsys, ".git"); {
		case err == nil:
			return nil, fmt.Errorf("%w: the tree holds .git, but git cannot list it", ErrNotMeasured)
		case !errors.Is(err, fs.ErrNotExist):
			return nil, fmt.Errorf("%w: looking for .git: %w", ErrNotMeasured, err)
		}
		return WalkFiles(fsys)
	})
}

// RepositoryFiles is TreeFiles over the directory root with the git binary.
func RepositoryFiles(root string, environ []string) (Files, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return Files{}, err
	}
	return TreeFiles(Repository(Git(abs, environ), abs), os.DirFS(abs))
}

// Listing is what a walk of the tree may read: the files the repository lists
// and, apart from them, what docs.json excludes from the page checks.
type Listing struct {
	held     map[string]bool
	excluded func(string) bool
}

// LeftOut is the listing of paths with excluded on top.
func LeftOut(paths []string, excluded func(string) bool) Listing {
	held := make(map[string]bool, 2*len(paths))
	for _, p := range paths {
		held[p] = true
		for i := strings.LastIndexByte(p, '/'); i > 0; i = strings.LastIndexByte(p[:i], '/') {
			held[p[:i+1]] = true
		}
	}
	return Listing{held: held, excluded: excluded}
}

// Lists reports whether rel, a file or a directory spelled with a trailing
// slash, is a listed file or holds one.
func (l Listing) Lists(rel string) bool { return l.held[rel] }

// Skips reports whether a walk leaves rel out: the listing does not hold it,
// or excluded names it. A Listing that LeftOut did not build skips everything.
func (l Listing) Skips(rel string) bool {
	return !l.held[rel] || l.excluded == nil || l.excluded(rel)
}
