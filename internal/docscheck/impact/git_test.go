package impact

import (
	"errors"
	"strings"
	"testing"
)

const (
	diffArgs    = "diff --name-only main..HEAD --"
	lsArgs      = "ls-files -z --cached --others --exclude-standard"
	deletedArgs = "ls-files -z --deleted"
	shallowArgs = "rev-parse --is-shallow-repository"
	logLine     = "log --format=%x00%h %s --name-only --diff-merges=first-parent --no-show-signature"
)

// git stands in for the binary: it records every call and answers from the
// table by the joined arguments. A call the table does not hold fails naming
// its arguments, so a test sees exactly what was asked.
func git(t *testing.T, answers map[string]string) (Runner, *[][]string) {
	t.Helper()
	var calls [][]string
	return func(args ...string) ([]byte, error) {
		calls = append(calls, args)
		out, ok := answers[strings.Join(args, " ")]
		if !ok {
			return nil, errors.New("unexpected git " + strings.Join(args, " "))
		}
		return []byte(out), nil
	}, &calls
}

// noGit fails every call the way an absent binary or a tree with no
// repository does.
func noGit(fail error) Runner {
	return func(...string) ([]byte, error) { return nil, fail }
}

func asked(calls [][]string) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

func TestParseNameOnly(t *testing.T) {
	got, err := ParseNameOnly([]byte("cmd/gw/main.go\ndocs/index.md\n"))
	if err != nil {
		t.Fatalf("ParseNameOnly: %v", err)
	}
	if want := "cmd/gw/main.go,docs/index.md"; strings.Join(got, ",") != want {
		t.Errorf("ParseNameOnly = %q, want %q", got, want)
	}
	got, err = ParseNameOnly(nil)
	if err != nil || len(got) != 0 {
		t.Errorf("ParseNameOnly(nil) = %q, %v; want no path and no error", got, err)
	}
}

func TestParseNameOnlyRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ out, reason string }{
		"a quoted path":         {"\"docs/caf\\303\\251.md\"\n", "quoted"},
		"a parent path":         {"../x.go\n", "clean"},
		"a path twice":          {"a.go\na.go\n", "twice"},
		"a carriage return":     {"a.go\r\n", "carriage"},
		"a blank line":          {"a.go\n\nb.go\n", "empty"},
		"a byte order mark":     {"\ufeffa.go\n", "byte order mark"},
		"a leading tab":         {"\ta.go\n", "white space"},
		"a trailing space":      {"a.go \n", "white space"},
		"a space inside":        {"cmd/a b.go\n", "white space"},
		"a control byte inside": {"a\x01.go\n", "control"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseNameOnly([]byte(tc.out))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("ParseNameOnly = %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error %q does not say %q", err, tc.reason)
			}
		})
	}
}

func TestChangedAsksGitForTheRange(t *testing.T) {
	r, calls := git(t, map[string]string{diffArgs: "cmd/gw/main.go\n"})
	got, err := Changed(r, "main..HEAD")
	if err != nil {
		t.Fatalf("Changed: %v", err)
	}
	if len(got) != 1 || got[0] != "cmd/gw/main.go" {
		t.Errorf("Changed = %q", got)
	}
	if got := asked(*calls); len(got) != 1 || got[0] != diffArgs {
		t.Errorf("git was asked %q, want %q", got, diffArgs)
	}
}

func TestChangedRefusesARangeThatIsAnOption(t *testing.T) {
	r, calls := git(t, nil)
	if _, err := Changed(r, "--output=x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Changed = %v, want ErrInvalid", err)
	}
	if _, err := Changed(r, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("Changed of an empty range = %v, want ErrInvalid", err)
	}
	if len(*calls) != 0 {
		t.Errorf("git was run with a refused range: %q", *calls)
	}
}

func TestChangedWithoutGitIsNotMeasured(t *testing.T) {
	_, err := Changed(noGit(errors.New("fatal: not a git repository")), "main..HEAD")
	if !errors.Is(err, ErrNotMeasured) {
		t.Fatalf("Changed = %v, want ErrNotMeasured", err)
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error %q lost git's reason", err)
	}
}

