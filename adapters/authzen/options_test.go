package authzen

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// TestNewRefuses: every option that could send the question somewhere
// unsafe, or leave an ask unbounded, is refused before any request, and the
// refusal quotes no credential.
func TestNewRefuses(t *testing.T) {
	const hidden = "hv-7731"
	good := func() Options {
		return Options{Identifier: "https://pdp.example", Timeout: time.Second, InFlight: 1}
	}
	cases := map[string]func(*Options){
		"no identifier":                     func(o *Options) { o.Identifier = "" },
		"a relative identifier":             func(o *Options) { o.Identifier = "pdp.example" },
		"an identifier not http":            func(o *Options) { o.Identifier = "ftp://pdp.example" },
		"an identifier with no host":        func(o *Options) { o.Identifier = "https:///tenant" },
		"an identifier with userinfo":       func(o *Options) { o.Identifier = "https://u:" + hidden + "@pdp.example" },
		"an identifier with a query":        func(o *Options) { o.Identifier = "https://pdp.example/?k=" + hidden },
		"an identifier with an empty query": func(o *Options) { o.Identifier = "https://pdp.example/?" },
		"an identifier with a fragment":     func(o *Options) { o.Identifier = "https://pdp.example/#" + hidden },
		"an identifier that does not parse": func(o *Options) { o.Identifier = "https://pdp.example:port" },
		"plaintext without the setting":     func(o *Options) { o.Identifier = "http://127.0.0.1:8080" },
		"plaintext to a remote host":        func(o *Options) { o.Identifier, o.AllowPlaintext = "http://pdp.example", true },
		"plaintext to a name":               func(o *Options) { o.Identifier, o.AllowPlaintext = "http://localhost:8080", true },
		"plaintext to a private address":    func(o *Options) { o.Identifier, o.AllowPlaintext = "http://10.0.0.1:8080", true },
		"plaintext to the unspecified":      func(o *Options) { o.Identifier, o.AllowPlaintext = "http://0.0.0.0:8080", true },
		"the plaintext setting on https":    func(o *Options) { o.AllowPlaintext = true },
		"an endpoint on another scheme": func(o *Options) {
			o.Identifier, o.Endpoint, o.AllowPlaintext = "http://127.0.0.1", "https://127.0.0.1/e", true
		},
		"an endpoint on another host":  func(o *Options) { o.Endpoint = "https://evil.example/access/v1/evaluation" },
		"an endpoint on another port":  func(o *Options) { o.Endpoint = "https://pdp.example:8443/access/v1/evaluation" },
		"an endpoint with userinfo":    func(o *Options) { o.Endpoint = "https://u:" + hidden + "@pdp.example/e" },
		"an endpoint with a query":     func(o *Options) { o.Endpoint = "https://pdp.example/e?k=" + hidden },
		"a relative endpoint":          func(o *Options) { o.Endpoint = "/access/v1/evaluation" },
		"a header name with a space":   func(o *Options) { o.Headers = map[string]string{"X Key": hidden} },
		"an empty header name":         func(o *Options) { o.Headers = map[string]string{"": hidden} },
		"a CR in a header value":       func(o *Options) { o.Headers = map[string]string{"X-Key": hidden + "\r"} },
		"an LF in a header value":      func(o *Options) { o.Headers = map[string]string{"X-Key": hidden + "\nX-B: 1"} },
		"a NUL in a header value":      func(o *Options) { o.Headers = map[string]string{"X-Key": hidden + "\x00"} },
		"an SOH in a header value":     func(o *Options) { o.Headers = map[string]string{"X-Key": hidden + "\x01"} },
		"an ESC in a header value":     func(o *Options) { o.Headers = map[string]string{"X-Key": "\x1b" + hidden} },
		"a US in a header value":       func(o *Options) { o.Headers = map[string]string{"X-Key": hidden + "\x1f"} },
		"a DEL in a header value":      func(o *Options) { o.Headers = map[string]string{"X-Key": hidden + "\x7f"} },
		"the accept-encoding header":   func(o *Options) { o.Headers = map[string]string{"accept-encoding": "identity"} },
		"the request id as a header":   func(o *Options) { o.Headers = map[string]string{"x-request-id": hidden} },
		"the content type as a header": func(o *Options) { o.Headers = map[string]string{"Content-Type": hidden} },
		"the accept header":            func(o *Options) { o.Headers = map[string]string{"ACCEPT": hidden} },
		"the host header":              func(o *Options) { o.Headers = map[string]string{"Host": hidden} },
		"a cookie header":              func(o *Options) { o.Headers = map[string]string{"Cookie": hidden} },
		"one header under two spellings": func(o *Options) {
			o.Headers = map[string]string{"X-Key": hidden, "x-KEY": hidden}
		},
		"a zero timeout":                  func(o *Options) { o.Timeout = 0 },
		"a negative timeout":              func(o *Options) { o.Timeout = -time.Second },
		"a zero in-flight bound":          func(o *Options) { o.InFlight = 0 },
		"a negative in-flight bound":      func(o *Options) { o.InFlight = -1 },
		"an empty informational name":     func(o *Options) { o.Informational = []string{""} },
		"a duplicated informational name": func(o *Options) { o.Informational = []string{"reason_user", "reason_user"} },
		"obligations as informational":    func(o *Options) { o.Informational = []string{"obligations"} },
		"obligations capitalised":         func(o *Options) { o.Informational = []string{"reason_user", "Obligations"} },
		"obligations in capitals":         func(o *Options) { o.Informational = []string{"OBLIGATIONS"} },
		"obligations with spaces around":  func(o *Options) { o.Informational = []string{" obligations\t"} },
		"a proxy that is not http":        func(o *Options) { o.Proxy = "socks5://proxy.example:1080" },
		"a proxy with no host":            func(o *Options) { o.Proxy = "http://" },
		"a proxy that does not parse":     func(o *Options) { o.Proxy = "http://u:" + hidden + "@proxy.example:port" },
		"plaintext through a remote proxy": func(o *Options) {
			o.Identifier, o.AllowPlaintext, o.Proxy = "http://127.0.0.1:8080", true, "http://u:"+hidden+"@proxy.example:3128"
		},
		"plaintext through a private proxy": func(o *Options) {
			o.Identifier, o.AllowPlaintext, o.Proxy = "http://127.0.0.1:8080", true, "https://10.0.0.1:3128"
		},
		"plaintext through a named proxy": func(o *Options) {
			o.Identifier, o.AllowPlaintext, o.Proxy = "http://[::1]:8080", true, "http://localhost:3128"
		},
	}
	for _, header := range []string{
		"connection", "Keep-Alive", "PROXY-AUTHENTICATE", "Proxy-Authorization", "proxy-connection", "te",
		"Trailer", "transfer-encoding", "Upgrade", "Content-encoding", "EXPECT",
	} {
		cases["the "+header+" header"] = func(o *Options) { o.Headers = map[string]string{"X-Key": "v", header: hidden} }
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			opts := good()
			mutate(&opts)
			c, err := New(opts)
			if c != nil || !errors.Is(err, ErrInvalidOptions) {
				t.Fatalf("New returned a client: %t, %v; want none, ErrInvalidOptions", c != nil, err)
			}
			if strings.Contains(err.Error(), hidden) {
				t.Errorf("the refusal quotes a secret: %v", err)
			}
		})
	}
}

