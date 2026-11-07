package runs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/pkg/contract"
)

// ErrTwoHistories is one run id two directories hold as two runs: another
// identity, lineage, lifetime or secret.
const ErrTwoHistories Error = "runs: one run id names two runs"

// lostState stands for a root's state a directory lost while it kept the
// root's record: everything taken in, nothing known of the reads.
var lostState = State{Untrusted: true, MaxRead: controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED}

// JoinStates is what a restore keeps of two states of one root: untrusted
// where either is, and the higher read, unknown where either is unknown.
func JoinStates(a, b State) State {
	read := max(a.MaxRead, b.MaxRead)
	if !contract.ValidSensitivityFloor(a.MaxRead) || !contract.ValidSensitivityFloor(b.MaxRead) {
		read = controlv1.Sensitivity_SENSITIVITY_UNSPECIFIED
	}
	return State{Untrusted: a.Untrusted || b.Untrusted, MaxRead: read}
}

// Restored is what a restore of a runs directory wrote.
type Restored struct {
	// Closed counts the records written closed, States the states written
	// and Carried the closes carried into a signed layout's state/.
	Closed, States, Carried int
}

// RestoreUnsigned rewrites live, an unsigned runs directory, from backup, a
// copy of one, and closes every run at now: each run either holds is written
// closed, a close already recorded kept, and each root's state is the join
// of the two, a state lost beside its record joined as lostState. It
// refuses one id held as two runs with ErrTwoHistories and a signed layout
// on either side with ErrLayout, and makes a missing live a runs directory.
// An empty backup is none: live's runs are closed all the same. It holds
// live's lock, and step is called before each write.
func RestoreUnsigned(ctx context.Context, live, backup string, now time.Time, step func(string) error) (_ Restored, err error) {
	if now.IsZero() {
		return Restored{}, ErrZeroTime
	}
	if step == nil {
		step = func(string) error { return nil }
	}
	var from flatRuns
	if backup != "" {
		if from, err = readFlat(backup); err != nil {
			return Restored{}, fmt.Errorf("the backup's runs: %w", err)
		}
	}
	if err := os.Mkdir(live, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return Restored{}, fmt.Errorf("%w: %w", ErrNotRunsDir, err)
	}
	a, err := InitAdmin(ctx, live)
	if err != nil {
		return Restored{}, err
	}
	defer func() { err = errors.Join(err, a.Close()) }()
	if a.Signed() {
		return Restored{}, fmt.Errorf("%w: %s is a signed layout", ErrLayout, clip(live))
	}
	end, err := a.begin(ctx)
	if err != nil {
		return Restored{}, err
	}
	defer func() { err = errors.Join(err, end()) }()
	cur, err := a.d.flat()
	if err != nil {
		return Restored{}, err
	}
	merged, err := mergeRuns(cur, from, now)
	if err != nil {
		return Restored{}, err
	}
	return a.writeMerged(cur, merged, step)
}

// flatRuns is what an unsigned directory holds: its records, and the state
// of each root whose state file is there.
type flatRuns struct {
	records map[string]Record
	states  map[string]State
}

func readFlat(path string) (flatRuns, error) {
	if signed, err := signedLayout(path); err != nil || signed {
		return flatRuns{}, errors.Join(err, fmt.Errorf("%w: a signed layout", ErrLayout))
	}
	d, err := openDir(path, roleFlat, ownedByThisUser)
	if err != nil {
		return flatRuns{}, err
	}
	defer func() { _ = d.close() }()
	if err := d.checkContents(); err != nil {
		return flatRuns{}, err
	}
	return d.flat()
}

func (d *dir) flat() (flatRuns, error) {
	entries, err := d.readDir()
	if err != nil {
		return flatRuns{}, err
	}
	ids, err := d.scan(entries)
	if err != nil {
		return flatRuns{}, err
	}
	out := flatRuns{records: map[string]Record{}, states: map[string]State{}}
	for _, id := range ids {
		rec, err := d.readRecord(id)
		if err != nil {
			return flatRuns{}, err
		}
		out.records[id] = rec
		if rec.Root != id {
			continue
		}
		switch s, err := d.readState(id); {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return flatRuns{}, err
		default:
			out.states[id] = s
		}
	}
	return out, nil
}