const log = "\x00c3 docs: the guide\n\ndocs/guides/run.md\n" +
	"\x00c2 feat: the command\n\ncmd/gw/main.go\nMakefile\n" +
	"\x00c1 feat: the adapter\n\ninternal/gateway/adapter.go\n" +
	"\x00c0 docs: first pages\n\ndocs/guides/run.md\ndocs/reference/contract.md\n"

// history answers a full clone whose log is the table above.
func history() map[string]string {
	return map[string]string{shallowArgs: "false\n", logLine: log}
}

func TestParseLog(t *testing.T) {
	got, err := ParseLog([]byte(log))
	if err != nil {
		t.Fatalf("ParseLog: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("ParseLog found %d commits, want 4", len(got))
	}
	if c := got[1]; c.Hash != "c2" || c.Subject != "feat: the command" || strings.Join(c.Files, ",") != "cmd/gw/main.go,Makefile" {
		t.Errorf("commit 1 = %+v", c)
	}
	if c := got[3]; c.Hash != "c0" || len(c.Files) != 2 {
		t.Errorf("last commit = %+v", c)
	}
}

func TestParseLogAcceptsAnEmptySubject(t *testing.T) {
	for name, out := range map[string]string{
		"a space and no subject": "\x00c1 \n\na.go\n",
		"no space at all":        "\x00c1\n\na.go\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := ParseLog([]byte(out))
			if err != nil {
				t.Fatalf("ParseLog: %v", err)
			}
			if len(got) != 1 || got[0].Hash != "c1" || got[0].Subject != "" || strings.Join(got[0].Files, ",") != "a.go" {
				t.Errorf("ParseLog = %+v, want one commit c1 with an empty subject touching a.go", got)
			}
		})
	}
}

func TestParseLogRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ out, reason string }{
		"no commit":                {"", "no commit"},
		"a file before any header": {"cmd/x.go\n\x00c1 s\n", "before"},
		"a header with no hash":    {"\x00 subject\n", "hash"},
		"a quoted path":            {"\x00c1 s\n\n\"a b\"\n", "quoted"},
		"a hash twice":             {"\x00c1 s\n\na.go\n\x00c1 t\n\nb.go\n", "twice"},
		"a carriage return":        {"\x00c1 s\r\n", "carriage"},
		"a signature line":         {"\x00c1 s\ngpg: Signature made Mon\n\na.go\n", "white space"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseLog([]byte(tc.out))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("ParseLog = %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error %q does not say %q", err, tc.reason)
			}
		})
	}
}

func TestStaleAsksForAFirstParentLogWithoutSignatures(t *testing.T) {
	r, calls := git(t, history())
	got, err := Stale(r, pages())
	if err != nil {
		t.Fatalf("Stale: %v", err)
	}
	if got := asked(*calls); len(got) != 2 || got[0] != shallowArgs || got[1] != logLine {
		t.Errorf("git was asked %q, want the shallow question and then %q", got, logLine)
	}
	if len(got) != 2 {
		t.Fatalf("Stale = %+v, want one row per page", got)
	}
	guide, contract := got[0], got[1]
	if guide.Page != "docs/guides/run.md" || guide.LastCommit != "c3" || len(guide.Since) != 0 {
		t.Errorf("the guide, committed last, is %+v; want no commit since c3", guide)
	}
	if contract.Page != "docs/reference/contract.md" || contract.LastCommit != "c0" {
		t.Fatalf("the contract page is %+v; want last commit c0", contract)
	}
	if len(contract.Since) != 0 {
		t.Errorf("the contract page covers pkg/** and nothing under it changed, yet Since = %+v", contract.Since)
	}
}

func TestStaleCountsOnlyCommitsTouchingTheCovers(t *testing.T) {
	ps := pages()
	ps[1].Covers = []string{"cmd/**", "internal/gateway/**"}
	r, _ := git(t, history())
	got, err := Stale(r, ps)
	if err != nil {
		t.Fatalf("Stale: %v", err)
	}
	since := got[1].Since
	if len(since) != 2 || since[0].Hash != "c2" || since[1].Hash != "c1" {
		t.Fatalf("Since = %+v, want c2 and c1, newest first", since)
	}
	ps[1].Covers = []string{"cmd/**"}
	got, err = Stale(r, ps)
	if err != nil {
		t.Fatalf("Stale: %v", err)
	}
	if since := got[1].Since; len(since) != 1 || since[0].Hash != "c2" {
		t.Errorf("Since = %+v, want c2 alone once the adapter is not covered", since)
	}
}

