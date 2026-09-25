package metricstest

import (
	"strings"
	"testing"
)

const valid = "# HELP x_calls_total Calls, by code.\n" +
	"# TYPE x_calls_total counter\n" +
	"x_calls_total{code=\"A\"} 3\n" +
	"x_calls_total{code=\"q\\\"uo\\\\te\\nnl\"} 0\n" +
	"# HELP x_open Open now \\\\ and\\nthen.\n" +
	"# TYPE x_open gauge\n" +
	"x_open -2.5\n" +
	"# HELP x_empty A family with no sample.\n" +
	"# TYPE x_empty gauge\n"

func TestParseReadsWhatTheRendererWrites(t *testing.T) {
	families, err := Parse([]byte(valid))
	if err != nil {
		t.Fatalf("Parse refused a valid exposition: %v", err)
	}
	if len(families) != 3 {
		t.Fatalf("%d families, want 3", len(families))
	}
	calls, _ := Find(families, "x_calls_total")
	if calls.Type != "counter" || calls.Help != "Calls, by code." || len(calls.Samples) != 2 {
		t.Errorf("x_calls_total read as %+v", calls)
	}
	if s, ok := calls.One(map[string]string{"code": "q\"uo\\te\nnl"}); !ok || s.Raw != "0" {
		t.Errorf("the escaped label value was not read back: %+v", calls.Samples)
	}
	open, _ := Find(families, "x_open")
	if open.Help != "Open now \\ and\nthen." {
		t.Errorf("HELP unescaped to %q", open.Help)
	}
	if s, ok := open.One(map[string]string{}); !ok || s.Value != -2.5 {
		t.Errorf("x_open read as %+v", open.Samples)
	}
	if _, ok := Find(families, "x_missing"); ok {
		t.Error("Find found a family the text does not hold")
	}
}

// Each case changes the valid exposition in one place, so the refusal it
// names is the one thing that differs from an accepted text.
func TestParseRefuses(t *testing.T) {
	for _, c := range []struct{ name, from, to, want string }{
		{"no final newline", "# TYPE x_empty gauge\n", "# TYPE x_empty gauge", "end with a newline"},
		{"a blank line", "x_open -2.5\n", "x_open -2.5\n\n", "a blank line"},
		{"another comment", "x_open -2.5\n", "x_open -2.5\n# note\n", "neither HELP nor TYPE"},
		{"HELP without text", "# HELP x_empty A family with no sample.", "# HELP x_empty", "carries no text"},
		{"a bad metric name", "# HELP x_empty A", "# HELP 1x_empty A", "not a metric name"},
		{"a family described twice", "# HELP x_empty", "# HELP x_open", "described twice"},
		{"HELP with a bad escape", "Open now \\\\ and", "Open now \\t and", "the escape \\t"},
		{"HELP with an escaped quote", "Open now \\\\ and", "Open now \\\" and", "outside a label value"},
		{"TYPE missing", "# TYPE x_empty gauge\n", "", "no TYPE line"},
		{"TYPE of another name", "# TYPE x_open gauge", "# TYPE x_other gauge", "TYPE names"},
		{"a type not counter or gauge", "# TYPE x_open gauge", "# TYPE x_open summary", "not counter or gauge"},
		{"a sample of a longer name", "x_open -2.5", "x_opener -2.5", "not one space and a value"},
		{"a sample of another family", "x_open -2.5", "y_open -2.5", "a sample of another family"},
		{"a sample before TYPE", "# TYPE x_open gauge\nx_open", "x_open 1\n# TYPE x_open gauge\nx_open", "before its family"},
		{"a timestamp", "x_open -2.5", "x_open -2.5 1700000000", "not one space and a value"},
		{"two spaces", "x_open -2.5", "x_open  -2.5", "not one space and a value"},
		{"not a number", "x_open -2.5", "x_open two", "not a finite number"},
		{"NaN", "x_open -2.5", "x_open NaN", "not a finite number"},
		{"infinity", "x_open -2.5", "x_open +Inf", "not a finite number"},
		{"a negative counter", "{code=\"A\"} 3", "{code=\"A\"} -3", "a counter at"},
		{"a negative zero counter", "{code=\"A\"} 3", "{code=\"A\"} -0", "a counter at"},
		{"a label set twice", "{code=\"A\"} 3", "{code=\"q\\\"uo\\\\te\\nnl\"} 3", "appears twice"},
		{"a label name twice", "{code=\"A\"}", "{code=\"A\",code=\"B\"}", "label code appears twice"},
		{"a reserved label name", "{code=\"A\"}", "{__code=\"A\"}", "does not start with a label name"},
		{"a bad label name", "{code=\"A\"}", "{co-de=\"A\"}", "does not start with a label name"},
		{"a trailing comma", "{code=\"A\"}", "{code=\"A\",}", "follows the value"},
		{"an unclosed value", "{code=\"A\"} 3", "{code=\"A} 3", "not closed"},
		{"a bad label escape", "{code=\"A\"}", "{code=\"\\A\"}", "the escape \\A"},
		{"an escaped closing quote", "{code=\"A\"}", "{code=\"A\\\"}", "not closed"},
		{"HELP ending in a backslash", "and\\nthen.\n", "and\\nthen.\\\n", "a backslash at the end"},
		{"junk after a value", "{code=\"A\"}", "{code=\"A\"x}", "follows the value"},
		{"a raw newline in a label", "{code=\"A\"} 3\n", "{code=\"A\n\"} 3\n", "not closed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !strings.Contains(valid, c.from) {
				t.Fatalf("the case does not change the valid text: %q", c.from)
			}
			text := strings.Replace(valid, c.from, c.to, 1)
			_, err := Parse([]byte(text))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Parse answered %v, want a refusal naming %q, for:\n%s", err, c.want, text)
			}
		})
	}
	for _, text := range []string{"", "\xff\n"} {
		if _, err := Parse([]byte(text)); err == nil {
			t.Errorf("Parse accepted %q", text)
		}
	}
}
