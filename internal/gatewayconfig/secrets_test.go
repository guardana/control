package gatewayconfig

import (
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/secretscan"
)

// secretsConfig holds a credential in every place the plane sends one from,
// and beside each a part that is never a credential: a path, a fragment, a
// query key, an argument and a variable every command receives anyway.
func secretsConfig() *Config {
	c := &Config{
		PDP:    PDPConfig{Headers: map[string]string{"Authorization": "Bearer pdp-tok"}},
		Export: ExportConfig{Headers: map[string]string{"X-Api-Key": "k1", "Authorization": "Basic c3ZjOnMzY3JldC1wdw=="}},
		Upstreams: []UpstreamConfig{
			{Name: "a"}, {Name: "b"}, {Name: "c"},
			{Name: "d", Command: "/bin/srv", Args: []string{"--key", "argsecret123"},
				Env: []string{"ORDERS_DSN", "UNSET_ONE", "HOME", "EMPTY_ONE"}},
			{Name: "e", Endpoint: "https://up4.example/mcp"},
		},
	}
	c.PDP.Proxy = "http://px:px%2Fpass@proxy.internal:3128"
	c.Export.Endpoint = "https://exp:ex-secret@collector.example/v1/logs?tenant=t-0123456789"
	c.Upstreams[0].Endpoint = "https://alice:s3cr%40t@up.example/mcp/path-seg?token=abc&token=def%2Bg&mode=x+y#frag-val"
	c.Upstreams[1].Endpoint = "https://b%20ob@up2.example/mcp?bad=%zz&empty="
	c.Upstreams[2].Endpoint = "https://carol:@up3.example/mcp"
	return c
}

func secretsEnv(name string) (string, bool) {
	value, ok := map[string]string{
		"ORDERS_DSN":  "dsn-orders-0001",
		"HOME":        "/home/someone",
		"EMPTY_ONE":   "",
		"HTTPS_PROXY": "hp:hp-pass@corp-proxy:8080",
		"http_proxy":  "http://lower@p.internal:1",
		"HTTP_PROXY":  "http://p.internal:1",
	}[name]
	return value, ok
}

// TestSecretsListsEveryConfiguredCredentialInOrder pins the whole list: each
// userinfo password, or the user where there is none, and the Basic token the
// HTTP client builds from it, every query value decoded, the listed variables
// a command receives, every header value, and the proxy variables' userinfo.
func TestSecretsListsEveryConfiguredCredentialInOrder(t *testing.T) {
	cred, maybe := secretscan.Credential, secretscan.MaybeCredential
	want := []secretscan.Secret{
		{Key: "pdp.proxy password", Value: "px/pass", Kind: cred},
		{Key: "pdp.proxy basic", Value: "cHg6cHgvcGFzcw==", Kind: cred},
		{Key: "pdp.headers.Authorization", Value: "Bearer pdp-tok", Kind: cred},
		{Key: "pdp.headers.Authorization credential", Value: "pdp-tok", Kind: cred},
		{Key: "export.endpoint password", Value: "ex-secret", Kind: cred},
		{Key: "export.endpoint basic", Value: "ZXhwOmV4LXNlY3JldA==", Kind: cred},
		{Key: "export.endpoint query value 1", Value: "t-0123456789", Kind: maybe},
		{Key: "export.headers.Authorization", Value: "Basic c3ZjOnMzY3JldC1wdw==", Kind: cred},
		{Key: "export.headers.Authorization credential", Value: "c3ZjOnMzY3JldC1wdw==", Kind: cred},
		{Key: "export.headers.Authorization password", Value: "s3cret-pw", Kind: cred},
		{Key: "export.headers.X-Api-Key", Value: "k1", Kind: cred},
		{Key: "upstreams.0.endpoint password", Value: "s3cr@t", Kind: cred},
		{Key: "upstreams.0.endpoint basic", Value: "YWxpY2U6czNjckB0", Kind: cred},
		{Key: "upstreams.0.endpoint query value 1", Value: "abc", Kind: maybe},
		{Key: "upstreams.0.endpoint query value 2", Value: "def+g", Kind: maybe},
		{Key: "upstreams.0.endpoint query value 3", Value: "x y", Kind: maybe},
		{Key: "upstreams.1.endpoint user", Value: "b ob", Kind: cred},
		{Key: "upstreams.1.endpoint basic", Value: "YiBvYjo=", Kind: cred},
		{Key: "upstreams.1.endpoint query value 1", Value: "%zz", Kind: maybe},
		{Key: "upstreams.2.endpoint user", Value: "carol", Kind: cred},
		{Key: "upstreams.2.endpoint basic", Value: "Y2Fyb2w6", Kind: cred},
		{Key: "upstreams.3.env ORDERS_DSN", Value: "dsn-orders-0001", Kind: maybe},
		{Key: "http_proxy user", Value: "lower", Kind: cred},
		{Key: "http_proxy basic", Value: "bG93ZXI6", Kind: cred},
		{Key: "HTTPS_PROXY password", Value: "hp-pass", Kind: cred},
		{Key: "HTTPS_PROXY basic", Value: "aHA6aHAtcGFzcw==", Kind: cred},
	}
	got := secretsConfig().Secrets(secretsEnv)
	if !slices.Equal(got, want) {
		t.Errorf("Secrets listed\n%s\nwant\n%s", listSecrets(got), listSecrets(want))
	}
}