func TestThePagesOwnCommitTouchingItsCoversIsNotSince(t *testing.T) {
	answers := history()
	answers[logLine] = "\x00c4 docs and code together\n\ndocs/guides/run.md\ncmd/gw/main.go\n" + log
	r, _ := git(t, answers)
	got, err := Stale(r, pages())
	if err != nil {
		t.Fatalf("Stale: %v", err)
	}
	if guide := got[0]; guide.LastCommit != "c4" || len(guide.Since) != 0 {
		t.Errorf("the guide is %+v; want last commit c4 and nothing since: c4 is the page's own commit", guide)
	}
}

func TestStaleReportsAPageNoCommitHolds(t *testing.T) {
	ps := append(pages(), Page{Path: "docs/new.md", Covers: []string{"cmd/**"}})
	r, _ := git(t, history())
	got, err := Stale(r, ps)
	if err != nil {
		t.Fatalf("Stale: %v", err)
	}
	if !got[2].Uncommitted || got[2].LastCommit != "" {
		t.Errorf("the new page is %+v, want Uncommitted", got[2])
	}
	text := FormatStale(got)
	for _, want := range []string{"3 pages examined", "docs/new.md: not committed yet", "docs/guides/run.md: last commit c3, 0 commits since"} {
		if !strings.Contains(text, want) {
			t.Errorf("FormatStale lacks %q:\n%s", want, text)
		}
	}
}

func TestStaleWithoutGitIsNotMeasured(t *testing.T) {
	_, err := Stale(noGit(errors.New(`exec: "git": executable file not found`)), pages())
	if !errors.Is(err, ErrNotMeasured) {
		t.Fatalf("Stale = %v, want ErrNotMeasured", err)
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error %q lost the reason", err)
	}
}

func TestStaleInAShallowCloneIsNotMeasured(t *testing.T) {
	for name, answer := range map[string]string{
		"a shallow clone":      "true\n",
		"an answer that is no": "--is-shallow-repository\n",
	} {
		t.Run(name, func(t *testing.T) {
			answers := history()
			answers[shallowArgs] = answer
			r, calls := git(t, answers)
			_, err := Stale(r, pages())
			if !errors.Is(err, ErrNotMeasured) || !strings.Contains(err.Error(), "shallow") {
				t.Fatalf("Stale = %v, want ErrNotMeasured naming the shallow clone", err)
			}
			if got := asked(*calls); len(got) != 1 {
				t.Errorf("git was asked %q; the log must not be read", got)
			}
		})
	}
}

func TestStaleRefusesZeroPagesAndAnEmptyLog(t *testing.T) {
	r, calls := git(t, history())
	if _, err := Stale(r, nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("Stale over no page = %v, want ErrInvalid", err)
	}
	if len(*calls) != 0 {
		t.Errorf("git was run for zero pages")
	}
	answers := history()
	answers[logLine] = ""
	r, _ = git(t, answers)
	if _, err := Stale(r, pages()); !errors.Is(err, ErrInvalid) {
		t.Errorf("Stale over an empty log = %v, want ErrInvalid", err)
	}
}

func walk(t *testing.T, paths []string, fail error) (func() ([]string, error), *int) {
	t.Helper()
	calls := 0
	return func() ([]string, error) {
		calls++
		return paths, fail
	}, &calls
}

