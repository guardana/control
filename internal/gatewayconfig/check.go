package gatewayconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// check refuses a configuration that is complete in its own keys but says two
// things at once, or says nothing where a value is needed. What the pipeline,
// the adapter and the spool refuse is left to them, so their message names
// their own rule; what only this file knows is refused here, naming the key.
func (c *Config) check() error {
	if err := checkRequired(c, configFields, ""); err != nil {
		return err
	}
	for _, check := range []func() error{c.checkListener, c.checkPDP, c.checkApprovals, c.checkPause, c.checkFlow, c.checkEvidence, c.checkExport, c.checkShaping, c.checkUpstreams, c.checkOverrides} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

// checkListener refuses an address on a listener that has none, and an empty
// origin.
func (c *Config) checkListener() error {
	if c.Listener.Kind == "stdio" && c.wasSet("listener.address") {
		return fmt.Errorf("listener.address: a stdio listener serves one agent over a pipe and binds nothing")
	}
	if c.Listener.Kind != "stdio" && c.Listener.Address == "" {
		return fmt.Errorf("listener.address: an HTTP listener needs an address to bind")
	}
	for i, origin := range c.Listener.Origins {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("listener.origins.%d: %s is not an origin such as http://localhost:5173", i, quoteValue(origin))
		}
		// A page on the operator's own machine is only the operator's machine
		// while the listener is not reachable from elsewhere.
		if loopback(u.Hostname()) && !loopback(listenerHost(c.Listener.Address)) {
			return fmt.Errorf("listener.origins.%d: a loopback origin is admitted only while listener.address is on loopback, and it is %s", i, quoteValue(c.Listener.Address))
		}
	}
	return nil
}

// listenerHost is the host an address binds, and the empty string for an
// address that binds every interface.
func listenerHost(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}
	return host
}

// loopback reports whether a host names this machine and nothing else.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// checkPDP refuses a decision point key set with no decision point to apply
// it to: a key that applies to nothing is a setting the operator believes is
// in force. What the decision point's client refuses is left to it.
func (c *Config) checkPDP() error {
	if c.PDP.Identifier != "" {
		return nil
	}
	var set []string
	for _, f := range configFields {
		if f.path != "pdp.identifier" && c.wasSet(f.path) {
			set = append(set, f.path)
		}
	}
	for _, l := range listFields {
		if len(*l.get(c)) > 0 {
			set = append(set, l.path)
		}
	}
	for _, m := range mapFields {
		if len(*m.get(c)) > 0 {
			set = append(set, strings.TrimSuffix(m.prefix, "."))
		}
	}
	for _, key := range set {
		if strings.HasPrefix(key, "pdp.") {
			return fmt.Errorf("%s: set, and pdp.identifier names no decision point for it to apply to", key)
		}
	}
	return nil
}

// checkApprovals pairs the provider with what it needs, refuses a mode no
// provider can serve, and keeps the hold journal out of a directory another
// writer owns (ADR-0016).
func (c *Config) checkApprovals() error {
	if err := c.checkApprovalBounds(); err != nil {
		return err
	}
	if c.Approvals.Provider != ProviderFile {
		// Under APPROVE every allowed material call is held, and a store
		// nothing outside this process can answer would hold every one of
		// them until it expired: a total denial wearing a pending state.
		if c.ModeName == "APPROVE" {
			return fmt.Errorf("approvals.provider: %s under APPROVE; nothing outside this process answers a memory-held request, so every held call would expire (ADR-0016)", c.Approvals.Provider)
		}
		for _, key := range []string{"approvals.dir", "approvals.hold_journal_dir"} {
			if c.wasSet(key) {
				return fmt.Errorf("%s: only the %s provider reads a directory, and the provider is %s", key, ProviderFile, c.Approvals.Provider)
			}
		}
		return nil
	}
	switch {
	case c.Approvals.Dir == "":
		return fmt.Errorf("approvals.dir: the %s provider keeps its records in a directory, and no directory is named", ProviderFile)
	case c.Approvals.HoldJournalDir == "":
		return fmt.Errorf("approvals.hold_journal_dir: the %s provider keeps a journal of its own holds, and no directory is named; without one a hold lost to a restart is never closed (ADR-0016)", ProviderFile)
	}
	return c.checkJournalDir()
}

// checkApprovalBounds refuses a bound that is not positive. A bound of zero
// would leave a store that takes no record, or a reconciliation that reads
// nothing and could still report itself done.
func (c *Config) checkApprovalBounds() error {
	for _, bound := range []struct {
		key string
		n   int
	}{
		{"approvals.max_records", c.Approvals.MaxRecords},
		{"approvals.max_record_bytes", c.Approvals.MaxRecordBytes},
		{"approvals.reconcile_max", c.Approvals.ReconcileMax},
	} {
		if bound.n <= 0 {
			return fmt.Errorf("%s: %d; the bound has to be positive", bound.key, bound.n)
		}
	}
	return nil
}