// TestSecretsNeverListsWhatIsNotACredential: a query key, a fragment, a path,
// an argument, a variable every command receives unlisted and the bare user
// beside a password are never a value of the set, and no key holds a value.
func TestSecretsNeverListsWhatIsNotACredential(t *testing.T) {
	got := secretsConfig().Secrets(secretsEnv)
	never := []string{"token", "mode", "tenant", "empty", "frag-val", "#frag-val", "/mcp/path-seg", "path-seg",
		"argsecret123", "--key", "/home/someone", "alice", "exp", "px", "hp", "p.internal", ""}
	for _, s := range got {
		if slices.Contains(never, s.Value) {
			t.Errorf("%s lists %q, which is not a credential", s.Key, s.Value)
		}
		for _, other := range got {
			if strings.Contains(s.Key, other.Value) {
				t.Errorf("the key %q holds the value of %s", s.Key, other.Key)
			}
		}
	}
}

// TestASecretsKeyNeverQuotesAQueryKey: a query key can itself be a token,
// and a secret's key is what doctor and the log print, so a query value is
// named by its place in the query.
func TestASecretsKeyNeverQuotesAQueryKey(t *testing.T) {
	cfg := &Config{Upstreams: []UpstreamConfig{{Name: "a", Endpoint: "https://up.example/mcp?querykey7opaque=on&v=2"}}}
	got := cfg.Secrets(func(string) (string, bool) { return "", false })
	if len(got) != 2 {
		t.Fatalf("%d secret(s), want 2\n%s", len(got), listSecrets(got))
	}
	for _, s := range got {
		if strings.Contains(s.Key, "querykey7opaque") || strings.Contains(s.Key, " v") && !strings.Contains(s.Key, " value ") {
			t.Errorf("the key %q quotes a query key", s.Key)
		}
	}
	if got[0].Key != "upstreams.0.endpoint query value 1" || got[1].Key != "upstreams.0.endpoint query value 2" {
		t.Errorf("keys %q and %q, want the query values named by place", got[0].Key, got[1].Key)
	}
}

// TestAnAuthorizationValueSplitsOnATab: a header value may separate its scheme
// from its credential with a tab, which a server that splits on whitespace
// echoes as the credential alone.
func TestAnAuthorizationValueSplitsOnATab(t *testing.T) {
	cfg := &Config{PDP: PDPConfig{Headers: map[string]string{"Authorization": "Bearer\tpdp-tab-token"}}}
	got := cfg.Secrets(func(string) (string, bool) { return "", false })
	want := []secretscan.Secret{
		{Key: "pdp.headers.Authorization", Value: "Bearer\tpdp-tab-token", Kind: secretscan.Credential},
		{Key: "pdp.headers.Authorization credential", Value: "pdp-tab-token", Kind: secretscan.Credential},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got\n%swant\n%s", listSecrets(got), listSecrets(want))
	}
}

// TestOnlyAnAuthorizationValueIsSplit: the credential after a scheme is its
// own secret for Authorization and Proxy-Authorization alone; any other
// header's words are not, since a short one would be matched in every answer.
// A value is listed as net/http sends it, trimmed and with a line break as a
// space, and an unpadded Basic token is decoded too.
func TestOnlyAnAuthorizationValueIsSplit(t *testing.T) {
	cfg := &Config{
		PDP: PDPConfig{Headers: map[string]string{
			"X-Scope-Orgid":       "team a",
			"Proxy-Authorization": " Bearer proxy-token-1 ",
		}},
		Export: ExportConfig{Headers: map[string]string{"Authorization": "Basic dXNlcjpwYXNzd29yZA"}},
	}
	got := cfg.Secrets(func(string) (string, bool) { return "", false })
	cred := secretscan.Credential
	want := []secretscan.Secret{
		{Key: "pdp.headers.Proxy-Authorization", Value: "Bearer proxy-token-1", Kind: cred},
		{Key: "pdp.headers.Proxy-Authorization credential", Value: "proxy-token-1", Kind: cred},
		{Key: "pdp.headers.X-Scope-Orgid", Value: "team a", Kind: cred},
		{Key: "export.headers.Authorization", Value: "Basic dXNlcjpwYXNzd29yZA", Kind: cred},
		{Key: "export.headers.Authorization credential", Value: "dXNlcjpwYXNzd29yZA", Kind: cred},
		{Key: "export.headers.Authorization password", Value: "password", Kind: cred},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got\n%swant\n%s", listSecrets(got), listSecrets(want))
	}
}

// TestSecretsSkipsAUserinfoWithNothingInIt: an endpoint whose userinfo is
// empty sends a Basic token of a lone colon, which is no credential and would
// match far more than it should.
func TestSecretsSkipsAUserinfoWithNothingInIt(t *testing.T) {
	cfg := &Config{Upstreams: []UpstreamConfig{{Name: "a", Endpoint: "https://:@up.example/mcp"}}}
	if got := cfg.Secrets(func(string) (string, bool) { return "", false }); len(got) != 0 {
		t.Errorf("an empty userinfo listed\n%s", listSecrets(got))
	}
}

func listSecrets(secrets []secretscan.Secret) string {
	var b strings.Builder
	for _, s := range secrets {
		b.WriteString("  " + s.Key + " = " + s.Value)
		if s.Kind == secretscan.MaybeCredential {
			b.WriteString(" (maybe)")
		}
		b.WriteString("\n")
	}
	return b.String()
}
