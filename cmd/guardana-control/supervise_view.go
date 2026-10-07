package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	ondisk "github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/runview"
	"github.com/guardana/control/internal/supervise"
)

// viewFileMode keeps a page to its owner: it holds the tool names, resource
// ids and reasons of a run, as the findings log does.
const viewFileMode = 0o600

// viewPath is the --view path, cleaned as text and judged by checkViewOut,
// or "" when none was given. --view-all without --view, or --view given
// twice, is a usage error.
func viewPath(a *superviseArgs) (string, error) {
	switch {
	case len(a.view) > 1:
		return "", errors.New("--view is given once")
	case len(a.view) == 0 && a.viewAll:
		return "", errors.New("--view-all takes --view")
	case len(a.view) == 0:
		return "", nil
	}
	out := filepath.Clean(a.view[0])
	return out, checkViewOut(out)
}

// drawView is the page of res, or nil when no --view was given.
func drawView(out string, p *supervise.Procedure, res *supervise.Result, all bool) ([]byte, error) {
	if out == "" {
		return nil, nil
	}
	return runview.Page(p, res, runview.Options{All: all})
}

// checkViewOut refuses a --view path whose directory is missing, and one
// that exists and is not a regular file of this account holding a page
// supervise drew: --view replaces an earlier page and nothing else, so a
// mistyped path cannot replace a procedure, an export, a log or a key.
// Whoever may write the path may write any page there.
func checkViewOut(out string) error {
	info, err := os.Lstat(out)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := ondisk.CheckDir(filepath.Dir(out), 0); err != nil {
			return fmt.Errorf("--view %s: %w", out, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("--view %s: %w", out, err)
	}
	notAPage := fmt.Errorf("--view %s exists and is not a page supervise drew, and --view replaces nothing else", out)
	if !info.Mode().IsRegular() {
		return notAPage
	}
	raw, err := ondisk.ReadOwned(out, runview.MaxPageBytes, 0, os.Geteuid())
	switch {
	case errors.Is(err, ondisk.ErrTooLarge):
		return notAPage
	case err != nil:
		return fmt.Errorf("--view %s: %w", out, err)
	case !runview.IsPage(raw):
		return notAPage
	}
	return nil
}

// writeView, when --view was given, judges out again, since the findings log was written after it
// was first judged, and puts the page there with its own mode whatever
// the old file's was.
func writeView(out string, page []byte) error {
	if out == "" {
		return nil
	}
	if err := checkViewOut(out); err != nil {
		return err
	}
	if err := ondisk.Replace(filepath.Dir(out), filepath.Base(out), page, viewFileMode); err != nil {
		return fmt.Errorf("--view %s: %w", out, err)
	}
	return nil
}
