package diagramdoc

import (
	"slices"
	"testing"
)

// A bucket with every value of a dimension and one action reads "any"; one
// with some values reads their list; one with two actions splits.
func TestCollapseHonoursTheCardinality(t *testing.T) {
	yes, no := "yes", "no"
	rows := []row{
		{[5]string{no, no, no, no, "A"}, "blocks"}, {[5]string{no, no, no, no, "B"}, "blocks"},
		{[5]string{no, no, no, yes, "A"}, "runs"}, {[5]string{no, no, no, yes, "B"}, "blocks"},
		{[5]string{yes, no, no, no, "A"}, "blocks"},
	}
	got := collapse(rows, [5]int{2, 2, 2, 2, 2})
	want := []row{
		{[5]string{no, no, no, no, "any"}, "blocks"},
		{[5]string{no, no, no, yes, "A"}, "runs"},
		{[5]string{no, no, no, yes, "B"}, "blocks"},
		{[5]string{yes, no, no, no, "A"}, "blocks"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("collapse =\n%v\nwant\n%v", got, want)
	}
	// With a third verdict declared, two of three values are a list, not "any".
	got = collapse(rows[:2], [5]int{2, 2, 2, 2, 3})
	if len(got) != 1 || got[0].values[4] != "A or B" {
		t.Errorf("two of three values collapsed to %v", got)
	}
}
