package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const openBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`

// TestAnOpenInFlightHoldsItsPlace: an open the library is still answering,
// before its session is live, holds its place, so a second open that arrives
// meanwhile is refused at a cap of one; once the first is answered and its
// session gone, an open is admitted again.
func TestAnOpenInFlightHoldsItsPlace(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var refused atomic.Int64
	c := &sessionCap{limit: 1, live: func() int { return 0 }, refused: &refused}
	h := c.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Hold") != "" {
			close(entered)
			<-release
		}
		w.WriteHeader(http.StatusOK)
	}))
	serve := func(hold bool) int {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(openBody))
		if hold {
			req.Header.Set("X-Hold", "1")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	first := make(chan int, 1)
	go func() { first <- serve(true) }()
	<-entered
	if got := serve(false); got != http.StatusServiceUnavailable {
		t.Errorf("a second open while the first is in flight: %d, want 503", got)
	}
	close(release)
	if got := <-first; got != http.StatusOK {
		t.Errorf("the open in flight: %d", got)
	}
	if got := serve(false); got != http.StatusOK {
		t.Errorf("an open once the first was answered: %d, want 200", got)
	}
	if n := refused.Load(); n != 1 {
		t.Errorf("%d refusals counted, want 1", n)
	}
}

// TestALiveSessionHoldsItsPlace: the live sessions count toward the cap
// alone, with nothing in flight.
func TestALiveSessionHoldsItsPlace(t *testing.T) {
	live := 2
	c := &sessionCap{limit: 2, live: func() int { return live }, refused: &atomic.Int64{}}
	h := c.handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	serve := func() int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(openBody)))
		return rec.Code
	}
	if got := serve(); got != http.StatusServiceUnavailable {
		t.Errorf("an open with two of two live: %d, want 503", got)
	}
	live = 1
	if got := serve(); got != http.StatusOK {
		t.Errorf("an open with one of two live: %d, want 200", got)
	}
}

// TestAnAnsweredOpenCountsOnceItsSessionIsLive: once the library has begun
// the answer to an open, by its header, a byte or a flush, its session is
// live and counted there, so an open that arrives before the handler returns
// finds one place taken, not two.
func TestAnAnsweredOpenCountsOnceItsSessionIsLive(t *testing.T) {
	for name, begin := range map[string]func(http.ResponseWriter){
		"a header": func(w http.ResponseWriter) { w.WriteHeader(http.StatusOK) },
		"a byte":   func(w http.ResponseWriter) { _, _ = w.Write([]byte("{")) },
		"a flush":  func(w http.ResponseWriter) { _ = http.NewResponseController(w).Flush() },
	} {
		t.Run(name, func(t *testing.T) { holdAnsweredOpen(t, begin) })
	}
}

func holdAnsweredOpen(t *testing.T, begin func(http.ResponseWriter)) {
	answered, release := make(chan struct{}), make(chan struct{})
	var live atomic.Int64
	c := &sessionCap{limit: 2, live: func() int { return int(live.Load()) }, refused: &atomic.Int64{}}
	h := c.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		live.Add(1)
		begin(w)
		if r.Header.Get("X-Hold") != "" {
			close(answered)
			<-release
		}
	}))
	first := make(chan int, 1)
	go func() { first <- serveOpen(h, true) }()
	<-answered
	if got := serveOpen(h, false); got != http.StatusOK {
		t.Errorf("a second open under a cap of 2 while the first's answer is being written: %d, want 200", got)
	}
	if got := serveOpen(h, false); got != http.StatusServiceUnavailable {
		t.Errorf("a third open with two sessions live under a cap of 2: %d, want 503", got)
	}
	close(release)
	if got := <-first; got != http.StatusOK {
		t.Errorf("the first open: %d", got)
	}
}

// TestAnOpenStopsCountingOnce: an answer that writes its header, bytes and
// flushes stops counting as opening once, and one that writes nothing stops
// when its handler returns.
func TestAnOpenStopsCountingOnce(t *testing.T) {
	for name, answer := range map[string]http.HandlerFunc{
		"an answer written in full": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{"))
			_ = http.NewResponseController(w).Flush()
			_, _ = w.Write([]byte("}"))
		},
		"no answer at all": func(http.ResponseWriter, *http.Request) {},
	} {
		c := &sessionCap{limit: 1, live: func() int { return 0 }, refused: &atomic.Int64{}}
		h := c.handler(answer)
		for range 3 {
			serveOpen(h, false)
		}
		if c.opening != 0 {
			t.Errorf("%s: %d opens still counted after three were answered, want 0", name, c.opening)
		}
	}
}

// serveOpen sends h one open, held in the handler when hold is set.
func serveOpen(h http.Handler, hold bool) int {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(openBody))
	if hold {
		req.Header.Set("X-Hold", "1")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}
