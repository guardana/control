package canon_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
)

// numbersFixture is the table in numbers.json: each literal with the text the
// canonical form writes for it, and the literals it refuses.
type numbersFixture struct {
	Accepted []struct {
		Literal   string `json:"literal"`
		Canonical string `json:"canonical"`
	} `json:"accepted"`
	Refused []string `json:"refused"`
}

func loadNumbers(t *testing.T) numbersFixture {
	t.Helper()
	raw, err := fs.ReadFile(os.DirFS(fixtureDir), "numbers.json")
	if err != nil {
		t.Fatalf("read numbers.json: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f numbersFixture
	if err := dec.Decode(&f); err != nil {
		t.Fatalf("decode numbers.json: %v", err)
	}
	if len(f.Accepted) == 0 || len(f.Refused) == 0 {
		t.Fatal("numbers.json holds no accepted or no refused literal")
	}
	return f
}

// TestNumbersGolden runs the table through every entry point that reads a
// number: a document, a json.Number handed to Canonicalize, and the arguments
// of a digest. The canonical texts were checked outside Go, by an ECMAScript
// engine's Number::toString and by exact decimal comparison.
func TestNumbersGolden(t *testing.T) {
	f := loadNumbers(t)
	env := &controlv1.ActionEnvelope{}
	for _, tc := range f.Accepted {
		got, err := canon.CanonicalizeJSON([]byte(tc.Literal))
		if err != nil || string(got) != tc.Canonical {
			t.Errorf("CanonicalizeJSON(%.40s) = %s, %v; want %s", tc.Literal, got, err, tc.Canonical)
		}
		got, err = canon.Canonicalize(json.Number(tc.Literal))
		if err != nil || string(got) != tc.Canonical {
			t.Errorf("Canonicalize(json.Number(%.40s)) = %s, %v; want %s", tc.Literal, got, err, tc.Canonical)
		}
		spelled, err := canon.DigestV1(env, []byte(`{"n":`+tc.Literal+`}`))
		if err != nil {
			t.Errorf("DigestV1 refused %.40s: %v", tc.Literal, err)
			continue
		}
		if plain, _ := canon.DigestV1(env, []byte(`{"n":`+tc.Canonical+`}`)); plain != spelled {
			t.Errorf("%.40s and %s give two digests", tc.Literal, tc.Canonical)
		}
	}
	for _, literal := range f.Refused {
		for entry, err := range map[string]error{
			"CanonicalizeJSON": second(canon.CanonicalizeJSON([]byte(literal))),
			"Canonicalize":     second(canon.Canonicalize(json.Number(literal))),
			"DigestV1":         second(canon.DigestV1(env, []byte(`{"n":`+literal+`}`))),
		} {
			if !errors.Is(err, canon.ErrUnsupportedValue) {
				t.Errorf("%s(%.40s): err = %v, want ErrUnsupportedValue", entry, literal, err)
				continue
			}
			if strings.Contains(err.Error(), literal) {
				t.Errorf("%s: the refusal %q repeats the literal", entry, err)
			}
		}
	}
}

func second[T any](_ T, err error) error { return err }

// TestNumberSpellingsAgree: every spelling of one value is one canonical text,
// so two producers that write the same number differently compute one digest.
func TestNumberSpellingsAgree(t *testing.T) {
	groups := map[string][]string{
		"1":         {"1", "1.0", "1e0", "1E0", "10e-1", "0.1e1", "1.000e+0"},
		"0.1":       {"0.1", "0.10", "1e-1", "0.01e1"},
		"0":         {"0", "-0", "0.0", "-0.0", "0e5", "0e999999999999", "0E-7"},
		"0.000001":  {"1e-6", "0.000001", "10e-7"},
		"1e-7":      {"1e-7", "0.0000001", "10e-8"},
		"1.5e-7":    {"1.5e-7", "0.00000015", "15e-8"},
		"-0.0015":   {"-1.5e-3", "-0.0015", "-15E-4"},
		"123.45":    {"123.45", "1.2345e2", "12345e-2"},
		"1000":      {"1e3", "1E+3", "1000.0", "1e0000000000000000000003"},
		"0.3":       {"0.3", "3e-1"},
		"100000000": {"1e8", "100000000"},
	}
	for want, spellings := range groups {
		for _, literal := range spellings {
			got, err := canon.CanonicalizeJSON([]byte(literal))
			if err != nil || string(got) != want {
				t.Errorf("CanonicalizeJSON(%s) = %s, %v; want %s", literal, got, err, want)
			}
		}
	}
}

// TestLongLiterals: the exactness check reads every digit. A 400-digit literal
// whose value is a double is accepted, one whose last digit moves it off that
// double is refused, and neither refusal nor panic depends on the length.
func TestLongLiterals(t *testing.T) {
	exact := "0.5" + strings.Repeat("0", 397)
	inexact := "0.5" + strings.Repeat("0", 396) + "1"
	if got, err := canon.CanonicalizeJSON([]byte(exact)); err != nil || string(got) != "0.5" {
		t.Errorf("a 400-digit exact literal = %s, %v; want 0.5", got, err)
	}
	if _, err := canon.CanonicalizeJSON([]byte(inexact)); !errors.Is(err, canon.ErrUnsupportedValue) {
		t.Errorf("a 400-digit inexact literal: err = %v, want ErrUnsupportedValue", err)
	}
	long := "0." + strings.Repeat("1", 60000)
	_, err := canon.CanonicalizeJSON([]byte(`{"n":` + long + `}`))
	if !errors.Is(err, canon.ErrUnsupportedValue) || len(err.Error()) > 200 || strings.Contains(err.Error(), "1111") {
		t.Errorf("a 60000-digit literal: err = %v, want a short ErrUnsupportedValue without the digits", err)
	}
	for _, literal := range []string{"1e-99999999999999999999", "1e99999999999999999999", "-1e-99999999999999999999"} {
		if _, err := canon.CanonicalizeJSON([]byte(literal)); !errors.Is(err, canon.ErrUnsupportedValue) {
			t.Errorf("%s: err = %v, want ErrUnsupportedValue", literal, err)
		}
	}
}

// TestLongExponentsAreReadAtThePoint: a long exponent offset by a long
// fraction is read at the decimal point's position, not from either part
// alone, in both forms.
func TestLongExponentsAreReadAtThePoint(t *testing.T) {
	huge := "0." + strings.Repeat("0", 9995) + "1e+100000"
	seven := "0." + strings.Repeat("0", 100000) + "7e100001"
	for name, canonicalize := range map[string]func([]byte) ([]byte, error){
		"CanonicalizeJSON": canon.CanonicalizeJSON, "FingerprintJSON": canon.FingerprintJSON,
	} {
		if got, err := canonicalize([]byte(huge)); !errors.Is(err, canon.ErrUnsupportedValue) || !strings.Contains(err.Error(), "outside the range of a double") {
			t.Errorf("%s(1e90004 spelled long) = %s, %v; want it outside the range of a double", name, got, err)
		}
		if got, err := canonicalize([]byte(seven)); err != nil || string(got) != "7" {
			t.Errorf("%s(7 spelled long) = %s, %v; want 7", name, got, err)
		}
	}
}

// TestNumberRefusalsNameTheReason: an inexact literal and one outside the
// safe range have different answers for whoever reads the refusal.
func TestNumberRefusalsNameTheReason(t *testing.T) {
	for literal, reason := range map[string]string{
		"0.30000000000000001": "not exactly the shortest decimal of a double",
		"4.9e-324":            "not exactly the shortest decimal of a double",
		"1e-400":              "outside the range of a double",
		"-1e-99999999999":     "outside the range of a double",
		"9007199254740992.0":  "outside the JSON-safe range",
		"1e21":                "outside the JSON-safe range",
		"1e400":               "outside the range of a double",
		"-1e99999999999":      "outside the range of a double",
		"9007199254740992":    "outside the JSON-safe range",
	} {
		_, err := canon.CanonicalizeJSON([]byte(`{"n":` + literal + `}`))
		if err == nil || !strings.Contains(err.Error(), reason) || !strings.Contains(err.Error(), `at "/n"`) {
			t.Errorf("%s: err = %v, want %q at /n", literal, err, reason)
		}
	}
}

// rfc8785Vectors are Appendix B of RFC 8785: IEEE-754 bits and the text
// ECMAScript's Number::toString writes for them.
var rfc8785Vectors = []struct {
	bits uint64
	want string
}{
	{0x0000000000000000, "0"},
	{0x8000000000000000, "0"},
	{0x0000000000000001, "5e-324"},
	{0x8000000000000001, "-5e-324"},
	{0x7fefffffffffffff, "1.7976931348623157e+308"},
	{0xffefffffffffffff, "-1.7976931348623157e+308"},
	{0x4340000000000000, "9007199254740992"},
	{0xc340000000000000, "-9007199254740992"},
	{0x4430000000000000, "295147905179352830000"},
	{0x44b52d02c7e14af5, "9.999999999999997e+22"},
	{0x44b52d02c7e14af6, "1e+23"},
	{0x44b52d02c7e14af7, "1.0000000000000001e+23"},
	{0x444b1ae4d6e2ef4e, "999999999999999700000"},
	{0x444b1ae4d6e2ef4f, "999999999999999900000"},
	{0x444b1ae4d6e2ef50, "1e+21"},
	{0x3eb0c6f7a0b5ed8c, "9.999999999999997e-7"},
	{0x3eb0c6f7a0b5ed8d, "0.000001"},
	{0x41b3de4355555553, "333333333.3333332"},
	{0x41b3de4355555554, "333333333.33333325"},
	{0x41b3de4355555555, "333333333.3333333"},
	{0x41b3de4355555556, "333333333.3333334"},
	{0x41b3de4355555557, "333333333.33333343"},
	{0xbecbf647612f3696, "-0.0000033333333333333333"},
	{0x43143ff3c1cb0959, "1424953923781206.2"},
}

// TestRFC8785Vectors writes each vector's double as Go spells it and reads it
// back through both forms. The definition form writes every finite double;
// the action form writes the ones inside the safe range and refuses the rest.
func TestRFC8785Vectors(t *testing.T) {
	for _, v := range rfc8785Vectors {
		checkNumberVector(t, v.bits, v.want)
	}
	for _, bits := range []uint64{0x7fffffffffffffff, 0x7ff0000000000000, 0xfff0000000000000} {
		literal := strconv.FormatFloat(math.Float64frombits(bits), 'g', -1, 64)
		if _, err := canon.FingerprintJSON([]byte(literal)); err == nil {
			t.Errorf("FingerprintJSON accepted %s", literal)
		}
	}
}

// TestES6Sample runs a sample in the layout of the es6 number test file, the
// right-hand side written by an ECMAScript engine, through checkNumberVector.
func TestES6Sample(t *testing.T) {
	file, err := os.Open("testdata/es6_sample.txt")
	if err != nil {
		t.Fatalf("open the sample: %v", err)
	}
	defer func() { _ = file.Close() }()
	lines := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		hexBits, want, ok := strings.Cut(scanner.Text(), ",")
		bits, err := strconv.ParseUint(hexBits, 16, 64)
		if !ok || err != nil {
			t.Fatalf("line %d is not <hex>,<text>: %q", lines+1, scanner.Text())
		}
		checkNumberVector(t, bits, want)
		lines++
	}
	if err := scanner.Err(); err != nil || lines < 900 {
		t.Fatalf("read %d lines of the sample, err %v; want at least 900", lines, err)
	}
}

func checkNumberVector(t *testing.T, bits uint64, want string) {
	t.Helper()
	f := math.Float64frombits(bits)
	for _, literal := range []string{
		strconv.FormatFloat(f, 'g', -1, 64),
		strconv.FormatFloat(f, 'e', -1, 64),
		want,
	} {
		got, err := canon.FingerprintJSON([]byte(literal))
		if err != nil || string(got) != want {
			t.Errorf("%016x: FingerprintJSON(%s) = %s, %v; want %s", bits, literal, got, err, want)
		}
		got, err = canon.CanonicalizeJSON([]byte(literal))
		switch {
		case math.Abs(f) <= 1<<53-1 && (err != nil || string(got) != want):
			t.Errorf("%016x: CanonicalizeJSON(%s) = %s, %v; want %s", bits, literal, got, err, want)
		case math.Abs(f) > 1<<53-1 && !errors.Is(err, canon.ErrUnsupportedValue):
			t.Errorf("%016x: CanonicalizeJSON(%s) = %s, %v; want it refused outside the safe range", bits, literal, got, err)
		}
	}
}

// TestFingerprintForm: the definition form keeps the canonical form's
// sorting, escaping, UTF-8 rules, duplicate refusal and depth bound, takes any
// finite double without a range or an exactness check, and keeps member names
// that fold together.
func TestFingerprintForm(t *testing.T) {
	accepted := map[string]string{
		`{"b":1,"a":"é\/"}`:                        "{\"a\":\"é/\",\"b\":1}",
		`{"maximum":9223372036854775807}`:          `{"maximum":9223372036854776000}`,
		`{"maximum":9223372036854776000}`:          `{"maximum":9223372036854776000}`,
		`{"default":0.7,"multipleOf":0.01}`:        `{"default":0.7,"multipleOf":0.01}`,
		`{"n":-0.0,"m":-0}`:                        `{"m":0,"n":0}`,
		`{"ID":1,"id":2}`:                          `{"ID":1,"id":2}`,
		`[0.30000000000000001,4.9e-324,1e-400]`:    `[0.3,5e-324,0]`,
		`[1e21,1e300,123456789012345678901234567]`: `[1e+21,1e+300,1.2345678901234568e+26]`,
	}
	for raw, want := range accepted {
		got, err := canon.FingerprintJSON([]byte(raw))
		if err != nil || string(got) != want {
			t.Errorf("FingerprintJSON(%s) = %s, %v; want %s", raw, got, err, want)
		}
	}
	deep := strings.Repeat("[", 33) + strings.Repeat("]", 33)
	for raw, sentinel := range map[string]error{
		`{"a":1,"a":2}`: canon.ErrUnsupportedValue,
		`"\ud800"`:      canon.ErrUnsupportedValue,
		`1e400`:         canon.ErrUnsupportedValue,
		`[-1e400]`:      canon.ErrUnsupportedValue,
		deep:            canon.ErrTooDeep,
	} {
		if _, err := canon.FingerprintJSON([]byte(raw)); !errors.Is(err, sentinel) {
			t.Errorf("FingerprintJSON(%.40s): err = %v, want %v", raw, err, sentinel)
		}
	}
	if _, err := canon.FingerprintJSON([]byte(strings.Repeat("[", 32) + strings.Repeat("]", 32))); err != nil {
		t.Errorf("FingerprintJSON refused 32 containers: %v", err)
	}
	for _, raw := range []string{"\"\xff\"", `{"a":1}x`, ``} {
		if _, err := canon.FingerprintJSON([]byte(raw)); err == nil {
			t.Errorf("FingerprintJSON accepted %q", raw)
		}
	}
}
