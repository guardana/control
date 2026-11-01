package segmentlog

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/pkg/contract"
)

// The names a log's directory holds, besides the sealed segments and the
// temporary files a manifest is written through.
const (
	ManifestName = "segments.json"
	LockName     = "segments.lock"
	LiveName     = "live.jsonl"
)

const (
	// ManifestFormat names the manifest's format.
	ManifestFormat = brand.OTelNamespace + ".segments"
	// ManifestVersion is the one version of the manifest written and read:
	// a 0.x version is read only when it is this one.
	ManifestVersion = "0.1"
	// MaxSegments bounds the segments, sealed and pruned, a manifest names.
	// A seal at the bound drops the oldest tombstone, so only sealed
	// segments fill it.
	MaxSegments = 10_000
	// MaxSeq bounds a segment's number, past every log's life.
	MaxSeq = 1 << 40
	// MaxManifestBytes bounds the manifest a read takes.
	MaxManifestBytes = 4 << 20
)

const (
	segmentPrefix = "segment-"
	segmentSuffix = ".jsonl"
	segmentDigits = 8
)

// segmentFile is the file name of the sealed segment seq.
func segmentFile(seq int) string {
	return fmt.Sprintf("%s%0*d%s", segmentPrefix, segmentDigits, seq, segmentSuffix)
}

// segmentSeq reads the seq a sealed segment's file name spells, in its one
// spelling.
func segmentSeq(name string) (int, bool) {
	digits, ok := strings.CutPrefix(name, segmentPrefix)
	if !ok {
		return 0, false
	}
	if digits, ok = strings.CutSuffix(digits, segmentSuffix); !ok || len(digits) < segmentDigits {
		return 0, false
	}
	seq, err := strconv.Atoi(digits)
	if err != nil || seq < 1 || segmentFile(seq) != name {
		return 0, false
	}
	return seq, true
}

type manifestDoc struct {
	Format    string       `json:"format"`
	Version   string       `json:"version"`
	TenantID  string       `json:"tenant_id"`
	ProjectID string       `json:"project_id"`
	Dropped   int          `json:"dropped"`
	Segments  []segmentDoc `json:"segments"`
}

type segmentDoc struct {
	Seq      int    `json:"seq"`
	State    string `json:"state"`
	Lines    int64  `json:"lines"`
	Bytes    int64  `json:"bytes"`
	First    string `json:"first_line_sha256"`
	Last     string `json:"last_line_sha256"`
	SealedAt string `json:"sealed_at"`
	PrunedAt string `json:"pruned_at,omitempty"`
}

// stamp is a time as the manifest spells it: UTC, to the second.
func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// encodeManifest is the manifest's one spelling.
func encodeManifest(tenant, project string, dropped int, segs []Segment) ([]byte, error) {
	doc := manifestDoc{Format: ManifestFormat, Version: ManifestVersion, TenantID: tenant, ProjectID: project,
		Dropped: dropped, Segments: make([]segmentDoc, 0, len(segs))}
	for _, s := range segs {
		d := segmentDoc{Seq: s.Seq, State: s.State.String(), Lines: s.Lines, Bytes: s.Bytes,
			First: hex.EncodeToString(s.First[:]), Last: hex.EncodeToString(s.Last[:]), SealedAt: stamp(s.SealedAt)}
		if s.State == StatePruned {
			d.PrunedAt = stamp(s.PrunedAt)
		}
		doc.Segments = append(doc.Segments, d)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("encoding the manifest: %w", err)
	}
	return append(raw, '\n'), nil
}

// decodeManifest reads a manifest. Its format and version are read first, so
// a manifest of another version is refused as one. Anything but the one
// spelling encodeManifest writes is refused, so no member, order or space a
// writer of this package never leaves is read.
func decodeManifest(raw []byte) (decoded, error) {
	if len(raw) > MaxManifestBytes {
		return decoded{}, fmt.Errorf("%w: over %d bytes", ErrManifest, MaxManifestBytes)
	}
	var head struct{ Format, Version string }
	if err := json.Unmarshal(raw, &head); err != nil {
		return decoded{}, fmt.Errorf("%w: it does not parse: %w", ErrManifest, err)
	}
	if head.Format != ManifestFormat || head.Version != ManifestVersion {
		return decoded{}, fmt.Errorf("%w: format %q version %q, this build reads %s %s",
			ErrVersion, head.Format, head.Version, ManifestFormat, ManifestVersion)
	}
	var doc manifestDoc
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return decoded{}, fmt.Errorf("%w: %w", ErrManifest, err)
	}
	if err := checkBinding(doc.TenantID, doc.ProjectID); err != nil {
		return decoded{}, fmt.Errorf("%w: %w", ErrManifest, err)
	}
	if doc.Dropped < 0 || doc.Dropped > MaxSeq {
		return decoded{}, fmt.Errorf("%w: %d segments dropped", ErrManifest, doc.Dropped)
	}
	segs, err := readSegments(doc.Dropped, doc.Segments)
	if err != nil {
		return decoded{}, fmt.Errorf("%w: %w", ErrManifest, err)
	}
	again, err := encodeManifest(doc.TenantID, doc.ProjectID, doc.Dropped, segs)
	if err != nil {
		return decoded{}, err
	}
	if !bytes.Equal(again, raw) {
		return decoded{}, fmt.Errorf("%w: it is not spelled as a writer of this format spells it", ErrManifest)
	}
	return decoded{tenant: doc.TenantID, project: doc.ProjectID, dropped: doc.Dropped, segs: segs}, nil
}

