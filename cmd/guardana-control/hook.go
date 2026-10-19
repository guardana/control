package main

import (
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"time"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/enforceclient"
	"github.com/guardana/control/internal/hookbridge"
)

const (
	hookName = "hook"
	hookForm = "(--profile <id> | --profile-file <file>) --socket <path>\n" +
		"      --state-dir <dir>"
)

// hookTokenVariable names the environment variable the run token is read
// from. A token in a flag would sit in the runtime's settings files and in
// the process list; one in the event is the agent's to write.
var hookTokenVariable = brand.Env("RUN_TOKEN")

// hookStdin is where the runtime writes the event; a test sets it.
var hookStdin io.Reader = os.Stdin

// exitHookRefused is the status of every refusal made before a profile is
// read: a usage error, no token, no profile. An event the profile cannot
// read exits with the profile's block status instead. It is the status the
// runtime blocks a call on, and for a completion it only shows the line.
const exitHookRefused = exitUsage

type hookArgs struct {
	profile, profileFile, socket, stateDir string
}

func hookFlags(command string, out io.Writer) (*flag.FlagSet, *hookArgs) {
	flags := commandFlags(command, out)
	a := &hookArgs{}
	flags.StringVar(&a.profile, "profile", "", "the id of a profile built into this binary")
	flags.StringVar(&a.profileFile, "profile-file", "", "a profile file, in place of a built-in one")
	flags.StringVar(&a.socket, "socket", "", "the enforcement receiver's socket")
	flags.StringVar(&a.stateDir, "state-dir", "", "an owner-only directory for the handles between an admit and its completion")
	return flags, a
}

func hookFlagSet(command string, out io.Writer) *flag.FlagSet {
	flags, _ := hookFlags(command, out)
	return flags
}

// hookCommand answers one hook event a coding runtime writes on standard
// input, as the profile says the runtime reads an answer. An admit the plane
// lets run exits 0 and prints nothing; every other admit, and every refusal
// before the event is read, exits with the profile's block status, 2 when
// no profile was read, and one line on standard error. A completion exits 0
// once the plane recorded it and with the profile's problem status otherwise.
// Standard output is never written: no reply allows, asks, defers or changes
// a call.
func hookCommand(args []string, _, stderr io.Writer) int {
	a, token, p, ev, code := hookEvent(args, stderr)
	if p == nil {
		return code
	}
	ctx, cancel := context.WithTimeout(context.Background(), p.HookTimeout*9/10)
	defer cancel()
	client, err := enforceclient.New(enforceclient.Config{Socket: a.socket, Token: token, Timeout: p.HookTimeout * 4 / 10})
	if err != nil {
		return hookAnswer(stderr, p, ev, err)
	}
	store, err := hookbridge.OpenStore(a.stateDir)
	if err != nil {
		return hookAnswer(stderr, p, ev, err)
	}
	defer func() { _ = store.Close() }()
	if _, err := store.Sweep(time.Now()); err != nil {
		writeLine(stderr, brand.CLI+": "+hookName+": sweeping expired entries: "+oneLine(err.Error()))
	}
	if ev.Phase == hookbridge.PhaseAdmit {
		out := hookbridge.Pre(ctx, p, ev, client, store, token)
		if out.Allow {
			return exitOK
		}
		writeLine(stderr, brand.CLI+": "+hookName+": "+oneLine(out.Reason))
		return p.Reply.BlockExitCode
	}
	out := hookbridge.Post(ctx, p, ev, client, store, token)
	if out.Result == hookbridge.PostRecorded {
		return exitOK
	}
	return hookAnswer(stderr, p, ev, errors.Join(errors.New(postResultText(out.Result)), out.Err))
}

// hookEvent parses the flags, reads the run token and the profile, and reads
// the event, or returns no profile and the status of the first refusal.
func hookEvent(args []string, stderr io.Writer) (hookArgs, string, *hookbridge.Profile, hookbridge.Event, int) {
	flags, a := hookFlags(hookName, io.Discard)
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 ||
		(a.profile == "") == (a.profileFile == "") || a.socket == "" || a.stateDir == "" {
		return *a, "", nil, hookbridge.Event{}, usageError(stderr, hookName,
			"takes one of --profile and --profile-file, --socket and --state-dir, and no argument")
	}
	token := os.Getenv(hookTokenVariable)
	if token == "" {
		return *a, "", nil, hookbridge.Event{}, hookRefused(stderr, "no run token in "+hookTokenVariable)
	}
	p, err := hookProfile(*a)
	if err != nil {
		return *a, "", nil, hookbridge.Event{}, hookRefused(stderr, "profile: "+err.Error())
	}
	ev, err := hookbridge.ReadEvent(p, hookStdin)
	if err != nil {
		hookRefused(stderr, "event: "+err.Error())
		return *a, "", nil, hookbridge.Event{}, p.Reply.BlockExitCode
	}
	return *a, token, p, ev, exitOK
}

func hookProfile(a hookArgs) (*hookbridge.Profile, error) {
	if a.profileFile != "" {
		return hookbridge.ReadProfileFile(a.profileFile)
	}
	return hookbridge.LoadProfile(a.profile)
}

// hookAnswer reports a failure once the event's phase is known: an admit is
// blocked, a completion that was not recorded exits with the profile's
// problem status.
func hookAnswer(stderr io.Writer, p *hookbridge.Profile, ev hookbridge.Event, err error) int {
	writeLine(stderr, brand.CLI+": "+hookName+": "+oneLine(err.Error()))
	if ev.Phase == hookbridge.PhaseAdmit {
		return p.Reply.BlockExitCode
	}
	return p.Reply.ProblemExitCode
}

func hookRefused(stderr io.Writer, message string) int {
	writeLine(stderr, brand.CLI+": "+hookName+": "+oneLine(message))
	return exitHookRefused
}

func postResultText(r hookbridge.PostResult) string {
	switch r {
	case hookbridge.PostNotRecorded:
		return "the plane did not record the completion"
	case hookbridge.PostUnmatched:
		return "no admit of this call is open"
	case hookbridge.PostNoInput:
		return "the completion carries no tool input; the plane closes the call as abandoned"
	}
	return "the completion could not be reported"
}
