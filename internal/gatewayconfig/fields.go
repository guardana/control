package gatewayconfig

import (
	"slices"
	"strings"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/brand"
)

// The spellings the enum keys take. A key whose meaning comes from the wire
// contract takes the contract's own name without its prefix; a key this
// package owns takes a lower-case name.
var (
	modeNames         = []string{"OBSERVE", "SHADOW", "WARN", "APPROVE", "ENFORCE", "LOCKDOWN"}
	listenerKinds     = []string{"stateless_http", "stateful_http", "stdio"}
	fsyncPolicies     = []string{"every_record", "interval"}
	unwritablePolicy  = []string{"block", "allow_reads"}
	approvalProviders = []string{ProviderMemory, ProviderFile}
	shapings          = []string{"none", "annotate", "hide"}
	logLevels         = []string{"debug", "info", "warn", "error"}
	effectNames       = contractEffectNames()
	trustZoneNames    = []string{"", "TRUSTED_INTERNAL", "PARTNER", "UNTRUSTED_EXTERNAL", "USER_CONTROLLED", "MODEL_GENERATED"}
	sensitivityNames  = []string{"", "PUBLIC", "INTERNAL", "CONFIDENTIAL", "RESTRICTED", "SECRET"}
	scalarSequenceKey = "args"
)

// contractEffectNames is every effect class the contract declares but the
// zero one, in its declared order and spelled as contract.ParseEffect takes
// it. Read from the descriptor, so the key takes what run will parse.
func contractEffectNames() []string {
	values := controlv1.EffectClass(0).Descriptor().Values()
	names := make([]string, 0, values.Len())
	for i := range values.Len() {
		if v := values.Get(i); v.Number() != 0 {
			names = append(names, strings.TrimPrefix(string(v.Name()), "EFFECT_CLASS_"))
		}
	}
	return names
}

// ModeNames returns the spellings the mode key admits, in the table's order.
// The list is a copy.
func ModeNames() []string { return slices.Clone(modeNames) }

