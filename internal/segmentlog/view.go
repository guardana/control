package segmentlog

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
)

// View is a log as a reader finds it, holding no lock and writing nothing: the
// manifest, and the live segment's whole lines when it was read. A writer may
// seal or prune while a view is held; Open notices a segment that moved.
type View struct {
	dir          string
	maxLineBytes int
	tenant       string
	project      string
	segs         []Segment
	live         stats
}

// Inspect reads the log in dir as it is: a manifest is required, and a live
// segment is read to its last newline. Nothing is created, cut or removed.
func Inspect(dir string, maxLineBytes int) (*View, error) {
	if maxLineBytes < 1 {
		return nil, fmt.Errorf("%w: line bound %d", ErrConfig, maxLineBytes)
	}
	root, err := os.OpenRoot(dir + string(os.PathSeparator) + ".")
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	raw, ok, err := readManifest(root)
	switch {
	case err != nil:
		return nil, err
	case !ok:
		return nil, fmt.Errorf("%w: %s holds no %s, so it is no segment log", ErrManifest, dir, ManifestName)
	}
	v := &View{dir: dir, maxLineBytes: maxLineBytes}
	if v.tenant, v.project, v.segs, err = decodeManifest(raw); err != nil {
		return nil, err
	}
	f, err := openRead(root, LiveName)
	if errors.Is(err, fs.ErrNotExist) {
		return v, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if v.live, err = scan(f, maxLineBytes, nil); err != nil {
		return nil, fmt.Errorf("%s: %w", LiveName, err)
	}
	return v, nil
}

// openRead opens name in root to read it, refusing anything but a regular
// file, a link at the name included, without waiting on a named pipe.
func openRead(root *os.Root, name string) (*os.File, error) {
	before, err := root.Lstat(name)
	switch {
	case err != nil:
		return nil, err
	case !before.Mode().IsRegular():
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrDirectory, name)
	}
	f, err := root.OpenFile(name, os.O_RDONLY|openFlags, 0)
	if err != nil {
		return nil, err
	}
	if after, err := f.Stat(); err != nil || !os.SameFile(before, after) {
		return nil, errors.Join(fmt.Errorf("%w: %s", ErrMoved, name), err, f.Close())
	}
	return f, nil
}

func (v *View) liveSegment() Segment {
	s := v.live.segment(StateLive)
	s.Seq, s.File = len(v.segs)+1, LiveName
	return s
}

// Manifest is the log as the view found it.
func (v *View) Manifest() Manifest {
	m := Manifest{Tenant: v.tenant, Project: v.project, Segments: slices.Clone(v.segs), Live: v.liveSegment(),
		Retained: v.live.bytes}
	for _, s := range v.segs {
		if s.State == StateSealed {
			m.Retained += s.Bytes
		}
	}
	return m
}

// Tail is the bytes after the live segment's last newline when it was read.
func (v *View) Tail() int64 { return v.live.tail }

// Locate answers for the segment whose first line has the digest first.
func (v *View) Locate(first Digest) SegmentAnswer {
	return locate(v.segs, v.liveSegment(), first)
}

// After is the segment that follows seq, sealed or live; Unknown when there is
// none, and Pruned when it was pruned.
func (v *View) After(seq int) SegmentAnswer {
	switch {
	case seq < len(v.segs):
		s := v.segs[seq]
		return SegmentAnswer{state: s.State, seg: s}
	case seq == len(v.segs):
		return SegmentAnswer{state: StateLive, seg: v.liveSegment()}
	default:
		return SegmentAnswer{cause: fmt.Sprintf("no segment follows %d", seq)}
	}
}

// First is the first segment not pruned: the first sealed one, or the live
// one when every sealed one was pruned.
func (v *View) First() SegmentAnswer {
	for _, s := range v.segs {
		if s.State == StateSealed {
			return SegmentAnswer{state: StateSealed, seg: s}
		}
	}
	return SegmentAnswer{state: StateLive, seg: v.liveSegment()}
}

// Open opens the file of a sealed or the live segment s and returns it with
// the length a reader takes. A sealed segment is read whole first and refused
// unless it holds what the manifest names it with. A live segment that no
// longer begins with the line it began with when the view was read was sealed
// since, and is ErrMoved; so is a sealed segment pruned since.
func (v *View) Open(s Segment) (*os.File, int64, error) {
	if s.State != StateSealed && s.State != StateLive {
		return nil, 0, fmt.Errorf("%w: segment %d is %s", ErrPrune, s.Seq, s.State)
	}
	root, err := os.OpenRoot(v.dir + string(os.PathSeparator) + ".")
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = root.Close() }()
	f, err := openRead(root, s.File)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, fmt.Errorf("%w: %s: %w", ErrMoved, s.File, err)
	}
	if err != nil {
		return nil, 0, err
	}
	size, err := v.check(f, s)
	if err != nil {
		return nil, 0, errors.Join(err, f.Close())
	}
	return f, size, nil
}

// check reads what it must of the open file of s to know it is s.
func (v *View) check(f *os.File, s Segment) (int64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	st, err := scan(f, v.maxLineBytes, nil)
	switch {
	case err != nil:
		return 0, fmt.Errorf("%s: %w", s.File, err)
	case s.State == StateSealed && !st.matches(s):
		return 0, fmt.Errorf("%w: %s does not hold what the manifest names it with", ErrManifest, s.File)
	case s.State == StateLive && s.Lines > 0 && (st.lines == 0 || st.first != s.First):
		return 0, fmt.Errorf("%w: %s no longer begins with the line it began with", ErrMoved, s.File)
	}
	return info.Size(), nil
}

// Held reports whether a writer holds the log's directory.
func (v *View) Held() (bool, error) {
	root, err := os.OpenRoot(v.dir + string(os.PathSeparator) + ".")
	if err != nil {
		return false, err
	}
	defer func() { _ = root.Close() }()
	f, err := openRead(root, LockName)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	return heldByWriter(f)
}
