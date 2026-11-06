package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/guardana/control/internal/pause"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/policystate"
)

// The names these commands report themselves under, and how the help spells
// what follows them. The add form is cut in three lines because a help line
// wider than a terminal wraps and reads as two commands.
const (
	pauseInitName      = "pause init"
	pauseAddName       = "pause add"
	pauseRemoveName    = "pause remove"
	pauseListName      = "pause list"
	pauseFloorInitName = "pause floor init"
	pauseInitForm      = "[--key <k> --tenant <t> --project <p> [--serial <n>]] <file>"
	pauseAddForm       = "(--global | --provider <p> [--action <k> [--name <n>]]\n      | --run <id> | --agent <id> | --principal <id>) [--reason <r>]\n      [--key <k>] <f>"
	pauseRemoveForm    = "[--key <k>] <file> <id>"
	pauseFloorInitForm = "--tenant <t> --project <p> <dir>"
)

// pauseAuthority is what the help and docs/reference/cli.md both say about
// who may pause: the file's permissions, and under the signed authority an
// operator's signature over the whole file.
const pauseAuthority = "Write access to the pause file is the authority to pause a call and to lift a pause;\n" +
	"under approvals.authority signed, only a pause file signed with an operator's --key counts."

// pauseLockWait bounds how long a writer waits for another writer's lock on
// the pause file's directory. A running plane never takes it.
const pauseLockWait = 10 * time.Second

// keyFlag declares --key, the operator key a signed pause file is signed
// with.
func keyFlag(flags *flag.FlagSet) *string {
	return flags.String("key", "", "the operator's private key file, which signs the whole pause file for a plane under the signed authority")
}

// pauseSigner reads the operator key at path into the signer a signed write
// signs with. The key is kept for the command's life and cleared by release.
func pauseSigner(path string) (pause.Signer, func(), error) {
	key, err := policykey.ReadPrivate(path)
	if err != nil {
		return pause.Signer{}, nil, fmt.Errorf("--key: %w", err)
	}
	s := pause.Signer{KeyID: policykey.KeyID(publicHalf(key)), Sign: func(payloadType string, body []byte) ([]byte, error) {
		return bundle.SignBytesAs(payloadType, body, key)
	}}
	return s, func() { clear(key) }, nil
}

// pauseHint is what an operator does about a write the file's signing
// refused: a signed file takes only a signed write, and an unsigned one is
// made signed by a new file.
func pauseHint(err error) error {
	switch {
	case errors.Is(err, pause.ErrSigned):
		return fmt.Errorf("%w; sign the change with --key <operator key file>", err)
	case errors.Is(err, pause.ErrUnsigned):
		return fmt.Errorf("%w; a signed pause file starts with pause init --key", err)
	}
	return err
}

// scopeFlags is what pause add is told about the calls to pause.
type scopeFlags struct {
	global                         bool
	provider, action, name, reason string
	run, agent, principal          string
	key                            *string
}

func pauseAddFlags(command string, out io.Writer) (*flag.FlagSet, *scopeFlags) {
	flags := commandFlags(command, out)
	var s scopeFlags
	flags.BoolVar(&s.global, "global", false, "pause every call")
	flags.StringVar(&s.provider, "provider", "", "the upstream, by its configured name, whose calls to pause")
	flags.StringVar(&s.action, "action", "", "tool, prompt or resource: pause only calls of this kind to the provider")
	flags.StringVar(&s.name, "name", "", "the tool, by the name the plane routes by; a prompt or a resource is paused by its provider only")
	flags.StringVar(&s.run, "run", "", "the opened run whose calls to pause, or the root whose every run to pause")
	flags.StringVar(&s.agent, "agent", "", "the agent, as the listener's configuration names it, whose calls to pause")
	flags.StringVar(&s.principal, "principal", "", "the principal, as the listener's configuration names it, whose calls to pause")
	flags.StringVar(&s.reason, "reason", "", "why, for whoever lifts the pause; it decides nothing")
	s.key = keyFlag(flags)
	return flags, &s
}

