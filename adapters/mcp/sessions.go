package mcp

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// sessionHeader names the stateful session a request belongs to; a POST
// without it opens a session.
const sessionHeader = "Mcp-Session-Id"

// sessionCap refuses a request that would open a stateful session while limit
// are live. live counts the server's sessions, which the library drops when
// one closes or idles out; opening counts the opens admitted whose answer has
// not begun, so opens that race cannot pass the cap together. The library
// lists a session before it writes a byte of the answer, so an open stops
// counting as opening when its answer begins and counts once from then on.
type sessionCap struct {
	limit   int
	live    func() int
	refused *atomic.Int64

	mu      sync.Mutex
	opening int
}

func (c *sessionCap) admit() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.live()+c.opening >= c.limit {
		return false
	}
	c.opening++
	return true
}

func (c *sessionCap) done() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.opening--
}

// handler reads a sessionless POST's body before it counts the request, so a
// body still arriving holds no place, and the library, which opens a session
// before it reads a body, sees none until the body is whole. A refusal says
// only its status, nothing of the cap or of how many sessions are live.
func (c *sessionCap) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get(sessionHeader) != "" {
			next.ServeHTTP(w, r)
			return
		}
		body, status := readBody(w, r)
		if status != 0 {
			http.Error(w, http.StatusText(status), status)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		if !opens(body) {
			next.ServeHTTP(w, r)
			return
		}
		if !c.admit() {
			c.refused.Add(1)
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		aw := &answerWriter{ResponseWriter: w, begun: c.done}
		defer aw.begin()
		next.ServeHTTP(aw, r)
	})
}

// answerWriter calls begun once, when the answer it carries begins: at its
// header, its first byte or its first flush, or, if none comes, when the
// handler returns.
type answerWriter struct {
	http.ResponseWriter
	begun func()
	once  sync.Once
}

func (a *answerWriter) begin() { a.once.Do(a.begun) }

func (a *answerWriter) WriteHeader(code int) {
	a.begin()
	a.ResponseWriter.WriteHeader(code)
}

func (a *answerWriter) Write(p []byte) (int, error) {
	a.begin()
	return a.ResponseWriter.Write(p)
}

// FlushError is what http.ResponseController.Flush calls.
func (a *answerWriter) FlushError() error {
	a.begin()
	return http.NewResponseController(a.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the server's writer.
func (a *answerWriter) Unwrap() http.ResponseWriter { return a.ResponseWriter }

// readBody reads a body under the library's own bound on its size, and
// returns the status to refuse it with when it cannot.
func readBody(w http.ResponseWriter, r *http.Request) ([]byte, int) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, mcp.DefaultMaxRequestBodyBytes))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return nil, http.StatusRequestEntityTooLarge
	case err != nil:
		return nil, http.StatusBadRequest
	}
	return body, 0
}

// opens reports whether a sessionless POST counts as an open: anything but
// one message the library's own decoder reads as a request other than
// initialize. A batch, which that decoder does not read, a response or a
// body nobody can read counts, so no reading of the body other than the
// library's lets an open pass uncounted.
func opens(body []byte) bool {
	msg, err := jsonrpc.DecodeMessage(body)
	if err != nil {
		return true
	}
	req, ok := msg.(*jsonrpc.Request)
	return !ok || req.Method == "initialize"
}

// capSessions bounds the live sessions of a stateful listener.
func (a *Adapter) capSessions(next http.Handler) http.Handler {
	c := &sessionCap{limit: a.sessionLimit(), live: a.liveSessions, refused: &a.sessionsRefused}
	return c.handler(next)
}

// liveSessions counts the sessions the server holds.
func (a *Adapter) liveSessions() int {
	n := 0
	for range a.server.Sessions() {
		n++
	}
	return n
}

// sessionLimit is the configured cap on live sessions, or the default.
func (a *Adapter) sessionLimit() int {
	if a.cfg.Listener.MaxSessions > 0 {
		return a.cfg.Listener.MaxSessions
	}
	return DefaultMaxSessions
}
