package gatewayconfig

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/guardana/control/internal/brand"
)

// maxConfigBytes bounds the file the loader reads. A configuration is a page
// of keys; anything larger is a file handed to the wrong program.
const maxConfigBytes = 1 << 20

// HeadersPrefix and PDPHeadersPrefix are the map keys: each leaf is a header
// the exporter, or the decision point's client, sends.
const (
	HeadersPrefix    = "export.headers."
	PDPHeadersPrefix = "pdp.headers."
)

// Load reads the file at path, applies the environment over it and returns
// the configuration, or the first refusal. environ is the process's, as
// os.Environ writes it.
func Load(path string, environ []string) (*Config, error) {
	text, err := readConfigFile(path)
	if err != nil {
		return nil, err
	}
	entries, err := parseYAML(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	b := &binder{cfg: &Config{dir: dir, source: map[string]string{}}, headers: map[string]string{}}
	for _, m := range mapFields {
		*m.get(b.cfg) = map[string]string{}
	}
	if err := applyDefaults(b.cfg, configFields); err != nil {
		return nil, err
	}
	for _, e := range entries {
		where := fmt.Sprintf("%s:%d", path, e.line)
		key, err := canonicalKey(e.path)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		if previous, ok := b.cfg.source[key]; ok {
			return nil, fmt.Errorf("%s: %s is set twice; it was set at %s", where, key, previous)
		}
		b.cfg.source[key] = where
		if err := b.set(key, e.value, where); err != nil {
			return nil, err
		}
	}
	if err := b.environment(environ); err != nil {
		return nil, err
	}
	if err := b.cfg.check(); err != nil {
		return nil, err
	}
	return b.cfg, nil
}

func readConfigFile(path string) (string, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("reading the configuration: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	if len(raw) > maxConfigBytes {
		return "", fmt.Errorf("%s: over %d bytes", path, maxConfigBytes)
	}
	return string(raw), nil
}

// binder sets keys on one configuration. The file's keys are bound first and
// the environment's over them, so a variable wins over the file.
type binder struct {
	cfg *Config
	// headers names, per map key's leaf a variable set, the variable.
	headers map[string]string
}

// set routes one key to the table that owns it. A key no table owns is
// refused: a misspelled key that is ignored is a setting the operator believes
// is in force. A map key's leaf arrives as canonicalKey spells it.
func (b *binder) set(path, value, where string) error {
	for _, m := range mapFields {
		if name, ok := strings.CutPrefix(path, m.prefix); ok {
			(*m.get(b.cfg))[name] = value
			return nil
		}
	}
	for _, f := range configFields {
		if f.path == path {
			if err := f.set(b.cfg, value); err != nil {
				return fmt.Errorf("%s: %s: %w", where, path, err)
			}
			return nil
		}
	}
	for _, l := range listFields {
		if rest, ok := strings.CutPrefix(path, l.path+"."); ok {
			return b.item(l, rest, value, where)
		}
	}
	if rest, ok := strings.CutPrefix(path, "upstreams."); ok {
		return bindItem(&b.cfg.Upstreams, upstreamFields, rest, value, where, path, b.arg)
	}
	if rest, ok := strings.CutPrefix(path, "overrides."); ok {
		return bindItem(&b.cfg.Overrides, overrideFields, rest, value, where, path, nil)
	}
	return fmt.Errorf("%s: %s is not a configuration key", where, path)
}

// item sets one item of a list at its index.
func (b *binder) item(l listField, rest, value, where string) error {
	items := l.get(b.cfg)
	index, spelled, follows := sequenceIndex(rest, len(*items))
	switch {
	case !spelled:
		return fmt.Errorf("%s: %s takes a sequence", where, l.path)
	case !follows:
		return fmt.Errorf("%s: %s.%d follows no item %d", where, l.path, index, index-1)
	case index == len(*items):
		*items = append(*items, value)
	default:
		(*items)[index] = value
	}
	return nil
}

// arg sets one element of an upstream's command arguments.
func (b *binder) arg(u *UpstreamConfig, rest, value, where string) error {
	index, _, follows := sequenceIndex(rest, len(u.Args))
	if !follows {
		return fmt.Errorf("%s: args takes a sequence, each index following the one before", where)
	}
	if index == len(u.Args) {
		u.Args = append(u.Args, value)
		return nil
	}
	u.Args[index] = value
	return nil
}

// sequenceIndex reads the index of an item of a sequence that holds n items
// so far. spelled says it is written as the parser writes one, a decimal
// with no sign or leading zero, so no index has a second spelling; follows
// says it is spelled and at most n, so no item is created past a gap.
func sequenceIndex(s string, n int) (index int, spelled, follows bool) {
	index, err := strconv.Atoi(s)
	if err != nil || index < 0 || strconv.Itoa(index) != s {
		return 0, false, false
	}
	return index, true, index <= n
}

// bindItem sets one key of one item of a sequence. The index has to follow the
// items already read, so a gap in the file, or an environment variable naming
// an item no file declared, is refused instead of creating an empty entry.
func bindItem[T any](items *[]T, fields []field[T], rest, value, where, path string, extra func(*T, string, string, string) error) error {
	head, tail, ok := strings.Cut(rest, ".")
	index, spelled, follows := sequenceIndex(head, len(*items))
	if !ok || !spelled {
		return fmt.Errorf("%s: %s is not a configuration key", where, path)
	}
	if !follows {
		return fmt.Errorf("%s: %s follows no entry %d", where, path, index-1)
	}
	if index == len(*items) {
		var item T
		if err := applyDefaults(&item, fields); err != nil {
			return err
		}
		*items = append(*items, item)
	}
	item := &(*items)[index]
	if key, isExtra := strings.CutPrefix(tail, scalarSequenceKey+"."); isExtra && extra != nil {
		return extra(item, key, value, where)
	}
	for _, f := range fields {
		if f.path == tail {
			if err := f.set(item, value); err != nil {
				return fmt.Errorf("%s: %s: %w", where, path, err)
			}
			return nil
		}
	}
	return fmt.Errorf("%s: %s is not a configuration key", where, path)
}

// environment applies every variable under the product's prefix, which wins
// over the file. A variable under the prefix that names no key is refused:
// a misspelled one that is ignored is a setting nobody applied.
func (b *binder) environment(environ []string) error {
	known := b.envNames()
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(name, brand.EnvPrefix) {
			continue
		}
		key := strings.TrimPrefix(name, brand.EnvPrefix)
		if isHeader, err := b.header(name, key, value); isHeader || err != nil {
			if err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
			continue
		}
		path, ok := known[key]
		if !ok {
			if sequence, isSequence := sequenceOf(key); isSequence {
				return fmt.Errorf("%s names no %s entry the file declares; a variable sets the values of an entry, it does not add one", name, sequence)
			}
			return fmt.Errorf("%s names no configuration key", name)
		}
		if err := b.set(path, value, name); err != nil {
			return err
		}
		b.cfg.source[path] = name
	}
	return nil
}

