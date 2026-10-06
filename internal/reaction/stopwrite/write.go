package stopwrite

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	"github.com/guardana/control/internal/files"
	"github.com/guardana/control/internal/reaction"
	"github.com/guardana/control/internal/reaction/stoplist"
)

// fileMode is the mode a new list is written with; a plane refuses a list the
// group or others may write.
const fileMode fs.FileMode = 0o600

// listIDPrefix begins every list id this package draws.
const listIDPrefix = "lst-"

// judged, when a test sets it, runs once the judge accepted an append and
// before a byte of it is written.
var judged func()

// Init starts a stop list in dir, which must exist and pass the plane's
// checks, holding only the header of a list id drawn here, judged against
// route at the writer's clock now. It refuses with ErrExists when the list's
// name is taken, by a file or by a link, and returns the header written.
func Init(ctx context.Context, dir string, route reaction.Route, now time.Time) (reaction.Header, error) {
	h, err := newHeader(route)
	if err != nil {
		return reaction.Header{}, err
	}
	line, err := h.Marshal()
	if err != nil {
		return reaction.Header{}, fmt.Errorf("%w: %w", ErrRefused, err)
	}
	if err := create(ctx, dir, route, now, [][]byte{line}); err != nil {
		return reaction.Header{}, err
	}
	return h, nil
}

// AppendStop appends s to the list in dir, judged against route at the
// writer's clock now, and returns its line number.
func AppendStop(ctx context.Context, dir string, route reaction.Route, s reaction.Stop, now time.Time) (int64, error) {
	line, err := s.Marshal()
	return appendLine(ctx, dir, route, now, line, err)
}

// AppendCovered appends c to the list in dir, judged against route at the
// writer's clock now, and returns its line number.
func AppendCovered(ctx context.Context, dir string, route reaction.Route, c reaction.Covered, now time.Time) (int64, error) {
	line, err := c.Marshal()
	return appendLine(ctx, dir, route, now, line, err)
}

// AppendLift appends l, signed elsewhere, to the list in dir, judged against
// route at the writer's clock now, and returns its line number. The writer
// holds no key: a lift the route's lift key did not sign is refused.
func AppendLift(ctx context.Context, dir string, route reaction.Route, l reaction.LiftLine, now time.Time) (int64, error) {
	line, err := l.Marshal()
	return appendLine(ctx, dir, route, now, line, err)
}

// appendLine appends line under the lock. The list is read with every check
// a plane's read makes, and judged with the line after its last complete
// line, with no tolerance past now; only a list the judge accepts is
// written. Bytes after the last newline are a line a writer did not finish,
// and are cut before the append. A refused write changes nothing.
func appendLine(ctx context.Context, dir string, route reaction.Route, now time.Time, line []byte, marshalled error) (int64, error) {
	if marshalled != nil {
		return 0, fmt.Errorf("%w: %w", ErrRefused, marshalled)
	}
	var n int64
	err := locked(ctx, dir, func(root *os.Root) error {
		named, err := stoplist.Named(root)
		if err != nil {
			return err
		}
		f, err := root.OpenFile(stoplist.FileName, os.O_RDWR|os.O_APPEND|stoplist.OpenFlags, 0)
		if err != nil {
			return err
		}
		n, err = appendTo(f, named, route, now, line)
		return errors.Join(err, f.Close())
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

func appendTo(f *os.File, named fs.FileInfo, route reaction.Route, now time.Time, line []byte) (int64, error) {
	if err := stoplist.CheckOpened(named, f); err != nil {
		return 0, err
	}
	content, err := io.ReadAll(io.LimitReader(f, reaction.MaxListBytes+1))
	if err != nil {
		return 0, err
	}
	complete := content[:bytes.LastIndexByte(content, '\n')+1]
	next := append(append(bytes.Clone(complete), line...), '\n')
	list, err := reaction.Judge(route, reaction.Prefix{}, next, now, 0)
	if err != nil {
		return 0, refusal(err)
	}
	if judged != nil {
		judged()
	}
	if len(complete) < len(content) {
		if err := f.Truncate(int64(len(complete))); err != nil {
			return 0, err
		}
	}
	if _, err := f.Write(next[len(complete):]); err != nil {
		return 0, err
	}
	if err := f.Sync(); err != nil {
		return 0, err
	}
	return list.Usage().Lines, nil
}

// create writes lines as a new list in dir, under the lock, once the judge
// accepts them; the name is never replaced.
func create(ctx context.Context, dir string, route reaction.Route, now time.Time, lines [][]byte) error {
	body := append(bytes.Join(lines, []byte{'\n'}), '\n')
	if _, err := reaction.Judge(route, reaction.Prefix{}, body, now, 0); err != nil {
		return refusal(err)
	}
	return locked(ctx, dir, func(root *os.Root) error {
		if err := files.CreateNoReplaceIn(root, stoplist.FileName, body, fileMode); err != nil {
			if errors.Is(err, files.ErrExists) {
				return fmt.Errorf("%w: %s", ErrExists, dir)
			}
			return err
		}
		return nil
	})
}

// refusal wraps a judge's refusal of the list a write would make.
func refusal(err error) error {
	if errors.Is(err, reaction.ErrListTooLarge) || errors.Is(err, reaction.ErrListLines) {
		return fmt.Errorf("%w: %w", ErrFull, err)
	}
	return fmt.Errorf("%w: %w", ErrRefused, err)
}

// locked runs write through dir, as the plane's reader judges and opens it,
// under the exclusive lock on that directory.
func locked(ctx context.Context, dir string, write func(root *os.Root) error) error {
	root, err := stoplist.OpenDir(dir)
	if err != nil {
		return err
	}
	release, err := lockDir(ctx, root)
	if err != nil {
		return errors.Join(err, root.Close())
	}
	return errors.Join(write(root), release(), root.Close())
}

// newHeader is the header of a list judged against route under a list id
// of 128 random bits.
func newHeader(route reaction.Route) (reaction.Header, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return reaction.Header{}, err
	}
	return reaction.HeaderFor(route, listIDPrefix+hex.EncodeToString(id[:])), nil
}
