package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	ondisk "github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/ingest/otelgenai"
	"github.com/guardana/control/internal/observe"
	"github.com/guardana/control/internal/observelog"
)

const (
	observeImportName = "observe import"
	observeExportName = "observe export"
	observeImportForm = "--source <file> --log <dir> <file>"
	observeExportForm = "[--after] [--limit] [--max-bytes] <file>"
)

// maxDescriptorBytes bounds the descriptor read; observe.ReadDescriptor
// refuses more than this anyway.
const maxDescriptorBytes = 64 << 10

// The descriptor states the trust a later finding rests on, so only its
// owner may write it.
const descriptorForbidden = 0o022

func observeImportFlags(command string, out io.Writer) (*flag.FlagSet, *string, *string) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(out)
	source := flags.String("source", "", "the source descriptor, a JSON file of this account that no other account may write")
	logDir := flags.String("log", "", "the observation log's directory, of this account and reachable by no other")
	return flags, source, logDir
}

func observeImportFlagSet(command string, out io.Writer) *flag.FlagSet {
	flags, _, _ := observeImportFlags(command, out)
	return flags
}

func observeExportFlags(command string, out io.Writer) (*flag.FlagSet, *observelog.Query) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(out)
	q := &observelog.Query{}
	flags.StringVar(&q.After, "after", "", "start after the line this `cursor`, from an earlier export, names")
	flags.IntVar(&q.Limit, "limit", observelog.DefaultExportLimit, "the most records to write, every type counted; at most 100000")
	flags.Int64Var(&q.MaxBytes, "max-bytes", 0, "the most bytes of whole lines to read; 0 is no bound")
	return flags, q
}

func observeExportFlagSet(command string, out io.Writer) *flag.FlagSet {
	flags, _ := observeExportFlags(command, out)
	return flags
}

// observeImportCommand imports one OTLP/JSON file of a source's spans into an
// observation log. It exits 0 when every span read was imported, skipped or
// already in the log, 1 when a line or a span was refused or an observation
// conflicted with the log, and 2 when nothing was written.
func observeImportCommand(args []string, stdout, stderr io.Writer) int {
	flags, source, logDir := observeImportFlags(observeImportName, io.Discard)
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 || *source == "" || *logDir == "" {
		return usageError(stderr, observeImportName, "takes --source and --log, and one file of OTLP/JSON trace lines")
	}
	raw, err := ondisk.ReadOwned(*source, maxDescriptorBytes, descriptorForbidden, os.Geteuid())
	if err != nil {
		return usageError(stderr, observeImportName, "--source: "+err.Error())
	}
	desc, err := observe.ReadDescriptor(raw)
	if err != nil {
		return usageError(stderr, observeImportName, "--source: "+err.Error())
	}
	batch, err := importFile(flags.Arg(0), desc, observe.DescriptorSHA256(raw))
	if err != nil {
		return usageError(stderr, observeImportName, err.Error())
	}
	log, err := observelog.Open(*logDir)
	if err != nil {
		return usageError(stderr, observeImportName, "--log: "+err.Error())
	}
	written, err := log.Write(batch.Observations, batch.Report)
	if err = errors.Join(err, log.Close()); err != nil {
		return usageError(stderr, observeImportName, "--log: "+err.Error())
	}
	return printImport(batch.Report.GetCounts(), written, stdout, stderr)
}

// maxInputBytes bounds one import's input, which is read whole.
const maxInputBytes = 256 << 20

// importFile reads the input as a regular file, so a pipe or a device put at
// its name cannot hold the command, and maps it at this moment's clock.
func importFile(path string, desc *observev1.SourceDescriptor, descriptorSHA256 string) (otelgenai.Batch, error) {
	raw, err := ondisk.ReadRegular(path, maxInputBytes, 0)
	if err != nil {
		return otelgenai.Batch{}, err
	}
	return otelgenai.Import(bytes.NewReader(raw), desc, descriptorSHA256, time.Now().UTC())
}

// printImport writes one line per count and returns the exit status the
// counts call for.
func printImport(c *observev1.ImportCounts, w observelog.Written, stdout, stderr io.Writer) int {
	var skipped uint64
	for _, n := range c.GetSkipped() {
		skipped += n
	}
	u := func(n uint64) string { return strconv.FormatUint(n, 10) }
	lines := [][2]string{
		{"spans_read", u(c.GetRead())}, {"observed", u(c.GetObserved())}, {"written", strconv.Itoa(w.Observations)},
		{"duplicate", strconv.Itoa(w.Duplicates)}, {"conflict", strconv.Itoa(w.Conflicts)}, {"other_resource", u(c.GetOtherResource())},
		{"refused", u(c.GetRefused())}, {"content_attributes_dropped", u(c.GetContentAttributesDropped())},
		{"reasoning_parts_dropped", u(c.GetReasoningPartsDropped())}, {"content_unparsed", u(c.GetContentUnparsed())},
		{"run_ids_dropped", u(c.GetRunIdsDropped())}, {"strings_dropped", u(c.GetStringsDropped())}, {"skipped", u(skipped)},
	}
	out := ""
	for _, l := range lines {
		out += l[0] + ": " + l[1] + "\n"
	}
	if _, err := io.WriteString(stdout, out); err != nil {
		return fail(stderr, observeImportName, fmt.Errorf("writing to standard output: %w; the import was written", err))
	}
	if c.GetRefused() > 0 || w.Conflicts > 0 {
		return fail(stderr, observeImportName, fmt.Errorf("%d span(s) or line(s) refused and %d observation(s) conflicting with the log; the import report holds the counts", c.GetRefused(), w.Conflicts))
	}
	return exitOK
}

// observeExportCommand writes the observation export of one log file. It
// exits 0 when the export holds no gap, 1 when it is whole and holds one, and
// 2 when it was refused or cut short.
func observeExportCommand(args []string, stdout, stderr io.Writer) int {
	flags, q := observeExportFlags(observeExportName, io.Discard)
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 {
		return usageError(stderr, observeExportName, "takes one observation log file")
	}
	tr, err := observelog.ExportFile(flags.Arg(0), *q, stdout)
	if err != nil {
		return usageError(stderr, observeExportName, err.Error())
	}
	if gaps := tr.Counts["gap"]; gaps > 0 {
		return fail(stderr, observeExportName, fmt.Errorf("%s: the export holds %d gap(s): lines that are not observation records this build reads, "+
			"an observation id with two contents, or a partial line no writer is writing", strconv.Quote(flags.Arg(0)), gaps))
	}
	return exitOK
}
