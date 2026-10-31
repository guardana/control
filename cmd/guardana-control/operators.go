package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	ondisk "github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/trust"
)

const (
	rosterSignName = "operators roster sign"
	rosterSignForm = "--key <file> --out <file>\n      [--valid-for <duration>] <roster.json>"
	floorInitName  = "operators floor init"
	floorInitForm  = "--roster-id <id> <dir>"
)

// operatorsAuthority is what the help and docs/reference/cli.md both say
// about who names an operator.
const operatorsAuthority = "The trust root names every operator key until its roster expires, and each signing\n" +
	"needs a higher serial; write access to its floor directory lowers the floor."

// rosterFileMode is the mode roster sign writes with: a signed roster is
// public, as a signed route is.
const rosterFileMode fs.FileMode = 0o644

type rosterSignValues struct {
	key, out string
	validFor time.Duration
}

func rosterSignFlags(command string, out io.Writer) (*flag.FlagSet, *rosterSignValues) {
	flags := commandFlags(command, out)
	var v rosterSignValues
	flags.StringVar(&v.key, "key", "", "the private key file keygen wrote for the trust root")
	flags.StringVar(&v.out, "out", "", "the roster file to write, replacing only a file holding a roster envelope, by its payload type, at a lower serial")
	flags.DurationVar(&v.validFor, "valid-for", trust.DefaultLifetime, "how long the roster is trusted, at most 168h")
	return flags, &v
}

func rosterSignFlagSet(command string, out io.Writer) *flag.FlagSet {
	flags, _ := rosterSignFlags(command, out)
	return flags
}

// rosterSignCommand checks its own arguments for key text, as signCommand
// does.
func rosterSignCommand(args []string, stdout, stderr io.Writer) int {
	if err := policykey.CheckArguments(args); err != nil {
		return usageError(stderr, rosterSignName, err.Error())
	}
	flags, v := rosterSignFlags(rosterSignName, io.Discard)
	if err := flags.Parse(args); err != nil || flags.NArg() != 1 || v.key == "" || v.out == "" {
		return usageError(stderr, rosterSignName, "takes --key and --out, then one roster source, with every flag before it")
	}
	return rosterSign(*v, flags.Arg(0), time.Now().UTC().Truncate(time.Second), stdout, stderr)
}

// rosterSign refuses in a fixed order and writes nothing on any refusal: the
// source, the output path, the key, the roster it signs, which it verifies
// before it writes, and then a roster at --out of the same id at that serial
// or above. Two rosters of one id and serial are a fork every plane refuses,
// so each signing needs a higher serial.
func rosterSign(v rosterSignValues, source string, now time.Time, stdout, stderr io.Writer) int {
	if err := policykey.CheckPlatform(); err != nil {
		return fail(stderr, rosterSignName, err)
	}
	raw, err := ondisk.ReadRegular(source, trust.MaxBodyBytes, 0)
	if err != nil {
		return fail(stderr, rosterSignName, fmt.Errorf("source %s: %w", source, err))
	}
	out := filepath.Clean(v.out)
	prev, err := checkRosterOut(out)
	if err != nil {
		return fail(stderr, rosterSignName, err)
	}
	key, err := policykey.ReadPrivate(v.key)
	if err != nil {
		return fail(stderr, rosterSignName, fmt.Errorf("--key: %w", err))
	}
	defer clear(key)
	env, d, err := trust.SignDocument(raw, now, v.validFor, key)
	if err != nil {
		return fail(stderr, rosterSignName, fmt.Errorf("source %s: %w", source, err))
	}
	if err := higherThan(prev, d, key); err != nil {
		return fail(stderr, rosterSignName, fmt.Errorf("--out %s: %w", out, err))
	}
	body, err := trust.MarshalFile(env)
	if err != nil {
		return fail(stderr, rosterSignName, err)
	}
	if err := ondisk.Replace(filepath.Dir(out), filepath.Base(out), body, rosterFileMode); err != nil {
		return fail(stderr, rosterSignName, fmt.Errorf("--out %s: %w", out, err))
	}
	lines := "roster_id: " + d.ID() + "\n" +
		"serial: " + strconv.FormatInt(d.Serial(), 10) + "\n" +
		"digest: " + d.Digest() + "\n" +
		"keys: " + strconv.Itoa(len(d.Keys())) + "\n" +
		"issued_at: " + policy.FormatIssuedAt(d.IssuedAt()) + "\n" +
		"expires_at: " + policy.FormatIssuedAt(d.ExpiresAt()) + "\n" +
		"root_key_id: " + env.Signatures[0].KeyID + "\n" +
		"out: " + oneLine(out) + "\n"
	if _, err := io.WriteString(stdout, lines); err != nil {
		return fail(stderr, rosterSignName, fmt.Errorf("writing to standard output: %w; the roster was written to %s", err, out))
	}
	return exitOK
}

