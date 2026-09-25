package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	adaptermcp "github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/gatewayconfig"
	"github.com/guardana/control/internal/pause"
)

// strayEntry is a pause entry whose scope names an upstream the
// configuration does not have, or a tool its upstream does not list. A field
// a call does not carry matches, so such an entry pauses only calls that carry
// no provider, which are calls to names no upstream lists, and the operator
// has to see that it pauses less than it says.
type strayEntry struct {
	id, provider, tool string
}

// strayEntries names, in the file's order, every entry of a provider or
// action scope whose provider no configured upstream is called and, when
// listed says the manifest was read, every tool scope whose tool its upstream
// does not list in manifest. A global entry names no upstream and is never
// one; a prompt or resource scope names no tool.
func strayEntries(entries []pause.Entry, upstreams []gatewayconfig.UpstreamConfig, manifest []adaptermcp.Entry, listed bool) []strayEntry {
	var out []strayEntry
	for _, e := range entries {
		s := e.Scope
		if s.Kind == pause.ScopeGlobal {
			continue
		}
		if !slices.ContainsFunc(upstreams, func(u gatewayconfig.UpstreamConfig) bool { return u.Name == s.Provider }) {
			out = append(out, strayEntry{id: e.ID, provider: s.Provider})
			continue
		}
		if !listed || s.Kind != pause.ScopeAction || s.Action != pause.ActionTool {
			continue
		}
		served := func(m adaptermcp.Entry) bool {
			return m.Upstream == s.Provider && m.Tool != nil && m.Tool.Name == s.Name
		}
		if !slices.ContainsFunc(manifest, served) {
			out = append(out, strayEntry{id: e.ID, provider: s.Provider, tool: s.Name})
		}
	}
	return out
}

// strayIDs is strayEntries over the manifest as it stands, by id: what the
// pause file's reader logs at warn with a change of the snapshot. Before the
// upstreams answered there is no manifest, and only the upstreams are
// judged.
func (p *plane) strayIDs(entries []pause.Entry) []string {
	var ids []string
	for _, e := range p.strayEntries(entries) {
		ids = append(ids, e.id)
	}
	return ids
}

func (p *plane) strayEntries(entries []pause.Entry) []strayEntry {
	var manifest []adaptermcp.Entry
	listed := p.listed.Load()
	if listed {
		manifest = p.adapter.Entries()
	}
	return strayEntries(entries, p.cfg.Upstreams, manifest, listed)
}

// warnStray logs, once the upstreams answered, every entry of the snapshot
// the plane serves that pauses only calls to names no upstream lists. The
// reader logs them with each change after this.
func (p *plane) warnStray() {
	if p.poller == nil {
		return
	}
	if ids := p.strayIDs(p.poller.Current().Entries()); len(ids) > 0 {
		p.logger.Warn("pause entries that pause only calls carrying no provider, which are calls to names no upstream lists", "unlisted_only", ids)
	}
}

// hasGlobal reports whether an entry pauses every call.
func hasGlobal(entries []pause.Entry) bool {
	return slices.ContainsFunc(entries, func(e pause.Entry) bool { return e.Scope.Kind == pause.ScopeGlobal })
}

// pauseFile reads the pause file once with every check a start makes, and
// takes no lock and writes nothing: the file's directory is the writers'
// (ADR-0019). It prints the path, the state, the number of entries and each
// entry naming an upstream this configuration does not have, and never a
// reason. A file the start would refuse fails here, naming the cause. The
// entries naming a tool are judged by the upstreams check, once the manifest
// is read.
func (d *examination) pauseFile(context.Context) (string, string, string) {
	if d.cfg.Pause.File == "" {
		return verdictOK, "pause", "disabled: pause.file is not set, so no call can be paused without a restart"
	}
	path := filepath.ToSlash(d.cfg.Resolve(d.cfg.Pause.File))
	snap := pause.Read(d.cfg.Resolve(d.cfg.Pause.File), time.Now(), d.cfg.Pause.PollInterval)
	if snap.State() == pause.Unknown {
		return verdictFail, "pause", fmt.Sprintf("%s cannot be read: %s: %s; run refuses to start on it",
			path, snap.Cause(), snap.Detail())
	}
	entries := snap.Entries()
	d.pauseEntries = entries
	stray := strayEntries(entries, d.cfg.Upstreams, nil, false)
	for _, e := range stray {
		writeLine(d.out, fmt.Sprintf("       pause entry %s names upstream %s, which this configuration does not have: it pauses only calls that carry no provider, which are calls to names no upstream lists",
			oneLine(e.id), oneLine(strconv.Quote(e.provider))))
	}
	return verdictOK, "pause", fmt.Sprintf("%s is %s: %d entry(ies), %d naming an upstream this configuration does not have; read every %s",
		path, snap.State(), len(entries), len(stray), d.cfg.Pause.PollInterval)
}

// strayTools prints each pause entry naming a tool its upstream does not
// list in the manifest just read, and returns how many there are.
func (d *examination) strayTools(manifest []adaptermcp.Entry) int {
	n := 0
	for _, e := range strayEntries(d.pauseEntries, d.cfg.Upstreams, manifest, true) {
		if e.tool == "" {
			continue
		}
		n++
		writeLine(d.out, fmt.Sprintf("       pause entry %s names tool %s, which upstream %s does not list: it pauses only calls to that name that carry no provider, which are calls when no upstream lists it",
			oneLine(e.id), oneLine(strconv.Quote(e.tool)), oneLine(strconv.Quote(e.provider))))
	}
	return n
}
