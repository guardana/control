package metrics

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	adaptermcp "github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/adapters/otel"
	"github.com/guardana/control/internal/brand"
	"github.com/guardana/control/internal/gateway"
	"github.com/guardana/control/internal/pause"
	"github.com/guardana/control/internal/spool"
)

// ContentType is the media type of the text Render writes.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Type is a metric's type as the text format names it.
type Type string

// The two types the table uses.
const (
	Counter Type = "counter"
	Gauge   Type = "gauge"
)

// Prefix begins every metric name: the product's slug in the character set
// a metric name admits.
func Prefix() string { return strings.ReplaceAll(brand.Slug, "-", "_") + "_" }

// Reading is every statistic a plane reports, as its seams return them.
type Reading struct {
	Pipeline gateway.Stats
	Adapter  adaptermcp.Stats
	// Pause is what the pause reader counted; zero where no pause file is
	// configured.
	Pause pause.PollStats
	// PauseState and PauseEntries are the pause state a call admitted now is
	// decided under, and the entries in force in it.
	PauseState   pause.State
	PauseEntries int
	Spool        spool.Stats
	// SpoolBroken says the spool reported an error instead of its
	// statistics, so Spool holds nothing and its metrics are left out.
	SpoolBroken bool
	Exporter    otel.Stats
	// ExporterStopped says the exporter's run ended.
	ExporterStopped bool
}

// Metric is one row of the table.
type Metric struct {
	// Name is the whole name, Prefix included.
	Name string
	Type Type
	Help string
	// Label is the one label's name, empty for a metric with none.
	Label string
	// Reads is the field of Reading the metric is read from, as a dotted
	// path.
	Reads string
	read  func(Reading) ([]sample, error)
}

// sample is one line of a metric: its label value, empty where the metric
// has no label, and its value as the text spells it.
type sample struct {
	label, value string
}

// Table is every metric in the order Render writes them. The slice is a
// copy.
func Table() []Metric {
	return append([]Metric(nil), table...)
}

// Render writes every metric of the table for r, but those reading the spool
// when r.SpoolBroken says it could not be read. It refuses a reading it cannot
// write truthfully: a counter below zero, a pause state outside the four, and
// counts outside a label's closed set that sum past a counter's range.
func Render(r Reading) ([]byte, error) {
	var b bytes.Buffer
	for _, m := range table {
		if r.SpoolBroken && strings.HasPrefix(m.Reads, "Spool.") {
			continue
		}
		samples, err := m.read(r)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.Name, err)
		}
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s %s\n", m.Name, escapeHelp(m.Help), m.Name, m.Type)
		for _, s := range samples {
			if m.Type == Counter && strings.HasPrefix(s.value, "-") {
				return nil, fmt.Errorf("%s: a counter at %s", m.Name, s.value)
			}
			b.WriteString(m.Name)
			if m.Label != "" {
				fmt.Fprintf(&b, "{%s=\"%s\"}", m.Label, escapeLabel(s.label))
			}
			b.WriteString(" " + s.value + "\n")
		}
	}
	if b.Len() == 0 {
		return nil, errors.New("the table is empty")
	}
	return b.Bytes(), nil
}

var (
	helpEscaper  = strings.NewReplacer(`\`, `\\`, "\n", `\n`)
	labelEscaper = strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`)
)

func escapeHelp(s string) string  { return helpEscaper.Replace(s) }
func escapeLabel(s string) string { return labelEscaper.Replace(s) }
