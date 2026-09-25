package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/canon"
	"github.com/guardana/control/internal/gateway"
)

// fingerprintDomain separates a tool fingerprint from every other digest
// this module computes.
const fingerprintDomain = "mcp-tool-fingerprint/v1\n"

// Bounds on a ResourceFrom pointer: enough for any argument shape a tool
// reads, and small enough that resolving one costs nothing.
const (
	maxPointerTokens    = 8
	maxPointerTokenSize = 64
)

// Override is the operator's classification of one tool definition. The
// fingerprint pins the definition it classifies: a tool whose name matches
// and whose fingerprint does not is unclassified until the operator looks
// again (ADR-0013). Annotations never classify; only this does.
type Override struct {
	// Upstream names the server that serves the tool.
	Upstream string
	// Tool is the tool's name as the upstream lists it.
	Tool string
	// Fingerprint is Fingerprint of the definition the operator classified.
	Fingerprint string
	// Effect is the effect class of a call to the tool.
	Effect controlv1.EffectClass
	// ResourceType is the envelope's resource.type.
	ResourceType string
	// ResourceFrom is an RFC 6901 pointer into the call's arguments, whose
	// string or integer value is the envelope's resource.id. Empty means the
	// call names no resource, which Validate refuses for the effects that
	// need one.
	ResourceFrom string
	// TrustZone is the destination's, for a tool that sends data somewhere.
	// It says nothing about what the tool returns.
	TrustZone controlv1.TrustZone
	// ReturnsTrust is the trust zone of what the tool's results contain;
	// UNSPECIFIED, the zero value, is untrusted.
	ReturnsTrust controlv1.TrustZone
	// ReturnsSensitivity is the highest sensitivity the tool's results can
	// hold; UNSPECIFIED, the zero value, is unknown. It is never copied into
	// the envelope's data labels, which say what a call carries (ADR-0021).
	ReturnsSensitivity controlv1.Sensitivity
}

// Entry is one tool as the manifest holds it: the upstream's definition and,
// when an override pins its fingerprint, its classification.
type Entry struct {
	Upstream    string
	Tool        *mcp.Tool
	Fingerprint string
	// Classified is false for a definition no override pins; a call to it
	// is refused before the policy sees it.
	Classified   bool
	Effect       controlv1.EffectClass
	ResourceType string
	ResourceFrom string
	TrustZone    controlv1.TrustZone
	// ReturnsTrust and ReturnsSensitivity classify what a call's result
	// contains, as the override declares it.
	ReturnsTrust       controlv1.TrustZone
	ReturnsSensitivity controlv1.Sensitivity
	// Ambiguous marks a name two upstreams serve: no call can be routed.
	Ambiguous bool
	// why is the reason a listed definition is not classified.
	why string
}

// Fingerprint identifies a tool definition by all of it as the library types
// it: name, title, description, input schema, output schema, annotations,
// icons and _meta, canonicalized through internal/canon and hashed under a
// domain tag. A definition the canonical form refuses, one with a float in a
// schema for instance, has no fingerprint and so cannot be classified.
func Fingerprint(t *mcp.Tool) (string, error) {
	if t == nil {
		return "", errors.New("mcp: no tool")
	}
	raw, err := json.Marshal(map[string]any{
		"name":         t.Name,
		"title":        t.Title,
		"description":  t.Description,
		"inputSchema":  t.InputSchema,
		"outputSchema": t.OutputSchema,
		"annotations":  t.Annotations,
		"icons":        t.Icons,
		"_meta":        t.Meta,
	})
	if err != nil {
		return "", fmt.Errorf("mcp: fingerprint: %w", err)
	}
	body, err := canon.CanonicalizeJSON(raw)
	if err != nil {
		return "", fmt.Errorf("mcp: fingerprint: %w", err)
	}
	sum := sha256.Sum256(append([]byte(fingerprintDomain), body...))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// checkPointer refuses a ResourceFrom pointer that is not RFC 6901 or is
// outside the bound. The empty pointer names no resource and is allowed.
func checkPointer(p string) error {
	if p == "" {
		return nil
	}
	if !strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: ResourceFrom %q does not start with /", ErrOverride, p)
	}
	tokens := strings.Split(p[1:], "/")
	if len(tokens) > maxPointerTokens {
		return fmt.Errorf("%w: ResourceFrom has %d tokens, limit %d", ErrOverride, len(tokens), maxPointerTokens)
	}
	for _, tok := range tokens {
		if len(tok) > maxPointerTokenSize {
			return fmt.Errorf("%w: a ResourceFrom token is %d bytes, limit %d", ErrOverride, len(tok), maxPointerTokenSize)
		}
		for i := 0; i < len(tok); i++ {
			if tok[i] == '~' && (i+1 >= len(tok) || (tok[i+1] != '0' && tok[i+1] != '1')) {
				return fmt.Errorf("%w: ResourceFrom has a bare ~", ErrOverride)
			}
		}
	}
	return nil
}

// resolvePointer reads the resource id at pointer p in the arguments: a
// string as it stands, an integer as its decimal text, anything else as no
// id. It decodes the document itself rather than through the canonical form,
// which the digest applies later to the same bytes.
func resolvePointer(args []byte, p string) string {
	if p == "" || len(args) == 0 {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return ""
	}
	for _, tok := range strings.Split(p[1:], "/") {
		var ok bool
		if v, ok = step(v, tok); !ok {
			return ""
		}
	}
	return scalarText(v)
}