// checkJournalDir keeps the hold journal's directory out of the spool's,
// which the spool owns and locks, and out of the approvals directory, which
// an approver may write. The journal is as trusted as the evidence, so
// nobody but the plane writes it.
func (c *Config) checkJournalDir() error {
	journal := c.Resolve(c.Approvals.HoldJournalDir)
	for _, other := range []struct{ key, dir string }{
		{"evidence.dir", c.Resolve(c.Evidence.Dir)},
		{"approvals.dir", c.Resolve(c.Approvals.Dir)},
	} {
		overlap, err := overlaps(journal, other.dir)
		if err != nil {
			return fmt.Errorf("approvals.hold_journal_dir: %w", err)
		}
		if overlap {
			return fmt.Errorf("approvals.hold_journal_dir: %q shares a path with %s %q; the journal is written by the plane alone (ADR-0016)",
				c.Approvals.HoldJournalDir, other.key, other.dir)
		}
	}
	return nil
}

// overlaps reports whether two directories are the same one or one of them
// sits inside the other. A pair this build cannot compare is reported as
// overlapping, because a journal that may share a directory with another
// writer is the answer nothing can take back.
func overlaps(a, b string) (bool, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return true, fmt.Errorf("%q is not a path this build can resolve: %w", a, err)
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return true, fmt.Errorf("%q is not a path this build can resolve: %w", b, err)
	}
	return inside(absA, absB) || inside(absB, absA), nil
}

// inside reports whether child is parent or sits under it.
func inside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// The bounds of pause.poll_interval. A pause bites up to one interval after
// it is written, so the ceiling keeps an emergency stop an emergency stop; the
// floor keeps the reader from spinning on the file.
const (
	minPausePoll = 100 * time.Millisecond
	maxPausePoll = time.Minute
)

// checkPause bounds the poll interval, refuses one set with no pause file to
// apply to, and keeps the file out of every directory a plane locks: the
// spool's, the approvals store's and the hold journal's, each locked while a
// plane runs, where a writer of the pause file, which locks the file's own
// directory, could never write (ADR-0019). The directories are compared by
// their paths and, where both exist, by identity, so a link or a spelling in
// another case that names a locked directory is refused too.
func (c *Config) checkPause() error {
	if c.Pause.File == "" {
		if c.wasSet("pause.poll_interval") {
			return fmt.Errorf("pause.poll_interval: set, and pause.file names no pause file for it to apply to")
		}
		return nil
	}
	if c.Pause.PollInterval < minPausePoll || c.Pause.PollInterval > maxPausePoll {
		return fmt.Errorf("pause.poll_interval: %v is outside %v to %v", c.Pause.PollInterval, minPausePoll, maxPausePoll)
	}
	dir := filepath.Dir(c.Resolve(c.Pause.File))
	for _, other := range []struct{ key, dir string }{
		{"evidence.dir", c.Evidence.Dir},
		{"approvals.dir", c.Approvals.Dir},
		{"approvals.hold_journal_dir", c.Approvals.HoldJournalDir},
	} {
		if other.dir == "" {
			continue
		}
		locked := c.Resolve(other.dir)
		within, err := insideDir(dir, locked)
		if err == nil && !within {
			within, err = underSameDir(dir, locked)
		}
		if err != nil {
			return fmt.Errorf("pause.file: %w", err)
		}
		if within {
			return fmt.Errorf("pause.file: %q sits in %s %q, which a running plane locks, so no pause could be written while it runs",
				c.Pause.File, other.key, other.dir)
		}
	}
	return nil
}

// insideDir reports whether directory a is b or sits under it. A pair this
// build cannot compare is reported as inside, the answer that refuses.
func insideDir(a, b string) (bool, error) {
	absA, err := filepath.Abs(a)
	if err != nil {
		return true, fmt.Errorf("%q is not a path this build can resolve: %w", a, err)
	}
	absB, err := filepath.Abs(b)
	if err != nil {
		return true, fmt.Errorf("%q is not a path this build can resolve: %w", b, err)
	}
	return inside(absA, absB), nil
}

// underSameDir reports whether directory a, or a directory it sits in, is
// the very directory b, compared by identity along a's path as the system
// resolves it. A directory b that does not exist yet is left to the
// comparison of paths; a pair this build cannot compare is reported as
// inside, the answer that refuses.
func underSameDir(a, b string) (bool, error) {
	target, err := os.Stat(b)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return true, fmt.Errorf("%q cannot be compared: %w", b, err)
	}
	existing, err := existingAncestor(a)
	if err != nil {
		return true, err
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return true, fmt.Errorf("%q cannot be compared: %w", a, err)
	}
	for dir := resolved; ; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil {
			return true, fmt.Errorf("%q cannot be compared: %w", dir, err)
		}
		if os.SameFile(info, target) {
			return true, nil
		}
		if filepath.Dir(dir) == dir {
			return false, nil
		}
	}
}

