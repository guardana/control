package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policystate"
)

// The names these commands report themselves under, and how the help spells
// what follows them. Each form takes a second line: with every flag spelled
// it is wider than a terminal.
const (
	stateInitName  = "policy state init"
	stateResetName = "policy state reset"
	stateInitForm  = "--kind plane|signer|route\n      (--bundle-id <id> | --route-id <id>) <dir>"
	stateResetForm = "--kind plane|signer --bundle-id <id> --reason <text>\n" +
		"      (--empty | --serial <n> --digest <d> --issued-at <t>) <dir>"
)

// floorAuthority is what the help and docs/reference/cli.md both say about
// who may lower a floor.
const floorAuthority = "Write access to a floor directory is the authority to lower its floors;\n" +
	"policy state reset is the one way that records why."

// stateFlagValues is what the state commands are told.
type stateFlagValues struct {
	kind, bundleID, routeID  string
	empty                    bool
	serial                   int64
	digest, issuedAt, reason string
}

// stateFlags is the flags both state commands take: whose floors, and the
// bundle id.
func stateFlags(command string, out io.Writer, kinds string) (*flag.FlagSet, *stateFlagValues) {
	flags := commandFlags(command, out)
	var v stateFlagValues
	flags.StringVar(&v.kind, "kind", "", kinds+": whose floors the directory keeps")
	flags.StringVar(&v.bundleID, "bundle-id", "", "the bundle id whose floor this is")
	return flags, &v
}

func stateInitFlags(command string, out io.Writer) (*flag.FlagSet, *stateFlagValues) {
	flags, v := stateFlags(command, out, "plane, signer or route")
	flags.StringVar(&v.routeID, "route-id", "", "the route id whose floor this is, with --kind route")
	return flags, v
}

func stateResetFlags(command string, out io.Writer) (*flag.FlagSet, *stateFlagValues) {
	flags, v := stateFlags(command, out, "plane or signer")
	flags.BoolVar(&v.empty, "empty", false, "set the floor to hold no serial")
	flags.Int64Var(&v.serial, "serial", 0, "the serial the floor is set to")
	flags.StringVar(&v.digest, "digest", "", "the digest of the bundle at that serial")
	flags.StringVar(&v.issuedAt, "issued-at", "", "the floor's issuedAt, YYYY-MM-DDTHH:MM:SSZ")
	flags.StringVar(&v.reason, "reason", "", "why, recorded in the floor file with the floor it replaces")
	return flags, v
}

func stateInitFlagSet(command string, out io.Writer) *flag.FlagSet {
	flags, _ := stateInitFlags(command, out)
	return flags
}

func stateResetFlagSet(command string, out io.Writer) *flag.FlagSet {
	flags, _ := stateResetFlags(command, out)
	return flags
}

// parseStateFlags parses args, requires each flag named in required and one
// argument, the directory, and returns which flags were given. On a refusal
// it has written the diagnostic, and the status it returns is not exitOK.
func parseStateFlags(flags *flag.FlagSet, command string, args []string, stderr io.Writer, required ...string) (map[string]bool, int) {
	if err := flags.Parse(args); err != nil {
		return nil, exitUsage
	}
	if flags.NArg() != 1 {
		return nil, usageError(stderr, command, "takes the floor directory, with every flag before it")
	}
	given := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { given[f.Name] = true })
	for _, name := range required {
		if !given[name] {
			return nil, usageError(stderr, command, "--"+name+" is missing")
		}
	}
	return given, exitOK
}