// decoded is a manifest as read.
type decoded struct {
	tenant, project string
	dropped         int
	segs            []Segment
}

// checkBinding refuses a tenant or project no identifier may be.
func checkBinding(tenant, project string) error {
	for _, id := range []struct{ name, value string }{{"tenant", tenant}, {"project", project}} {
		if id.value == "" {
			return fmt.Errorf("an empty %s", id.name)
		}
		if err := contract.CheckIdentifier(id.value); err != nil {
			return fmt.Errorf("the %s: %w", id.name, err)
		}
	}
	return nil
}

// readSegments checks each segment of a manifest: numbered in order from
// the one after those dropped, every pruned one before every sealed one, as a
// prune takes a prefix.
func readSegments(dropped int, docs []segmentDoc) ([]Segment, error) {
	if docs == nil {
		return nil, fmt.Errorf("no segments member")
	}
	if len(docs) > MaxSegments {
		return nil, fmt.Errorf("%d segments, at most %d", len(docs), MaxSegments)
	}
	out := make([]Segment, 0, len(docs))
	for i, d := range docs {
		s, err := readSegment(d)
		switch {
		case err != nil:
			return nil, fmt.Errorf("segment %d: %w", i+1, err)
		case s.Seq != dropped+i+1:
			return nil, fmt.Errorf("segment %d is numbered %d", dropped+i+1, s.Seq)
		case s.State == StatePruned && i > 0 && out[i-1].State == StateSealed:
			return nil, fmt.Errorf("segment %d is pruned after a sealed one", s.Seq)
		}
		out = append(out, s)
	}
	return out, nil
}

func readSegment(d segmentDoc) (Segment, error) {
	s := Segment{Seq: d.Seq, Lines: d.Lines, Bytes: d.Bytes}
	switch d.State {
	case "sealed":
		s.State, s.File = StateSealed, segmentFile(d.Seq)
	case "pruned":
		s.State = StatePruned
	default:
		return Segment{}, fmt.Errorf("state %q", d.State)
	}
	var ok bool
	if s.First, ok = readDigest(d.First); !ok {
		return Segment{}, fmt.Errorf("the first line's digest is not 64 lowercase hex digits")
	}
	if s.Last, ok = readDigest(d.Last); !ok {
		return Segment{}, fmt.Errorf("the last line's digest is not 64 lowercase hex digits")
	}
	if err := s.checkCounts(); err != nil {
		return Segment{}, err
	}
	return s, s.readDates(d)
}

// checkCounts refuses a number, or a count of lines and bytes, no seal
// writes.
func (s Segment) checkCounts() error {
	switch {
	case s.Seq < 1 || s.Seq > MaxSeq:
		return fmt.Errorf("seq %d", s.Seq)
	case s.Lines < 1 || s.Bytes < s.Lines:
		return fmt.Errorf("%d lines in %d bytes", s.Lines, s.Bytes)
	case s.Lines == 1 && s.First != s.Last:
		return fmt.Errorf("one line with two digests")
	}
	return nil
}

// readDates reads when s was sealed and, for a pruned segment alone, pruned,
// no earlier than its seal.
func (s *Segment) readDates(d segmentDoc) error {
	var err error
	if s.SealedAt, err = readStamp(d.SealedAt); err != nil {
		return fmt.Errorf("sealed_at: %w", err)
	}
	if s.State == StateSealed {
		if d.PrunedAt != "" {
			return fmt.Errorf("a sealed segment dated pruned")
		}
		return nil
	}
	if s.PrunedAt, err = readStamp(d.PrunedAt); err != nil {
		return fmt.Errorf("pruned_at: %w", err)
	}
	if s.PrunedAt.Before(s.SealedAt) {
		return fmt.Errorf("pruned before it was sealed")
	}
	return nil
}

func readDigest(s string) (Digest, bool) {
	var d Digest
	if len(s) != 2*len(d) || strings.ContainsFunc(s, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') }) {
		return d, false
	}
	_, err := hex.Decode(d[:], []byte(s))
	return d, err == nil
}

func readStamp(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	if stamp(t) != s {
		return time.Time{}, fmt.Errorf("%q is not UTC to the second", s)
	}
	return t.UTC(), nil
}
