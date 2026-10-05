package gatewayconfig

import (
	"encoding/base64"
	"fmt"
	"net/textproto"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/guardana/control/internal/secretscan"
)

// proxyVariables are the variables net/http's ProxyFromEnvironment reads a
// proxy's URL from, in the order it reads them.
var proxyVariables = []string{"HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy"}

// Secrets lists the values the plane sends as credentials, and the values it
// sends that may be one, in the configuration's order (ADR-0042). Each key
// names where its value is configured and never holds it. lookup reads the
// plane's environment: the variables a command upstream lists, and the proxy
// variables the HTTP client reads. An empty value is left out.
func (c *Config) Secrets(lookup func(string) (string, bool)) []secretscan.Secret {
	var out []secretscan.Secret
	out = append(out, userinfoSecrets("pdp.proxy", parseURL(c.PDP.Proxy))...)
	out = append(out, headerSecrets(PDPHeadersPrefix, c.PDP.Headers)...)
	out = append(out, endpointSecrets("export.endpoint", c.Export.Endpoint)...)
	out = append(out, headerSecrets(HeadersPrefix, c.Export.Headers)...)
	for i, up := range c.Upstreams {
		at := fmt.Sprintf("upstreams.%d.", i)
		if up.Endpoint != "" {
			out = append(out, endpointSecrets(at+"endpoint", up.Endpoint)...)
		}
		for _, name := range up.Env {
			if slices.Contains(baseEnv, name) {
				continue
			}
			if value, ok := lookup(name); ok {
				out = appendSecret(out, at+"env "+name, value, secretscan.MaybeCredential)
			}
		}
	}
	for _, name := range proxyVariables {
		if value, ok := lookup(name); ok {
			out = append(out, userinfoSecrets(name, proxyURL(value))...)
		}
	}
	return out
}

func appendSecret(out []secretscan.Secret, key, value string, kind secretscan.Kind) []secretscan.Secret {
	if value == "" {
		return out
	}
	return append(out, secretscan.Secret{Key: key, Value: value, Kind: kind})
}

// endpointSecrets is an endpoint's userinfo and every value of its query. The
// path and the query's keys name things rather than hold credentials, and the
// fragment is never sent. A query value is named by its place in the query,
// since a query key may itself be a token, and a key is printed.
func endpointSecrets(key, endpoint string) []secretscan.Secret {
	u := parseURL(endpoint)
	if u == nil {
		return nil
	}
	out := userinfoSecrets(key, u)
	for i, pair := range strings.Split(u.RawQuery, "&") {
		_, value, _ := strings.Cut(pair, "=")
		out = appendSecret(out, key+" query value "+strconv.Itoa(i+1), unescapeQuery(value), secretscan.MaybeCredential)
	}
	return out
}

// unescapeQuery decodes a query's part as the server reads it; a part that
// does not decode is sent as written, so it is kept as written.
func unescapeQuery(part string) string {
	if decoded, err := url.QueryUnescape(part); err == nil {
		return decoded
	}
	return part
}

// userinfoSecrets is the password of a URL's userinfo, or its user when the
// password is empty, and the Basic token net/http sends from both: the
// Authorization header of a request to the URL, or the Proxy-Authorization
// header of one through it.
func userinfoSecrets(key string, u *url.URL) []secretscan.Secret {
	if u == nil || u.User == nil {
		return nil
	}
	user := u.User.Username()
	password, _ := u.User.Password()
	if user == "" && password == "" {
		return nil
	}
	var out []secretscan.Secret
	if password != "" {
		out = appendSecret(out, key+" password", password, secretscan.Credential)
	} else {
		out = appendSecret(out, key+" user", user, secretscan.Credential)
	}
	basic := base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
	return appendSecret(out, key+" basic", basic, secretscan.Credential)
}

// headerSecrets is every header value under prefix, by the header's name, as
// net/http sends it: trimmed, a line break read as a space. Of an
// authorization value it also lists the parts a server may quote on their
// own: the credential after its scheme and, for Basic, the password inside.
func headerSecrets(prefix string, headers map[string]string) []secretscan.Secret {
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []secretscan.Secret
	for _, name := range names {
		key, value := prefix+name, sentValue(headers[name])
		out = appendSecret(out, key, value, secretscan.Credential)
		if isAuthorization(name) {
			out = append(out, authorizationSecrets(key, value)...)
		}
	}
	return out
}

// sentValue is a header value as net/http writes it on the wire.
func sentValue(value string) string {
	return textproto.TrimString(strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ").Replace(value))
}

func isAuthorization(name string) bool {
	return strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Proxy-Authorization")
}

// authorizationSecrets is the credential after an authorization value's
// scheme and, for Basic, the password it encodes, padded or not.
func authorizationSecrets(key, value string) []secretscan.Secret {
	gap := strings.IndexAny(value, " \t")
	if gap < 0 || !isToken(value[:gap]) {
		return nil
	}
	scheme, credential := value[:gap], strings.TrimSpace(value[gap+1:])
	out := appendSecret(nil, key+" credential", credential, secretscan.Credential)
	if !strings.EqualFold(scheme, "Basic") {
		return out
	}
	pair, err := base64.StdEncoding.DecodeString(credential)
	if err != nil {
		pair, err = base64.RawStdEncoding.DecodeString(credential)
	}
	if err == nil {
		out = append(out, basicPairSecrets(key, string(pair))...)
	}
	return out
}

// basicPairSecrets is the password of a decoded Basic user:password, or the
// user when the password is empty.
func basicPairSecrets(key, pair string) []secretscan.Secret {
	user, password, _ := strings.Cut(pair, ":")
	if password != "" {
		return appendSecret(nil, key+" password", password, secretscan.Credential)
	}
	return appendSecret(nil, key+" user", user, secretscan.Credential)
}

// isToken reports whether s is an HTTP token, which an auth scheme is.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune(nameBytes, rune(s[i])) {
			return false
		}
	}
	return true
}

func parseURL(value string) *url.URL {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil {
		return nil
	}
	return u
}

// proxyURL reads a proxy variable as net/http does: a value with no scheme or
// no host is read again as an http URL.
func proxyURL(value string) *url.URL {
	u, err := url.Parse(value)
	if err != nil || u.Scheme == "" || u.Host == "" {
		if again, againErr := url.Parse("http://" + value); againErr == nil {
			return again
		}
	}
	if err != nil {
		return nil
	}
	return u
}