func pauseAddFlagSet(command string, out io.Writer) *flag.FlagSet {
	flags, _ := pauseAddFlags(command, out)
	return flags
}

func pauseRemoveFlags(command string, out io.Writer) (*flag.FlagSet, *string) {
	flags := commandFlags(command, out)
	return flags, keyFlag(flags)
}

func pauseRemoveFlagSet(command string, out io.Writer) *flag.FlagSet {
	flags, _ := pauseRemoveFlags(command, out)
	return flags
}

// initFlags is what pause init is told: nothing for an unsigned file, and
// the key, the plane's tenant and project and the first serial for a signed
// one.
type initFlags struct {
	key, tenant, project *string
	serial               *int64
}

func pauseInitFlags(command string, out io.Writer) (*flag.FlagSet, initFlags) {
	flags := commandFlags(command, out)
	v := initFlags{key: keyFlag(flags)}
	v.tenant = flags.String("tenant", "", "the plane's tenant_id, which a signed pause file is for")
	v.project = flags.String("project", "", "the plane's project_id, which a signed pause file is for")
	v.serial = flags.Int64("serial", 1, "the signed file's first serial, above the plane's pause floor")
	return flags, v
}

func pauseInitFlagSet(command string, out io.Writer) *flag.FlagSet {
	flags, _ := pauseInitFlags(command, out)
	return flags
}

func pauseFloorInitFlags(command string, out io.Writer) (*flag.FlagSet, *string, *string) {
	flags := commandFlags(command, out)
	tenant := flags.String("tenant", "", "the plane's tenant_id")
	project := flags.String("project", "", "the plane's project_id")
	return flags, tenant, project
}

func pauseFloorInitFlagSet(command string, out io.Writer) *flag.FlagSet {
	flags, _, _ := pauseFloorInitFlags(command, out)
	return flags
}

// scope reads the one scope the flags name, or says why they name none or
// more than one. The values themselves are judged by internal/pause.
func (s scopeFlags) scope() (pause.Scope, string) {
	if scope, problem, ok := s.identityScope(); ok {
		return scope, problem
	}
	switch {
	case s.global && s.provider == "" && s.action == "" && s.name == "":
		return pause.Scope{Kind: pause.ScopeGlobal}, ""
	case s.global, s.provider == "":
		return pause.Scope{}, oneScope
	case s.action == "" && s.name != "":
		return pause.Scope{}, "--name names a tool, and needs --action tool"
	case s.action == "":
		return pause.Scope{Kind: pause.ScopeProvider, Provider: s.provider}, ""
	}
	return pause.Scope{Kind: pause.ScopeAction, Provider: s.provider, Action: s.action, Name: s.name}, ""
}

const oneScope = "names one scope: --global, --provider <name>, --action <kind> with --provider <name>, --run <id>, --agent <id> or --principal <id>"

// identityScope is the run, agent or principal scope the flags name, or why
// they name it beside another; ok is false when they name none of the three.
func (s scopeFlags) identityScope() (pause.Scope, string, bool) {
	kind, id, named := s.identity()
	switch {
	case named == 0:
		return pause.Scope{}, "", false
	case named > 1 || s.global || s.provider+s.action+s.name != "":
		return pause.Scope{}, oneScope, true
	}
	return pause.Scope{Kind: kind, ID: id}, "", true
}

// identity is the run, agent or principal scope the flags name, and how many
// of the three they set. An empty value is no scope, as for --provider.
func (s scopeFlags) identity() (pause.ScopeKind, string, int) {
	var kind pause.ScopeKind
	var id string
	named := 0
	for _, f := range []struct {
		kind pause.ScopeKind
		id   string
	}{{pause.ScopeRun, s.run}, {pause.ScopeAgent, s.agent}, {pause.ScopePrincipal, s.principal}} {
		if f.id != "" {
			kind, id, named = f.kind, f.id, named+1
		}
	}
	return kind, id, named
}

