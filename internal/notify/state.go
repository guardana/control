package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/guardana/control/internal/files"
)

const (
	markerName    = "notify-state"
	markerBody    = "notify-state 1\n"
	lockName      = "lock"
	deliveredName = "delivered.jsonl"
	digestName    = "log-first-line.sha256"
)

// forbidden is the permission bits neither the state directory nor a file
// in it may have: the delivered list says what one account was told.
const forbidden fs.FileMode = 0o077

// state is one state directory, held under its lock for one run. digest is
// the hex SHA-256 of the findings log's first line, newline included, or
// empty while the log the state started on had none.
type state struct {
	root   *os.Root
	lock   *os.File
	list   *os.File
	size   int64
	keys   map[string]bool
	digest string
	ops    fileOps
}

// openState judges and holds the state in dir. Init starts one where no
// marker is; without it a missing marker is ErrNotInitialised and nothing is
// created.
func openState(dir string, init bool, ops fileOps) (*state, error) {
	root, err := openDir(dir)
	if err != nil {
		return nil, err
	}
	s := &state{root: root, ops: ops}
	if err := s.open(init); err != nil {
		return nil, errors.Join(err, s.close())
	}
	return s, nil
}

func (s *state) open(init bool) error {
	if !init {
		if err := s.checkMarker(); err != nil {
			return err
		}
	}
	if err := s.takeLock(init); err != nil {
		return err
	}
	if init {
		if err := s.start(); err != nil {
			return err
		}
	}
	if err := s.readDigest(); err != nil {
		return err
	}
	return s.openList()
}

func (s *state) close() error {
	var errs []error
	for _, f := range []*os.File{s.list, s.lock} {
		if f != nil {
			errs = append(errs, f.Close())
		}
	}
	return errors.Join(append(errs, s.root.Close())...)
}

func (s *state) checkMarker() error {
	body, err := s.readSmall(markerName, int64(len(markerBody)))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ErrNotInitialised
	case err != nil:
		return err
	case string(body) != markerBody:
		return fmt.Errorf("%w: %s is not a marker this package writes", ErrState, markerName)
	}
	return nil
}

// takeLock holds the lock file for the run. Init creates it; a started
// state without one is damaged.
func (s *state) takeLock(init bool) error {
	f, err := s.openIn(lockName, os.O_RDWR)
	if init && errors.Is(err, fs.ErrNotExist) {
		f, err = s.root.OpenFile(lockName, os.O_RDWR|os.O_CREATE|os.O_EXCL|openFlags, 0o600)
	}
	if err != nil {
		return err
	}
	s.lock = f
	return lock(f)
}

