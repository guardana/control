package main

import (
	"context"
	"fmt"

	adaptermcp "github.com/guardana/control/adapters/mcp"
	"github.com/guardana/control/internal/gatewayconfig"
)

// upstreams connects every upstream through the plane the previous check built,
// reads its tools into the manifest and says which of them the operator's
// overrides classify. An unclassified tool is blocked in every mode but
// OBSERVE, so outside OBSERVE it is not a pass; a name two upstreams list is
// routed to neither, so it is not a pass in any mode. An enforce_api plane has no
// upstream; there it checks the socket's directory and lock file as run's
// Listen does.
func (d *examination) upstreams(ctx context.Context) (string, string, string) {
	if d.plane == nil {
		return verdictUnknown, "upstreams", "the plane was not built, so nothing connected"
	}
	if d.plane.enforce != nil {
		if err := d.plane.enforce.CheckSocketDir(); err != nil {
			return verdictFail, "upstreams", err.Error()
		}
		return verdictOK, "upstreams", fmt.Sprintf("an %s plane calls no upstream; %d tool(s) are classified by name, "+
			"each decision handed to a cooperating client; the socket's directory is this user's alone", gatewayconfig.ListenerEnforceAPI, len(d.cfg.Tools))
	}
	ctx, cancel := context.WithTimeout(ctx, doctorTimeout)
	defer cancel()
	if err := d.plane.startUpstreams(ctx); err != nil {
		return verdictUnknown, "upstreams", "not every upstream answered: " + err.Error()
	}
	entries := d.plane.adapter.Entries()
	unclassified, withheld, shared := d.listTools(entries)
	found := fmt.Sprintf("%d upstream(s) answered, %d tool(s) listed, %d classified, %d unclassified",
		len(d.cfg.Upstreams), len(entries), len(entries)-unclassified, unclassified)
	if withheld > 0 {
		found += fmt.Sprintf(", %d withheld", withheld)
	}
	if d.cfg.Pause.File != "" {
		found += fmt.Sprintf("; %d pause entry(ies) name a tool its upstream does not list", d.strayTools(entries))
	}
	if shared > 0 {
		found += fmt.Sprintf("; %d tool name(s) listed by more than one upstream", shared)
		return verdictUnknown, "upstreams", found + "; a call to such a name reaches no upstream in any mode"
	}
	if unclassified > 0 && d.cfg.ModeName != gatewayconfig.ModeObserve {
		return verdictUnknown, "upstreams", found + "; outside OBSERVE a call to an unclassified tool is blocked with ACTION_UNCLASSIFIED"
	}
	return verdictOK, "upstreams", found
}

// listTools writes a line for each tool name more than one upstream lists and
// for each unclassified tool, and counts the unclassified, the withheld and
// the shared names.
func (d *examination) listTools(entries []adaptermcp.Entry) (unclassified, withheld, shared int) {
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Ambiguous && !seen[e.Tool.Name] {
			seen[e.Tool.Name] = true
			writeLine(d.out, fmt.Sprintf("       %s is listed by more than one upstream, so no call to it can be routed", oneLine(e.Tool.Name)))
		}
		if e.Classified {
			continue
		}
		unclassified++
		if why, ok := e.Withheld(); ok {
			// The name and the definition may hold the very value the scan found.
			withheld++
			writeLine(d.out, fmt.Sprintf("       %s: a tool definition is withheld; %s", oneLine(e.Upstream), oneLine(why)))
			continue
		}
		writeLine(d.out, fmt.Sprintf("       %s/%s is unclassified; its definition fingerprints as %s",
			oneLine(e.Upstream), oneLine(e.Tool.Name), oneLine(e.Fingerprint)))
	}
	return unclassified, withheld, len(seen)
}