// pauseInitCommand writes a pause file that pauses nothing: unsigned, or
// with --key signed for the plane's tenant and project at --serial.
func pauseInitCommand(args []string, stdout, stderr io.Writer) int {
	flags, v := pauseInitFlags(pauseInitName, stderr)
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 1 {
		return usageError(stderr, pauseInitName, "takes the pause file, with every flag before it")
	}
	given := map[string]bool{}
	flags.Visit(func(f *flag.Flag) { given[f.Name] = true })
	ctx, cancel := context.WithTimeout(context.Background(), pauseLockWait)
	defer cancel()
	path := flags.Arg(0)
	if !given["key"] {
		if given["tenant"] || given["project"] || given["serial"] {
			return usageError(stderr, pauseInitName, "--tenant, --project and --serial make a signed pause file, with --key")
		}
		if err := pause.Init(ctx, path); err != nil {
			return fail(stderr, pauseInitName, err)
		}
		return report(stdout, stderr, pauseInitName, "created "+oneLine(path)+", which pauses nothing")
	}
	if !given["tenant"] || !given["project"] {
		return usageError(stderr, pauseInitName, "a signed pause file names the plane's --tenant and --project")
	}
	signer, release, err := pauseSigner(*v.key)
	if err != nil {
		return fail(stderr, pauseInitName, err)
	}
	defer release()
	h := pause.Header{KeyID: signer.KeyID, Tenant: *v.tenant, Project: *v.project, Serial: *v.serial}
	if err := pause.InitSigned(ctx, path, h, signer); err != nil {
		return fail(stderr, pauseInitName, err)
	}
	return report(stdout, stderr, pauseInitName, fmt.Sprintf("created %s, which pauses nothing, signed by key %s at serial %d",
		oneLine(path), signer.KeyID, h.Serial))
}

// pauseAddCommand adds one entry under an id it draws itself and prints that
// id alone, which is what pause remove takes. With --key it signs the whole
// file at the next serial.
func pauseAddCommand(args []string, stdout, stderr io.Writer) int {
	flags, s := pauseAddFlags(pauseAddName, stderr)
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 1 {
		return usageError(stderr, pauseAddName, "takes the pause file, with every flag before it")
	}
	scope, problem := s.scope()
	if problem != "" {
		return usageError(stderr, pauseAddName, problem)
	}
	e := pause.Entry{ID: rand.Text(), Scope: scope, CreatedAt: time.Now().UTC(), Reason: s.reason}
	if err := e.Check(); err != nil {
		return fail(stderr, pauseAddName, fmt.Errorf("the entry: %w", err))
	}
	err := writePause(*s.key, func(ctx context.Context, signer *pause.Signer) error {
		if signer == nil {
			return pause.Add(ctx, flags.Arg(0), e)
		}
		return pause.AddSigned(ctx, flags.Arg(0), e, *signer)
	})
	if err != nil {
		return fail(stderr, pauseAddName, err)
	}
	return report(stdout, stderr, pauseAddName, e.ID)
}

func pauseRemoveCommand(args []string, stdout, stderr io.Writer) int {
	flags, key := pauseRemoveFlags(pauseRemoveName, stderr)
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 2 {
		return usageError(stderr, pauseRemoveName, "takes the pause file and the id of the entry to remove, with every flag before them")
	}
	path, id := flags.Arg(0), flags.Arg(1)
	err := writePause(*key, func(ctx context.Context, signer *pause.Signer) error {
		if signer == nil {
			return pause.Remove(ctx, path, id)
		}
		return pause.RemoveSigned(ctx, path, id, *signer)
	})
	if err != nil {
		return fail(stderr, pauseRemoveName, err)
	}
	return report(stdout, stderr, pauseRemoveName, "removed "+oneLine(id))
}