// header sets the header the variable names under a map key, and reports
// whether it named one. The variable's underscores are the header's hyphens,
// and the name is held to the file's rule and spelled as the file's is, so
// two variables of one header are one header set twice.
func (b *binder) header(variable, key, value string) (bool, error) {
	for _, m := range mapFields {
		if name, ok := strings.CutPrefix(key, EnvName(m.prefix)); ok {
			canonical, err := headerName(m.prefix, strings.ReplaceAll(name, "_", "-"))
			if err != nil {
				return true, err
			}
			path := m.prefix + canonical
			if previous, ok := b.headers[path]; ok {
				return true, fmt.Errorf("%s is set twice; it was set by %s", path, previous)
			}
			b.headers[path] = variable
			(*m.get(b.cfg))[canonical] = value
			return true, nil
		}
	}
	return false, nil
}

// sequenceOf names the sequence a variable was reaching into, so a variable
// that names an entry the file does not declare says which sequence it meant.
func sequenceOf(key string) (string, bool) {
	sequences := []string{"upstreams", "overrides"}
	for _, l := range listFields {
		sequences = append(sequences, l.path)
	}
	for _, sequence := range sequences {
		if strings.HasPrefix(key, EnvName(sequence)+"_") {
			return sequence, true
		}
	}
	return "", false
}

// envNames maps every variable this configuration reads to the key it sets:
// the scalar keys, and the items of the lists and of the two sequences the
// file declared, each at its own index.
func (b *binder) envNames() map[string]string {
	known := map[string]string{}
	for _, f := range configFields {
		known[EnvName(f.path)] = f.path
	}
	for _, l := range listFields {
		for i := range *l.get(b.cfg) {
			path := fmt.Sprintf("%s.%d", l.path, i)
			known[EnvName(path)] = path
		}
	}
	for i := range b.cfg.Upstreams {
		for _, f := range upstreamFields {
			path := fmt.Sprintf("upstreams.%d.%s", i, f.path)
			known[EnvName(path)] = path
		}
		for j := range b.cfg.Upstreams[i].Args {
			path := fmt.Sprintf("upstreams.%d.args.%d", i, j)
			known[EnvName(path)] = path
		}
	}
	for i := range b.cfg.Overrides {
		for _, f := range overrideFields {
			path := fmt.Sprintf("overrides.%d.%s", i, f.path)
			known[EnvName(path)] = path
		}
	}
	return known
}
