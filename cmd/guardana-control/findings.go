package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strconv"

	"github.com/guardana/control/internal/findinglog"
	"github.com/guardana/control/internal/lineexport"
)

const (
	findingsExportName = "findings export"
	findingsExportForm = "--findings <dir> [--after <cursor>] [--limit <n>]\n" +
		"      [--max-bytes <n>]"
)

type findingsExportArgs struct {
	findings valueList
	q        findinglog.ExportQuery
}

func findingsExportFlags(command string, out io.Writer) (*flag.FlagSet, *findingsExportArgs) {
	flags := commandFlags(command, out)
	a := &findingsExportArgs{}
	flags.Var(&a.findings, "findings", "the findings log `dir`; given once")
	flags.StringVar(&a.q.After, "after", "", "start after the line this `cursor`, from an earlier export of this log, names")
	flags.IntVar(&a.q.Limit, "limit", findinglog.DefaultExportLimit, "the most records to write, every type counted; at most 100000")
	flags.Int64Var(&a.q.MaxBytes, "max-bytes", 0, "the most bytes of whole lines to read; 0 is no bound")
	return flags, a
}

func findingsExportFlagSet(command string, out io.Writer) *flag.FlagSet {
	flags, _ := findingsExportFlags(command, out)
	return flags
}

// findingsExportCommand writes the findings export of one findings log. It
// exits 0 when the export holds no gap, 1 when it is whole and holds one, and
// 2 when it was refused or cut short.
func findingsExportCommand(args []string, stdout, stderr io.Writer) int {
	flags, a := findingsExportFlags(findingsExportName, io.Discard)
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || len(a.findings) != 1 {
		return usageError(stderr, findingsExportName, "takes --findings once, and no argument")
	}
	path := filepath.Join(a.findings[0], findinglog.FileName)
	tr, err := findinglog.ExportFile(path, a.q, stdout)
	if err != nil {
		return usageError(stderr, findingsExportName, err.Error())
	}
	if gaps := tr.Counts[lineexport.Gap]; gaps > 0 {
		return fail(stderr, findingsExportName, fmt.Errorf("%s: the export holds %d gap(s): lines changed since the log was judged, "+
			"or a write no report closes that no writer is writing", strconv.Quote(path), gaps))
	}
	return exitOK
}
