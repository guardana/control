//go:build unix

package console

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// raceFor bounds each race below.
const raceFor = 4 * time.Second

// swapper renames dir away, puts a link to other at its name, and puts dir
// back, over and over until stop is called, which reports how many swaps put
// the link in place.
func swapper(t *testing.T, dir, other string) (stop func() int64) {
	t.Helper()
	moved := filepath.Join(filepath.Dir(dir), "moved")
	done := make(chan struct{})
	var wg sync.WaitGroup
	var swaps atomic.Int64
	wg.Go(func() {
		for {
			select {
			case <-done:
				return
			default:
			}
			if os.Rename(dir, moved) != nil {
				continue
			}
			linked := os.Symlink(other, dir) == nil
			_ = os.Remove(dir)
			if err := os.Rename(moved, dir); err != nil {
				t.Error(err)
				return
			}
			if linked {
				swaps.Add(1)
			}
		}
	})
	return func() int64 {
		close(done)
		wg.Wait()
		return swaps.Load()
	}
}

// TestAPageRacingASwapNeverListsTheOtherStore: the name flips between the
// page's directory and a link to another store while the page lists. No
// listing names the other store's record.
func TestAPageRacingASwapNeverListsTheOtherStore(t *testing.T) {
	dir, plane := newPlane(t)
	hold(t, plane, "HOME")
	s := serve(t, dir, "")
	other, plane2 := newPlane(t)
	hold(t, plane2, "ELSEWHERE")
	stop := swapper(t, dir, other)
	reads, shown := 0, ""
	for deadline := time.Now().Add(raceFor); time.Now().Before(deadline) && shown == ""; reads++ {
		if a := s.do(t, s.readState()); strings.Contains(a.body, `"approval_id":"ELSEWHERE"`) {
			shown = a.body
		}
	}
	if swaps := stop(); swaps == 0 || reads == 0 {
		t.Fatalf("%d swaps and %d reads: the race examined nothing", swaps, reads)
	}
	if shown != "" {
		t.Errorf("the page listed a record of %s as its own directory's: %.400s", other, shown)
	}
}

// TestAPageRacingASwapNeverAnswersTheOtherStore: the same flip while the
// page answers the other store's record with that record's own digest. No
// answer is filed there and none is answered 200.
func TestAPageRacingASwapNeverAnswersTheOtherStore(t *testing.T) {
	dir, _ := newPlane(t)
	s := serve(t, dir, "")
	other, plane2 := newPlane(t)
	hold(t, plane2, "ELSEWHERE")
	body := `{"id":"ELSEWHERE","reason":"","action_digest":"` + freshDigest(t, other, "ELSEWHERE") + `"}`
	stop := swapper(t, dir, other)
	answers, filed, landed := 0, 0, false
	for deadline := time.Now().Add(raceFor); time.Now().Before(deadline) && !landed; answers++ {
		if a := s.do(t, s.write("/api/approve", body)); a.status == http.StatusOK {
			filed++
		}
		_, err := os.Lstat(filepath.Join(other, "ELSEWHERE.1-answered.rec"))
		landed = err == nil
	}
	if swaps := stop(); swaps == 0 || answers == 0 {
		t.Fatalf("%d swaps and %d answers: the race examined nothing", swaps, answers)
	}
	if landed || filed > 0 {
		t.Errorf("the page filed an answer in %s, a store it never opened: %d answered 200", other, filed)
	}
	if got := freshState(t, other, "ELSEWHERE"); got != pending {
		t.Errorf("the other store's record is %s", got)
	}
}