// checkRosterOut refuses an output path whose directory is missing, and one
// that exists and is not a regular file holding an envelope of the roster's
// payload type, so a mistyped path never replaces a key or another signed
// file. It returns the envelope it found, or nil when nothing stands at out;
// it checks no signature.
func checkRosterOut(out string) (*trust.Envelope, error) {
	info, err := os.Lstat(out)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := ondisk.CheckDir(filepath.Dir(out), 0); err != nil {
			return nil, fmt.Errorf("--out %s: %w", out, err)
		}
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("--out %s: %w", out, err)
	}
	notARoster := fmt.Errorf("--out %s exists and is not a roster's envelope, and roster sign replaces nothing else", out)
	if !info.Mode().IsRegular() {
		return nil, notARoster
	}
	raw, err := ondisk.ReadRegular(out, trust.MaxFileBytes, 0)
	if errors.Is(err, ondisk.ErrTooLarge) {
		return nil, notARoster
	}
	if err != nil {
		return nil, fmt.Errorf("--out %s: %w", out, err)
	}
	env, err := trust.ParseFile(raw)
	if err != nil || env.PayloadType != trust.PayloadType {
		return nil, notARoster
	}
	return &env, nil
}

// higherThan refuses a roster whose serial is not above that of prev, the
// roster --out holds, when prev verifies under the same root and names the
// same roster id. An envelope that does not verify under this root is
// another root's, or damaged, and no plane pinning this root reads it.
func higherThan(prev *trust.Envelope, next trust.Document, root ed25519.PrivateKey) error {
	if prev == nil {
		return nil
	}
	held, verified := verifiedUnder(*prev, root.Public().(ed25519.PublicKey))
	if !verified || held.ID() != next.ID() || held.Serial() < next.Serial() {
		return nil
	}
	return fmt.Errorf("it holds roster %s at serial %d, and each signing needs a higher serial than that, not %d",
		held.ID(), held.Serial(), next.Serial())
}

func verifiedUnder(env trust.Envelope, root ed25519.PublicKey) (trust.Document, bool) {
	d, err := trust.VerifyDocument(env, root)
	return d, err == nil
}

func floorInitFlags(command string, out io.Writer) (*flag.FlagSet, *string) {
	flags := commandFlags(command, out)
	var id string
	flags.StringVar(&id, "roster-id", "", "the roster id whose floor this is")
	return flags, &id
}

func floorInitFlagSet(command string, out io.Writer) *flag.FlagSet {
	flags, _ := floorInitFlags(command, out)
	return flags
}

// floorInitCommand gives a roster id its authority floor file holding no
// serial yet, and never replaces one.
func floorInitCommand(args []string, stdout, stderr io.Writer) int {
	flags, id := floorInitFlags(floorInitName, stderr)
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}
	if flags.NArg() != 1 || *id == "" {
		return usageError(stderr, floorInitName, "takes --roster-id, then one directory, with every flag before it")
	}
	ctx, cancel := context.WithTimeout(context.Background(), floorLockWait)
	defer cancel()
	if err := policystate.InitAuthority(ctx, flags.Arg(0), *id); err != nil {
		return fail(stderr, floorInitName, err)
	}
	return writeFloorLines(stdout, stderr, floorInitName, "roster_id: "+oneLine(*id)+"\nfloor: no serial\n",
		"the floor file was made as asked")
}