// existingAncestor is dir, made absolute, or the nearest directory above it
// that exists. What does not exist yet cannot be a link.
func existingAncestor(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("%q is not a path this build can resolve: %w", dir, err)
	}
	for {
		_, err := os.Lstat(abs)
		switch {
		case err == nil:
			return abs, nil
		case !errors.Is(err, fs.ErrNotExist) || filepath.Dir(abs) == abs:
			return "", fmt.Errorf("%q cannot be compared: %w", dir, err)
		}
		abs = filepath.Dir(abs)
	}
}

// checkEvidence pairs the fsync policy with its interval: a timer under the
// policy that has none, or no timer under the policy that needs one, is a
// loss window nobody chose.
func (c *Config) checkEvidence() error {
	switch {
	case c.Evidence.Fsync == "interval" && c.Evidence.FsyncInterval <= 0:
		return fmt.Errorf("evidence.fsync_interval: the interval policy needs a positive interval, which is what a power loss may cost")
	case c.Evidence.Fsync == "every_record" && c.Evidence.FsyncInterval != 0:
		return fmt.Errorf("evidence.fsync_interval: only the interval policy takes one")
	case c.Evidence.SegmentBytes > c.Evidence.MaxBytes:
		return fmt.Errorf("evidence.segment_bytes: %d is over evidence.max_bytes %d", c.Evidence.SegmentBytes, c.Evidence.MaxBytes)
	case c.Evidence.ClosingReserve > c.Evidence.MaxBytes:
		return fmt.Errorf("evidence.closing_reserve: %d is over evidence.max_bytes %d", c.Evidence.ClosingReserve, c.Evidence.MaxBytes)
	}
	return nil
}

// checkExport refuses an endpoint that is not a collector's URL.
func (c *Config) checkExport() error {
	if !httpURL(c.Export.Endpoint) {
		return notHTTP("export.endpoint", c.Export.Endpoint)
	}
	return nil
}

// httpURL reports whether value is an http or https URL with a host.
func httpURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}

// notHTTP refuses a value of key that is not an http or https URL, quoting it
// only where ShowAddress would print it.
func notHTTP(key, value string) error {
	if shown := ShowAddress(value); shown != value {
		return fmt.Errorf("%s: %s, and it is not an http or https URL", key, shown)
	}
	return fmt.Errorf("%s: %s is not an http or https URL", key, quoteValue(value))
}

// checkShaping holds ADR-0013's rule that shaping only subtracts where the
// mode enforces: under OBSERVE and SHADOW a list is answered as the upstream
// serves it.
func (c *Config) checkShaping() error {
	if c.List.Shaping != "none" && (c.ModeName == "OBSERVE" || c.ModeName == "SHADOW") {
		return fmt.Errorf("list.shaping: %s under %s; shaping is none in a mode that does not enforce (ADR-0013)", c.List.Shaping, c.ModeName)
	}
	return nil
}

// checkUpstreams refuses an upstream that names neither a URL nor a command,
// or both, and arguments without a command.
func (c *Config) checkUpstreams() error {
	if len(c.Upstreams) == 0 {
		return fmt.Errorf("upstreams: no upstream; the gateway has nothing to serve")
	}
	for i, up := range c.Upstreams {
		at := fmt.Sprintf("upstreams.%d.", i)
		if err := checkRequired(&c.Upstreams[i], upstreamFields, at); err != nil {
			return err
		}
		switch {
		case (up.Endpoint == "") == (up.Command == ""):
			return fmt.Errorf("%s: name either an endpoint or a command, not both and not neither", at[:len(at)-1])
		case up.Command == "" && len(up.Args) > 0:
			return fmt.Errorf("%sargs: arguments without a command", at)
		}
		if up.Endpoint != "" && !httpURL(up.Endpoint) {
			return notHTTP(at+"endpoint", up.Endpoint)
		}
	}
	return nil
}

// checkOverrides refuses a classification of a tool on a server this
// configuration does not have, which would classify nothing and leave the
// tool blocked.
func (c *Config) checkOverrides() error {
	names := map[string]bool{}
	for _, up := range c.Upstreams {
		names[up.Name] = true
	}
	for i := range c.Overrides {
		at := fmt.Sprintf("overrides.%d.", i)
		if err := checkRequired(&c.Overrides[i], overrideFields, at); err != nil {
			return err
		}
		if !names[c.Overrides[i].Upstream] {
			return fmt.Errorf("%supstream: %s is not a configured upstream", at, quoteValue(c.Overrides[i].Upstream))
		}
	}
	return nil
}

// wasSet reports whether the file or the environment set the key, as against
// the key standing at its default.
func (c *Config) wasSet(path string) bool {
	_, ok := c.source[path]
	return ok
}