func TestListFilesTakesGitsListOverTheWalk(t *testing.T) {
	r, calls := git(t, map[string]string{lsArgs: "cmd/gw/main.go\x00Makefile\x00", deletedArgs: ""})
	w, walked := walk(t, []string{"cmd/gw/main.go", "Makefile", "cover.out"}, nil)
	got, err := ListFiles(r, w)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if got := asked(*calls); len(got) != 2 || got[0] != lsArgs || got[1] != deletedArgs {
		t.Errorf("git was asked %q, want %q then %q", got, lsArgs, deletedArgs)
	}
	if *walked != 0 {
		t.Errorf("the walk ran %d times although git answered", *walked)
	}
	if strings.Join(got.Paths, ",") != "cmd/gw/main.go,Makefile" || got.Source != "git ls-files" {
		t.Fatalf("ListFiles = %+v, want git's two files", got)
	}
	res, err := Report(pages(), surfaces, frozen, got.Paths, nil)
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	res.FilesSource = got.Source
	if res.FilesExamined != 2 {
		t.Errorf("FilesExamined = %d, want 2: the third file is one git ignores", res.FilesExamined)
	}
	if text := res.String(); !strings.Contains(text, "2 files examined (git ls-files)") {
		t.Errorf("String() does not say where the files came from:\n%s", text)
	}
}

func TestListFilesLeavesOutATrackedFileDeletedFromDisk(t *testing.T) {
	r, _ := git(t, map[string]string{lsArgs: "cmd/gw/main.go\x00Makefile\x00", deletedArgs: "Makefile\x00"})
	w, _ := walk(t, nil, nil)
	got, err := ListFiles(r, w)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if strings.Join(got.Paths, ",") != "cmd/gw/main.go" {
		t.Errorf("ListFiles = %q, want the one file still on disk", got.Paths)
	}
	r, _ = git(t, map[string]string{lsArgs: "Makefile\x00", deletedArgs: "Makefile\x00"})
	if _, err := ListFiles(r, w); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "no file") {
		t.Errorf("ListFiles with every file deleted = %v, want ErrInvalid saying no file", err)
	}
}

func TestListFilesWalksWhenGitCannotAnswer(t *testing.T) {
	r := noGit(errors.New("fatal: not a git repository"))
	w, walked := walk(t, []string{"a.go", "b.go", "c.go"}, nil)
	got, err := ListFiles(r, w)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if *walked != 1 || len(got.Paths) != 3 {
		t.Errorf("ListFiles = %+v after %d walks, want the walk's three files", got, *walked)
	}
	if !strings.Contains(got.Source, "walk") || !strings.Contains(got.Source, "not a git repository") {
		t.Errorf("Source = %q, want the walk and git's reason", got.Source)
	}
	w, _ = walk(t, nil, errors.New("the walk found no file"))
	if _, err := ListFiles(r, w); err == nil || !strings.Contains(err.Error(), "no file") {
		t.Errorf("ListFiles with a failing walk = %v, want its error", err)
	}
}

func TestListFilesRefusesWhatGitShouldNotSay(t *testing.T) {
	for name, tc := range map[string]struct{ cached, deleted, reason string }{
		"no file":                {"", "", "no file"},
		"a parent path":          {"../x.go\x00", "", "clean"},
		"a path twice":           {"a.go\x00a.go\x00", "", "twice"},
		"an empty entry":         {"a.go\x00\x00b.go\x00", "", "empty"},
		"a deleted parent path":  {"a.go\x00", "../x.go\x00", "clean"},
		"a deleted path unknown": {"a.go\x00", "b.go\x00", "not listed"},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := git(t, map[string]string{lsArgs: tc.cached, deletedArgs: tc.deleted})
			w, walked := walk(t, []string{"a.go"}, nil)
			_, err := ListFiles(r, w)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("ListFiles = %v, want ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Errorf("error %q does not say %q", err, tc.reason)
			}
			if *walked != 0 {
				t.Errorf("the walk stood in for a git answer that was refused")
			}
		})
	}
}

func TestListFilesWalksWhenTheDeletedListCannotBeRead(t *testing.T) {
	r, _ := git(t, map[string]string{lsArgs: "a.go\x00"})
	w, walked := walk(t, []string{"a.go", "b.go"}, nil)
	got, err := ListFiles(r, w)
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if *walked != 1 || len(got.Paths) != 2 || !strings.Contains(got.Source, "walk") {
		t.Errorf("ListFiles = %+v after %d walks; want the walk once half of git's answer is missing", got, *walked)
	}
}
