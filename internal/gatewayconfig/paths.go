package gatewayconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// overlaps reports whether two directories are the same one or one of them
// sits inside the other. A pair this build cannot compare is reported as
// overlapping, because a journal that may share a directory with another
// writer is the answer nothing can take back.
func overlaps(a, b string) (bool, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return true, fmt.Errorf("%q is not a path this build can resolve: %w", a, err)
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return true, fmt.Errorf("%q is not a path this build can resolve: %w", b, err)
	}
	return inside(absA, absB) || inside(absB, absA), nil
}

// inside reports whether child is parent or sits under it.
func inside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// insideDir reports whether directory a is b or sits under it. A pair this
// build cannot compare is reported as inside, the answer that refuses.
func insideDir(a, b string) (bool, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return true, fmt.Errorf("%q is not a path this build can resolve: %w", a, err)
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return true, fmt.Errorf("%q is not a path this build can resolve: %w", b, err)
	}
	return inside(absA, absB), nil
}

// underSameDir reports whether directory a, or a directory it sits in, is
// the very directory b, compared by identity along a's path as the system
// resolves it. A directory b that does not exist yet is left to the
// comparison of paths; a pair this build cannot compare is reported as
// inside, the answer that refuses.
func underSameDir(a, b string) (bool, error) {
	target, err := os.Stat(b)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return true, fmt.Errorf("%q cannot be compared: %w", b, err)
	}
	existing, err := existingAncestor(a)
	if err != nil {
		return true, err
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return true, fmt.Errorf("%q cannot be compared: %w", a, err)
	}
	for dir := resolved; ; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil {
			return true, fmt.Errorf("%q cannot be compared: %w", dir, err)
		}
		if os.SameFile(info, target) {
			return true, nil
		}
		if filepath.Dir(dir) == dir {
			return false, nil
		}
	}
}

// existingAncestor is dir, made absolute, or the nearest directory above it
// that exists. What does not exist yet cannot be a link.
func existingAncestor(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("%q is not a path this build can resolve: %w", dir, err)
	}
	for {
		_, err := os.Lstat(abs)
		switch {
		case err == nil:
			return abs, nil
		case !errors.Is(err, fs.ErrNotExist) || filepath.Dir(abs) == abs:
			return "", fmt.Errorf("%q cannot be compared: %w", dir, err)
		}
		abs = filepath.Dir(abs)
	}
}
