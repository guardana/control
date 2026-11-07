package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/gatewayconfig"
	"github.com/guardana/control/internal/recovery"
)

// The names the state commands report themselves under, and what follows
// them.
const (
	stateBackupName  = "state backup"
	stateRestoreName = "state restore"
	stateForm        = "<config> <dir>"
)

// stateFlagSet declares no flag: both commands take the plane's
// configuration and the backup's directory.
func stateFlagSet(command string, out io.Writer) *flag.FlagSet { return commandFlags(command, out) }

// stateArgs loads the configuration args name, with this process's
// environment, which is the plane's when the command runs as the plane's
// account, and returns it with the backup's directory.
func stateArgs(command string, args []string, stderr io.Writer) (*gatewayconfig.Config, string, int) {
	flags := stateFlagSet(command, stderr)
	if err := flags.Parse(args); err != nil {
		return nil, "", exitUsage
	}
	if flags.NArg() != 2 {
		return nil, "", usageError(stderr, command, "takes the plane's configuration and the backup's directory")
	}
	cfg, err := gatewayconfig.Load(flags.Arg(0), os.Environ())
	if err != nil {
		return nil, "", fail(stderr, command, err)
	}
	return cfg, flags.Arg(1), exitOK
}

func stateBackupCommand(args []string, stdout, stderr io.Writer) int {
	cfg, dir, code := stateArgs(stateBackupName, args, stderr)
	if code != exitOK {
		return code
	}
	taken, err := recovery.Backup(context.Background(), cfg, dir, time.Now())
	if err != nil {
		return fail(stderr, stateBackupName, err)
	}
	lines := []string{fmt.Sprintf("backed up %d files into %s", len(taken.Manifest.Entries), dir)}
	for _, key := range taken.Absent {
		lines = append(lines, key+": configured, and nothing stands there to back up")
	}
	if err := writeLines(stdout, lines); err != nil {
		return fail(stderr, stateBackupName, err)
	}
	return exitOK
}

func stateRestoreCommand(args []string, stdout, stderr io.Writer) int {
	cfg, dir, code := stateArgs(stateRestoreName, args, stderr)
	if code != exitOK {
		return code
	}
	report, err := recovery.Restore(context.Background(), cfg, dir, time.Now())
	if err != nil {
		return fail(stderr, stateRestoreName, err)
	}
	lines := append([]string{}, report.Done...)
	for _, key := range report.PutBack {
		lines = append(lines, key+": an operator's signed file, never written by a restore; the backup holds a copy to put back")
	}
	lines = append(lines,
		"a statement issued before the restore at a higher serial than the floor still confirms, within policy.max_stale",
		"next: "+brand.Gateway+" doctor --config <config> names each signed file the raised floors refuse")
	if err := writeLines(stdout, lines); err != nil {
		return fail(stderr, stateRestoreName, err)
	}
	return exitOK
}
