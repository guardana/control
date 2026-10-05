package secretscan_test

import (
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/guardana/control/internal/secretscan"
)

func TestNewRefusesASecretWithoutKindOrKey(t *testing.T) {
	const value = "do-not-print-me"
	for _, tc := range []struct {
		name    string
		secret  secretscan.Secret
		wantKey string
	}{
		{"unspecified kind", secretscan.Secret{Key: "pdp.headers.x-kind", Value: value}, "pdp.headers.x-kind"},
		{"kind past the last", secretscan.Secret{Key: "pdp.headers.x-high", Value: value, Kind: secretscan.MaybeCredential + 1}, "pdp.headers.x-high"},
		{"negative kind", secretscan.Secret{Key: "pdp.headers.x-low", Value: value, Kind: -1}, "pdp.headers.x-low"},
		{"unspecified kind, empty value", secretscan.Secret{Key: "pdp.headers.x-empty"}, "pdp.headers.x-empty"},
		{"empty key", secretscan.Secret{Value: value, Kind: secretscan.Credential}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok := secretscan.Secret{Key: "a.valid.one", Value: "fine-value-1", Kind: secretscan.Credential}
			set, err := secretscan.New([]secretscan.Secret{ok, tc.secret})
			if !errors.Is(err, secretscan.ErrSecret) {
				t.Fatalf("err %v, want ErrSecret", err)
			}
			if set != nil {
				t.Fatal("New returned a set beside its error")
			}
			if strings.Contains(err.Error(), value) {
				t.Fatalf("error %q carries the value", err)
			}
			if tc.wantKey != "" && !strings.Contains(err.Error(), tc.wantKey) {
				t.Fatalf("error %q does not name key %q", err, tc.wantKey)
			}
		})
	}
}

func TestNewAcceptsBothKinds(t *testing.T) {
	for _, k := range []secretscan.Kind{secretscan.Credential, secretscan.MaybeCredential} {
		if _, err := secretscan.New([]secretscan.Secret{{Key: "k", Value: "abcdefgh", Kind: k}}); err != nil {
			t.Fatalf("kind %d: %v", k, err)
		}
	}
}

