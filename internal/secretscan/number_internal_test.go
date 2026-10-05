package secretscan

import (
	"math/big"
	"math/rand/v2"
	"strings"
	"testing"
)

func randomDigits(r *rand.Rand, maxLen int) string {
	b := make([]byte, 1+r.IntN(maxLen))
	for i := range b {
		b[i] = "0000123456789"[r.IntN(13)]
	}
	return string(b)
}

// randomNumber is a valid JSON number whose integer, when it has one, falls
// on both sides of maxIntegerDigits.
func randomNumber(r *rand.Rand) string {
	var b strings.Builder
	if r.IntN(3) == 0 {
		b.WriteByte('-')
	}
	whole := strings.TrimLeft(randomDigits(r, 6), "0")
	if whole == "" {
		whole = "0"
	}
	b.WriteString(whole)
	if r.IntN(2) == 0 {
		b.WriteString("." + randomDigits(r, 12))
	}
	if r.IntN(4) != 0 {
		b.WriteString([]string{"e", "E", "e+", "E-", "e-", "e00"}[r.IntN(6)])
		b.WriteString(big.NewInt(int64(r.IntN(420))).String())
	}
	return b.String()
}

// bigInteger is the integer a number denotes, read by math/big.
func bigInteger(t *testing.T, text string) (string, bool) {
	t.Helper()
	if !strings.ContainsAny(text, ".eE") {
		return "", false
	}
	var x big.Rat
	if _, ok := x.SetString(text); !ok {
		t.Fatalf("math/big refuses %q", text)
	}
	if !x.IsInt() {
		return "", false
	}
	s := x.Num().String()
	if len(strings.TrimPrefix(s, "-")) > maxIntegerDigits {
		return "", false
	}
	return s, true
}

func TestIntegerFormAgreesWithMathBig(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 8)) //nolint:gosec // G404: a fixed seed keeps the property reproducible
	integers, tooLong := 0, 0
	for range 30000 {
		text := randomNumber(r)
		got, ok := integerForm(text)
		want, wantOK := bigInteger(t, text)
		if got != want || ok != wantOK {
			t.Fatalf("%q: got %q %v, math/big %q %v", text, got, ok, want, wantOK)
		}
		if ok {
			integers++
			if len(strings.TrimPrefix(got, "-")) > 390 {
				tooLong++
			}
		}
	}
	if integers < 5000 || tooLong < 50 {
		t.Fatalf("%d integers, %d of more than 390 digits; the property examined too little", integers, tooLong)
	}
}