// configFields is every scalar key of the configuration, in the order the
// reference page and `doctor` print them. Nothing outside this table, the
// two item tables, the lists and the maps below is a key at all.
var configFields = []field[Config]{
	enumField("mode", "OBSERVE", modeNames, true, func(c *Config) *string { return &c.ModeName }),
	stringField("project_id", "", true, func(c *Config) *string { return &c.ProjectID }),
	stringField("tenant_id", "", true, func(c *Config) *string { return &c.TenantID }),
	stringField("environment", "", false, func(c *Config) *string { return &c.Environment }),
	enumField("log.level", "info", logLevels, false, func(c *Config) *string { return &c.LogLevel }),

	enumField("listener.kind", "stateless_http", listenerKinds, true, func(c *Config) *string { return &c.Listener.Kind }),
	stringField("listener.address", "127.0.0.1:8080", false, func(c *Config) *string { return &c.Listener.Address }),
	stringField("listener.principal.id", "", true, func(c *Config) *string { return &c.Listener.PrincipalID }),
	stringField("listener.principal.type", "service", false, func(c *Config) *string { return &c.Listener.PrincipalType }),
	stringField("listener.principal.tenant_id", "", false, func(c *Config) *string { return &c.Listener.PrincipalTenant }),
	stringField("listener.agent.id", "", true, func(c *Config) *string { return &c.Listener.AgentID }),
	stringField("listener.agent.framework", "", false, func(c *Config) *string { return &c.Listener.AgentFramework }),
	stringField("listener.agent.version", "", false, func(c *Config) *string { return &c.Listener.AgentVersion }),

	stringField("health.address", "127.0.0.1:8081", false, func(c *Config) *string { return &c.Health.Address }),

	stringField("policy.bundle_id", "", true, func(c *Config) *string { return &c.Policy.BundleID }),
	filePath(stringField("policy.bundle_file", "", true, func(c *Config) *string { return &c.Policy.BundleFile })),
	stringField("policy.key_id", "", true, func(c *Config) *string { return &c.Policy.KeyID }),
	stringField("policy.public_key", "", true, func(c *Config) *string { return &c.Policy.PublicKey }),
	durationField("policy.max_stale", "10m", func(c *Config) *time.Duration { return &c.Policy.MaxStale }),
	boolField("policy.fail_open_read", "false", func(c *Config) *bool { return &c.Policy.FailOpenRead }),

	address(stringField("pdp.identifier", "", false, func(c *Config) *string { return &c.PDP.Identifier })),
	address(stringField("pdp.evaluation_endpoint", "", false, func(c *Config) *string { return &c.PDP.EvaluationEndpoint })),
	durationField("pdp.timeout", "100ms", func(c *Config) *time.Duration { return &c.PDP.Timeout }),
	intField("pdp.max_in_flight", "16", func(c *Config) *int { return &c.PDP.MaxInFlight }),
	boolField("pdp.allow_plaintext", "false", func(c *Config) *bool { return &c.PDP.AllowPlaintext }),
	credential(stringField("pdp.proxy", "", false, func(c *Config) *string { return &c.PDP.Proxy })),

	enumField("approvals.provider", ProviderMemory, approvalProviders, true, func(c *Config) *string { return &c.Approvals.Provider }),
	filePath(stringField("approvals.dir", "", false, func(c *Config) *string { return &c.Approvals.Dir })),
	filePath(stringField("approvals.hold_journal_dir", "", false, func(c *Config) *string { return &c.Approvals.HoldJournalDir })),
	durationField("approvals.ttl", "15m", func(c *Config) *time.Duration { return &c.Approvals.TTL }),
	durationField("approvals.retry_after", "30s", func(c *Config) *time.Duration { return &c.Approvals.RetryAfter }),
	intField("approvals.max_held", "128", func(c *Config) *int { return &c.Approvals.MaxHeld }),
	intField("approvals.max_open", "256", func(c *Config) *int { return &c.Approvals.MaxOpen }),
	intField("approvals.max_records", "1024", func(c *Config) *int { return &c.Approvals.MaxRecords }),
	intField("approvals.max_record_bytes", "16384", func(c *Config) *int { return &c.Approvals.MaxRecordBytes }),
	intField("approvals.reconcile_max", "256", func(c *Config) *int { return &c.Approvals.ReconcileMax }),

	filePath(stringField("pause.file", "", false, func(c *Config) *string { return &c.Pause.File })),
	durationField("pause.poll_interval", "1s", func(c *Config) *time.Duration { return &c.Pause.PollInterval }),

	intField("flow.max_runs", "64", func(c *Config) *int { return &c.Flow.MaxRuns }),

	filePath(stringField("evidence.dir", "", true, func(c *Config) *string { return &c.Evidence.Dir })),
	bytesField("evidence.max_bytes", "1GiB", func(c *Config) *int64 { return &c.Evidence.MaxBytes }),
	bytesField("evidence.segment_bytes", "64MiB", func(c *Config) *int64 { return &c.Evidence.SegmentBytes }),
	bytesField("evidence.closing_reserve", "64KiB", func(c *Config) *int64 { return &c.Evidence.ClosingReserve }),
	enumField("evidence.fsync", "every_record", fsyncPolicies, true, func(c *Config) *string { return &c.Evidence.Fsync }),
	durationField("evidence.fsync_interval", "0s", func(c *Config) *time.Duration { return &c.Evidence.FsyncInterval }),
	enumField("evidence.on_unwritable", "block", unwritablePolicy, true, func(c *Config) *string { return &c.Evidence.OnUnwritable }),

	address(stringField("export.endpoint", "", true, func(c *Config) *string { return &c.Export.Endpoint })),
	boolField("export.allow_plaintext", "false", func(c *Config) *bool { return &c.Export.AllowPlaintext }),
	durationField("export.timeout", "10s", func(c *Config) *time.Duration { return &c.Export.Timeout }),
	intField("export.in_flight", "4", func(c *Config) *int { return &c.Export.InFlight }),
	intField("export.max_batch", "128", func(c *Config) *int { return &c.Export.MaxBatch }),
	durationField("export.linger", "100ms", func(c *Config) *time.Duration { return &c.Export.Linger }),
	durationField("export.backoff", "500ms", func(c *Config) *time.Duration { return &c.Export.Backoff }),
	durationField("export.max_backoff", "30s", func(c *Config) *time.Duration { return &c.Export.MaxBackoff }),

	enumField("list.shaping", "none", shapings, true, func(c *Config) *string { return &c.List.Shaping }),
	durationField("list.ttl", "1m", func(c *Config) *time.Duration { return &c.List.TTL }),

	durationField("upstream.call_timeout", "30s", func(c *Config) *time.Duration { return &c.Upstream.CallTimeout }),
	durationField("upstream.list_timeout", "10s", func(c *Config) *time.Duration { return &c.Upstream.ListTimeout }),
}

// upstreamFields is every key of one entry of the upstreams sequence.
var upstreamFields = []field[UpstreamConfig]{
	stringField("name", "", true, func(u *UpstreamConfig) *string { return &u.Name }),
	address(stringField("endpoint", "", false, func(u *UpstreamConfig) *string { return &u.Endpoint })),
	filePath(stringField("command", "", false, func(u *UpstreamConfig) *string { return &u.Command })),
	stringField("tenant_id", "", false, func(u *UpstreamConfig) *string { return &u.TenantID }),
	stringField("environment", "", false, func(u *UpstreamConfig) *string { return &u.Environment }),
}

