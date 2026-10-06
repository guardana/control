package reaction

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"slices"
	"time"

	"github.com/guardana/control/internal/policy"
)

const (
	// MaxListBytes bounds a stop list, a torn last line included.
	MaxListBytes = 4 << 20
	// MaxListLines bounds the lines of a stop list, its header included.
	MaxListLines = 20000
)

// The refusals of a stop list as a whole, or of a line in its place.
const (
	ErrListTooLarge  Error = "reaction: the stop list is over its bound of bytes"
	ErrListLines     Error = "reaction: the stop list is over its bound of lines"
	ErrListRoute     Error = "reaction: the stop list's header names another route than the plane's"
	ErrListHeader    Error = "reaction: the stop list's header is not the one accepted before"
	ErrListShrunk    Error = "reaction: the stop list is shorter than the prefix accepted before"
	ErrListRewritten Error = "reaction: the stop list does not begin with the prefix accepted before"
	ErrHeaderPlace   Error = "reaction: the header is not the stop list's first line, or not its only header"
	ErrFindingAgain  Error = "reaction: a finding id is named by two lines"
	ErrCoveredRun    Error = "reaction: a covered line names a run and tenant no earlier stop names"
	ErrStopRefused   Error = "reaction: a stop is one the route does not permit"
	ErrDatedAhead    Error = "reaction: a line's created_at is later than the plane's clock and its tolerance"
	ErrLiftOrder     Error = "reaction: a lift names a line at or after itself"
	ErrLiftAgain     Error = "reaction: a lift names a run and line an earlier lift named"
	ErrLiftUnsigned  Error = "reaction: a lift's envelope does not verify under the route's lift key"
	ErrLiftMismatch  Error = "reaction: a lift's signed run, line, list or route is not what its line and header name"
	ErrJudgeClock    Error = "reaction: the plane's clock or tolerance is not usable"
)

// Prefix is the part of a stop list a judge accepted: its length, its
// SHA-256 and the SHA-256 of its header line. The zero value accepts
// nothing. Only Judge makes another, and a later judge takes only a list
// that begins with it, so what a plane accepted never shrinks.
type Prefix struct {
	length int64
	sum    [sha256.Size]byte
	header [sha256.Size]byte
}

// Length is the prefix's length in bytes.
func (p Prefix) Length() int64 { return p.length }

// Sum is the prefix's SHA-256.
func (p Prefix) Sum() [sha256.Size]byte { return p.sum }

// Usage is how much of its bounds a stop list uses.
type Usage struct {
	Bytes, Lines int64
}

// Degraded reports whether the list is past nine tenths of either bound.
func (u Usage) Degraded() bool {
	return u.Bytes*10 > MaxListBytes*9 || u.Lines*10 > MaxListLines*9
}

// Entry is a stop that no lift ended, and the line that holds it.
type Entry struct {
	Line int64
	Stop
}

// List is a stop list a judge accepted whole.
type List struct {
	header   Header
	prefix   Prefix
	usage    Usage
	entries  []Entry
	findings map[string]bool
}

// Header is the list's header.
func (l List) Header() Header { return l.header }

// Prefix is what the judge accepted, to hand the next judge.
func (l List) Prefix() Prefix { return l.prefix }

// Usage is how much of its bounds the accepted list uses.
func (l List) Usage() Usage { return l.usage }

// Entries is a copy of the stops no lift ended, in the order of their lines,
// expired ones among them.
func (l List) Entries() []Entry { return slices.Clone(l.entries) }

// Names reports whether a stop or covered line names findingID.
func (l List) Names(findingID string) bool { return l.findings[findingID] }

type runKey struct{ run, tenant string }

type liftKey struct {
	run  string
	line int64
}

// judging is one judge's walk over a list's lines.
type judging struct {
	route     Route
	now       time.Time
	tolerance time.Duration
	header    Header
	stops     []Entry
	stopped   map[runKey]bool
	findings  map[string]bool
	lifts     map[liftKey]bool
	through   map[string]int64
}

// Judge judges content, a stop list as read at the plane's clock now, against
// route, given the prefix accepted before (the zero Prefix at first) and a
// tolerance of one poll interval. Bytes after the last newline are a line
// being written and are left out. It refuses a list over MaxListBytes or
// MaxListLines; one whose header line is not the accepted one's, that is
// shorter than the accepted prefix or does not begin with it; a header that
// is not line 1 alone or names another route; a line ParseLine refuses; a
// finding id two lines name; a stop the route does not permit; a covered line
// naming a run and tenant no earlier stop names; a created_at later than now
// plus tolerance; and a lift naming a line at or after itself, naming a run
// and line again, that does not verify under the route's lift key, or whose
// signed run, line, list id and route digest are not its line's and the
// header's. A refusal returns no List: a list is accepted whole or not.
func Judge(route Route, accepted Prefix, content []byte, now time.Time, tolerance time.Duration) (List, error) {
	if route.digest == "" {
		return List{}, fmt.Errorf("%w: no route was read", ErrListRoute)
	}
	if !policy.UsableTime(now) || tolerance < 0 {
		return List{}, ErrJudgeClock
	}
	if n := len(content); n > MaxListBytes {
		return List{}, fmt.Errorf("%w: %d bytes, limit %d", ErrListTooLarge, n, MaxListBytes)
	}
	complete := content[:bytes.LastIndexByte(content, '\n')+1]
	lines := bytes.Count(complete, []byte{'\n'})
	if lines > MaxListLines {
		return List{}, fmt.Errorf("%w: %d lines, limit %d", ErrListLines, lines, MaxListLines)
	}
	if err := extends(accepted, complete); err != nil {
		return List{}, err
	}
	j := judging{
		route: route, now: now, tolerance: tolerance,
		stopped: map[runKey]bool{}, findings: map[string]bool{}, lifts: map[liftKey]bool{}, through: map[string]int64{},
	}
	if lines == 0 {
		return List{}, fmt.Errorf("%w: the list holds no line", ErrHeaderPlace)
	}
	rest := complete
	for n := int64(1); len(rest) > 0; n++ {
		end := bytes.IndexByte(rest, '\n')
		if err := j.line(n, rest[:end]); err != nil {
			return List{}, fmt.Errorf("line %d: %w", n, err)
		}
		rest = rest[end+1:]
	}
	return j.list(complete, int64(lines)), nil
}