// TestNewAccepts: the boundary cases on the accepting side of each refusal.
func TestNewAccepts(t *testing.T) {
	cases := map[string]Options{
		"https with a path":           {Identifier: "https://pdp.example/tenant-a"},
		"https with a trailing slash": {Identifier: "https://pdp.example/"},
		"the default port written out": {
			Identifier: "https://PDP.example", Endpoint: "https://pdp.example:443/access/v1/evaluation",
		},
		"an endpoint on another path": {Identifier: "https://pdp.example:8443/a", Endpoint: "https://pdp.example:8443/b/eval"},
		"plaintext on 127.0.0.1":      {Identifier: "http://127.0.0.1:8080", AllowPlaintext: true},
		"plaintext on 127.1.2.3":      {Identifier: "http://127.1.2.3:8080", AllowPlaintext: true},
		"plaintext on ::1":            {Identifier: "http://[::1]:8080", AllowPlaintext: true},
		"headers":                     {Identifier: "https://pdp.example", Headers: map[string]string{"Authorization": "Bearer x", "X-Key": ""}},
		"names near the refused ones": {Identifier: "https://pdp.example", Headers: map[string]string{
			"X-Proxy-Authorization": "a", "Te-Tenant": "b", "Expected": "c", "Connection-Id": "d", "X-Key": "e", "X-Key2": "f",
		}},
		"a tab in a header value":     {Identifier: "https://pdp.example", Headers: map[string]string{"X-Key": "a\tb"}},
		"non-ASCII in a header value": {Identifier: "https://pdp.example", Headers: map[string]string{"X-Key": "caf\xc3\xa9 ~"}},
		"informational names":         {Identifier: "https://pdp.example", Informational: []string{"reason_user", "reason_admin"}},
		"names near obligations":      {Identifier: "https://pdp.example", Informational: []string{"obligation", "obligations_note"}},
		"a proxy":                     {Identifier: "https://pdp.example", Proxy: "http://proxy.example:3128"},
		"plaintext through a loopback proxy": {
			Identifier: "http://127.0.0.1:8080", AllowPlaintext: true, Proxy: "http://127.0.0.1:3128",
		},
		"plaintext through an https loopback proxy": {
			Identifier: "http://[::1]:8080", AllowPlaintext: true, Proxy: "https://[::1]:3128",
		},
	}
	for name, opts := range cases {
		t.Run(name, func(t *testing.T) {
			opts.Timeout, opts.InFlight = time.Nanosecond, 1
			if _, err := New(opts); err != nil {
				t.Errorf("New: %v", err)
			}
		})
	}
}
