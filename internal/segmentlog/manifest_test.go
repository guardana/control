package segmentlog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guardana/control/internal/brand"
)

// manifestText is the manifest of tenant t1 and project p1 naming one sealed
// segment of the lines aaa and bbb, spelled out apart from the encoder.
const manifestText = `{"format":"` + brand.OTelNamespace + `.segments","version":"0.1","tenant_id":"t1","project_id":"p1","dropped":0,"segments":[` +
	`{"seq":1,"state":"sealed","lines":2,"bytes":8,` +
	`"first_line_sha256":"17e682f060b5f8e47ea04c5c4855908b0a5ad612022260fe50e11ecb0cc0ab76",` +
	`"last_line_sha256":"3cf9a1a81f6bdeaf08a343c1e1c73e89cf44c06ac2427a892382cae825e7c9c1",` +
	`"sealed_at":"2027-01-15T08:00:00Z"}]}` + "\n"

// sealedLog is a closed log holding one sealed segment of aaa and bbb and a
// live one of ccc.
func sealedLog(t *testing.T) Config {
	t.Helper()
	cfg := config(logDir(t), 8, 64)
	l := openLog(t, cfg)
	mustAppend(t, l, "aaa", "bbb")
	mustAppend(t, l, "ccc")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestTheManifestHasOneSpelling: what the writer writes is the spelling
// given here, digests computed apart from the package, and that spelling
// decodes to what it names.
func TestTheManifestHasOneSpelling(t *testing.T) {
	cfg := config(logDir(t), 8, 64)
	l := openLog(t, cfg)
	mustAppend(t, l, "aaa", "bbb")
	mustAppend(t, l, "ccc")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if got := fileText(t, filepath.Join(cfg.Dir, ManifestName)); got != manifestText {
		t.Errorf("the manifest is\n%s\nwant\n%s", got, manifestText)
	}
	m, err := decodeManifest([]byte(manifestText))
	tenant, project, segs := m.tenant, m.project, m.segs
	if err != nil || tenant != "t1" || project != "p1" || len(segs) != 1 || segs[0].Bytes != 8 || segs[0].Lines != 2 ||
		segs[0].First != sum("aaa") || segs[0].Last != sum("bbb") || segs[0].SealedAt.Unix() != 1_800_000_000 {
		t.Errorf("decoding the fixture: %v %s %s %+v", err, tenant, project, segs)
	}
}

// TestAManifestNotSpelledAsWrittenIsRefused: each change of the fixture, any
// one of which a reader taking JSON loosely would read, is refused; the
// fixture beside them is read.
func TestAManifestNotSpelledAsWrittenIsRefused(t *testing.T) {
	cases := map[string]struct {
		old, new string
		want     error
	}{
		"a space":                    {`"tenant_id":"t1"`, `"tenant_id": "t1"`, ErrManifest},
		"members in another order":   {`"tenant_id":"t1","project_id":"p1"`, `"project_id":"p1","tenant_id":"t1"`, ErrManifest},
		"a member twice":             {`"tenant_id":"t1"`, `"tenant_id":"t2","tenant_id":"t1"`, ErrManifest},
		"an unknown member":          {`"tenant_id":"t1"`, `"tenant_id":"t1","x":1`, ErrManifest},
		"no newline":                 {"]}\n", "]}", ErrManifest},
		"a second document":          {"]}\n", "]}\n{}\n", ErrManifest},
		"another minor":              {`"version":"0.1"`, `"version":"0.2"`, ErrVersion},
		"another major":              {`"version":"0.1"`, `"version":"1.0"`, ErrVersion},
		"another format":             {brand.OTelNamespace + `.segments`, brand.OTelNamespace + `.segment`, ErrVersion},
		"an empty tenant":            {`"tenant_id":"t1"`, `"tenant_id":""`, ErrManifest},
		"a null segments":            {`"segments":[{`, `"segments":null,"x":[{`, ErrManifest},
		"a negative count dropped":   {`"dropped":0`, `"dropped":-1`, ErrManifest},
		"dropped and not renumbered": {`"dropped":0`, `"dropped":1`, ErrManifest},
		"no count dropped":           {`"dropped":0,`, ``, ErrManifest},
		"a second numbered 2":        {`"seq":1`, `"seq":2`, ErrManifest},
		"no lines":                   {`"lines":2,"bytes":8`, `"lines":0,"bytes":8`, ErrManifest},
		"fewer bytes than lines":     {`"lines":2,"bytes":8`, `"lines":9,"bytes":8`, ErrManifest},
		"one line with two digests":  {`"lines":2`, `"lines":1`, ErrManifest},
		"a digest in capitals":       {`17e682f0`, `17E682F0`, ErrManifest},
		"a short digest":             {`17e682f0`, `17e682f`, ErrManifest},
		"a state unknown":            {`"state":"sealed"`, `"state":"live"`, ErrManifest},
		"a time not in UTC":          {`2027-01-15T08:00:00Z`, `2027-01-15T09:00:00+01:00`, ErrManifest},
		"a time to the nanosecond":   {`2027-01-15T08:00:00Z`, `2027-01-15T08:00:00.5Z`, ErrManifest},
		"a sealed one dated pruned":  {`"sealed_at":"2027-01-15T08:00:00Z"}`, `"sealed_at":"2027-01-15T08:00:00Z","pruned_at":"2027-01-15T08:00:00Z"}`, ErrManifest},
		"pruned with no date":        {`"state":"sealed"`, `"state":"pruned"`, ErrManifest},
	}
	for name, c := range cases {
		raw := strings.Replace(manifestText, c.old, c.new, 1)
		if raw == manifestText {
			t.Fatalf("%s: the change is not in the fixture", name)
		}
		if _, err := decodeManifest([]byte(raw)); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	pruned := strings.Replace(manifestText, `"state":"sealed"`, `"state":"pruned"`, 1)
	pruned = strings.Replace(pruned, `"sealed_at":"2027-01-15T08:00:00Z"}`, `"sealed_at":"2027-01-15T08:00:00Z","pruned_at":"2027-01-15T08:00:01Z"}`, 1)
	if m, err := decodeManifest([]byte(pruned)); err != nil || m.segs[0].State != StatePruned || m.segs[0].File != "" {
		t.Errorf("a pruned segment: %v %+v", err, m.segs)
	}
	if _, err := decodeManifest([]byte(strings.Repeat(" ", MaxManifestBytes+1))); !errors.Is(err, ErrManifest) {
		t.Errorf("a manifest over the bound: %v", err)
	}
}

// TestAPruneAfterASealedSegmentIsRefused: a manifest naming a pruned segment
// after a sealed one names no state a prune leaves, as a prune takes a
// prefix; the same two segments the other way round are read.
func TestAPruneAfterASealedSegmentIsRefused(t *testing.T) {
	second := `{"seq":2,"state":"%s","lines":2,"bytes":8,` +
		`"first_line_sha256":"17e682f060b5f8e47ea04c5c4855908b0a5ad612022260fe50e11ecb0cc0ab76",` +
		`"last_line_sha256":"3cf9a1a81f6bdeaf08a343c1e1c73e89cf44c06ac2427a892382cae825e7c9c1",` +
		`"sealed_at":"2027-01-15T08:00:00Z"%s}]}`
	pruned := fmt.Sprintf(second, "pruned", `,"pruned_at":"2027-01-15T08:00:01Z"`)
	sealed := fmt.Sprintf(second, "sealed", "")
	after := strings.Replace(manifestText, "}]}", "},"+pruned, 1)
	if _, err := decodeManifest([]byte(after)); !errors.Is(err, ErrManifest) {
		t.Errorf("a pruned segment after a sealed one: %v, want ErrManifest", err)
	}
	both := strings.Replace(manifestText, "}]}", "},"+sealed, 1)
	if m, err := decodeManifest([]byte(both)); err != nil || len(m.segs) != 2 {
		t.Errorf("two sealed segments: %v %+v", err, m.segs)
	}
}

// TestAManifestThatDisagreesWithItsFilesIsRefused: a sealed segment changed
// in place, grown, cut, gone, an unlisted one, a foreign name, a pruned one
// back with other bytes, and a live segment with no manifest are each
// refused; the log they came from opens.
func TestAManifestThatDisagreesWithItsFilesIsRefused(t *testing.T) {
	seg := "segment-00000001.jsonl"
	cases := map[string]struct {
		change func(t *testing.T, dir string)
		want   error
	}{
		"a byte changed in place": {func(t *testing.T, dir string) { writeIn(t, dir, seg, "aaa\nbbc\n") }, ErrManifest},
		"a line added":            {func(t *testing.T, dir string) { writeIn(t, dir, seg, "aaa\nbbb\nzzz\n") }, ErrManifest},
		"a line cut":              {func(t *testing.T, dir string) { writeIn(t, dir, seg, "aaa\n") }, ErrManifest},
		"a partial line after":    {func(t *testing.T, dir string) { writeIn(t, dir, seg, "aaa\nbbb\nz") }, ErrManifest},
		"the file gone, the live one other": {func(t *testing.T, dir string) {
			removeIn(t, dir, seg)
		}, ErrManifest},
		"an unlisted segment":  {func(t *testing.T, dir string) { writeIn(t, dir, "segment-00000002.jsonl", "ccc\n") }, ErrManifest},
		"a foreign name":       {func(t *testing.T, dir string) { writeIn(t, dir, "notes.txt", "x") }, ErrDirectory},
		"a near segment name":  {func(t *testing.T, dir string) { writeIn(t, dir, "segment-1.jsonl", "x\n") }, ErrDirectory},
		"no manifest":          {func(t *testing.T, dir string) { removeIn(t, dir, ManifestName) }, ErrManifest},
		"a group-writable one": {func(t *testing.T, dir string) { chmodIn(t, dir, seg, 0o620) }, ErrDirectory},
		"a link at a segment": {func(t *testing.T, dir string) {
			removeIn(t, dir, seg)
			if err := os.Symlink(LiveName, filepath.Join(dir, seg)); err != nil {
				t.Fatal(err)
			}
		}, ErrDirectory},
	}
	for name, c := range cases {
		cfg := sealedLog(t)
		c.change(t, cfg.Dir)
		before := snapshot(t, cfg.Dir)
		if l, err := openSegments(cfg); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
			if l != nil {
				_ = l.Close()
			}
		}
		if after := snapshot(t, cfg.Dir); !mapsEqual(before, after) {
			t.Errorf("%s: a refused open changed the directory:\n%v\nwas\n%v", name, after, before)
		}
	}
	cfg := sealedLog(t)
	_ = openLog(t, cfg).Close()
}

// TestAPrunedSegmentBackWithOtherBytesIsRefused: a file at a pruned
// segment's name is removed at open only when it is the one the tombstone
// names.
func TestAPrunedSegmentBackWithOtherBytesIsRefused(t *testing.T) {
	cfg := sealedLog(t)
	l := openLog(t, cfg)
	mustAppend(t, l, "ddd", "eee")
	if err := l.Prune(1); err != nil {
		t.Fatal(err)
	}
	_ = l.Close()
	writeIn(t, cfg.Dir, "segment-00000001.jsonl", "aaa\nbbx\n")
	if _, err := openSegments(cfg); !errors.Is(err, ErrManifest) {
		t.Errorf("other bytes at a pruned name: %v, want ErrManifest", err)
	}
	writeIn(t, cfg.Dir, "segment-00000001.jsonl", "aaa\nbbb\n")
	l = openLog(t, cfg)
	_ = l.Close()
	if _, err := os.Stat(filepath.Join(cfg.Dir, "segment-00000001.jsonl")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the pruned segment's own bytes were not removed: %v", err)
	}
}

// TestADirectoryAnotherCouldWriteIsRefused: a directory the group or others
// may write, read or search is refused, and the same directory narrowed to
// 0700 opens.
func TestADirectoryAnotherCouldWriteIsRefused(t *testing.T) {
	cfg := sealedLog(t)
	for _, mode := range []os.FileMode{0o770, 0o750, 0o710, 0o705, 0o701} {
		if err := os.Chmod(cfg.Dir, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := openSegments(cfg); !errors.Is(err, ErrDirectory) {
			t.Errorf("a directory of mode %04o: %v, want ErrDirectory", mode, err)
		}
	}
	if err := os.Chmod(cfg.Dir, 0o700); err != nil { //nolint:gosec // G302: a directory, which needs its search bit
		t.Fatal(err)
	}
	_ = openLog(t, cfg).Close()
}

func writeIn(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func removeIn(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
}

func chmodIn(t *testing.T, dir, name string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(filepath.Join(dir, name), mode); err != nil {
		t.Fatal(err)
	}
}

// fullOfTombstones writes a manifest naming MaxSegments-1 pruned segments
// and one sealed, of the line aaa, whose file it writes too, beside an empty
// live segment, and returns the log's config.
func fullOfTombstones(t *testing.T) Config {
	t.Helper()
	cfg := config(logDir(t), 8, 64)
	_ = openLog(t, cfg).Close()
	at := time.Unix(1_800_000_000, 0).UTC()
	segs := make([]Segment, 0, MaxSegments)
	for seq := 1; seq < MaxSegments; seq++ {
		segs = append(segs, Segment{Seq: seq, State: StatePruned, Lines: 1, Bytes: 4, First: sum("zzz"), Last: sum("zzz"),
			SealedAt: at, PrunedAt: at})
	}
	segs = append(segs, Segment{Seq: MaxSegments, State: StateSealed, Lines: 1, Bytes: 4, First: sum("aaa"), Last: sum("aaa"),
		SealedAt: at, File: segmentFile(MaxSegments)})
	body, err := encodeManifest("t1", "p1", 0, segs)
	if err != nil {
		t.Fatal(err)
	}
	writeIn(t, cfg.Dir, ManifestName, string(body))
	writeIn(t, cfg.Dir, segmentFile(MaxSegments), "aaa\n")
	return cfg
}

// TestAPruneLiftsTheCapOnSegments: at MaxSegments entries a seal drops the
// oldest tombstone and the manifest stays within its bound; at MaxSegments
// sealed ones a seal is refused until a prune, after which it goes on. A
// cursor of a dropped segment is located as no segment, never as another.
func TestAPruneLiftsTheCapOnSegments(t *testing.T) {
	cfg := fullOfTombstones(t)
	l := openLog(t, cfg)
	mustAppend(t, l, "bbb", "ccc")
	mustAppend(t, l, "ddd")
	m := l.Manifest()
	if m.Dropped != 1 || len(m.Segments) != MaxSegments || m.Segments[0].Seq != 2 || m.Live.Seq != MaxSegments+2 {
		t.Fatalf("after a seal at the bound: dropped %d, %d entries from %d, live %d", m.Dropped, len(m.Segments),
			m.Segments[0].Seq, m.Live.Seq)
	}
	if got := l.Locate(key(1, "zzz")).State(); got != StateUnknown {
		t.Errorf("the dropped segment is located %v", got)
	}
	if got := l.Locate(key(MaxSegments+1, "bbb")).State(); got != StateSealed {
		t.Errorf("the segment sealed at the bound is located %v", got)
	}
	_ = l.Close()
	again := openLog(t, cfg)
	if m := again.Manifest(); m.Dropped != 1 || len(m.Segments) != MaxSegments {
		t.Errorf("reopened: dropped %d, %d entries", m.Dropped, len(m.Segments))
	}
	if err := again.Prune(5); err != nil {
		t.Errorf("a prune through a number some of whose tombstones were dropped: %v", err)
	}
	if err := again.Prune(1); err != nil {
		t.Errorf("a prune through a dropped segment: %v", err)
	}
	_ = again.Close()
	writeIn(t, cfg.Dir, segmentFile(1), "zzz\n")
	if _, err := openSegments(cfg); !errors.Is(err, ErrManifest) {
		t.Errorf("a file at a dropped segment's name: %v, want ErrManifest", err)
	}
}

// TestASealAtTheCapOfSealedSegmentsWaitsForAPrune: a log whose manifest
// holds as many sealed segments as it may takes no seal, and a prune of one
// lets the next seal drop its tombstone. The bound is lowered to three, so
// the case needs three segment files rather than 10,000.
func TestASealAtTheCapOfSealedSegmentsWaitsForAPrune(t *testing.T) {
	cfg := config(logDir(t), 8, 64)
	l := openLog(t, cfg)
	l.most = 3
	mustAppend(t, l, "aaa", "bbb")
	mustAppend(t, l, "ccc", "ddd")
	mustAppend(t, l, "eee", "fff")
	mustAppend(t, l, "ggg", "hhh")
	if err := l.Append(context.Background(), []byte("iii\n")); !errors.Is(err, ErrFull) {
		t.Fatalf("a seal at three sealed segments of three: %v, want ErrFull", err)
	}
	if m := l.Manifest(); len(m.Segments) != 3 || m.Live.Lines != 2 {
		t.Fatalf("a refused seal changed the log: %+v", m)
	}
	if err := l.Prune(1); err != nil {
		t.Fatal(err)
	}
	mustAppend(t, l, "iii")
	if m := l.Manifest(); m.Dropped != 1 || len(m.Segments) != 3 || m.Segments[0].Seq != 2 || m.Live.Seq != 5 {
		t.Errorf("after a prune and a seal: dropped %d, %d entries from %d, live %d", m.Dropped, len(m.Segments),
			m.Segments[0].Seq, m.Live.Seq)
	}
}