func TestShortCredentialIsMatchedRaw(t *testing.T) {
	set, err := secretscan.New([]secretscan.Secret{{Key: "files.endpoint.userinfo", Value: "q7x", Kind: secretscan.Credential}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	checkFound(t, set.ScanJSON([]byte(`{"m":"user q7x denied"}`)), "files.endpoint.userinfo", "raw")
	if got := set.NotScanned(); got != nil {
		t.Fatalf("NotScanned %q, want none", got)
	}
}

func TestMaybeCredentialFloor(t *testing.T) {
	set, err := secretscan.New([]secretscan.Secret{
		{Key: "env.SEVEN", Value: "abcdefg", Kind: secretscan.MaybeCredential},
		{Key: "env.EMPTY", Value: "", Kind: secretscan.MaybeCredential},
		{Key: "env.EIGHT", Value: "ijklmnop", Kind: secretscan.MaybeCredential},
		{Key: "env.ONE", Value: "z", Kind: secretscan.MaybeCredential},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := set.NotScanned(), []string{"env.SEVEN", "env.ONE"}; !slices.Equal(got, want) {
		t.Fatalf("NotScanned %q, want %q", got, want)
	}
	if got := set.ScanText("the value abcdefg went by"); got.State != secretscan.Clean {
		t.Fatalf("seven bytes: verdict %+v, want clean", got)
	}
	checkFoundKey(t, set.ScanText("the value ijklmnop went by"), "env.EIGHT")
}

func TestNotScannedIsACopy(t *testing.T) {
	set, err := secretscan.New([]secretscan.Secret{{Key: "env.SHORT", Value: "ab", Kind: secretscan.MaybeCredential}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first := set.NotScanned()
	if len(first) != 1 {
		t.Fatalf("NotScanned %q, want one key", first)
	}
	first[0] = "changed"
	if got := set.NotScanned(); !slices.Equal(got, []string{"env.SHORT"}) {
		t.Fatalf("NotScanned %q after a caller wrote to it", got)
	}
}

func TestShortBase64CoreIsNotSearched(t *testing.T) {
	// Six bytes give an eight-character core at alignment zero and a
	// seven-character one at alignment one.
	const value = "abcdef"
	set, err := secretscan.New([]secretscan.Secret{{Key: "files.endpoint.userinfo", Value: value, Kind: secretscan.Credential}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	aligned := base64.StdEncoding.EncodeToString([]byte(value + "!!!"))
	if aligned[:8] != "YWJjZGVm" {
		t.Fatalf("fixture: %q", aligned)
	}
	checkFoundKey(t, set.ScanText("blob "+aligned), "files.endpoint.userinfo")

	shifted := base64.StdEncoding.EncodeToString([]byte("Z" + value + "!!"))
	if strings.Contains(shifted, value) {
		t.Fatalf("fixture %q holds the raw value", shifted)
	}
	if got := set.ScanJSON([]byte(`{"b":"` + shifted + `"}`)); got.State != secretscan.Clean {
		t.Fatalf("seven-character core: verdict %+v, want clean", got)
	}

	short, err := secretscan.New([]secretscan.Secret{{Key: "k", Value: "abc", Kind: secretscan.Credential}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := short.ScanJSON([]byte(`{"b":"YWJj","c":"YWJjISEh"}`)); got.State != secretscan.Clean {
		t.Fatalf("four-character core: verdict %+v, want clean", got)
	}
}

func TestUnparsableDocumentIsUnscanned(t *testing.T) {
	set := fixtureSet(t)
	for _, doc := range []string{
		``, ` `, `{`, `{"a":1`, `{"a" 1}`, `[1,]`, `{"a":1,}`, `nul`, `'x'`,
		`{} {}`, `{}x`, `"opensesame-42"]`, `[1] 2`, `{"a":"opensesame-42"} trailing`,
		"\xef\xbb\xbf{}",
	} {
		t.Run(doc, func(t *testing.T) {
			got := set.ScanJSON([]byte(doc))
			if got != (secretscan.Verdict{}) {
				t.Fatalf("verdict %+v, want unscanned", got)
			}
			if !got.Withhold() {
				t.Fatal("an unscanned verdict does not withhold")
			}
		})
	}
}

func TestNilSetScansNothing(t *testing.T) {
	var set *secretscan.Set
	if got := set.ScanJSON([]byte(`{}`)); got.State != secretscan.Unscanned {
		t.Fatalf("ScanJSON verdict %+v, want unscanned", got)
	}
	if got := set.ScanText("x"); got.State != secretscan.Unscanned {
		t.Fatalf("ScanText verdict %+v, want unscanned", got)
	}
	if got := set.NotScanned(); got != nil {
		t.Fatalf("NotScanned %q, want nil", got)
	}
}

func TestEmptySetIsClean(t *testing.T) {
	set, err := secretscan.New(nil)
	if err != nil {
		t.Fatalf("New(nil): %v", err)
	}
	if got := set.ScanJSON([]byte(`{"a":["b",1]}`)); got.State != secretscan.Clean {
		t.Fatalf("ScanJSON verdict %+v, want clean", got)
	}
	if got := set.ScanText("anything"); got.State != secretscan.Clean {
		t.Fatalf("ScanText verdict %+v, want clean", got)
	}
	if got := set.ScanJSON([]byte(`{`)); got.State != secretscan.Unscanned {
		t.Fatalf("an empty set scanned bad JSON as %+v", got)
	}
}

func TestMalformedPercentEscapesStayAsWritten(t *testing.T) {
	set, err := secretscan.New([]secretscan.Secret{{Key: "k", Value: "50%off+tax", Kind: secretscan.Credential}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	checkFound(t, set.ScanText("price 50%off+tax"), "k", "raw")
	checkFound(t, set.ScanText("price 50%25off%2Btax"), "k", "raw after percent-decoding")
	if got := set.ScanText("price 50%off tax"); got.State != secretscan.Clean {
		t.Fatalf("verdict %+v, want clean", got)
	}
}
