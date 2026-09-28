package canon_test

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/guardana/control/internal/canon"
)

// goldenFileSums pins every byte of every golden file. A digest that moves
// under an unchanged tag invalidates every approval stored against it, so a
// changed file fails here even when its test was changed along with it.
var goldenFileSums = map[string]string{
	"arguments_hash.json":   "8710710c4be05ac8f0dc90515a56eb22db51efc65bb5d1bb2bb4023c81164598",
	"binding.json":          "17df3e5fa66b4c01d028c6661751b95178e196396b15e70a2ae876aa0ef5e645",
	"delegated.json":        "4061a7fac19d371730753c8dd4f7685821a51e1c67037429b4de0228766b86bc",
	"escapes.json":          "3b06ff4c779e1b2b2de1816d22953e1075df3f9fdb6fdce58dd8190a8b1a1958",
	"fractions.json":        "6d75fc3ee1971848bd06147934654272a7846e96aabc5936b936f3b58df5379b",
	"integral_spelled.json": "06b2ab7a8911328b9fbadaf6a9dabe760b84312e311f500e6868baa7228bfd96",
	"minimal.json":          "9d11bc2c53a3dabf03956ca7b6cd5edda11e95785885fc1687f4745597af2fb4",
	"mutated_amount.json":   "a1553c90c84c24a25350818daf7664cb77dc28cc3209f51c2057357764c72fc1",
	"numbers.json":          "8d466849c082512ecc608413d68ee517dddc8b95c29fa298483be597cbb97a54",
	"refund_prod.json":      "30bba2c0800c85bb716a2180052019ff9f8a9e13603bec9a587f543150a2ea6d",
}

func TestGoldenFilesAreUnchanged(t *testing.T) {
	entries, err := fs.ReadDir(os.DirFS(fixtureDir), ".")
	if err != nil {
		t.Fatalf("read the fixture directory: %v", err)
	}
	seen := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		want, ok := goldenFileSums[entry.Name()]
		if !ok {
			t.Errorf("%s has no pinned sha256", entry.Name())
			continue
		}
		raw, err := fs.ReadFile(os.DirFS(fixtureDir), entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		sum := sha256.Sum256(raw)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s changed: sha256 %s, pinned %s", entry.Name(), got, want)
		}
		seen++
	}
	if seen != len(goldenFileSums) {
		t.Errorf("found %d pinned golden files, want %d", seen, len(goldenFileSums))
	}
}

// TestFractionsArgumentsHash pins the arguments hash of the fractions fixture,
// computed, like its digest, by a second implementation from the README.
func TestFractionsArgumentsHash(t *testing.T) {
	f := loadFixture(t, "fractions")
	if f.ExpectedHash == "" {
		t.Fatal("fractions.json has no expected_hash")
	}
	got, err := canon.ArgumentsHashV1(f.AuthorizedArgs)
	if err != nil || got != f.ExpectedHash {
		t.Errorf("ArgumentsHashV1 = %s, %v; want %s", got, err, f.ExpectedHash)
	}
	page, err := fs.ReadFile(os.DirFS(fixtureDir), "README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	if n := strings.Count(string(page), f.ExpectedHash); n != 1 {
		t.Errorf("README names the fractions arguments hash %d times, want once", n)
	}
}

// TestSpelledIntegersKeepTheDigest: refund_prod with its integers written as
// 1250.0, 2e0 and 10e-1 has refund_prod's pinned digest: a spelling of an
// integer with a fraction or an exponent is the same value, with the same
// digest.
func TestSpelledIntegersKeepTheDigest(t *testing.T) {
	spelled := loadFixture(t, "integral_spelled")
	plain := loadFixture(t, "refund_prod")
	if spelled.ExpectedDigest != plain.ExpectedDigest {
		t.Fatalf("integral_spelled.json pins %s, refund_prod.json %s; want one digest", spelled.ExpectedDigest, plain.ExpectedDigest)
	}
	if got := digestOf(t, envelopeOf(t, spelled, "integral_spelled"), spelled.AuthorizedArgs); got != plain.ExpectedDigest {
		t.Errorf("digest = %s, want refund_prod's %s", got, plain.ExpectedDigest)
	}
	for _, spelling := range []string{"1250.0", "2e0", "10e-1"} {
		if !strings.Contains(string(spelled.AuthorizedArgs), spelling) {
			t.Errorf("integral_spelled.json no longer spells %s", spelling)
		}
	}
}
