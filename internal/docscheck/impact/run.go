package impact

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/guardana/control/internal/docscheck/docsconfig"
)

const (
	configPath = "docs/docs.json"
	docsDir    = "docs"
)

type options struct {
	rng, changed, forPath string
	stale                 bool
}

// Run is the docs-impact program over its seams: the arguments after the
// name, the standard streams, a git Runner, the repository root as a file
// system and its path. It returns the exit status: 0, 1 when a page did not
// parse and is missing from the lists, 2 when the run could not be made,
// a result git could not measure among them.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, run Runner, fsys fs.FS, wd string) int {
	o, err := parseArgs(args, stderr)
	if err != nil {
		return refuse(stderr, err)
	}
	code, err := execute(o, stdin, stdout, run, fsys, wd)
	if err != nil {
		return refuse(stderr, err)
	}
	return code
}

func parseArgs(args []string, stderr io.Writer) (options, error) {
	var o options
	set := flag.NewFlagSet("docs-impact", flag.ContinueOnError)
	set.SetOutput(stderr)
	set.StringVar(&o.rng, "range", "", "report on the paths `git diff --name-only <range>` lists")
	set.StringVar(&o.changed, "changed", "", "report on the paths in this file under the root, one per line; - reads standard input")
	set.StringVar(&o.forPath, "for", "", "print the pages covering this path")
	set.BoolVar(&o.stale, "stale", false, "per page, the commits touching its covers since the page's last commit")
	if err := set.Parse(args); err != nil {
		return options{}, err
	}
	if set.NArg() != 0 {
		return options{}, fmt.Errorf("unexpected argument %q", set.Arg(0))
	}
	modes := 0
	for _, on := range []bool{o.rng != "", o.changed != "", o.forPath != "", o.stale} {
		if on {
			modes++
		}
	}
	if modes != 1 {
		return options{}, errors.New("exactly one of --range, --changed, --for and --stale is required")
	}
	return o, nil
}

// refuse writes the reason the run was not made and returns the status
// that says so. The write is best effort: when standard error itself cannot
// be written there is nowhere left to report that, and the status still says
// the run failed.
func refuse(stderr io.Writer, err error) int {
	_, _ = io.WriteString(stderr, "docs-impact: "+err.Error()+"\n")
	return 2
}

func execute(o options, stdin io.Reader, stdout io.Writer, run Runner, fsys fs.FS, wd string) (int, error) {
	if _, err := fs.Stat(fsys, "go.mod"); err != nil {
		return 0, errors.New("run from the repository root")
	}
	cfg, err := readConfig(fsys)
	if err != nil {
		return 0, err
	}
	pages, broken, err := WalkPages(fsys, docsDir, cfg.Excluded)
	if err != nil {
		return 0, err
	}
	git := Repository(run, wd)
	var text string
	switch {
	case o.forPath != "":
		text, err = coveringText(pages, o.forPath)
	case o.stale:
		text, err = staleText(git, pages)
	default:
		text, err = reportText(stdin, cfg, pages, git, fsys, o)
	}
	if err != nil {
		return 0, err
	}
	// A failed write is a failed run: exiting 0 would claim lists that never
	// arrived.
	if _, err := io.WriteString(stdout, text+missing(broken)); err != nil {
		return 0, fmt.Errorf("writing to standard output: %w", err)
	}
	if len(broken) > 0 {
		return 1, nil
	}
	return 0, nil
}

func missing(broken []Unparsed) string {
	if len(broken) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d pages did not parse and are missing above\n", len(broken))
	for _, u := range broken {
		fmt.Fprintf(&b, "  %s: %v\n", u.Path, u.Err)
	}
	return b.String()
}

func readConfig(fsys fs.FS) (docsconfig.Config, error) {
	data, err := fs.ReadFile(fsys, configPath)
	if err != nil {
		return docsconfig.Config{}, fmt.Errorf("reading %s: %w", configPath, err)
	}
	cfg, err := docsconfig.Parse(data)
	if err != nil {
		return docsconfig.Config{}, fmt.Errorf("%s: %w", configPath, err)
	}
	return cfg, nil
}

func readChanged(stdin io.Reader, git Runner, fsys fs.FS, o options) ([]string, error) {
	if o.rng != "" {
		return Changed(git, o.rng)
	}
	var data []byte
	var err error
	if o.changed == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = fs.ReadFile(fsys, o.changed)
	}
	if err != nil {
		return nil, fmt.Errorf("reading the changed paths: %w", err)
	}
	return ParseNameOnly(data)
}

func reportText(stdin io.Reader, cfg docsconfig.Config, pages []Page, git Runner, fsys fs.FS, o options) (string, error) {
	paths, err := readChanged(stdin, git, fsys, o)
	if err != nil {
		return "", err
	}
	files, err := ListFiles(git, func() ([]string, error) { return WalkFiles(fsys) })
	if err != nil {
		return "", err
	}
	r, err := Report(pages, cfg.Surfaces, cfg.Frozen, files.Paths, paths)
	if err != nil {
		return "", err
	}
	r.FilesSource = files.Source
	return r.String(), nil
}

func coveringText(pages []Page, p string) (string, error) {
	pagesCovering, err := Covering(pages, p)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d pages cover %s of %d examined\n", len(pagesCovering), p, len(pages))
	for _, page := range pagesCovering {
		fmt.Fprintf(&b, "  %s\n", page)
	}
	return b.String(), nil
}

func staleText(git Runner, pages []Page) (string, error) {
	rows, err := Stale(git, pages)
	if err != nil {
		return "", err
	}
	return FormatStale(rows), nil
}