// extends refuses complete unless it begins with the accepted prefix, naming
// a changed header before a shorter or rewritten list.
func extends(accepted Prefix, complete []byte) error {
	if accepted.length == 0 {
		return nil
	}
	first := bytes.IndexByte(complete, '\n')
	if first < 0 {
		return ErrListShrunk
	}
	if sha256.Sum256(complete[:first]) != accepted.header {
		return ErrListHeader
	}
	if int64(len(complete)) < accepted.length {
		return ErrListShrunk
	}
	if sha256.Sum256(complete[:accepted.length]) != accepted.sum {
		return ErrListRewritten
	}
	return nil
}

func (j *judging) line(n int64, raw []byte) error {
	l, err := ParseLine(raw)
	if err != nil {
		return err
	}
	if (n == 1) != (l.Kind == KindHeader) {
		return ErrHeaderPlace
	}
	switch l.Kind {
	case KindHeader:
		return j.headerLine(l.Header)
	case KindStop:
		return j.stopLine(n, l.Stop)
	case KindCovered:
		return j.coveredLine(l.Covered)
	default:
		return j.liftLine(n, l.Lift)
	}
}

func (j *judging) headerLine(h Header) error {
	if h.RouteID != j.route.id || h.RouteSerial != j.route.serial || h.RouteDigest != j.route.digest {
		return ErrListRoute
	}
	j.header = h
	return nil
}

func (j *judging) stopLine(n int64, s Stop) error {
	if err := j.named(s.FindingID, s.CreatedAt); err != nil {
		return err
	}
	if err := j.route.Permits(s.Claim()); err != nil {
		return fmt.Errorf("%w: %w", ErrStopRefused, err)
	}
	j.stopped[runKey{s.RunID, s.TenantID}] = true
	j.stops = append(j.stops, Entry{Line: n, Stop: s})
	return nil
}

func (j *judging) coveredLine(c Covered) error {
	if err := j.named(c.FindingID, c.CreatedAt); err != nil {
		return err
	}
	if !j.stopped[runKey{c.RunID, c.TenantID}] {
		return ErrCoveredRun
	}
	return nil
}

// named refuses a finding id named before and a created_at past the
// tolerance, and records the finding id.
func (j *judging) named(findingID string, created time.Time) error {
	if j.findings[findingID] {
		return ErrFindingAgain
	}
	if created.After(j.now.Add(j.tolerance)) {
		return ErrDatedAhead
	}
	j.findings[findingID] = true
	return nil
}

func (j *judging) liftLine(n int64, l LiftLine) error {
	if l.ThroughLine >= n {
		return ErrLiftOrder
	}
	key := liftKey{l.RunID, l.ThroughLine}
	if j.lifts[key] {
		return ErrLiftAgain
	}
	if _, err := VerifyLiftLine(l, j.header, j.route); err != nil {
		return err
	}
	j.lifts[key] = true
	j.through[l.RunID] = max(j.through[l.RunID], l.ThroughLine)
	return nil
}

// VerifyLiftLine verifies l's envelope under r's lift key and ties the lift
// it signs to its place: the signed run and line are l's, the signed list id
// is h's, and the signed route digest is h's and r's. A lift is never read
// apart from the list and route it names.
func VerifyLiftLine(l LiftLine, h Header, r Route) (Lift, error) {
	if r.digest == "" {
		return Lift{}, fmt.Errorf("%w: no route was read", ErrListRoute)
	}
	signed, err := VerifyLift(l.Envelope, r.liftKey)
	if err != nil {
		return Lift{}, fmt.Errorf("%w: %w", ErrLiftUnsigned, err)
	}
	if signed.RunID != l.RunID || signed.ThroughLine != l.ThroughLine ||
		signed.ListID != h.ListID || signed.RouteDigest != h.RouteDigest || h.RouteDigest != r.digest {
		return Lift{}, ErrLiftMismatch
	}
	return signed, nil
}

func (j *judging) list(complete []byte, lines int64) List {
	l := List{
		header:   j.header,
		usage:    Usage{Bytes: int64(len(complete)), Lines: lines},
		findings: j.findings,
		prefix:   Prefix{length: int64(len(complete)), sum: sha256.Sum256(complete)},
	}
	l.prefix.header = sha256.Sum256(complete[:bytes.IndexByte(complete, '\n')])
	for _, e := range j.stops {
		if e.Line > j.through[e.RunID] {
			l.entries = append(l.entries, e)
		}
	}
	return l
}
