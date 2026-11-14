package docscheck

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const releaseBarHeading = "## Release bar for 1.0"

var (
	dispositionMark = regexp.MustCompile("1\\.0 bar: `([^`]*)`")
	barTaskRef      = regexp.MustCompile(`\bB([0-9]{2})(?: to B([0-9]{2}))?\b`)
	dispositions    = map[string]bool{"own evidence": true, "not met": true}
)

// releaseBarProblems holds the 1.0 release bar to its dispositions: every
// disposition in the backlog sits in a task row and names one of the closed
// list, every task of the backlog's release bar table carries one, and the
// tasks ROADMAP's release bar names are exactly the tasks that carry one.
func releaseBarProblems(backlog, roadmap []string) []string {
	marked, problems := backlogDispositions(backlog)
	named, more := roadmapBarTasks(roadmap)
	problems = append(problems, more...)
	for _, id := range sortedKeys(named) {
		if !marked[id] {
			problems = append(problems, fmt.Sprintf("ROADMAP.md names %s in its release bar, and no backlog task %s carries a disposition", id, id))
		}
	}
	for _, id := range sortedKeys(marked) {
		if !named[id] {
			problems = append(problems, fmt.Sprintf("%s carries a disposition, and ROADMAP.md's release bar does not name it", id))
		}
	}
	return problems
}

// backlogDispositions reads the backlog's task rows: the tasks that carry a
// disposition, and each row or line that breaks the rule.
func backlogDispositions(backlog []string) (map[string]bool, []string) {
	var problems []string
	marked := map[string]bool{}
	barRows := 0
	section := ""
	for i, line := range backlog {
		if strings.HasPrefix(line, "## ") {
			section = strings.TrimSpace(line)
		}
		marks := dispositionMark.FindAllStringSubmatch(line, -1)
		id := backlogRowID(line)
		if id == "" {
			if len(marks) > 0 {
				problems = append(problems, fmt.Sprintf("docs/backlog.md:%d: a disposition outside a task row", i+1))
			}
			continue
		}
		if section == releaseBarHeading {
			barRows++
			if len(marks) == 0 {
				problems = append(problems, fmt.Sprintf("docs/backlog.md:%d: %s is a release bar task with no disposition", i+1, id))
			}
		}
		for _, m := range marks {
			if !dispositions[m[1]] {
				problems = append(problems, fmt.Sprintf("docs/backlog.md:%d: %s names the disposition %q, which is not own evidence or not met", i+1, id, m[1]))
			}
		}
		if len(marks) > 0 {
			marked[id] = true
		}
	}
	if barRows == 0 {
		problems = append(problems, "docs/backlog.md: no task row under "+releaseBarHeading)
	}
	return marked, problems
}

// backlogRowID is the task identifier a backlog table row opens with, or "".
func backlogRowID(line string) string {
	s := strings.TrimSpace(line)
	if !strings.HasPrefix(s, "|") {
		return ""
	}
	if cells := tableCells(s); backlogID.MatchString(cells[0]) {
		return cells[0]
	}
	return ""
}

// roadmapBarTasks reads the task identifiers of ROADMAP's release bar
// section, a range "B29 to B34" standing for each task in it.
func roadmapBarTasks(roadmap []string) (map[string]bool, []string) {
	named := map[string]bool{}
	var problems []string
	in, found := false, false
	for _, line := range roadmap {
		if strings.HasPrefix(line, "## ") {
			in = strings.TrimSpace(line) == releaseBarHeading
			found = found || in
			continue
		}
		if !in {
			continue
		}
		for _, m := range barTaskRef.FindAllStringSubmatch(line, -1) {
			from, to, err := barRange(m)
			if err != nil {
				problems = append(problems, fmt.Sprintf("ROADMAP.md: the task reference %s does not read: %v", m[0], err))
				continue
			}
			if to < from {
				problems = append(problems, fmt.Sprintf("ROADMAP.md: the range %s runs backwards", m[0]))
				continue
			}
			for n := from; n <= to; n++ {
				named[fmt.Sprintf("B%02d", n)] = true
			}
		}
	}
	switch {
	case !found:
		problems = append(problems, "ROADMAP.md: no section "+releaseBarHeading)
	case len(named) == 0:
		problems = append(problems, "ROADMAP.md: the release bar names no task")
	}
	return named, problems
}

