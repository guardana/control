package console

import (
	"net/http"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

const pending = controlv1.ApprovalState_APPROVAL_STATE_PENDING

// approveHeld is the body that approves the held record, padded with white
// space JSON allows to exactly n bytes.
func approveHeld(t *testing.T, s *site, n int) string {
	t.Helper()
	body := s.answering(t, "HELD1", "checked")
	if n < len(body) {
		t.Fatalf("cannot pad %d bytes to %d", len(body), n)
	}
	return body + strings.Repeat(" ", n-len(body))
}

// TestEveryRefusalLeavesTheRecordPending: each call differs from the one
// that approves in one thing, and each is refused with its own status and
// leaves the held record pending. The last call is the well-formed one, on
// the same record, and it approves: without it, a page that refused
// everything would pass every case above it.
func TestEveryRefusalLeavesTheRecordPending(t *testing.T) {
	dir, plane := newPlane(t)
	hold(t, plane, "HELD1")
	s := serve(t, dir, "")
	good := s.write("/api/approve", approveHeld(t, s, 160))
	otherPort := "http://127.0.0.1:1"
	if strings.HasSuffix(s.addr, ":1") {
		otherPort = "http://127.0.0.1:2"
	}
	flipped := []byte(s.token)
	flipped[len(flipped)-1] ^= 1
	digest := s.digestOf(t, "HELD1")
	for _, c := range []struct {
		name string
		call call
		want int
	}{
		{"a rebound name", withHost(good, "evil.example:"+port(s.addr)), http.StatusForbidden},
		{"a name for the loopback", withHost(good, "localhost:"+port(s.addr)), http.StatusForbidden},
		{"another port", withHost(good, "127.0.0.1:1"), http.StatusForbidden},
		{"no token", good.without(TokenHeader), http.StatusUnauthorized},
		{"another token of the same length", good.with(TokenHeader, string(flipped)), http.StatusUnauthorized},
		{"the token twice", withExtra(good, TokenHeader, s.token), http.StatusUnauthorized},
		{"the printed token", good.with(TokenHeader, s.printed), http.StatusUnauthorized},
		{"another port's origin", good.with("Origin", otherPort), http.StatusForbidden},
		{"an opaque origin", good.with("Origin", "null"), http.StatusForbidden},
		{"a second origin after this one", withExtra(good, "Origin", otherPort), http.StatusForbidden},
		{"a same-site fetch", good.with("Sec-Fetch-Site", "same-site"), http.StatusForbidden},
		{"a cross-site fetch", good.with("Sec-Fetch-Site", "cross-site"), http.StatusForbidden},
		{"a typed address", good.with("Sec-Fetch-Site", "none"), http.StatusForbidden},
		{"a second fetch site after this one", withExtra(good, "Sec-Fetch-Site", "cross-site"), http.StatusForbidden},
		{"plain text", good.with("Content-Type", "text/plain"), http.StatusUnsupportedMediaType},
		{"a form", good.with("Content-Type", "application/x-www-form-urlencoded"), http.StatusUnsupportedMediaType},
		{"no content type", good.without("Content-Type"), http.StatusUnsupportedMediaType},
		{"another charset", good.with("Content-Type", "application/json; charset=latin1"), http.StatusUnsupportedMediaType},
		{"one byte over 4 KiB", withBody(good, approveHeld(t, s, 4097)), http.StatusRequestEntityTooLarge},
		{"an unknown member", withBody(good, `{"id":"HELD1","reason":"","action_digest":"`+digest+`","approver_id":"someone-else"}`), http.StatusBadRequest},
		{"a member named twice", withBody(good, `{"id":"NOPE","id":"HELD1","reason":"","action_digest":"`+digest+`"}`), http.StatusBadRequest},
		{"text after the object", withBody(good, `{"id":"HELD1","reason":"","action_digest":"`+digest+`"} {}`), http.StatusBadRequest},
		{"an array", withBody(good, `[{"id":"HELD1","reason":"","action_digest":"`+digest+`"}]`), http.StatusBadRequest},
		{"a byte that is not UTF-8", withBody(good, "{\"id\":\"HELD1\",\"reason\":\"ok\xff\",\"action_digest\":\""+digest+"\"}"), http.StatusBadRequest},
		{"no action digest", withBody(good, `{"id":"HELD1","reason":""}`), http.StatusConflict},
		{"another action digest", withBody(good, `{"id":"HELD1","reason":"","action_digest":"`+otherDigest(digest)+`"}`), http.StatusConflict},
		{"a read of a write", withMethod(good, http.MethodGet), http.StatusMethodNotAllowed},
	} {
		a := s.do(t, c.call)
		if a.status != c.want {
			t.Errorf("%s: answered %d %q, want %d", c.name, a.status, a.body, c.want)
		}
		if got := s.stateOf(t, "HELD1"); got != pending {
			t.Fatalf("%s: the record is %s after a refused call", c.name, got)
		}
	}
	if a := s.do(t, withBody(good, approveHeld(t, s, 4096))); a.status != http.StatusOK {
		t.Fatalf("the well-formed call of exactly 4 KiB answered %d %q, want 200", a.status, a.body)
	}
	if got := s.stateOf(t, "HELD1"); got != controlv1.ApprovalState_APPROVAL_STATE_APPROVED {
		t.Errorf("after the well-formed call the record is %s, want approved", got)
	}
}

// otherDigest is d with its last hex digit changed.
func otherDigest(d string) string {
	last := byte('0')
	if d[len(d)-1] == '0' {
		last = '1'
	}
	return d[:len(d)-1] + string(last)
}

// TestAMemberSpelledAnyOtherWayIsRefused: the decoder matches member names
// ignoring case and folding, and keeps the last match. Each body would be
// filed under it, the digest being the one of the record it would reach, and
// each is refused, leaving every record pending with no reason on it.
func TestAMemberSpelledAnyOtherWayIsRefused(t *testing.T) {
	dir, plane := newPlane(t)
	for _, id := range []string{"SHOWN", "OTHER", "UPPER", "LONGS", "DIGEST"} {
		hold(t, plane, id)
	}
	s := serve(t, dir, "")
	for name, body := range map[string]string{
		"a second id in capitals":       `{"id":"SHOWN","ID":"OTHER","reason":"","action_digest":"` + s.digestOf(t, "OTHER") + `"}`,
		"every member in capitals":      `{"ID":"UPPER","REASON":"x","ACTION_DIGEST":"` + s.digestOf(t, "UPPER") + `"}`,
		"a long s in reason":            `{"id":"LONGS","reaſon":"y","action_digest":"` + s.digestOf(t, "LONGS") + `"}`,
		"a digest in another case only": `{"id":"DIGEST","reason":"","Action_Digest":"` + s.digestOf(t, "DIGEST") + `"}`,
	} {
		if a := s.do(t, s.write("/api/reject", body)); a.status != http.StatusBadRequest {
			t.Errorf("%s: answered %d %q, want 400", name, a.status, a.body)
		}
	}
	for _, id := range []string{"SHOWN", "OTHER", "UPPER", "LONGS", "DIGEST"} {
		if got := s.stateOf(t, id); got != pending {
			t.Errorf("%s is %s after refused calls", id, got)
		}
	}
	if a := s.do(t, s.write("/api/reject", s.answering(t, "OTHER", ""))); a.status != http.StatusOK {
		t.Errorf("the same answer spelled exactly answered %d %q", a.status, a.body)
	}
}

// TestABrowserlessClientNeedsOnlyTheToken: Origin and Sec-Fetch-Site are
// judged when a browser sends them; a client that sends neither is held to
// the token alone.
func TestABrowserlessClientNeedsOnlyTheToken(t *testing.T) {
	dir, plane := newPlane(t)
	hold(t, plane, "HELD1")
	s := serve(t, dir, "")
	c := s.write("/api/reject", s.answering(t, "HELD1", "")).without("Origin").without("Sec-Fetch-Site")
	if a := s.do(t, c); a.status != http.StatusOK {
		t.Fatalf("answered %d %q, want 200", a.status, a.body)
	}
	if got := s.stateOf(t, "HELD1"); got != controlv1.ApprovalState_APPROVAL_STATE_REJECTED {
		t.Errorf("the record is %s, want rejected", got)
	}
}

// TestReadsNeedTheTokenAndTheOrigin: the listing is refused without the
// session token and from another site, and says nothing of the record when
// it is.
func TestReadsNeedTheTokenAndTheOrigin(t *testing.T) {
	dir, plane := newPlane(t)
	hold(t, plane, "HELD1")
	s := serve(t, dir, "")
	for _, c := range []struct {
		name string
		call call
		want int
	}{
		{"no token", s.readState().without(TokenHeader), http.StatusUnauthorized},
		{"the printed token", s.readState().with(TokenHeader, s.printed), http.StatusUnauthorized},
		{"a rebound name", withHost(s.readState(), "evil.example:"+port(s.addr)), http.StatusForbidden},
		{"a cross-site fetch", s.readState().with("Sec-Fetch-Site", "cross-site"), http.StatusForbidden},
		{"another origin", s.readState().with("Origin", "http://evil.example"), http.StatusForbidden},
		{"a write of a read", withMethod(s.readState(), http.MethodPost), http.StatusMethodNotAllowed},
	} {
		a := s.do(t, c.call)
		if a.status != c.want || strings.Contains(a.body, "HELD1") {
			t.Errorf("%s: answered %d %q, want %d and no record", c.name, a.status, a.body, c.want)
		}
	}
	if a := s.do(t, s.readState()); a.status != http.StatusOK || !strings.Contains(a.body, `"approval_id":"HELD1"`) {
		t.Errorf("the well-formed read answered %d %q, want 200 naming the record", a.status, a.body)
	}
}

// TestOnlyTheEmbeddedFilesAreServed: every spelling of a path outside the
// three files and the API is 404, whatever it climbs to, the three files are
// what the build embedded, and they are read, never written.
func TestOnlyTheEmbeddedFilesAreServed(t *testing.T) {
	dir, _ := newPlane(t)
	s := serve(t, dir, "")
	for _, path := range []string{
		"/../signing.key", "/app.js/..%2f..%2fsigning.key", "/%2e%2e/signing.key", "/signing.key",
		"/assets/app.js", "/index.html", "/app.js/", "//app.js", "/api/", "/api/state/", "/api/../app.js",
	} {
		c := call{method: http.MethodGet, path: path, host: s.addr, headers: [][2]string{{TokenHeader, s.token}}}
		if a := s.do(t, c); a.status != http.StatusNotFound {
			t.Errorf("GET %s answered %d, want 404", path, a.status)
		}
	}
	for path, file := range map[string]string{"/": "assets/index.html", "/app.js": "assets/app.js", "/app.css": "assets/app.css"} {
		want, err := assets.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		a := s.do(t, call{method: http.MethodGet, path: path, host: s.addr})
		if a.status != http.StatusOK || a.body != string(want) {
			t.Errorf("GET %s answered %d with %d bytes, want 200 and the %d bytes of %s", path, a.status, len(a.body), len(want), file)
		}
		if a := s.do(t, call{method: http.MethodPost, path: path, host: s.addr}); a.status != http.StatusMethodNotAllowed {
			t.Errorf("POST %s answered %d, want 405", path, a.status)
		}
	}
	if a := s.do(t, withHost(call{method: http.MethodGet, path: "/"}, "evil.example:"+port(s.addr))); a.status != http.StatusForbidden {
		t.Errorf("the shell under a rebound name answered %d, want 403", a.status)
	}
}

func port(addr string) string { return addr[strings.LastIndexByte(addr, ':')+1:] }

func withHost(c call, host string) call { c.host = host; return c }

func withBody(c call, body string) call { c.body = body; return c }

func withMethod(c call, method string) call { c.method = method; return c }

// withExtra returns c with a second header named name, after the first.
func withExtra(c call, name, value string) call {
	c.headers = append(append([][2]string(nil), c.headers...), [2]string{name, value})
	return c
}