// mergeRuns is every run either side holds, closed at now unless a side
// closed it already, with each root's joined state.
func mergeRuns(cur, from flatRuns, now time.Time) (flatRuns, error) {
	out := flatRuns{records: map[string]Record{}, states: map[string]State{}}
	for _, side := range []flatRuns{from, cur} {
		for id, rec := range side.records {
			have, ok := out.records[id]
			if ok && !sameRun(have, rec) {
				return flatRuns{}, fmt.Errorf("%w: %s", ErrTwoHistories, id)
			}
			if !rec.Closed() && ok {
				rec.ClosedAt = have.ClosedAt
			}
			out.records[id] = rec
		}
	}
	for id, rec := range out.records {
		if !rec.Closed() {
			rec.ClosedAt = canonTime(now)
			out.records[id] = rec
		}
		if rec.Root == id {
			out.states[id] = joinedState(id, cur, from)
		}
	}
	return out, nil
}

// joinedState is the join of root's state on each side that holds its
// record, a state lost beside its record counting as lostState.
func joinedState(root string, cur, from flatRuns) State {
	var joined *State
	for _, side := range []flatRuns{cur, from} {
		if _, held := side.records[root]; !held {
			continue
		}
		s, ok := side.states[root]
		if !ok {
			s = lostState
		}
		if joined != nil {
			s = JoinStates(*joined, s)
		}
		joined = &s
	}
	return *joined
}

// sameRun reports whether a and b are one run, whether or not either is
// closed.
func sameRun(a, b Record) bool {
	a.ClosedAt, b.ClosedAt = time.Time{}, time.Time{}
	return a.ID == b.ID && a.Who == b.Who && a.Root == b.Root && a.Parent == b.Parent &&
		a.OpenedAt.Equal(b.OpenedAt) && a.ExpiresAt.Equal(b.ExpiresAt) && a.SecretSHA256 == b.SecretSHA256
}

// writeMerged writes each root's lock and state, then each record, wherever
// it differs from what cur holds, so a root never has a record without them.
func (a *Admin) writeMerged(cur, merged flatRuns, step func(string) error) (Restored, error) {
	var out Restored
	for _, root := range sortedKeys(merged.states) {
		s := merged.states[root]
		if have, ok := cur.states[root]; ok && have == s {
			continue
		}
		if err := step("state " + root); err != nil {
			return out, err
		}
		if err := a.d.writeState(root, s); err != nil {
			return out, err
		}
		out.States++
	}
	for _, id := range sortedKeys(merged.records) {
		rec := merged.records[id]
		if have, ok := cur.records[id]; ok && have.ClosedAt.Equal(rec.ClosedAt) {
			continue
		}
		if err := step("record " + id); err != nil {
			return out, err
		}
		body, err := a.encodeRecord(rec)
		if err != nil {
			return out, err
		}
		if err := a.d.replace(id+recordTemp, id+recordSuffix, body); err != nil {
			return out, err
		}
		out.Closed++
	}
	return out, nil
}

