package secretscan_test

import (
	"strings"
	"testing"

	"github.com/guardana/control/internal/secretscan"
)

const integerSpelling = "raw as an integer"

func numberSet(t *testing.T, values ...string) *secretscan.Set {
	t.Helper()
	secrets := make([]secretscan.Secret, 0, len(values))
	for _, v := range values {
		secrets = append(secrets, secretscan.Secret{Key: "env." + v, Value: v, Kind: secretscan.Credential})
	}
	set, err := secretscan.New(secrets)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return set
}

func TestANumberIsReadAsTheIntegerItDenotes(t *testing.T) {
	set := numberSet(t, "12345678", "-87654321")
	for _, tc := range []struct{ doc, key, spelling string }{
		{`{"n":1.2345678e7}`, "env.12345678", integerSpelling},
		{`{"data":{"pin":1234567.8e1}}`, "env.12345678", integerSpelling},
		{`[1.2345678E+7]`, "env.12345678", integerSpelling},
		{`[1234.5678e4]`, "env.12345678", integerSpelling},
		{`[0.000001234567800e13]`, "env.12345678", "raw"},
		{`[12345678.0]`, "env.12345678", "raw"},
		{`[-8.7654321e7]`, "env.-87654321", integerSpelling},
		{`[-876.54321e5]`, "env.-87654321", integerSpelling},
		{`[-87654321.000]`, "env.-87654321", "raw"},
	} {
		t.Run(tc.doc, func(t *testing.T) {
			checkFound(t, set.ScanJSON([]byte(tc.doc)), tc.key, tc.spelling)
		})
	}
}

func TestANumberThatIsNoIntegerOrAnotherIsClean(t *testing.T) {
	set := numberSet(t, "12345678", "-87654321")
	for _, doc := range []string{
		`[1.23456789e7]`,
		`[1.2345678e6]`,
		`[1.2345679e7]`,
		`[8.7654321e7]`,
		`[12345.678e2, 1234567.89e1]`,
		`["1.2345678e7"]`,
		`[0e99999999999999999999999]`,
	} {
		t.Run(doc, func(t *testing.T) {
			if got := set.ScanJSON([]byte(doc)); got.State != secretscan.Clean {
				t.Fatalf("verdict %+v, want clean", got)
			}
		})
	}
	if got := set.ScanText("1.2345678e7"); got.State != secretscan.Clean {
		t.Fatalf("ScanText read a text as a number: %+v", got)
	}
}

// TestANumbersIntegerFormIsBounded: the integer is built only when it has at
// most 400 digits, however long the number text that denotes it.
func TestANumbersIntegerFormIsBounded(t *testing.T) {
	set := numberSet(t, "1000000000", "12345678")
	checkFound(t, set.ScanJSON([]byte(`[1e399]`)), "env.1000000000", integerSpelling)
	checkFound(t, set.ScanJSON([]byte(`[0.1e400]`)), "env.1000000000", integerSpelling)
	for _, doc := range []string{
		`[1e400]`, `[0.1e401]`, `[1e100000]`, `[1e999999999]`,
		`[1e99999999999999999999999]`, `[1e18446744073709551625]`,
		"[1e" + strings.Repeat("9", 5000) + "]",
		"[1e-" + strings.Repeat("9", 5000) + "]",
		"[0.1e" + strings.Repeat("0", 5000) + "1]",
	} {
		if got := set.ScanJSON([]byte(doc)); got.State != secretscan.Clean {
			t.Fatalf("%.20s: verdict %+v, want clean", doc, got)
		}
	}

	long := func(n int) string {
		text := "1.2345678" + strings.Repeat("0", n-len("1.2345678e7")) + "e7"
		if len(text) != n {
			t.Fatalf("fixture is %d bytes, want %d", len(text), n)
		}
		return "[" + text + "]"
	}
	for _, n := range []int{400, 401, 100000} {
		checkFound(t, set.ScanJSON([]byte(long(n))), "env.12345678", integerSpelling)
	}
	checkFound(t, set.ScanJSON([]byte("[1e"+strings.Repeat("0", 5000)+"9]")), "env.1000000000", integerSpelling)
	tiny := "[0." + strings.Repeat("0", 9999) + "1e10009]"
	checkFound(t, set.ScanJSON([]byte(tiny)), "env.1000000000", integerSpelling)
}

func TestANumbersTextWinsOverItsIntegerForm(t *testing.T) {
	set := numberSet(t, "12345678", "5e3")
	checkFound(t, set.ScanJSON([]byte(`[12345.5e3]`)), "env.5e3", "raw")
}
