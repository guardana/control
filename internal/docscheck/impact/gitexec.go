package impact

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// gitRedirects are the variables that point git at a repository other than
// the one found from its working directory; the Runner never passes them on.
var gitRedirects = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE", "GIT_CEILING_DIRECTORIES"}

// Git returns the Runner that executes the binary in dir. Discovery stops
// one directory up, so an export placed inside some other repository is not
// measured against that repository's log; Repository checks the answer,
// since git resolves a symbolic link past the ceiling. What git said on
// standard error is kept, so a run that could not be measured says why.
func Git(dir string, environ []string) Runner {
	return func(args ...string) ([]byte, error) {
		out, err := gitCommand(dir, environ, args...).Output()
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				reason, _, _ := strings.Cut(strings.TrimSpace(string(exit.Stderr)), "\n")
				return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, reason)
			}
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return out, nil
	}
}

func gitCommand(dir string, environ []string, args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...) //nolint:gosec // G204: the arguments are this package's own tables, never input
	cmd.Dir = dir
	cmd.Env = append(gitEnv(environ), "GIT_CEILING_DIRECTORIES="+filepath.Dir(dir))
	return cmd
}

func gitEnv(environ []string) []string {
	kept := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(gitRedirects, name) {
			kept = append(kept, kv)
		}
	}
	return kept
}

// Repository binds a Runner to one working directory: before every call it
// asks git for its top level and refuses, as not measured, one whose real
// path is not the directory's own. A top level elsewhere is another
// repository's, or the directory is inside the repository, where the paths
// git prints are not the ones the pages name.
func Repository(run Runner, wd string) Runner {
	return func(args ...string) ([]byte, error) {
		if err := sameTopLevel(run, wd); err != nil {
			return nil, err
		}
		return run(args...)
	}
}

func sameTopLevel(run Runner, wd string) error {
	out, err := run("rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	top, err := filepath.EvalSymlinks(strings.TrimSuffix(string(out), "\n"))
	if err != nil {
		return fmt.Errorf("%w: git's top level: %w", ErrNotMeasured, err)
	}
	here, err := filepath.EvalSymlinks(wd)
	if err != nil {
		return fmt.Errorf("%w: the working directory: %w", ErrNotMeasured, err)
	}
	if top != here {
		return fmt.Errorf("%w: git's top level %s is not the working directory %s", ErrNotMeasured, top, here)
	}
	return nil
}
