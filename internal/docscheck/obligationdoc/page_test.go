package obligationdoc

import (
	"strings"
	"testing"
)

// catalogueSource is the shape of internal/policy/rules/catalogue.go that
// Names reads: the one function, returning a literal list of strings.
func catalogueSource(list string) string {
	return "package rules\n\nfunc other() []string { return []string{\"x\"} }\n\n" +
		"func obligationTypes() []string {\n\treturn []string{" + list + "}\n}\n"
}

func TestNamesReadsTheListInOrder(t *testing.T) {
	got, err := Names([]byte(catalogueSource(`"cap_amount", "redact_fields",
		"emit_alert"`)))
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	if want := []string{"cap_amount", "redact_fields", "emit_alert"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Names = %q, want %q", got, want)
	}
}

func TestNamesRefusals(t *testing.T) {
	for name, src := range map[string]string{
		"not go":           "not go source",
		"no such function": "package rules\n\nfunc other() []string { return []string{\"x\"} }\n",
		"two returns": "package rules\n\nfunc obligationTypes() []string {\n\tif true {\n\t\treturn []string{\"a\"}\n\t}\n" +
			"\treturn []string{\"b\"}\n}\n",
		"a call, not a literal":            "package rules\n\nfunc obligationTypes() []string {\n\treturn other()\n}\n",
		"an element that is not a literal": catalogueSource(`"a", name`),
		"a concatenation":                  catalogueSource(`"a" + "b"`),
		"empty":                            catalogueSource(``),
		"a duplicate":                      catalogueSource(`"a", "b", "a"`),
		"an empty name":                    catalogueSource(`"a", ""`),
		"a pipe in a name":                 catalogueSource(`"a|b"`),
		"a backtick in a name":             catalogueSource(`"a` + "`" + `b"`),
		"a control character in a name":    catalogueSource(`"a\nb"`),
		"a raw string element":             catalogueSource("`a`"),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := Names([]byte(src))
			if err == nil {
				t.Fatalf("Names accepted the source: %q", got)
			}
			if got != nil {
				t.Errorf("Names returned %q beside the error", got)
			}
		})
	}
}

func TestRenderOneRowPerNameInOrder(t *testing.T) {
	page, err := render([]string{"cap_amount", "redact_fields"}, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	text := string(page)
	rows := strings.Count(text, "\n| `")
	if rows != 2 {
		t.Errorf("page holds %d rows, want 2:\n%s", rows, text)
	}
	first, second := strings.Index(text, "| `cap_amount` |"), strings.Index(text, "| `redact_fields` |")
	if first < 0 || second < 0 || second < first {
		t.Errorf("rows at %d and %d, want both present and in the catalogue's order", first, second)
	}
	for _, label := range []string{"`implemented`", "`planned`"} {
		if !strings.Contains(text, label) {
			t.Errorf("page has no %s label", label)
		}
	}
	if !strings.HasSuffix(text, "|\n") {
		t.Errorf("page does not end in the last row's newline: %q", text[max(0, len(text)-20):])
	}
}

func TestRenderRefusesAnEmptyCatalogue(t *testing.T) {
	page, err := render(nil, nil)
	if err == nil {
		t.Fatalf("render(nil, nil) returned %d bytes, want an error", len(page))
	}
	if page != nil {
		t.Errorf("render(nil, nil) returned %d bytes beside the error", len(page))
	}
}

func TestPageIsDeterministic(t *testing.T) {
	src := []byte(catalogueSource(`"cap_amount", "redact_fields"`))
	appliers := []Applier{{Name: "the gateway", Types: []string{"redact_fields", "cap_amount"}}}
	first, err := Page(src, appliers)
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	second, err := Page(src, appliers)
	if err != nil {
		t.Fatalf("Page, second call: %v", err)
	}
	if string(first) != string(second) {
		t.Error("two calls to Page produced different bytes")
	}
}

// A name Names refuses never reaches a row; a name it accepts always does.
func FuzzNames(f *testing.F) {
	f.Add(catalogueSource(`"cap_amount", "redact_fields"`))
	f.Add(catalogueSource(`"a|b"`))
	f.Add("package rules\n")
	f.Fuzz(func(t *testing.T, src string) {
		names, err := Names([]byte(src))
		if err != nil {
			if names != nil {
				t.Errorf("names %q beside the error", names)
			}
			return
		}
		page, err := Page([]byte(src), nil)
		if err != nil {
			t.Fatalf("Names accepted what Page refuses: %v", err)
		}
		if strings.Count(string(page), "\n| `") != len(names) {
			t.Errorf("%d names, page:\n%s", len(names), page)
		}
	})
}

func TestRenderNamesWhoAppliesEachType(t *testing.T) {
	appliers := []Applier{
		{Name: "the gateway", Types: []string{"cap_amount"}},
		{Name: "the adapter", Types: []string{"cap_amount", "read_only"}},
	}
	page, err := render([]string{"cap_amount", "read_only", "emit_alert"}, appliers)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	text := string(page)
	for _, row := range []string{
		"| `cap_amount` | the gateway, the adapter |\n",
		"| `read_only` | the adapter |\n",
		"| `emit_alert` | " + unapplied + " |\n",
	} {
		if !strings.Contains(text, row) {
			t.Errorf("page has no row %q:\n%s", row, text)
		}
	}
}

// A page that listed an applier for a type the catalogue lacks would state a
// capability the kernel refuses at start, so the renderer refuses it first.
func TestRenderRefusesAnApplierThePageCannotState(t *testing.T) {
	names := []string{"cap_amount"}
	for _, tc := range []struct {
		name     string
		appliers []Applier
	}{
		{"a type outside the catalogue", []Applier{{Name: "the adapter", Types: []string{"cap_rate"}}}},
		{"no name", []Applier{{Types: []string{"cap_amount"}}}},
		{"a name a cell cannot carry", []Applier{{Name: "a|b", Types: []string{"cap_amount"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page, err := render(names, tc.appliers)
			if err == nil {
				t.Fatalf("render accepted %+v and returned %d bytes", tc.appliers, len(page))
			}
			if page != nil {
				t.Errorf("%d bytes beside the error", len(page))
			}
		})
	}
}

// Appliers reads each applier's own declaration; an empty one would render
// every type as planned and still pin.
func TestAppliersDeclareSomething(t *testing.T) {
	for _, a := range Appliers() {
		if len(a.Types) == 0 {
			t.Errorf("%s declares no obligation type", a.Name)
		}
	}
}
