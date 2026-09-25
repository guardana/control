package scenario

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/guardana/control/internal/gatewayconfig"
	"github.com/guardana/control/pkg/contract"
)

// Read reads the scenario named name from raw. name is the file's base name
// and becomes the scenario's id. Every refusal is a *Refusal. A document
// over MaxBytes is refused before it is read; after that the kind is checked
// before anything else the document holds, wherever it stands.
func Read(name string, raw []byte) (*Scenario, error) {
	if r := checkName(name); r != nil {
		return nil, r
	}
	if len(raw) > MaxBytes {
		return nil, refuse(nil, ErrTooLarge, fmt.Sprintf("%d bytes, the bound is %d", len(raw), MaxBytes))
	}
	root, noted, fatal := parse(raw)
	if fatal != nil {
		return nil, fatal
	}
	if r := checkKind(root); r != nil {
		return nil, r
	}
	if noted != nil {
		return nil, noted
	}
	s, r := readScenario(root, raw)
	if r != nil {
		return nil, r
	}
	s.ID = name
	return s, nil
}

// ReadFile reads the scenario at path, whose base name is its id. It reads at
// most one byte past MaxBytes, so a file of any size costs the bound.
func ReadFile(path string) (*Scenario, error) {
	name := filepath.Base(path)
	if r := checkName(name); r != nil {
		return nil, r
	}
	// Checked before the open as well as after it, since opening a named pipe
	// waits for a writer.
	if info, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("scenario: %w", err)
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("scenario: %s is not a regular file", name)
	}
	f, err := os.Open(path) //nolint:gosec // G304: the path is the operator's own argument, and the file is only read
	if err != nil {
		return nil, fmt.Errorf("scenario: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("scenario: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("scenario: %s is not a regular file", name)
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("scenario: %w", err)
	}
	return Read(name, raw)
}

// checkName holds a file name to [a-z0-9][a-z0-9-]{0,63}\.json. The id is
// printed in reports and compared on file systems that fold case, so it is
// held to characters that print as themselves and have one case.
func checkName(name string) *Refusal {
	stem, ok := strings.CutSuffix(name, ".json")
	valid := ok && stem != "" && len(stem) <= 64 && stem[0] != '-'
	for i := 0; valid && i < len(stem); i++ {
		c := stem[i]
		valid = c == '-' || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
	}
	if !valid {
		return refuseQuoting(nil, ErrName, `want [a-z0-9][a-z0-9-]{0,63}.json`, []byte(name))
	}
	return nil
}

// checkKind finds kind among the root's members and refuses any kind but
// Kind.
func checkKind(root *node) *Refusal {
	at := path{}.key("kind")
	if root.kind != kindObject {
		return refuse(nil, ErrWrongType, "want an object")
	}
	var kinds []*node
	for _, m := range root.members {
		if m.name == "kind" {
			kinds = append(kinds, m.value)
		}
	}
	switch {
	case len(kinds) == 0:
		return refuse(at, ErrMissing, "")
	case len(kinds) > 1:
		return refuse(at, ErrDuplicate, "")
	case kinds[0].kind == kindNull:
		return refuse(at, ErrNull, "")
	case kinds[0].kind != kindString:
		return wrongType(at, "a string")
	case kinds[0].text != Kind:
		return refuseQuoting(at, ErrKind, "this build reads "+Kind, []byte(kinds[0].text))
	}
	return nil
}

func readScenario(root *node, raw []byte) (*Scenario, *Refusal) {
	f, r := closed(root, nil, []string{"kind", "about", "plane", "steps"}, "run")
	if r != nil {
		return nil, r
	}
	s := &Scenario{Run: RunFresh}
	if s.About, r = readAbout(f["about"], path{}.key("about")); r != nil {
		return nil, r
	}
	if s.Plane, r = readPlane(f["plane"], path{}.key("plane")); r != nil {
		return nil, r
	}
	if n, ok := f["run"]; ok {
		if s.Run, r = readRun(n, path{}.key("run")); r != nil {
			return nil, r
		}
	}
	if s.Steps, r = readSteps(f["steps"], path{}.key("steps"), raw); r != nil {
		return nil, r
	}
	return s, nil
}

func readAbout(n *node, at path) (string, *Refusal) {
	s, r := text(n, at)
	if r != nil {
		return "", r
	}
	if s == "" || len(s) > MaxAboutBytes {
		return "", refuse(at, ErrBound, fmt.Sprintf("%d bytes, want 1 to %d", len(s), MaxAboutBytes))
	}
	return s, noControl(s, at)
}

