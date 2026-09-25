package loopback

import (
	"net"
	"net/url"
	"strings"
	"testing"
)

// TestCheckRefusesWhatIsNotALoopbackLiteral: each address breaks one rule,
// and each refusal says it is about the loopback.
func TestCheckRefusesWhatIsNotALoopbackLiteral(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{
		"0.0.0.0:0", "[::]:0", ":0", "192.0.2.1:0", "[2001:db8::1]:0",
		"localhost:0", "LOCALHOST:0", "ip6-localhost:0",
		"127.0.0.1", "[::1]", "127.0.0.1:x", "127.0.0.1:", "127.0.0.1:65536",
		"127.0.0.1:+80", "127.0.0.1:080", "127.0.0.1:-1", "", "127.0.0.1:80:80",
	} {
		err := Check(addr)
		if err == nil {
			t.Errorf("Check(%q) took it", addr)
			continue
		}
		if !strings.Contains(err.Error(), "loopback") {
			t.Errorf("Check(%q) = %q, which does not name the loopback", addr, err)
		}
	}
}

// TestCheckTakesALoopbackLiteral: every loopback address of both families,
// port 0 and the highest port among them.
func TestCheckTakesALoopbackLiteral(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"127.0.0.1:0", "127.0.0.2:4318", "127.255.255.254:1", "[::1]:4318", "127.0.0.1:65535"} {
		if err := Check(addr); err != nil {
			t.Errorf("Check(%q) = %v", addr, err)
		}
	}
}

// TestCheckURL: an http or https URL is held to Check by its host, with the
// scheme's port where it names none; anything else is refused naming the
// loopback, and no refusal quotes the URL's userinfo, path or query.
func TestCheckURL(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"http://127.0.0.1/mcp", "https://127.0.0.1", "http://[::1]:4318/v1/logs",
		"HTTP://127.0.0.1:9/mcp", "http://user:sesame@127.0.0.1:3128",
	} {
		if err := CheckURL(raw); err != nil {
			t.Errorf("CheckURL(%q) = %v", raw, err)
		}
	}
	for _, raw := range []string{
		"http://192.0.2.1/mcp", "https://192.0.2.1:8443/pdp", "http://[2001:db8::1]:80",
		"http://localhost:9/mcp", "http://orders.example/mcp", "http://0.0.0.0:9",
		"http://user:sesame@192.0.2.1:3128/sesame?sesame", "ftp://127.0.0.1:21/sesame", "127.0.0.1:9",
		"http:///sesame", "http://127.0.0.1:080/sesame", "http://127.0.0.1:65536/sesame", "http://%zz/sesame", "",
	} {
		err := CheckURL(raw)
		if err == nil {
			t.Errorf("CheckURL(%q) took it", raw)
			continue
		}
		if !strings.Contains(err.Error(), "loopback") || strings.Contains(err.Error(), "sesame") {
			t.Errorf("CheckURL(%q) = %q; want the loopback named and nothing past the host and port", raw, err)
		}
	}
}

// FuzzCheckURL: whatever CheckURL takes is an http or https URL whose host
// is a loopback IP literal.
func FuzzCheckURL(f *testing.F) {
	for _, seed := range []string{"http://127.0.0.1/mcp", "https://[::1]:8443/pdp", "http://u:p@127.0.0.1:3128", "http://localhost/", "http://192.0.2.1:9"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if CheckURL(raw) != nil {
			return
		}
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			t.Fatalf("CheckURL took %q, which is no http or https URL: %v", raw, err)
		}
		if ip := net.ParseIP(u.Hostname()); ip == nil || !ip.IsLoopback() {
			t.Fatalf("CheckURL took %q, whose host %q is no loopback IP literal", raw, u.Hostname())
		}
	})
}

// FuzzCheck: whatever Check takes, the resolver reads as a loopback IP and
// a port, without a name to look up.
func FuzzCheck(f *testing.F) {
	for _, seed := range []string{"127.0.0.1:0", "[::1]:4318", "localhost:0", "0.0.0.0:80", "127.0.0.1:080", "[127.0.0.1]:1"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, addr string) {
		if Check(addr) != nil {
			return
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil || net.ParseIP(host) == nil {
			t.Fatalf("Check took %q, whose host is no IP literal", addr)
		}
		resolved, err := net.ResolveTCPAddr("tcp", addr)
		if err != nil || !resolved.IP.IsLoopback() {
			t.Fatalf("Check took %q, which resolves to %v, %v", addr, resolved, err)
		}
	})
}
