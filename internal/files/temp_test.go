package files

import (
	"os"
	"strings"
	"testing"
)

func TestIsTempKnowsTheNamesWriteTempMakes(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	for range 64 {
		name, err := writeTemp(root, nil, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if !IsTemp(name) {
			t.Fatalf("IsTemp(%q) is false for a name writeTemp made", name)
		}
	}
}

func TestIsTempRefusesEveryOtherShape(t *testing.T) {
	for _, name := range []string{
		"",
		".tmp-",
		".tmp-" + strings.Repeat("A", 25),
		".tmp-" + strings.Repeat("A", 27),
		".tmp-" + strings.Repeat("a", 26),
		".tmp-" + strings.Repeat("A", 25) + "1",
		".tmp-" + strings.Repeat("A", 25) + "8",
		".tmp-" + strings.Repeat("A", 25) + "/",
		"tmp-" + strings.Repeat("A", 26),
		"x.tmp-" + strings.Repeat("A", 26),
		"runs.meta",
	} {
		if IsTemp(name) {
			t.Errorf("IsTemp(%q) is true", name)
		}
	}
}
