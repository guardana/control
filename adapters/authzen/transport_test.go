package authzen

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/guardana/control/internal/core"
)

// TestOnlyStatus200IsAnAnswer: every other status is no decision, even with a
// body that would allow, and a redirect is never followed.
func TestOnlyStatus200IsAnAnswer(t *testing.T) {
	for _, status := range []int{201, 202, 204, 301, 302, 303, 307, 308, 400, 401, 403, 404, 408, 429, 500, 502, 503} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var followed atomic.Int32
			p := newPDP(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/elsewhere" {
					followed.Add(1)
					answer(`{"decision":true}`)(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-ID", r.Header.Get("X-Request-ID"))
				w.Header().Set("Location", "/elsewhere")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"decision":true}`)
			})
			got := client(t, options(p)).Ask(within(t, 10*time.Second), envelope())
			if got != core.ExternalUnavailable() {
				t.Errorf("status %d = %v, want unavailable", status, got)
			}
			if followed.Load() != 0 {
				t.Errorf("status %d: the redirect was followed", status)
			}
		})
	}
}

// TestContentTypeIsJSON: a 200 that does not say it is JSON is refused.
func TestContentTypeIsJSON(t *testing.T) {
	cases := map[string]core.External{
		"application/json":                 core.ExternalAllowed(),
		"application/json; charset=utf-8":  core.ExternalAllowed(),
		"Application/JSON; Charset=UTF-8":  core.ExternalAllowed(),
		"":                                 core.ExternalAnswerRefused(),
		"text/plain":                       core.ExternalAnswerRefused(),
		"text/json":                        core.ExternalAnswerRefused(),
		"application/jsonx":                core.ExternalAnswerRefused(),
		"application/problem+json":         core.ExternalAnswerRefused(),
		"application/json; charset=utf-16": core.ExternalAnswerRefused(),
		"application/json; profile=x":      core.ExternalAnswerRefused(),
	}
	for ct, want := range cases {
		t.Run(ct, func(t *testing.T) {
			p := newPDP(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header()["Content-Type"] = []string{ct}
				if ct == "" {
					w.Header()["Content-Type"] = nil
				}
				w.Header().Set("X-Request-ID", r.Header.Get("X-Request-ID"))
				_, _ = io.WriteString(w, `{"decision":true}`)
			})
			if got := client(t, options(p)).Ask(within(t, 10*time.Second), envelope()); got != want {
				t.Errorf("Content-Type %q = %v, want %v", ct, got, want)
			}
		})
	}
}

// TestRequestIDIsReturned: an answer that does not return the identifier sent
// is refused, on a denial as much as on an allow.
func TestRequestIDIsReturned(t *testing.T) {
	cases := map[string]struct {
		echo []string
		body string
		want core.External
	}{
		"returned":                  {[]string{"req-0001"}, `{"decision":true}`, core.ExternalAllowed()},
		"missing on an allow":       {nil, `{"decision":true}`, core.ExternalAnswerRefused()},
		"missing on a deny":         {nil, `{"decision":false}`, core.ExternalAnswerRefused()},
		"another one":               {[]string{"req-0002"}, `{"decision":true}`, core.ExternalAnswerRefused()},
		"another case":              {[]string{"REQ-0001"}, `{"decision":true}`, core.ExternalAnswerRefused()},
		"empty":                     {[]string{""}, `{"decision":true}`, core.ExternalAnswerRefused()},
		"twice, the right one last": {[]string{"req-0002", "req-0001"}, `{"decision":true}`, core.ExternalAnswerRefused()},
		"twice, the same":           {[]string{"req-0001", "req-0001"}, `{"decision":true}`, core.ExternalAnswerRefused()},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := newPDP(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header()["X-Request-Id"] = tc.echo
				_, _ = io.WriteString(w, tc.body)
			})
			if got := client(t, options(p)).Ask(within(t, 10*time.Second), envelope()); got != tc.want {
				t.Errorf("echo %q = %v, want %v", tc.echo, got, tc.want)
			}
		})
	}
}

// TestAnswerSizeIsBounded: a denial of exactly 64 KiB is read, one byte more
// is refused.
func TestAnswerSizeIsBounded(t *testing.T) {
	sized := func(n int) string {
		head, tail := `{"decision":false,"pad":"`, `"}`
		return head + strings.Repeat("x", n-len(head)-len(tail)) + tail
	}
	cases := map[int]core.External{
		64 << 10:       core.ExternalDenied(),
		64<<10 + 1:     core.ExternalAnswerRefused(),
		4 * (64 << 10): core.ExternalAnswerRefused(),
	}
	for n, want := range cases {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			body := sized(n)
			if len(body) != n {
				t.Fatalf("built %d bytes, want %d", len(body), n)
			}
			p := newPDP(t, answer(body))
			if got := client(t, options(p)).Ask(within(t, 10*time.Second), envelope()); got != want {
				t.Errorf("a %d-byte answer = %v, want %v", n, got, want)
			}
		})
	}
}

// hold is a handler that answers nothing until the client gives up or the
// test ends, and reports each request it holds on arrived.
func hold(t *testing.T, arrived chan<- struct{}) (http.HandlerFunc, func()) {
	t.Helper()
	release := make(chan struct{})
	var once atomic.Bool
	stop := func() {
		if once.CompareAndSwap(false, true) {
			close(release)
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if arrived != nil {
			arrived <- struct{}{}
		}
		select {
		case <-r.Context().Done():
		case <-release:
			answer(`{"decision":true}`)(w, r)
		}
	}, stop
}

// TestDeadline: no answer within the options' timeout is a timeout, and a
// shorter deadline on ctx governs.
func TestDeadline(t *testing.T) {
	t.Run("the options' timeout", func(t *testing.T) {
		h, stop := hold(t, nil)
		p := newPDP(t, h)
		t.Cleanup(stop)
		opts := options(p)
		opts.Timeout = 100 * time.Millisecond
		start := time.Now()
		if got := client(t, opts).Ask(within(t, 30*time.Second), envelope()); got != core.ExternalTimeout() {
			t.Errorf("a held answer = %v, want timeout", got)
		}
		if el := time.Since(start); el > 5*time.Second {
			t.Errorf("the ask took %v past a 100ms timeout", el)
		}
	})
	t.Run("ctx's deadline", func(t *testing.T) {
		h, stop := hold(t, nil)
		p := newPDP(t, h)
		t.Cleanup(stop)
		opts := options(p)
		opts.Timeout = 30 * time.Second
		start := time.Now()
		if got := client(t, opts).Ask(within(t, 100*time.Millisecond), envelope()); got != core.ExternalTimeout() {
			t.Errorf("a held answer = %v, want timeout", got)
		}
		if el := time.Since(start); el > 5*time.Second {
			t.Errorf("the ask took %v past a 100ms deadline on ctx", el)
		}
	})
	t.Run("a body cut by the deadline", func(t *testing.T) {
		p := newPDP(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("X-Request-ID", r.Header.Get("X-Request-ID"))
			_, _ = io.WriteString(w, `{"decision":`)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		})
		opts := options(p)
		opts.Timeout = 100 * time.Millisecond
		if got := client(t, opts).Ask(within(t, 30*time.Second), envelope()); got != core.ExternalTimeout() {
			t.Errorf("a body cut by the deadline = %v, want timeout", got)
		}
	})
	t.Run("ctx cancelled", func(t *testing.T) {
		p := newPDP(t, answer(`{"decision":true}`))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if got := client(t, options(p)).Ask(ctx, envelope()); got != core.ExternalUnavailable() {
			t.Errorf("a cancelled ask = %v, want unavailable", got)
		}
	})
}

// endsPastTheDeadline answers every header check at once and holds its body
// until the request's deadline has passed, then ends it cleanly after text:
// what the transport can report for a chunked body the deadline cut.
type endsPastTheDeadline string

func (text endsPastTheDeadline) RoundTrip(r *http.Request) (*http.Response, error) {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("X-Request-ID", r.Header.Get("X-Request-ID"))
	body := &heldBody{ctx: r.Context(), text: string(text)}
	return &http.Response{StatusCode: http.StatusOK, Header: h, Body: body, Request: r}, nil
}

type heldBody struct {
	ctx  context.Context
	text string
	read bool
}

func (b *heldBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, io.EOF
	}
	<-b.ctx.Done()
	b.read = true
	return copy(p, b.text), nil
}

func (b *heldBody) Close() error { return nil }

// TestABodyThatEndsPastTheDeadlineIsATimeout: a body read to its end only
// after the deadline is no answer within it, cut or whole.
func TestABodyThatEndsPastTheDeadlineIsATimeout(t *testing.T) {
	p := newPDP(t, answer(`{"decision":true}`))
	for _, text := range []string{`{"decision":`, `{"decision":true}`} {
		opts := options(p)
		opts.Timeout = 50 * time.Millisecond
		c := client(t, opts)
		c.http.Transport = endsPastTheDeadline(text)
		if got := c.Ask(within(t, 30*time.Second), envelope()); got != core.ExternalTimeout() {
			t.Errorf("%s read past the deadline = %v, want timeout", text, got)
		}
	}
}

// TestTransportFailureIsUnavailable: a decision point nobody answers at, and
// one whose certificate this client does not trust, give no decision.
func TestTransportFailureIsUnavailable(t *testing.T) {
	p := newPDP(t, answer(`{"decision":true}`))
	untrusted := options(p)
	untrusted.RootCAs = nil
	if got := client(t, untrusted).Ask(within(t, 10*time.Second), envelope()); got != core.ExternalUnavailable() {
		t.Errorf("an untrusted certificate = %v, want unavailable", got)
	}
	if p.hits() != 0 {
		t.Errorf("a request reached a decision point whose certificate is untrusted")
	}
	gone := options(p)
	p.Close()
	if got := client(t, gone).Ask(within(t, 10*time.Second), envelope()); got != core.ExternalUnavailable() {
		t.Errorf("a closed decision point = %v, want unavailable", got)
	}
}

// TestInFlightBound: an ask past the bound is unavailable at once, without a
// request, and the slot comes back when the held ask ends.
func TestInFlightBound(t *testing.T) {
	arrived := make(chan struct{}, 4)
	h, stop := hold(t, arrived)
	p := newPDP(t, h)
	t.Cleanup(stop)
	opts := options(p)
	opts.InFlight = 1
	c := client(t, opts)

	first := make(chan core.External, 1)
	go func() { first <- c.Ask(within(t, 30*time.Second), envelope()) }()
	select {
	case <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the first ask never reached the decision point")
	}
	start := time.Now()
	if got := c.Ask(within(t, 30*time.Second), envelope()); got != core.ExternalUnavailable() {
		t.Errorf("an ask past the bound = %v, want unavailable", got)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Errorf("an ask past the bound waited %v", el)
	}
	if n := p.hits(); n != 1 {
		t.Errorf("the decision point saw %d requests, want 1", n)
	}
	stop()
	if got := <-first; got != core.ExternalAllowed() {
		t.Errorf("the held ask = %v, want allowed", got)
	}
	if got := c.Ask(within(t, 10*time.Second), envelope()); got != core.ExternalAllowed() {
		t.Errorf("an ask after the slot came back = %v, want allowed", got)
	}
}

// TestPlaintextOnTheLoopback: plain HTTP works only under the risk setting,
// and New refuses it without.
func TestPlaintextOnTheLoopback(t *testing.T) {
	p := newPlainPDP(t, answer(`{"decision":true}`))
	opts := Options{Identifier: p.URL, Timeout: 5 * time.Second, InFlight: 1}
	if _, err := New(opts); err == nil {
		t.Fatal("New accepted a plaintext decision point without AllowPlaintext")
	}
	opts.AllowPlaintext = true
	if got := client(t, opts).Ask(within(t, 10*time.Second), envelope()); got != core.ExternalAllowed() {
		t.Errorf("a plaintext loopback decision point = %v, want allowed", got)
	}
}

// TestProxyFromTheEnvironmentIsNotUsed: a proxy variable does not redirect the
// question, and the transport has no proxy function unless one is configured.
func TestProxyFromTheEnvironmentIsNotUsed(t *testing.T) {
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		proxied.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(proxy.Close)
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("https_proxy", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	// A name that does not resolve: without a proxy the ask fails here, with
	// one it would reach the proxy.
	c := client(t, Options{Identifier: "https://pdp.invalid", Timeout: 2 * time.Second, InFlight: 1})
	if c.transport.Proxy != nil {
		t.Error("the transport has a proxy function with none configured")
	}
	if got := c.Ask(within(t, 10*time.Second), envelope()); got == core.ExternalAllowed() || got == core.ExternalDenied() {
		t.Errorf("an unresolvable decision point = %v", got)
	}
	if n := proxied.Load(); n != 0 {
		t.Errorf("the proxy from the environment saw %d requests", n)
	}
}

// TestConfiguredProxyIsUsed: a configured proxy carries the question.
func TestConfiguredProxyIsUsed(t *testing.T) {
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied.Add(1)
		answer(`{"decision":true}`)(w, r)
	}))
	t.Cleanup(proxy.Close)
	p := newPlainPDP(t, answer(`{"decision":false}`))
	opts := Options{Identifier: p.URL, AllowPlaintext: true, Proxy: proxy.URL, Timeout: 5 * time.Second, InFlight: 1}
	if got := client(t, opts).Ask(within(t, 10*time.Second), envelope()); got != core.ExternalAllowed() {
		t.Errorf("an ask through the proxy = %v, want the proxy's allow", got)
	}
	if proxied.Load() != 1 || p.hits() != 0 {
		t.Errorf("proxy saw %d, decision point saw %d; want 1 and 0", proxied.Load(), p.hits())
	}
}

// TestCompressedAnswerIsRead: the transport asks for gzip and decodes the
// answer itself, which is why Accept-Encoding is not configurable.
func TestCompressedAnswerIsRead(t *testing.T) {
	p := newPDP(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			answer(`{"decision":false}`)(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("X-Request-ID", r.Header.Get("X-Request-ID"))
		zw := gzip.NewWriter(w)
		_, _ = io.WriteString(zw, `{"decision":true}`)
		_ = zw.Close()
	})
	if got := client(t, options(p)).Ask(within(t, 10*time.Second), envelope()); got != core.ExternalAllowed() {
		t.Errorf("a gzip answer = %v, want allowed", got)
	}
}

// TestContentTypeIsDeclaredOnce: an answer that declares its Content-Type
// more than once is refused, even when every value says JSON.
func TestContentTypeIsDeclaredOnce(t *testing.T) {
	cases := map[string]struct {
		values []string
		want   core.External
	}{
		"once":                   {[]string{"application/json"}, core.ExternalAllowed()},
		"twice, both JSON":       {[]string{"application/json", "application/json"}, core.ExternalAnswerRefused()},
		"twice, JSON first":      {[]string{"application/json", "text/plain"}, core.ExternalAnswerRefused()},
		"twice, JSON with UTF-8": {[]string{"application/json; charset=utf-8", "application/json"}, core.ExternalAnswerRefused()},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p := newPDP(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header()["Content-Type"] = tc.values
				w.Header().Set("X-Request-ID", r.Header.Get("X-Request-ID"))
				_, _ = io.WriteString(w, `{"decision":true}`)
			})
			if got := client(t, options(p)).Ask(within(t, 10*time.Second), envelope()); got != tc.want {
				t.Errorf("Content-Type %q = %v, want %v", tc.values, got, tc.want)
			}
			if p.hits() != 1 {
				t.Errorf("the decision point was asked %d times, want 1", p.hits())
			}
		})
	}
}

// newH2PDP is newPDP speaking HTTP/2, and records the protocol of each
// request in protos.
func newH2PDP(t *testing.T, protos *atomic.Int64, handle http.HandlerFunc) *pdp {
	t.Helper()
	p := &pdp{}
	p.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		protos.Store(int64(r.ProtoMajor))
		p.record(r)
		handle(w, r)
	}))
	p.EnableHTTP2 = true
	p.StartTLS()
	t.Cleanup(p.Close)
	return p
}

// TestResponseHeadersAreBounded: an allow whose headers run past 64 KiB is no
// decision, over HTTP/1.1 and HTTP/2, and it comes back well before the
// deadline; the same allow with headers well under the bound is read.
func TestResponseHeadersAreBounded(t *testing.T) {
	cases := map[int]core.External{
		32 << 10:  core.ExternalAllowed(),
		128 << 10: core.ExternalUnavailable(),
	}
	for _, h2 := range []bool{false, true} {
		for size, want := range cases {
			t.Run(fmt.Sprintf("h2=%t/%d", h2, size), func(t *testing.T) {
				padded := func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("X-Padding", strings.Repeat("x", size))
					answer(`{"decision":true}`)(w, r)
				}
				var proto atomic.Int64
				var p *pdp
				if h2 {
					p = newH2PDP(t, &proto, padded)
				} else {
					p = newPDP(t, func(w http.ResponseWriter, r *http.Request) {
						proto.Store(int64(r.ProtoMajor))
						padded(w, r)
					})
				}
				opts := options(p)
				opts.Timeout = 5 * time.Second
				start := time.Now()
				if got := client(t, opts).Ask(within(t, 30*time.Second), envelope()); got != want {
					t.Errorf("an allow with %d bytes of headers = %v, want %v", size, got, want)
				}
				if el := time.Since(start); el > 2*time.Second {
					t.Errorf("the ask took %v, want it well before the 5s deadline", el)
				}
				if major, wantMajor := proto.Load(), map[bool]int64{false: 1, true: 2}[h2]; major != wantMajor {
					t.Errorf("the decision point was asked over HTTP/%d, want HTTP/%d", major, wantMajor)
				}
			})
		}
	}
}
