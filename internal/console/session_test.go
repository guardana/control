package console

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guardana/control/internal/approvals"
)

// fakeClock is a clock a test moves by hand.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// TestThePrintedTokenIsTradedOnce: the printed link is kept in the browser's
// history, so what it carries opens one session and nothing after that. It
// reads nothing before the trade, trades once, and is refused everywhere
// after; the session token reads, and is no printed token to trade again.
func TestThePrintedTokenIsTradedOnce(t *testing.T) {
	dir, plane := newPlane(t)
	hold(t, plane, "HELD1")
	s := serveAt(t, dir, "", nil)
	if a := s.do(t, s.readState().with(TokenHeader, s.printed)); a.status != http.StatusUnauthorized {
		t.Errorf("a read with the printed token before the trade answered %d %q, want 401", a.status, a.body)
	}
	s.token = s.trade(t)
	for name, c := range map[string]call{
		"a second trade":                   s.tradeCall(),
		"a read with the printed token":    s.readState().with(TokenHeader, s.printed),
		"a trade of the session token":     s.tradeCall().with(TokenHeader, s.token),
		"an answer with the printed token": s.write("/api/approve", s.answering(t, "HELD1", "")).with(TokenHeader, s.printed),
	} {
		if a := s.do(t, c); a.status != http.StatusUnauthorized {
			t.Errorf("%s answered %d %q, want 401", name, a.status, a.body)
		}
	}
	if got := s.stateOf(t, "HELD1"); got != pending {
		t.Fatalf("the record is %s after refused calls", got)
	}
	if a := s.do(t, s.readState()); a.status != http.StatusOK {
		t.Errorf("a read with the session token answered %d %q", a.status, a.body)
	}
}

// TestAnUntradedTokenLivesTenMinutes: a page nobody opened refuses its
// printed token from the tenth minute on, and a page opened a second before
// that trades it.
func TestAnUntradedTokenLivesTenMinutes(t *testing.T) {
	dir, _ := newPlane(t)
	start := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		after time.Duration
		want  int
	}{
		{10*time.Minute - time.Second, http.StatusOK},
		{10 * time.Minute, http.StatusUnauthorized},
		{time.Hour, http.StatusUnauthorized},
	} {
		clock := &fakeClock{now: start}
		s := serveAt(t, dir, "", clock.read)
		clock.set(start.Add(c.after))
		if a := s.do(t, s.tradeCall()); a.status != c.want {
			t.Errorf("a trade %s after start answered %d %q, want %d", c.after, a.status, a.body, c.want)
		}
	}
}

// TestARefusedTradeSpendsNothing: a trade the page refuses for where it came
// from or for what it carries leaves the printed token to the trade that is
// well formed.
func TestARefusedTradeSpendsNothing(t *testing.T) {
	dir, _ := newPlane(t)
	s := serveAt(t, dir, "", nil)
	for name, c := range map[string]struct {
		call call
		want int
	}{
		"another origin":     {s.tradeCall().with("Origin", "http://evil.example"), http.StatusForbidden},
		"a cross-site fetch": {s.tradeCall().with("Sec-Fetch-Site", "cross-site"), http.StatusForbidden},
		"a form":             {s.tradeCall().with("Content-Type", "application/x-www-form-urlencoded"), http.StatusUnsupportedMediaType},
		"a member":           {withBody(s.tradeCall(), `{"session":"mine"}`), http.StatusBadRequest},
		"a read":             {withMethod(s.tradeCall(), http.MethodGet), http.StatusMethodNotAllowed},
	} {
		if a := s.do(t, c.call); a.status != c.want {
			t.Errorf("%s answered %d %q, want %d", name, a.status, a.body, c.want)
		}
	}
	s.trade(t)
}

// TestTwoTradesAtOnceOpenOneSession: of trades racing for one printed token,
// exactly one is answered with a session.
func TestTwoTradesAtOnceOpenOneSession(t *testing.T) {
	dir, _ := newPlane(t)
	s := serveAt(t, dir, "", nil)
	var won atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if a := s.do(t, s.tradeCall()); a.status == http.StatusOK {
				won.Add(1)
			}
		})
	}
	wg.Wait()
	if n := won.Load(); n != 1 {
		t.Errorf("%d trades opened a session, want 1", n)
	}
}

// TestATradeChecksThePrintedTokenAgainUnderItsLock calls the trade itself,
// past the check every request passes first, as the second of two trades
// that both passed it would. Only the first opens a session; a second trade,
// the session token and a trade after the printed token's life are refused
// there too.
func TestATradeChecksThePrintedTokenAgainUnderItsLock(t *testing.T) {
	dir, _ := newPlane(t)
	store, err := approvals.OpenApprover(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	start := time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: start}
	printed := NewToken()
	var h http.Handler
	h, err = New(Options{Approvals: store, Directory: dir, ApproverID: testApprover, Host: "127.0.0.1:8123", Token: printed, Now: clock.read})
	if err != nil {
		t.Fatal(err)
	}
	p := h.(*page)
	trade := func(token string) int {
		r := httptest.NewRequest(http.MethodPost, SessionPath, strings.NewReader("{}"))
		r.Header.Set(TokenHeader, token)
		w := httptest.NewRecorder()
		p.startSession(w, r, nil)
		return w.Code
	}
	if got := trade(printed); got != http.StatusOK {
		t.Fatalf("the first trade answered %d", got)
	}
	session := p.session
	for name, token := range map[string]string{"a second trade": printed, "the session token": session} {
		if got := trade(token); got != http.StatusUnauthorized {
			t.Errorf("%s answered %d, want 401", name, got)
		}
	}
	if p.session != session {
		t.Error("a refused trade replaced the session")
	}
	late, err := New(Options{Approvals: store, Directory: dir, ApproverID: testApprover, Host: "127.0.0.1:8123", Token: printed, Now: clock.read})
	if err != nil {
		t.Fatal(err)
	}
	p = late.(*page)
	clock.set(start.Add(PrintedTokenLife + 10*time.Minute))
	if got := trade(printed); got != http.StatusUnauthorized {
		t.Errorf("a trade after the printed token's life answered %d, want 401", got)
	}
}