func readPlane(n *node, at path) (Plane, *Refusal) {
	f, r := closed(n, at, []string{"mode", "bundle"})
	if r != nil {
		return Plane{}, r
	}
	var p Plane
	if p.Mode, r = text(f["mode"], at.key("mode")); r != nil {
		return Plane{}, r
	}
	if !slices.Contains(gatewayconfig.ModeNames(), p.Mode) {
		return Plane{}, refuseQuoting(at.key("mode"), ErrValue, "want one of "+strings.Join(gatewayconfig.ModeNames(), ", "), []byte(p.Mode))
	}
	at = at.key("bundle")
	if f, r = closed(f["bundle"], at, []string{"id"}, "digest"); r != nil {
		return Plane{}, r
	}
	if p.BundleID, r = identifier(f["id"], at.key("id"), contract.MaxStringBytes); r != nil {
		return Plane{}, r
	}
	if d, ok := f["digest"]; ok {
		if p.BundleDigest, r = text(d, at.key("digest")); r != nil {
			return Plane{}, r
		}
		if !isDigest(p.BundleDigest) {
			return Plane{}, refuseQuoting(at.key("digest"), ErrValue, `want "sha256:" and 64 lower-case hex digits`, []byte(p.BundleDigest))
		}
	}
	return p, nil
}

func readRun(n *node, at path) (Run, *Refusal) {
	s, r := text(n, at)
	if r != nil {
		return "", r
	}
	if run := Run(s); run == RunFresh || run == RunContinues {
		return run, nil
	}
	return "", refuseQuoting(at, ErrValue, "want fresh or continues", []byte(s))
}

// isDigest holds a bundle digest to the one form a plane reports, so that a
// digest no plane could carry fails when the scenario loads.
func isDigest(s string) bool {
	hex, ok := strings.CutPrefix(s, "sha256:")
	if !ok || len(hex) != 64 {
		return false
	}
	for i := 0; i < len(hex); i++ {
		if c := hex[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// closed reads an object whose members the format fixes. It refuses the
// first unknown member in document order, then the first missing required
// one in the order listed.
func closed(n *node, at path, required []string, optional ...string) (map[string]*node, *Refusal) {
	if n.kind != kindObject {
		return nil, wrongType(at, "an object")
	}
	f := make(map[string]*node, len(n.members))
	for _, m := range n.members {
		if !slices.Contains(required, m.name) && !slices.Contains(optional, m.name) {
			return nil, refuseQuoting(at.key(m.name), ErrUnknownMember, "", []byte(m.name))
		}
		f[m.name] = m.value
	}
	for _, name := range required {
		if _, ok := f[name]; !ok {
			return nil, refuse(at.key(name), ErrMissing, "")
		}
	}
	return f, nil
}

func wrongType(at path, want string) *Refusal { return refuse(at, ErrWrongType, "want "+want) }

func text(n *node, at path) (string, *Refusal) {
	if n.kind != kindString {
		return "", wrongType(at, "a string")
	}
	return n.text, nil
}

// identifier reads a non-empty string of at most limit bytes that the
// contract's identifier rule admits.
func identifier(n *node, at path, limit int) (string, *Refusal) {
	s, r := text(n, at)
	if r != nil {
		return "", r
	}
	if s == "" || len(s) > limit {
		return "", refuse(at, ErrBound, fmt.Sprintf("%d bytes, want 1 to %d", len(s), limit))
	}
	if err := contract.CheckIdentifier(s); err != nil {
		return "", refuse(at, ErrValue, err.Error())
	}
	return s, nil
}

func noControl(s string, at path) *Refusal {
	for i, c := range s {
		if unicode.Is(unicode.Cc, c) {
			return refuse(at, ErrValue, fmt.Sprintf("a control character U+%04X at byte %d", c, i))
		}
	}
	return nil
}

// list reads an array of at most MaxListItems items.
func list(n *node, at path) ([]*node, *Refusal) {
	if n.kind != kindArray {
		return nil, wrongType(at, "an array")
	}
	if len(n.items) > MaxListItems {
		return nil, refuse(at, ErrBound, fmt.Sprintf("%d items, the bound is %d", len(n.items), MaxListItems))
	}
	return n.items, nil
}

// names reads a list of strings, each of which accept must take.
func names(n *node, at path, accept func(string) bool, want string) ([]string, *Refusal) {
	items, r := list(n, at)
	if r != nil {
		return nil, r
	}
	out := make([]string, len(items))
	for i, item := range items {
		if out[i], r = text(item, at.index(i)); r != nil {
			return nil, r
		}
		if !accept(out[i]) {
			return nil, refuseQuoting(at.index(i), ErrValue, want, []byte(out[i]))
		}
	}
	return out, nil
}