// step descends one pointer token: a member of an object, or an element of
// an array by a decimal index without a leading zero.
func step(v any, tok string) (any, bool) {
	tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
	switch c := v.(type) {
	case map[string]any:
		next, ok := c[tok]
		return next, ok
	case []any:
		i, err := strconv.Atoi(tok)
		if err != nil || i < 0 || i >= len(c) || (len(tok) > 1 && tok[0] == '0') {
			return nil, false
		}
		return c[i], true
	}
	return nil, false
}

func scalarText(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case json.Number:
		if _, err := s.Int64(); err == nil {
			return s.String()
		}
	}
	return ""
}

// manifest is the adapter's own view of every upstream's tools, keyed by
// name across upstreams, rebuilt per upstream from its tools/list. An entry
// is never written after it is published: a refresh builds new entries and a
// new index, so a reader holding an entry sees what was classified when it
// looked.
type manifest struct {
	overrides map[string]Override // upstream + "\x00" + tool

	mu        sync.RWMutex
	perUp     map[string][]*Entry
	byName    map[string]*Entry
	ambiguous map[string]bool
	// generation counts replacements, so a list shaped from an older
	// manifest is never cached as current.
	generation uint64
}

func newManifest(overrides []Override) *manifest {
	m := &manifest{
		overrides: make(map[string]Override, len(overrides)),
		perUp:     map[string][]*Entry{},
		byName:    map[string]*Entry{},
		ambiguous: map[string]bool{},
	}
	for _, o := range overrides {
		m.overrides[o.Upstream+"\x00"+o.Tool] = o
	}
	return m
}

// refresh replaces upstream's entries with what it lists now. A list that
// cannot be read, or is longer than the bound, drops the upstream's entries:
// a manifest that kept the old ones would classify a definition it can no
// longer see.
func (m *manifest) refresh(ctx context.Context, upstream string, cs *mcp.ClientSession) error {
	tools, err := readAll(ctx, func(ctx context.Context, cursor string) ([]*mcp.Tool, string, error) {
		res, err := cs.ListTools(ctx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			return nil, "", err
		}
		return res.Tools, res.NextCursor, nil
	})
	if err != nil {
		m.replace(upstream, nil)
		return fmt.Errorf("mcp: tools/list from %s: %w", upstream, err)
	}
	entries := make([]*Entry, 0, len(tools))
	for _, tool := range tools {
		if tool != nil {
			entries = append(entries, m.classify(upstream, tool))
		}
	}
	m.replace(upstream, entries)
	return nil
}

// classify builds the entry for one listed tool from its fingerprint and the
// override that pins it, if any.
func (m *manifest) classify(upstream string, tool *mcp.Tool) *Entry {
	e := &Entry{Upstream: upstream, Tool: tool}
	fp, err := Fingerprint(tool)
	if err != nil {
		e.why = err.Error()
		return e
	}
	e.Fingerprint = fp
	o, ok := m.overrides[upstream+"\x00"+tool.Name]
	switch {
	case !ok:
		e.why = "no override classifies it"
	case o.Fingerprint != fp:
		e.why = "the definition changed since it was classified"
	default:
		e.Classified = true
		e.Effect = o.Effect
		e.ResourceType = o.ResourceType
		e.ResourceFrom = o.ResourceFrom
		e.TrustZone = o.TrustZone
		e.ReturnsTrust = o.ReturnsTrust
		e.ReturnsSensitivity = o.ReturnsSensitivity
	}
	return e
}

// replace installs upstream's entries and rebuilds the name index from every
// upstream's, marking a name listed twice ambiguous in the index and never
// on an entry.
func (m *manifest) replace(upstream string, entries []*Entry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.perUp[upstream] = entries
	byName := map[string]*Entry{}
	ambiguous := map[string]bool{}
	for _, list := range m.perUp {
		for _, e := range list {
			if _, dup := byName[e.Tool.Name]; dup {
				ambiguous[e.Tool.Name] = true
				continue
			}
			byName[e.Tool.Name] = e
		}
	}
	m.byName, m.ambiguous = byName, ambiguous
	m.generation++
}

// route returns the entry for name and, when a call to it cannot be
// classified, gateway.ErrUnclassified wrapped with the reason. The entry is
// nil when no upstream, or more than one, lists the name.
func (m *manifest) route(name string) (*Entry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.byName[name]
	switch {
	case !ok:
		return nil, fmt.Errorf("%w: no upstream lists it", gateway.ErrUnclassified)
	case m.ambiguous[name]:
		return nil, fmt.Errorf("%w: two upstreams list it", gateway.ErrUnclassified)
	case !e.Classified:
		return e, fmt.Errorf("%w: %s", gateway.ErrUnclassified, e.why)
	}
	return e, nil
}

// snapshot returns a copy of every entry, in upstream order then list
// order, each marked with the index's ambiguity, and the generation they
// belong to.
func (m *manifest) snapshot(upstreams []string) ([]Entry, uint64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Entry
	for _, up := range upstreams {
		for _, e := range m.perUp[up] {
			c := *e
			c.Ambiguous = m.ambiguous[e.Tool.Name]
			out = append(out, c)
		}
	}
	return out, m.generation
}

// currentGeneration is the manifest's generation as it stands.
func (m *manifest) currentGeneration() uint64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.generation
}
