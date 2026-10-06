package main

import (
	"bytes"
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

	"google.golang.org/protobuf/proto"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	ondisk "github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
	"github.com/guardana/control/internal/policykey"
	"github.com/guardana/control/internal/policystate"
)

// The name renew reports itself under, and how the help spells what follows
// it. The form carries no value placeholders, since every flag names a file
// or a directory and with them the line is wider than a terminal.
const (
	renewName = "policy renew"
	renewForm = "--key --bundle --bundle-public-key --floor --out"
)

// renew's refusals of what it was given, beside those of the packages it
// calls.
const (
	// errNotABundle is a --bundle file that is not a serialized policy bundle.
	errNotABundle = "not a serialized policy bundle"
	// errFloorDirectory is an --out in a floor directory, a signer's or a
	// plane's, which holds nothing but its own files.
	errFloorDirectory = "the directory is a floor directory, which holds no statement"
	// errOneKey is a freshness key whose public half is the bundle's, or its
	// negation: the key that vouches for bundles would vouch for those it
	// signs itself.
	errOneKey = "--key and --bundle-public-key are one key pair; the freshness key is kept apart from the bundle key"
	// errRaceLost is an --out holding a newer statement for the bundle id
	// than the one the floor took, which no renew under this floor
	// directory's lock wrote.
	errRaceLost = "it holds a newer statement for the bundle id, which another renew wrote meanwhile; it was left as it is"
)

// floorMarker is the name internal/policystate gives a floor directory's
// marker, by which renew knows a floor directory before it opens one.
const floorMarker = "floors.meta"

// afterRaise runs between the raise and the write, under the floor
// directory's lock. A test starts a renew that runs meanwhile from it.
var afterRaise = func() {}

// maxPublicKeyFileBytes bounds the public key file: one line of base64 and a
// newline, with room to spare.
const maxPublicKeyFileBytes = 1 << 10

// publicLineBytes is the length of the public key line: standard base64 of
// 32 bytes.
const publicLineBytes = 44

// floorLockWait bounds how long a command waits for another holder of the
// floor directory's lock. A test shortens it.
var floorLockWait = 10 * time.Second

// renewPaths is what renew is told.
type renewPaths struct{ key, bundle, bundlePublicKey, floor, out string }

func renewFlags(command string, out io.Writer) (*flag.FlagSet, *renewPaths) {
	flags := commandFlags(command, out)
	var p renewPaths
	flags.StringVar(&p.key, "key", "", "the freshness private key file keygen wrote")
	flags.StringVar(&p.bundle, "bundle", "", "the authority's own copy of the bundle the statement vouches for")
	flags.StringVar(&p.bundlePublicKey, "bundle-public-key", "", "the public key file of the key that signed the bundle")
	flags.StringVar(&p.floor, "floor", "", "the signer's floor directory, made by policy state init --kind signer")
	flags.StringVar(&p.out, "out", "", "the statement file to write, replacing only a statement")
	return flags, &p
}

func renewFlagSet(command string, out io.Writer) *flag.FlagSet {
	flags, _ := renewFlags(command, out)
	return flags
}

// renewCommand checks its own arguments for key text, as keygenCommand does.
func renewCommand(args []string, stdout, stderr io.Writer) int {
	if err := policykey.CheckArguments(args); err != nil {
		return usageError(stderr, renewName, err.Error())
	}
	flags, p := renewFlags(renewName, io.Discard)
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 ||
		p.key == "" || p.bundle == "" || p.bundlePublicKey == "" || p.floor == "" || p.out == "" {
		return usageError(stderr, renewName, "takes --key, --bundle, --bundle-public-key, --floor and --out, and no argument")
	}
	return renew(*p, time.Now(), stdout, stderr)
}

// renew refuses in a fixed order and writes nothing on any refusal: the
// output path, the two keys, the bundle under its public key, the signer's
// floor. It raises the floor before it writes the statement, so no statement
// is ever written that the floor did not take, and holds the floor
// directory's lock until the statement is written, so two renews under one
// floor write in the order they raised. A write that fails after the raise
// leaves the floor at a statement nobody holds, which only refuses what it
// would have refused anyway, and a second run at the same or a later second
// is taken.
func renew(p renewPaths, now time.Time, stdout, stderr io.Writer) int {
	if err := policykey.CheckPlatform(); err != nil {
		return fail(stderr, renewName, err)
	}
	issuedAt := now.UTC().Truncate(time.Second)
	out := filepath.Clean(p.out)
	if err := checkStatementOut(out); err != nil {
		return fail(stderr, renewName, err)
	}
	s, err := signedStatement(p, issuedAt)
	if err != nil {
		return fail(stderr, renewName, err)
	}
	if err := raiseAndWrite(p.floor, out, s, issuedAt); err != nil {
		return fail(stderr, renewName, err)
	}
	lines := "bundle_id: " + oneLine(s.st.BundleID()) + "\n" +
		"serial: " + strconv.FormatInt(s.st.Serial(), 10) + "\n" +
		"digest: " + s.st.Digest() + "\n" +
		"issued_at: " + policy.FormatIssuedAt(s.st.IssuedAt()) + "\n"
	if _, err := io.WriteString(stdout, lines); err != nil {
		return fail(stderr, renewName, fmt.Errorf("writing to standard output: %w; the statement was written to %s", err, out))
	}
	return exitOK
}

