package authzen

import (
	"crypto/x509"
	"fmt"
	"net"
	"net/textproto"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Options configure a client once, at New.
type Options struct {
	// Identifier is the decision point's identifier, the policy_decision_point
	// its metadata names: an absolute https URL with no userinfo, query or
	// fragment.
	Identifier string
	// Endpoint is the access evaluation endpoint. It has to share the
	// identifier's scheme, host and port; empty means the identifier's path
	// followed by /access/v1/evaluation.
	Endpoint string
	// AllowPlaintext lets the identifier be http, on a loopback IP literal
	// only. It is the named risk of a decision point reached over plaintext:
	// anyone on the path can forge the true that removes a veto. With an
	// https identifier it applies to nothing and is refused.
	AllowPlaintext bool
	// Headers go on every question, for the decision point's authentication.
	// A name has to be an HTTP token, not one the client sets itself, not one
	// that directs the connection, a proxy or the body's framing, and not
	// another spelling of a name already given; a value has to be free of
	// control characters other than tab.
	Headers map[string]string
	// Timeout bounds one ask; it has to be positive.
	Timeout time.Duration
	// InFlight bounds the asks waiting on the decision point at once; one
	// past it is unavailable at once. It has to be positive.
	InFlight int
	// Informational names the context members an allowing answer may carry.
	// Any other member makes an allow unreadable. None by default.
	Informational []string
	// Proxy is the http(s) URL of the proxy questions go through. Empty means
	// none, whatever the environment says. With a plaintext identifier it has
	// to be on a loopback IP literal too.
	Proxy string
	// RootCAs verifies the decision point's certificate; nil means the
	// system's roots.
	RootCAs *x509.CertPool
}

// setByClient are the headers the client or its transport write, and the
// cookie a question never carries; a configured value would contradict them.
var setByClient = []string{"Accept", "Accept-Encoding", "Content-Type", "Content-Length", "Cookie", "Host", "X-Request-Id"}

// notCarried are the headers that address the connection, a proxy, the
// body's framing or the exchange rather than the decision point. Through an
// https proxy the tunnel carries a proxy credential on to the decision point.
var notCarried = []string{
	"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Proxy-Connection",
	"TE", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Encoding", "Expect",
}

// config is what New keeps of checked options.
type config struct {
	raw        string
	identifier *url.URL
	endpoint   string
	proxy      *url.URL
	listed     map[string]bool
}

func (o Options) check() (config, error) {
	var cfg config
	id, err := o.identifier()
	if err != nil {
		return cfg, err
	}
	cfg.raw, cfg.identifier = o.Identifier, id
	if cfg.endpoint, err = o.endpoint(id); err != nil {
		return cfg, err
	}
	switch {
	case o.Timeout <= 0:
		return cfg, fmt.Errorf("%w: Timeout %v is not positive", ErrInvalidOptions, o.Timeout)
	case o.InFlight <= 0:
		return cfg, fmt.Errorf("%w: InFlight %d is not positive", ErrInvalidOptions, o.InFlight)
	}
	if err := checkHeaders(o.Headers); err != nil {
		return cfg, err
	}
	if cfg.listed, err = listed(o.Informational); err != nil {
		return cfg, err
	}
	if cfg.proxy, err = proxyURL(o.Proxy); err != nil {
		return cfg, err
	}
	// Plaintext is allowed only because it never leaves the host; a remote
	// proxy would carry the question and the answer across a network.
	if id.Scheme == "http" && cfg.proxy != nil && !isLoopback(cfg.proxy.Hostname()) {
		return cfg, fmt.Errorf("%w: a plaintext identifier's proxy has to be a loopback IP literal", ErrInvalidOptions)
	}
	return cfg, nil
}

// identifier checks the decision point's identifier. Its refusals quote
// nothing of the URL, which may carry a credential.
func (o Options) identifier() (*url.URL, error) {
	u, err := absolute(o.Identifier, "the identifier")
	if err != nil {
		return nil, err
	}
	switch {
	case u.Scheme == "https" && o.AllowPlaintext:
		return nil, fmt.Errorf("%w: AllowPlaintext is set for an https identifier, where it applies to nothing", ErrInvalidOptions)
	case u.Scheme == "https":
		return u, nil
	case !o.AllowPlaintext:
		return nil, fmt.Errorf("%w: the identifier is plaintext and AllowPlaintext is not set", ErrInvalidOptions)
	case !isLoopback(u.Hostname()):
		return nil, fmt.Errorf("%w: a plaintext identifier has to be a loopback IP literal", ErrInvalidOptions)
	}
	return u, nil
}

// isLoopback reports whether host is a loopback IP literal.
func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// endpoint checks the evaluation endpoint against the identifier's origin, or
// derives it from the identifier.
func (o Options) endpoint(id *url.URL) (string, error) {
	if o.Endpoint == "" {
		u := *id
		u.Path = strings.TrimSuffix(id.Path, "/") + evaluationPath
		u.RawPath = ""
		return u.String(), nil
	}
	u, err := absolute(o.Endpoint, "the endpoint")
	if err != nil {
		return "", err
	}
	if origin(u) != origin(id) {
		return "", fmt.Errorf("%w: the endpoint's scheme, host or port differ from the identifier's", ErrInvalidOptions)
	}
	return u.String(), nil
}

// absolute parses an http(s) URL with a host and none of userinfo, query or
// fragment.
func absolute(raw, what string) (*url.URL, error) {
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: %s does not parse as a URL", ErrInvalidOptions, what)
	case (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return nil, fmt.Errorf("%w: %s is not an absolute http(s) URL with a host", ErrInvalidOptions, what)
	case u.User != nil || u.RawQuery != "" || u.ForceQuery || strings.Contains(raw, "#"):
		return nil, fmt.Errorf("%w: %s carries userinfo, a query or a fragment", ErrInvalidOptions, what)
	}
	return u, nil
}

// origin is a URL's scheme, host and port, with the scheme's default port
// written out.
func origin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

// checkHeaders refuses a name checkHeaderName refuses, two names that fold to
// one header, and a value with a control character other than tab. The
// refusal names the header, never its value.
func checkHeaders(headers map[string]string) error {
	folded := make(map[string]string, len(headers))
	for name, value := range headers {
		if err := checkHeaderName(name); err != nil {
			return err
		}
		key := textproto.CanonicalMIMEHeaderKey(name)
		if other, ok := folded[key]; ok {
			return fmt.Errorf("%w: headers %q and %q are one header", ErrInvalidOptions, other, name)
		}
		folded[key] = name
		if strings.IndexFunc(value, isControl) >= 0 {
			return fmt.Errorf("%w: the value of header %q holds a control character", ErrInvalidOptions, name)
		}
	}
	return nil
}

// checkHeaderName refuses a name that is not an HTTP token, one the client
// sets itself, and one a question does not carry to the decision point.
func checkHeaderName(name string) error {
	is := func(listed string) bool { return strings.EqualFold(name, listed) }
	switch {
	case !isToken(name):
		return fmt.Errorf("%w: a header name is not an HTTP token", ErrInvalidOptions)
	case slices.ContainsFunc(setByClient, is):
		return fmt.Errorf("%w: header %q is set by the client", ErrInvalidOptions, name)
	case slices.ContainsFunc(notCarried, is):
		return fmt.Errorf("%w: header %q is not one a question may carry", ErrInvalidOptions, name)
	}
	return nil
}

// isToken reports whether s is an HTTP token (RFC 9110, section 5.6.2).
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alnum && !strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c)) {
			return false
		}
	}
	return true
}

// isControl reports whether r is a control character a header value may not
// hold: every one but tab.
func isControl(r rune) bool {
	return (r < ' ' && r != '\t') || r == 0x7f
}

// listed checks the informational names. Obligations are never
// informational, under any spelling a reader could take for them: a
// non-empty list is a denial whatever is listed.
func listed(names []string) (map[string]bool, error) {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		switch {
		case name == "":
			return nil, fmt.Errorf("%w: an informational name is empty", ErrInvalidOptions)
		case strings.EqualFold(strings.TrimSpace(name), "obligations"):
			return nil, fmt.Errorf("%w: obligations cannot be informational", ErrInvalidOptions)
		case out[name]:
			return nil, fmt.Errorf("%w: informational name %q is listed twice", ErrInvalidOptions, name)
		}
		out[name] = true
	}
	return out, nil
}

// proxyURL checks a configured proxy; empty is none. Its refusals quote
// nothing of the URL, whose userinfo is the proxy's credential.
func proxyURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	switch {
	case err != nil:
		return nil, fmt.Errorf("%w: the proxy does not parse as a URL", ErrInvalidOptions)
	case (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return nil, fmt.Errorf("%w: the proxy is not an http(s) URL with a host", ErrInvalidOptions)
	}
	return u, nil
}