// stateInitCommand gives a bundle id, or with --kind route a route id, its
// floor file holding no serial yet, and never replaces one.
func stateInitCommand(args []string, stdout, stderr io.Writer) int {
	flags, v := stateInitFlags(stateInitName, stderr)
	given, status := parseStateFlags(flags, stateInitName, args, stderr, "kind")
	if status != exitOK {
		return status
	}
	if policystate.Kind(v.kind) == policystate.KindRoute {
		return stateInitRoute(flags.Arg(0), v.routeID, given, stdout, stderr)
	}
	switch {
	case given["route-id"]:
		return usageError(stderr, stateInitName, "--route-id names a route's floor, with --kind route")
	case !given["bundle-id"]:
		return usageError(stderr, stateInitName, "--bundle-id is missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), floorLockWait)
	defer cancel()
	if err := policystate.Init(ctx, flags.Arg(0), policystate.Kind(v.kind), v.bundleID); err != nil {
		return fail(stderr, stateInitName, err)
	}
	return writeFloorLines(stdout, stderr, stateInitName, "bundle_id: "+oneLine(v.bundleID)+"\nfloor: no serial\n",
		"the floor file was made as asked")
}

// stateInitRoute gives routeID its route floor file holding no serial yet,
// and never replaces one.
func stateInitRoute(dir, routeID string, given map[string]bool, stdout, stderr io.Writer) int {
	switch {
	case given["bundle-id"]:
		return usageError(stderr, stateInitName, "--bundle-id names a plane's or a signer's floor, not a route's")
	case !given["route-id"]:
		return usageError(stderr, stateInitName, "--route-id is missing")
	}
	ctx, cancel := context.WithTimeout(context.Background(), floorLockWait)
	defer cancel()
	if err := policystate.InitRoute(ctx, dir, routeID); err != nil {
		return fail(stderr, stateInitName, err)
	}
	return writeFloorLines(stdout, stderr, stateInitName, "route_id: "+oneLine(routeID)+"\nfloor: no serial\n",
		"the floor file was made as asked")
}

// stateResetCommand sets a floor to the value named, lower or not, and prints
// the floor it replaced and the new one. Every flag but the new floor's is
// required, and the new floor is named in full or as --empty, never in part.
func stateResetCommand(args []string, stdout, stderr io.Writer) int {
	flags, v := stateResetFlags(stateResetName, stderr)
	given, status := parseStateFlags(flags, stateResetName, args, stderr, "kind", "bundle-id", "reason")
	if status != exitOK {
		return status
	}
	if !namesOneFloor(given, v.empty) {
		return usageError(stderr, stateResetName, "takes --empty, or --serial, --digest and --issued-at, and not both")
	}
	to, err := v.floor()
	if err != nil {
		return fail(stderr, stateResetName, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), floorLockWait)
	defer cancel()
	from, err := policystate.Reset(ctx, flags.Arg(0), policystate.Kind(v.kind), to, v.reason)
	if err != nil {
		return fail(stderr, stateResetName, err)
	}
	lines := "bundle_id: " + oneLine(v.bundleID) + "\nfrom: " + floorText(from) + "\nto: " + floorText(to) + "\n"
	return writeFloorLines(stdout, stderr, stateResetName, lines, "the floor was reset as asked")
}

// namesOneFloor reports whether the flags given name the new floor once:
// --empty alone, or --serial, --digest and --issued-at together.
func namesOneFloor(given map[string]bool, empty bool) bool {
	valued := given["serial"] || given["digest"] || given["issued-at"]
	whole := given["serial"] && given["digest"] && given["issued-at"]
	if given["empty"] {
		return empty && !valued
	}
	return whole
}

// floor is the floor reset sets: none at all with --empty, else the one the
// three values name, its latest issuedAt its issuedAt.
func (v stateFlagValues) floor() (policy.Floor, error) {
	if v.empty {
		return policy.EmptyFloor(v.bundleID)
	}
	at, err := policy.ParseIssuedAt(v.issuedAt)
	if err != nil {
		return policy.Floor{}, fmt.Errorf("--issued-at: %w", err)
	}
	return policy.NewFloor(v.bundleID, v.serial, v.digest, at, at)
}

// writeFloorLines writes a state command's result. A write that fails is a
// failure that says the change was made all the same.
func writeFloorLines(stdout, stderr io.Writer, command, lines, done string) int {
	if _, err := io.WriteString(stdout, lines); err != nil {
		return fail(stderr, command, fmt.Errorf("writing to standard output: %w; %s", err, done))
	}
	return exitOK
}