// signed is a freshness statement renew signed, its envelope, and the public
// half of the key that signed it.
type signed struct {
	env      policy.StatementEnvelope
	st       policy.Statement
	freshPub ed25519.PublicKey
}

// signedStatement reads both keys and the bundle, refusing in renew's order,
// and signs a statement for the bundle at issuedAt. The private key is
// cleared before it returns.
func signedStatement(p renewPaths, issuedAt time.Time) (signed, error) {
	bundlePub, err := readPublicKey("--bundle-public-key", p.bundlePublicKey)
	if err != nil {
		return signed{}, err
	}
	key, err := policykey.ReadPrivate(p.key)
	if err != nil {
		return signed{}, fmt.Errorf("--key: %w", err)
	}
	defer clear(key)
	freshPub, _ := key.Public().(ed25519.PublicKey)
	if policykey.SameKey(freshPub, bundlePub) {
		return signed{}, errors.New(errOneKey)
	}
	snap, err := verifiedBundle(p.bundle, bundlePub, issuedAt)
	if err != nil {
		return signed{}, err
	}
	env, st, err := signStatement(snap, key, issuedAt)
	if err != nil {
		return signed{}, err
	}
	return signed{env: env, st: st, freshPub: freshPub}, nil
}

// raiseAndWrite raises the signer floor to s and, still holding the floor
// directory's lock, checks out for a newer statement and writes s there. An
// error after the write says the statement was written.
func raiseAndWrite(floor, out string, s signed, issuedAt time.Time) error {
	written := false
	err := raiseSignerFloor(floor, s.st, issuedAt, func() error {
		afterRaise()
		if err := checkNotNewer(out, s.st, s.freshPub); err != nil {
			return err
		}
		if err := policykey.WriteStatement(out, s.env); err != nil {
			return fmt.Errorf("--out %s: %w; the signer floor was raised to the statement", out, err)
		}
		written = true
		return nil
	})
	if err != nil && written {
		return fmt.Errorf("%w; the statement was written to %s", err, out)
	}
	return err
}

// checkStatementOut refuses, before anything is raised, an output path whose
// directory is missing or is a floor directory, and one that exists and is
// not a regular file holding a freshness statement. A floor directory always
// holds its marker, since policystate opens none without one, so the marker
// tells the signer's own floor directory and a plane's alike.
// policykey.WriteStatement judges the path again as it writes; this check
// keeps a refused path from costing a raise.
func checkStatementOut(out string) error {
	switch _, err := os.Stat(filepath.Join(filepath.Dir(out), floorMarker)); {
	case err == nil:
		return fmt.Errorf("--out %s: %s", out, errFloorDirectory)
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("--out %s: %w", out, err)
	}
	info, err := os.Lstat(out)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := ondisk.CheckDir(filepath.Dir(out), 0); err != nil {
			return fmt.Errorf("--out %s: %w", out, err)
		}
		return nil
	case err != nil:
		return fmt.Errorf("--out %s: %w", out, err)
	case !info.Mode().IsRegular():
		return fmt.Errorf("--out %s: %w", out, policykey.ErrStatementOut)
	}
	raw, err := ondisk.ReadRegular(out, policykey.MaxStatementFileBytes, 0)
	// The file judged may be a key given in the wrong place.
	defer clear(raw)
	if err != nil || !policykey.IsStatement(raw) {
		return fmt.Errorf("--out %s: %w", out, policykey.ErrStatementOut)
	}
	return nil
}

// checkNotNewer refuses an out that holds, under the freshness key, a
// statement for st's bundle id ordered after st, which a renew under another
// floor directory wrote. One equal to st is st, byte for byte, since an
// Ed25519 signature over one body is one value, so writing it again changes
// nothing. An absent out, or one holding a statement this key did not sign
// or another id's, is replaced; one that can no longer be read as a
// statement is refused, since it was one before the raise.
func checkNotNewer(out string, st policy.Statement, freshPub ed25519.PublicKey) error {
	env, err := policykey.ReadStatement(out)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("--out %s: %w; the signer floor was raised to this renew's statement", out, err)
	}
	held, ours := signedBy(env, freshPub)
	if !ours || held.BundleID() != st.BundleID() {
		return nil
	}
	if held.Serial() > st.Serial() || (held.Serial() == st.Serial() && held.IssuedAt().After(st.IssuedAt())) {
		return fmt.Errorf("--out %s: %s; the signer floor was raised to this renew's statement", out, errRaceLost)
	}
	return nil
}