func TestReleaseBarTasksCarryADisposition(t *testing.T) {
	fsys := repoFS(t)
	backlog := readLines(t, fsys, "docs/backlog.md")
	roadmap := readLines(t, fsys, "ROADMAP.md")
	named, _ := roadmapBarTasks(roadmap)
	if len(named) < 2 {
		t.Fatalf("ROADMAP.md's release bar named %d tasks; the section was not read", len(named))
	}
	report(t, "release bar", releaseBarProblems(backlog, roadmap))
}

func TestReleaseBarProblems(t *testing.T) {
	head := []string{"# Backlog", "", "## Milestone 1", "", "| ID | Work | Depends on | Acceptance |", "| --- | --- | --- | --- |"}
	other := "| B06 | Trial | none | Users try it. 1.0 bar: `not met` |"
	bar := []string{"", releaseBarHeading, "", "| ID | Work | Depends on | Acceptance |", "| --- | --- | --- | --- |"}
	b29 := "| B29 | Trail | none | Links. 1.0 bar: `own evidence` |"
	b30 := "| B30 | Digest | none | Vectors. 1.0 bar: `own evidence` |"
	b31 := "| B31 | Contracts | none | Records. 1.0 bar: `not met` for one part. 1.0 bar: `own evidence` for the rest |"
	backlog := func(rows ...string) []string {
		return slices.Concat(head, []string{other}, bar, rows)
	}
	roadmap := func(text string) []string {
		return []string{"# Roadmap", "", "## 3. A small team", "", "B13 and B41.", "", releaseBarHeading, "", text, "", "## After", "", "B16."}
	}
	good := backlog(b29, b30, b31)
	for name, c := range map[string]struct {
		backlog, roadmap []string
		want             []string
	}{
		"every bar task carries a disposition and the roadmap names each": {good, roadmap("Its items are B06 and B29 to B31."), nil},
		"a disposition outside the closed list": {backlog(b29, strings.Replace(b30, "own evidence", "met", 1), b31), roadmap("B06 and B29 to B31."),
			[]string{"docs/backlog.md:14: B30 names the disposition \"met\", which is not own evidence or not met"}},
		"a release bar task with none": {backlog(b29, "| B30 | Digest | none | Vectors |", b31), roadmap("B06, B29 and B31."),
			[]string{"docs/backlog.md:14: B30 is a release bar task with no disposition"}},
		"a disposition in prose": {slices.Concat(good, []string{"", "Each task carries 1.0 bar: `not met` or another."}), roadmap("B06 and B29 to B31."),
			[]string{"docs/backlog.md:17: a disposition outside a task row"}},
		"the roadmap names a task with none": {good, roadmap("B06, B16 and B29 to B31."),
			[]string{"ROADMAP.md names B16 in its release bar, and no backlog task B16 carries a disposition"}},
		"a task with one the roadmap leaves out": {good, roadmap("B29 to B31."),
			[]string{"B06 carries a disposition, and ROADMAP.md's release bar does not name it"}},
		"a range stops at its end": {good, roadmap("B06 and B29 to B30."),
			[]string{"B31 carries a disposition, and ROADMAP.md's release bar does not name it"}},
		"a range that runs backwards": {good, roadmap("B06, B31 to B29, B29, B30 and B31."),
			[]string{"ROADMAP.md: the range B31 to B29 runs backwards"}},
		"no task row under the release bar": {slices.Concat(head, []string{other}, bar), roadmap("B06."), []string{"docs/backlog.md: no task row under " + releaseBarHeading}},
		"no release bar section in the roadmap": {good, []string{"# Roadmap", "", "## 3. A small team", "", "B06 and B29 to B31."},
			[]string{"ROADMAP.md: no section " + releaseBarHeading,
				"B06 carries a disposition, and ROADMAP.md's release bar does not name it",
				"B29 carries a disposition, and ROADMAP.md's release bar does not name it",
				"B30 carries a disposition, and ROADMAP.md's release bar does not name it",
				"B31 carries a disposition, and ROADMAP.md's release bar does not name it"}},
		"a release bar that names no task": {slices.Concat(head, bar, []string{"| B29 | Trail | none | Links |"}), roadmap("Nothing yet."),
			[]string{"docs/backlog.md:12: B29 is a release bar task with no disposition", "ROADMAP.md: the release bar names no task"}},
	} {
		if got := releaseBarProblems(c.backlog, c.roadmap); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

// barRange is the first and last task number a reference names.
func barRange(m []string) (from, to int, err error) {
	if from, err = strconv.Atoi(m[1]); err != nil {
		return 0, 0, err
	}
	if m[2] == "" {
		return from, from, nil
	}
	to, err = strconv.Atoi(m[2])
	return from, to, err
}