// start writes an empty delivered list and an unbound digest, then the
// marker, so a state is started only once both are on disk.
func (s *state) start() error {
	switch _, err := s.root.Lstat(markerName); {
	case err == nil:
		return ErrInitialised
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if err := files.ReplaceIn(s.root, deliveredName, nil, 0o600); err != nil {
		return err
	}
	if err := files.ReplaceIn(s.root, digestName, nil, 0o600); err != nil {
		return err
	}
	if err := files.CreateNoReplaceIn(s.root, markerName, []byte(markerBody), 0o600); err != nil {
		if errors.Is(err, files.ErrExists) {
			return ErrInitialised
		}
		return err
	}
	return nil
}

func (s *state) readDigest() error {
	body, err := s.readSmall(digestName, 2*sha256.Size+1)
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	d, ok := strings.CutSuffix(string(body), "\n")
	if !ok || !lowerHex(d, 2*sha256.Size) {
		return fmt.Errorf("%w: %s is not a digest this package writes", ErrState, digestName)
	}
	s.digest = d
	return nil
}

// checkLog refuses a findings log whose first line is not the one the state
// recorded, and records it when the state has none yet.
func (s *state) checkLog(first []byte) error {
	sum := sha256.Sum256(first)
	got := hex.EncodeToString(sum[:])
	switch {
	case s.digest == got:
		return nil
	case s.digest != "":
		return ErrOtherLog
	}
	if err := files.ReplaceIn(s.root, digestName, []byte(got+"\n"), 0o600); err != nil {
		return err
	}
	s.digest = got
	return nil
}

// openList reads the delivered list and cuts a tail a torn mark left.
func (s *state) openList() error {
	f, err := s.openIn(deliveredName, os.O_RDWR|os.O_APPEND)
	if err != nil {
		return err
	}
	s.list = f
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > MaxDeliveredBytes {
		return fmt.Errorf("%w: %d bytes, limit %d", ErrTooLarge, info.Size(), MaxDeliveredBytes)
	}
	data := make([]byte, info.Size())
	if _, err := io.ReadFull(io.NewSectionReader(f, 0, info.Size()), data); err != nil {
		return err
	}
	keys, end, err := parseDelivered(data)
	if err != nil {
		return err
	}
	if end < len(data) {
		if err := errors.Join(f.Truncate(int64(end)), f.Sync()); err != nil {
			return err
		}
	}
	s.size, s.keys = int64(end), make(map[string]bool, len(keys))
	for _, k := range keys {
		s.keys[k] = true
	}
	return nil
}

// room refuses a mark that would take the list past its bound, so it is
// asked before the program runs and a delivery is never left unmarkable.
func (s *state) room(key string) error {
	if n := s.size + int64(len(keyLine(key))); n > MaxDeliveredBytes {
		return fmt.Errorf("%w: a mark would take it to %d bytes, limit %d", ErrTooLarge, n, MaxDeliveredBytes)
	}
	return nil
}

// mark appends key to the list and forces it to disk. A failure leaves the
// key unmarked, or marked and not known to be on disk; either way the next
// run delivers that record again at most.
func (s *state) mark(key string) error {
	line := keyLine(key)
	n, err := s.ops.write(s.list, line)
	if err == nil && n != len(line) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = s.ops.sync(s.list)
	}
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrMark, key, err)
	}
	s.size += int64(n)
	s.keys[key] = true
	return nil
}

// openIn opens name in the state, refusing anything but a regular file of
// this account that the group and others cannot reach. A root follows a link
// that stays inside the directory whatever the open's flags say, so a link is
// refused by its directory entry, and the descriptor has to be the file that
// entry named.
func (s *state) openIn(name string, flag int) (*os.File, error) {
	before, err := s.root.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("%w: %s is missing: %w", ErrState, name, err)
	case err != nil:
		return nil, err
	case !before.Mode().IsRegular():
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrState, name)
	}
	f, err := s.root.OpenFile(name, flag|openFlags, 0)
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err == nil && !os.SameFile(before, after) {
		err = fmt.Errorf("%w: another file stood at %s when it was opened", ErrState, name)
	}
	if err == nil {
		err = judge(after)
	}
	if err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return f, nil
}

// readSmall reads name whole, refusing one longer than limit bytes.
func (s *state) readSmall(name string, limit int64) ([]byte, error) {
	f, err := s.openIn(name, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err = errors.Join(err, f.Close()); err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%w: %s is longer than this package writes it", ErrState, name)
	}
	return b, nil
}

// openDir opens dir and refuses it unless it is a directory of this account
// that the group and others cannot reach, with no link at its name. The open
// that refuses the link and the root are two opens of one path, so the root
// has to be the directory that was judged.
func openDir(dir string) (*os.Root, error) {
	// A trailing separator would make the open follow a link at the name.
	dir = filepath.Clean(dir)
	d, err := files.OpenDir(dir)
	if err != nil {
		return nil, fmt.Errorf("%w: the directory: %w", ErrState, err)
	}
	judged, err := d.Stat()
	if err = errors.Join(err, d.Close()); err != nil {
		return nil, err
	}
	if err := judge(judged); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err == nil && !os.SameFile(judged, opened) {
		err = fmt.Errorf("%w: another directory stood at the name when it was opened", ErrState)
	}
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	return root, nil
}

func judge(info fs.FileInfo) error {
	if perm := info.Mode().Perm(); perm&forbidden != 0 {
		return fmt.Errorf("%w: %w: mode %04o", ErrStateMode, files.ErrMode, perm)
	}
	if err := files.CheckOwnedBy(info, os.Geteuid()); err != nil {
		return fmt.Errorf("%w: %w", ErrOwner, err)
	}
	return nil
}