// writePause runs write under the writers' lock wait, with the signer the
// key file at key makes, or none where key is empty.
func writePause(key string, write func(context.Context, *pause.Signer) error) error {
	var signer *pause.Signer
	if key != "" {
		s, release, err := pauseSigner(key)
		if err != nil {
			return err
		}
		defer release()
		signer = &s
	}
	ctx, cancel := context.WithTimeout(context.Background(), pauseLockWait)
	defer cancel()
	return pauseHint(write(ctx, signer))
}

// pauseFloorInitCommand makes a directory the pause floor of a plane of one
// tenant and project, with no serial yet, and never replaces one.
func pauseFloorInitCommand(args []string, stdout, stderr io.Writer) int {
	flags, tenant, project := pauseFloorInitFlags(pauseFloorInitName, stderr)
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 1 || *tenant == "" || *project == "" {
		return usageError(stderr, pauseFloorInitName, "takes --tenant and --project, then one directory, with every flag before it")
	}
	ctx, cancel := context.WithTimeout(context.Background(), floorLockWait)
	defer cancel()
	if err := policystate.InitPause(ctx, flags.Arg(0), *tenant, *project); err != nil {
		return fail(stderr, pauseFloorInitName, err)
	}
	return writeFloorLines(stdout, stderr, pauseFloorInitName,
		"tenant: "+oneLine(*tenant)+"\nproject: "+oneLine(*project)+"\nfloor: no serial\n", "the floor file was made as asked")
}

// pauseListCommand prints one line per entry, reason included: the reason is
// for the operator, and this is the operator's own view of the file. The file
// is read with every check a plane's read makes, so whatever refuses it here
// is a state a plane reading it blocks every call under, and it says so.
func pauseListCommand(args []string, stdout, stderr io.Writer) int {
	doc, err := pause.List(args[0])
	if err != nil {
		return fail(stderr, pauseListName, fmt.Errorf("%w; a plane reading this file blocks every call", err))
	}
	var b strings.Builder
	if h := doc.Signed; h != nil {
		fmt.Fprintf(&b, "signed by key %s for tenant %s project %s at serial %d; the plane checks the signature, this listing does not\n",
			oneLine(h.KeyID), oneLine(h.Tenant), oneLine(h.Project), h.Serial)
	}
	if len(doc.Entries) == 0 {
		return report(stdout, stderr, pauseListName, b.String()+"no entries")
	}
	for _, e := range doc.Entries {
		fmt.Fprintf(&b, "%s %s created %s reason %s\n", oneLine(e.ID), oneLine(scopeText(e.Scope)),
			e.CreatedAt.UTC().Format(time.RFC3339), oneLine(strconv.Quote(e.Reason)))
	}
	return report(stdout, stderr, pauseListName, strings.TrimSuffix(b.String(), "\n"))
}

// scopeText is a scope as a listing prints it.
func scopeText(s pause.Scope) string {
	switch s.Kind {
	case pause.ScopeGlobal:
		return "global"
	case pause.ScopeProvider:
		return "provider " + s.Provider
	case pause.ScopeRun, pause.ScopeAgent, pause.ScopePrincipal:
		return string(s.Kind) + " " + s.ID
	}
	if s.Name == "" {
		return s.Action + " " + s.Provider
	}
	return s.Action + " " + s.Provider + "/" + s.Name
}

// report writes a command's result to stdout. A write that fails is a
// failure; for a command that writes the pause file it also says the change
// was made all the same.
func report(stdout, stderr io.Writer, command, text string) int {
	if _, err := io.WriteString(stdout, text+"\n"); err != nil {
		if command != pauseListName {
			err = fmt.Errorf("%w; the pause file was changed as asked", err)
		}
		return fail(stderr, command, fmt.Errorf("writing to standard output: %w", err))
	}
	return exitOK
}