// overrideFields is every key of one entry of the overrides sequence.
var overrideFields = []field[OverrideConfig]{
	stringField("upstream", "", true, func(o *OverrideConfig) *string { return &o.Upstream }),
	stringField("tool", "", true, func(o *OverrideConfig) *string { return &o.Tool }),
	stringField("fingerprint", "", true, func(o *OverrideConfig) *string { return &o.Fingerprint }),
	enumField("effect", "", effectNames, true, func(o *OverrideConfig) *string { return &o.Effect }),
	stringField("resource_type", "", true, func(o *OverrideConfig) *string { return &o.ResourceType }),
	stringField("resource_from", "", false, func(o *OverrideConfig) *string { return &o.ResourceFrom }),
	enumField("trust_zone", "", trustZoneNames, false, func(o *OverrideConfig) *string { return &o.TrustZone }),
	enumField("returns.trust", "", trustZoneNames, false, func(o *OverrideConfig) *string { return &o.ReturnsTrust }),
	enumField("returns.sensitivity", "", sensitivityNames, false, func(o *OverrideConfig) *string { return &o.ReturnsSensitivity }),
}

// listField is a key whose value is a sequence of scalars, one item per
// index.
type listField struct {
	path  string
	holds string
	get   func(*Config) *[]string
}

// listFields is every list key of the top-level configuration.
var listFields = []listField{
	{"listener.origins", "one origin a browser may call the listener from",
		func(c *Config) *[]string { return &c.Listener.Origins }},
	{"pdp.informational_context", "one context member an allowing answer of the decision point may carry",
		func(c *Config) *[]string { return &c.PDP.InformationalContext }},
}

// mapField is a key whose leaf names are the operator's: each is a header,
// and its value a credential a program never prints.
type mapField struct {
	prefix string
	holds  string
	get    func(*Config) *map[string]string
}

// mapFields is every map key of the configuration.
var mapFields = []mapField{
	{HeadersPrefix, "one header the exporter sends; its value is a credential and is never printed",
		func(c *Config) *map[string]string { return &c.Export.Headers }},
	{PDPHeadersPrefix, "one header every question to the decision point carries; its value is a credential and is never printed",
		func(c *Config) *map[string]string { return &c.PDP.Headers }},
}

// Field describes one key of the configuration for a reader: where it sits,
// what it takes, its default, whether a value is required, the variable that
// sets it and, for a key that takes one of a closed set, the spellings.
type Field struct {
	Path     string
	Kind     string
	Default  string
	Required bool
	Env      string
	Values   []string
}

// IndexPlaceholder stands for an entry's index, from 0, in the path of a key
// of a sequence entry.
const IndexPlaceholder = "N"

// Fields lists every declared key, in the tables' order: the scalar keys,
// then the keys of an upstream entry and of an override entry with the index
// placeholder in their paths. It is read from the same tables the loader
// binds, so a key the loader reads is a key it lists.
func Fields() []Field {
	var out []Field
	out = appendFields(out, "", configFields)
	out = appendFields(out, "upstreams."+IndexPlaceholder+".", upstreamFields)
	out = appendFields(out, "overrides."+IndexPlaceholder+".", overrideFields)
	return out
}

// NamePlaceholder stands for the operator's own name in the path of a map
// key's leaf.
const NamePlaceholder = "<name>"

// Collection describes a key that holds a list or a map rather than one
// value: its path with the index or name placeholder, the variable that sets
// one element, and what one element holds.
type Collection struct {
	Path  string
	Env   string
	Holds string
}

// Collections lists every list and map key: the top-level lists, the
// arguments of an upstream's command, then the maps. It is read from the
// tables the loader binds, as Fields is.
func Collections() []Collection {
	var out []Collection
	for _, l := range listFields {
		out = appendCollection(out, l.path+"."+IndexPlaceholder, l.holds)
	}
	out = appendCollection(out, "upstreams."+IndexPlaceholder+"."+scalarSequenceKey+"."+IndexPlaceholder,
		"one argument of an upstream's command")
	for _, m := range mapFields {
		out = appendCollection(out, m.prefix+NamePlaceholder, m.holds)
	}
	return out
}

func appendCollection(out []Collection, path, holds string) []Collection {
	return append(out, Collection{Path: path, Env: brand.Env(EnvName(path)), Holds: holds})
}

func appendFields[T any](out []Field, prefix string, fields []field[T]) []Field {
	for _, f := range fields {
		path := prefix + f.path
		out = append(out, Field{
			Path: path, Kind: f.kind, Default: f.def, Required: f.required,
			Env: brand.Env(EnvName(path)), Values: slices.Clone(f.values),
		})
	}
	return out
}