// writeState writes root's lock file where it is missing, then its state.
func (d *dir) writeState(root string, s State) error {
	if err := d.createEmpty(root + lockSuffix); err != nil {
		return err
	}
	body, err := encodeState(root, s)
	if err != nil {
		return err
	}
	return d.replace(root+stateTemp, root+stateSuffix, body)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// RestoreSigned carries the close of every run whose record live's or
// backup's records/ names into live's state/, and joins each state backup's
// state/ holds into live's; a missing state is a root's first. It reads the
// records' names and judges no record: a close carried is the safe side. It
// writes nothing to records/, and step is called before each write. An
// empty backup is none: live's runs are closed all the same.
func RestoreSigned(ctx context.Context, live, backup string, step func(string) error) (out Restored, err error) {
	if err := ctx.Err(); err != nil {
		return Restored{}, err
	}
	if step == nil {
		step = func(string) error { return nil }
	}
	ids, states, err := signedBackup(backup)
	if err != nil {
		return Restored{}, fmt.Errorf("the backup's runs: %w", err)
	}
	more, err := signedIDs(live)
	if err != nil {
		return Restored{}, err
	}
	ids = append(ids, more...)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if err := step("carry"); err != nil {
		return Restored{}, err
	}
	if err := CarryClosed(live, ids); err != nil {
		return Restored{}, err
	}
	out.Carried = len(ids)
	out.States, err = joinSigned(live, states, step)
	return out, err
}

// joinSigned joins each of states into the state/ of live, a missing one
// counting as a root's first, and returns how many it wrote.
func joinSigned(live string, states map[string]State, step func(string) error) (n int, err error) {
	st, err := openState(live)
	if err != nil {
		return 0, err
	}
	defer func() { err = errors.Join(err, st.close()) }()
	for _, root := range sortedKeys(states) {
		have, err := st.readState(root)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			have = initialState
		case err != nil:
			return n, err
		}
		next := JoinStates(have, states[root])
		if next == have {
			continue
		}
		if err := step("state " + root); err != nil {
			return n, err
		}
		if err := st.writeState(root, next); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// signedBackup is the run ids backup's records/ names and the states its
// state/ holds; an empty backup is none.
func signedBackup(backup string) ([]string, map[string]State, error) {
	if backup == "" {
		return nil, map[string]State{}, nil
	}
	ids, err := signedIDs(backup)
	if err != nil {
		return nil, nil, err
	}
	states, err := signedStates(backup)
	if err != nil {
		return nil, nil, err
	}
	return ids, states, nil
}

// CarryClosed writes into the state/ of dir, a signed layout, the close of
// each run of ids, so an open record of one of them is refused whatever
// records/ holds later. It refuses an id CheckRunID refuses before it
// writes any, leaves a close carried already as it is, and judges nothing
// else: closing is the safe side.
func CarryClosed(dir string, ids []string) (err error) {
	for _, id := range ids {
		if err := checkRunID(id); err != nil {
			return err
		}
	}
	st, err := openState(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, st.close()) }()
	for _, id := range ids {
		if err := st.createEmpty(id + closedSuffix); err != nil {
			return err
		}
	}
	return nil
}

func openState(dir string) (*dir, error) {
	if signed, err := signedLayout(dir); err != nil || !signed {
		return nil, errors.Join(err, fmt.Errorf("%w: %s is not a signed layout", ErrLayout, clip(dir)))
	}
	if err := judgeTop(dir, true); err != nil {
		return nil, err
	}
	st, err := openDir(filepath.Join(dir, StateDir), roleState, ownedByThisUser)
	if err != nil {
		return nil, fmt.Errorf("%s/: %w", StateDir, err)
	}
	if err := st.checkContents(); err != nil {
		return nil, errors.Join(err, st.close())
	}
	return st, nil
}

// signedIDs is every run id a record's name under dir's records/ holds.
func signedIDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, RecordsDir))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotRunsDir, err)
	}
	var ids []string
	for _, e := range entries {
		switch k, id := classify(e.Name(), roleRecords); {
		case !e.Type().IsRegular() || k == kindForeign:
			return nil, fmt.Errorf("%w: %q", ErrForeignFile, clip(e.Name()))
		case k == kindRecord:
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// signedStates is every state dir's state/ holds, by root.
func signedStates(dir string) (map[string]State, error) {
	st, err := openDir(filepath.Join(dir, StateDir), roleState, ownedByThisUser)
	if err != nil {
		return nil, err
	}
	defer func() { _ = st.close() }()
	entries, err := st.readDir()
	if err != nil {
		return nil, err
	}
	if _, err := st.scan(entries); err != nil {
		return nil, err
	}
	out := map[string]State{}
	for _, e := range entries {
		root, ok := strings.CutSuffix(e.Name(), stateSuffix)
		if !ok || checkRunID(root) != nil {
			continue
		}
		s, err := st.readState(root)
		if err != nil {
			return nil, err
		}
		out[root] = s
	}
	return out, nil
}
