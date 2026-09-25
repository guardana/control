package policy_test

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"pgregory.net/rapid"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policy/bundle"
)

// TestInstallIsSerialized installs serials 1 to n of one bundle id from n
// goroutines at once, in an order drawn per round. Beside them, refreshers
// keep installing whichever bundle is current, and a watcher reads Current.
//
// Serialized, an install either takes a serial above every serial taken
// before it or refuses it, and a refresh confirms a bundle that is still
// current. So the watcher never sees the serial fall, nor sees it below a
// serial whose install has returned, and serial n is the one left standing.
// Unserialized, an install or a refresh can read the current snapshot, lose
// the processor, and store what it read after a higher serial landed. The
// refreshers are there because a refresh stores a copy of what it read, which
// gives that race a window wide enough to be hit on every run.
func TestInstallIsSerialized(t *testing.T) {
	const n, refreshers, rounds = 32, 4, 50
	bundles := make([]*controlv1.PolicyBundle, n)
	indices := make([]int, n)
	for i := range bundles {
		bundles[i] = signedDocument("payments", "v"+strconv.Itoa(i+1), int64(i+1))
		indices[i] = i
	}
	keys := pinned()
	for round := range rounds {
		order := rapid.Permutation(indices).Example(round)
		if problem := installAtOnce(bundles, order, refreshers, keys); problem != "" {
			t.Fatalf("round %d, order %v: %s", round, order, problem)
		}
	}
}

// installAtOnce runs one round and says what went wrong, or "".
func installAtOnce(bundles []*controlv1.PolicyBundle, order []int, refreshers int, keys bundle.Keyring) string {
	var h policy.Holder
	var returned atomic.Int64 // the highest serial whose install has returned nil
	start, stop := make(chan struct{}), make(chan struct{})
	reports := make(chan string, refreshers+1)
	go func() { reports <- watch(&h, &returned, stop) }()
	for range refreshers {
		go func() { reports <- refresh(&h, bundles, keys, start, stop) }()
	}
	errs := make([]error, len(order))
	var wg sync.WaitGroup
	for i, index := range order {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if errs[i] = h.Install(bundles[index], keys, loadTime()); errs[i] == nil {
				raise(&returned, int64(index+1))
			}
		}()
	}
	close(start)
	wg.Wait()
	close(stop)
	for range refreshers + 1 {
		if problem := <-reports; problem != "" {
			return problem
		}
	}
	for i, err := range errs {
		if err != nil && !errors.Is(err, policy.ErrRollback) {
			return fmt.Sprintf("install of serial %d: %v", order[i]+1, err)
		}
	}
	if got := h.Current().Serial(); got != int64(len(order)) {
		return fmt.Sprintf("the last serial standing is %d, want %d", got, len(order))
	}
	return ""
}

// raise sets m to v if v is higher.
func raise(m *atomic.Int64, v int64) {
	for {
		old := m.Load()
		if v <= old || m.CompareAndSwap(old, v) {
			return
		}
	}
}

// watch reads Current until stop closes, and says what it saw go wrong, or "".
// It reads the floor before the snapshot: an install that returned before the
// floor was read stored its serial before that, so Current cannot be below it.
func watch(h *policy.Holder, returned *atomic.Int64, stop <-chan struct{}) string {
	var last int64
	for {
		select {
		case <-stop:
			return ""
		default:
		}
		floor := returned.Load()
		s := h.Current()
		if s == nil {
			continue
		}
		switch {
		case s.Serial() < last:
			return fmt.Sprintf("Current().Serial() fell from %d to %d", last, s.Serial())
		case s.Serial() < floor:
			return fmt.Sprintf("Current().Serial() is %d after the install of serial %d returned", s.Serial(), floor)
		}
		last = s.Serial()
	}
}

// refresh keeps installing the bundle that is current until stop closes. When
// a higher serial lands between its read and its install, the install is
// refused as a rollback, which is not a problem.
func refresh(h *policy.Holder, bundles []*controlv1.PolicyBundle, keys bundle.Keyring, start, stop <-chan struct{}) string {
	<-start
	for {
		select {
		case <-stop:
			return ""
		default:
		}
		s := h.Current()
		if s == nil {
			continue
		}
		if err := h.Install(bundles[s.Serial()-1], keys, loadTime()); err != nil && !errors.Is(err, policy.ErrRollback) {
			return fmt.Sprintf("refresh of serial %d: %v", s.Serial(), err)
		}
	}
}

// TestConcurrentInstallAndCurrent has installers take, and take again, eight
// serials of one bundle id while readers use every snapshot they see. Run
// with -race: a snapshot changed after it was handed out is a race here.
func TestConcurrentInstallAndCurrent(t *testing.T) {
	const serials, installers, readers = 8, 4, 4
	bundles := make([]*controlv1.PolicyBundle, serials)
	digests := map[int64]string{}
	for i := range bundles {
		bundles[i] = signedDocument("payments", "v"+strconv.Itoa(i+1), int64(i+1))
		digests[int64(i+1)] = digestOf(bundles[i].GetCanonical())
	}
	var h policy.Holder
	keys := pinned()
	stop := make(chan struct{})
	problems := make(chan string, installers+readers)
	var installing, reading sync.WaitGroup
	for range readers {
		reading.Add(1)
		go func() { defer reading.Done(); problems <- readUntil(&h, stop, digests) }()
	}
	for g := range installers {
		installing.Add(1)
		go func() { defer installing.Done(); problems <- installAll(&h, bundles, keys, g) }()
	}
	installing.Wait()
	close(stop)
	reading.Wait()
	close(problems)
	for problem := range problems {
		if problem != "" {
			t.Error(problem)
		}
	}
	if got := h.Current().Serial(); got != serials {
		t.Errorf("the last serial standing is %d, want %d", got, serials)
	}
}

// installAll installs each bundle twice, the second time a refresh unless
// another installer got further meanwhile.
func installAll(h *policy.Holder, bundles []*controlv1.PolicyBundle, keys bundle.Keyring, installer int) string {
	for i, b := range bundles {
		for again := range 2 {
			err := h.Install(b, keys, at(installer*100+i*10+again))
			if err != nil && !errors.Is(err, policy.ErrRollback) {
				return fmt.Sprintf("install of serial %d: %v", i+1, err)
			}
		}
	}
	return ""
}

// readUntil checks every snapshot it sees until stop closes: its reference
// names the digest of its own serial, its program allows the READ its
// document allows, and its confirmation time holds still while it is read.
func readUntil(h *policy.Holder, stop <-chan struct{}, digests map[int64]string) string {
	for {
		select {
		case <-stop:
			return ""
		default:
		}
		s := h.Current()
		if s == nil {
			continue
		}
		confirmed := s.ConfirmedAt()
		if got, want := s.Ref().GetDigest(), digests[s.Serial()]; got != want {
			return fmt.Sprintf("serial %d names digest %s, want %s", s.Serial(), got, want)
		}
		if got := s.Evaluate(readCall(), matchInputs()).Verdict; got != controlv1.Verdict_VERDICT_ALLOW {
			return fmt.Sprintf("serial %d decided a READ %v, want ALLOW", s.Serial(), got)
		}
		if !s.ConfirmedAt().Equal(confirmed) {
			return "a snapshot's ConfirmedAt changed while it was read"
		}
	}
}