// signedBy reads env as a statement pub signed, and reports whether it is
// one.
func signedBy(env policy.StatementEnvelope, pub ed25519.PublicKey) (policy.Statement, bool) {
	st, err := policy.VerifyStatement(env, bundle.Keyring{policykey.KeyID(pub): pub})
	return st, err == nil
}

// readPublicKey reads the public key file flagName names. A file the length
// of no public key line is refused before it becomes a string, which nothing
// can clear: the file may be a private key given in the wrong place.
func readPublicKey(flagName, path string) (ed25519.PublicKey, error) {
	line, err := ondisk.ReadRegular(path, maxPublicKeyFileBytes, 0)
	defer clear(line)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", flagName, err)
	}
	if n := len(bytes.TrimSuffix(line, []byte("\n"))); n != publicLineBytes {
		return nil, fmt.Errorf("%s: %w", flagName, policykey.ErrPublicLine)
	}
	pub, err := policykey.ParsePublic(string(line))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", flagName, err)
	}
	return pub, nil
}

// verifiedBundle reads the bundle and verifies it under pub, by the id that
// key derives, with every check a plane's load makes: the signature, the
// canonical form, the digest.
func verifiedBundle(bundlePath string, pub ed25519.PublicKey, now time.Time) (*policy.Snapshot, error) {
	raw, err := ondisk.ReadRegular(bundlePath, maxReadBytes, 0)
	// The file named may be a key given in the wrong place; the decoder
	// copies what it keeps.
	defer clear(raw)
	if err != nil {
		return nil, fmt.Errorf("--bundle %s: %w", bundlePath, err)
	}
	var b controlv1.PolicyBundle
	if !policykey.IsBundle(raw) || proto.Unmarshal(raw, &b) != nil {
		return nil, fmt.Errorf("--bundle %s: %s", bundlePath, errNotABundle)
	}
	snap, err := policy.Load(&b, bundle.Keyring{policykey.KeyID(pub): pub}, now)
	if err != nil {
		return nil, fmt.Errorf("--bundle %s: %w", bundlePath, err)
	}
	return snap, nil
}

// signStatement signs a statement for snap's bundle at issuedAt and reads it
// back under the key's own public half, which is what makes the Statement a
// floor can take.
func signStatement(snap *policy.Snapshot, key ed25519.PrivateKey, issuedAt time.Time) (policy.StatementEnvelope, policy.Statement, error) {
	pub, _ := key.Public().(ed25519.PublicKey)
	keyID := policykey.KeyID(pub)
	env, err := policy.SignStatement(snap.Ref().GetBundleId(), snap.Serial(), snap.Ref().GetDigest(), issuedAt, key, keyID)
	if err != nil {
		return policy.StatementEnvelope{}, policy.Statement{}, err
	}
	st, err := policy.VerifyStatement(env, bundle.Keyring{keyID: pub})
	if err != nil {
		return policy.StatementEnvelope{}, policy.Statement{}, err
	}
	return env, st, nil
}

// raiseSignerFloor raises the signer's floor in dir to st and, once the floor
// took it, runs then under the directory's lock. Opening the directory and
// raising share one wait of floorLockWait for its lock. A refusal of the
// raise names what the floor holds when it can be read; then's error is
// returned as it is.
func raiseSignerFloor(dir string, st policy.Statement, now time.Time, then func() error) (err error) {
	ctx, cancel := context.WithTimeout(context.Background(), floorLockWait)
	defer cancel()
	store, err := policystate.OpenContext(ctx, dir, policystate.KindSigner)
	if err != nil {
		return fmt.Errorf("--floor %s: %w", dir, lockWaited(err))
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("--floor %s: %w", dir, closeErr))
		}
	}()
	var ran bool
	var thenErr error
	_, err = store.RaiseWith(ctx, st, now, func(policy.Floor) error {
		ran = true
		thenErr = then()
		return thenErr
	})
	switch {
	case err == nil:
		return nil
	case ran && thenErr != nil:
		return err
	case ran:
		return fmt.Errorf("--floor %s: %w", dir, err)
	}
	err = lockWaited(err)
	id := oneLine(st.BundleID())
	if f, readErr := store.Floor(ctx, st.BundleID()); readErr == nil {
		return fmt.Errorf("--floor %s: bundle id %s: %w; the signer floor holds %s", dir, id, err, floorText(f))
	}
	return fmt.Errorf("--floor %s: bundle id %s: %w", dir, id, err)
}

// lockWaited names a wait for the floor directory's lock that ran out.
func lockWaited(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w; another holder kept the floor directory's lock past %s", err, floorLockWait)
	}
	return err
}

// floorText is a floor as the commands print it.
func floorText(f policy.Floor) string {
	if !f.HasSerial() {
		return "no serial"
	}
	return "serial " + strconv.FormatInt(f.Serial(), 10) + " digest " + f.Digest() +
		" issued_at " + policy.FormatIssuedAt(f.IssuedAt()) + " latest_issued_at " + policy.FormatIssuedAt(f.LatestIssuedAt())
}
