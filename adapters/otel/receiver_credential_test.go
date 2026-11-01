package otel_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/guardana/control/adapters/otel"
)

// TestAReceiverRequiresACredential: no credential, and one outside the bounds
// or the characters a header carries as they are, builds no receiver; each
// refused case sits beside the one byte that makes it a credential.
func TestAReceiverRequiresACredential(t *testing.T) {
	if h, err := otel.NewReceiver(&memorySink{}, nil, otel.Credential{}); h != nil || !errors.Is(err, otel.ErrCredential) {
		t.Errorf("NewReceiver with no credential = %v, %v; want nil, ErrCredential", h, err)
	}
	for token, ok := range map[string]bool{
		strings.Repeat("a", 31):                false,
		strings.Repeat("a", 32):                true,
		strings.Repeat("a", 512):               true,
		strings.Repeat("a", 513):               false,
		strings.Repeat("a", 31) + " ":          false,
		strings.Repeat("a", 31) + "~":          true,
		strings.Repeat("a", 31) + "\x7f":       false,
		strings.Repeat("a", 31) + "\t":         false,
		strings.Repeat("a", 31) + "é":          false,
		strings.Repeat("a", 31) + "\r\nX-A: b": false,
	} {
		_, err := otel.NewCredential(token)
		if (err == nil) != ok || (err != nil && !errors.Is(err, otel.ErrCredential)) {
			t.Errorf("NewCredential(%q) = %v, want accepted %v", token, err, ok)
		}
	}
}

// TestARequestWithoutTheCredentialIsRefusedBeforeItIsRead: every request that
// does not carry the credential exactly is answered 401, whatever its method
// or size, and the sink is never reached; the request beside each, carrying
// it, is taken.
func TestARequestWithoutTheCredentialIsRefusedBeforeItIsRead(t *testing.T) {
	over := base() + strings.Repeat(" ", otel.MaxRequestBytes)
	cases := []struct {
		name    string
		headers []string
		method  string
		body    string
		status  int
	}{
		{"the credential", []string{"Bearer " + testToken}, http.MethodPost, base(), http.StatusOK},
		{"the scheme in another case", []string{"bearer " + testToken}, http.MethodPost, base(), http.StatusOK},
		{"no header", nil, http.MethodPost, base(), http.StatusUnauthorized},
		{"an empty header", []string{""}, http.MethodPost, base(), http.StatusUnauthorized},
		{"the token alone", []string{testToken}, http.MethodPost, base(), http.StatusUnauthorized},
		{"another scheme", []string{"Basic " + testToken}, http.MethodPost, base(), http.StatusUnauthorized},
		{"one byte short", []string{"Bearer " + testToken[1:]}, http.MethodPost, base(), http.StatusUnauthorized},
		{"one byte more", []string{"Bearer " + testToken + "k"}, http.MethodPost, base(), http.StatusUnauthorized},
		{"one byte other", []string{"Bearer " + testToken[1:] + "j"}, http.MethodPost, base(), http.StatusUnauthorized},
		{"a space after", []string{"Bearer " + testToken + " "}, http.MethodPost, base(), http.StatusUnauthorized},
		{"two spaces between", []string{"Bearer  " + testToken}, http.MethodPost, base(), http.StatusUnauthorized},
		{"two headers", []string{"Bearer " + testToken, "Bearer " + testToken}, http.MethodPost, base(), http.StatusUnauthorized},
		{"a wrong one before", []string{"Bearer x", "Bearer " + testToken}, http.MethodPost, base(), http.StatusUnauthorized},
		{"a GET without it", nil, http.MethodGet, base(), http.StatusUnauthorized},
		{"a GET with it", []string{"Bearer " + testToken}, http.MethodGet, base(), http.StatusMethodNotAllowed},
		{"over the bound without it", nil, http.MethodPost, over, http.StatusUnauthorized},
		{"over the bound with it", []string{"Bearer " + testToken}, http.MethodPost, over, http.StatusRequestEntityTooLarge},
	}
	for _, c := range cases {
		sink := &memorySink{}
		h, err := otel.NewReceiver(sink, slog.New(slog.DiscardHandler), testCredential(t))
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(c.method, "/v1/logs", strings.NewReader(c.body))
		req.Header.Set("Content-Type", "application/json")
		for _, v := range c.headers {
			req.Header.Add("Authorization", v)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		want := 0
		if c.status == http.StatusOK {
			want = 1
		}
		if rec.Code != c.status || rec.Body.Len() != 0 || sink.appends() != want {
			t.Errorf("%s: answered %d with %q and the sink was called %d times; want %d, no body, %d calls",
				c.name, rec.Code, rec.Body.String(), sink.appends(), c.status, want)
		}
		if c.status == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: WWW-Authenticate = %q, want Bearer", c.name, rec.Header().Get("WWW-Authenticate"))
		}
	}
}

// lines counts the records a logger writes.
type lines struct {
	mu    sync.Mutex
	texts []string
}

func (l *lines) Enabled(context.Context, slog.Level) bool { return true }
func (l *lines) Handle(_ context.Context, r slog.Record) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	text := r.Message
	r.Attrs(func(a slog.Attr) bool { text += " " + a.String(); return true })
	l.texts = append(l.texts, text)
	return nil
}
func (l *lines) WithAttrs([]slog.Attr) slog.Handler { return l }
func (l *lines) WithGroup(string) slog.Handler      { return l }

// TestRequestsWithoutTheCredentialAreLoggedAtABoundedRate: a thousand
// refused requests within the bound write one line, and the first refusal
// past it writes the count of every one since.
func TestRequestsWithoutTheCredentialAreLoggedAtABoundedRate(t *testing.T) {
	logged := &lines{}
	h, err := otel.NewReceiver(&memorySink{}, slog.New(logged), testCredential(t))
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(1_800_000_000, 0)
	otel.SetReceiverClock(h, func() time.Time { return at })
	refuse := func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/logs", strings.NewReader(base())))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("answered %d, want 401", rec.Code)
		}
	}
	for range 1000 {
		refuse()
	}
	if len(logged.texts) != 1 || !strings.Contains(logged.texts[0], "requests=1") {
		t.Fatalf("a thousand refusals within the bound logged %q", logged.texts)
	}
	at = at.Add(59 * time.Second)
	refuse()
	if len(logged.texts) != 1 {
		t.Fatalf("a refusal a second inside the bound logged %q", logged.texts)
	}
	at = at.Add(time.Second)
	refuse()
	if len(logged.texts) != 2 || !strings.Contains(logged.texts[1], "requests=1001") {
		t.Errorf("the refusal at the bound logged %q, want the count of 1001", logged.texts)
	}
}
