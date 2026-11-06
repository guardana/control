package main

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/guardana/control/internal/operators"
	"github.com/guardana/control/internal/pause"
	"github.com/guardana/control/internal/policy"
	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/trust"
)

// signedPauseFile is pauseFile under the signed authority: the file read as
// the plane reads it, its signature checked against the roster file, its
// signer's grant and its serial against the pause floor, which is read and
// never raised: a check that raised it would be a start. It returns the
// entries in force, or the line that fails.
func (d *examination) signedPauseFile() ([]pause.Entry, string, error) {
	path := d.cfg.Resolve(d.cfg.Pause.File)
	f, err := pause.Inspect(path)
	if err != nil {
		return nil, "", err
	}
	body, env := f.Body, f.Envelope
	root, err := readRouteKey(d.cfg.Resolve(d.cfg.Trust.RootPublicKey))
	if err != nil {
		return nil, "", fmt.Errorf("trust.root_public_key: %w", err)
	}
	roster, err := d.roster(root)
	if err != nil {
		return nil, "", fmt.Errorf("trust.roster: %w", err)
	}
	subject, err := pauseSigner(roster, env, body, d.cfg.TenantID, d.cfg.ProjectID)
	if err != nil {
		return nil, "", err
	}
	floor, err := policystate.ReadPause(d.cfg.Resolve(d.cfg.Pause.FloorDir), d.cfg.TenantID, d.cfg.ProjectID)
	switch {
	case err != nil:
		return nil, "", fmt.Errorf("pause.floor_dir: %w", err)
	case body.Serial < floor.Serial:
		return nil, "", fmt.Errorf("%w: serial %d, the floor's %d", pause.ErrBelowFloor, body.Serial, floor.Serial)
	case body.Serial == floor.Serial && body.Digest != floor.Digest:
		return nil, "", fmt.Errorf("%w: serial %d", pause.ErrForked, body.Serial)
	}
	return body.Entries, fmt.Sprintf("signed by %s (key %s) at serial %d, the floor at %d", oneLine(subject), body.KeyID, body.Serial, floor.Serial), nil
}

// pauseSigner is the subject of the operator key of roster that signed env,
// granted operate or admin over tenant and project, refusing as the plane's
// authenticator and authorizer would.
func pauseSigner(roster trust.Document, env policy.StatementEnvelope, body pause.SignedDocument, tenant, project string) (string, error) {
	if body.Tenant != tenant || body.Project != project {
		return "", pause.ErrOtherScope
	}
	keys := roster.Keys()
	i := slices.IndexFunc(keys, func(k trust.Key) bool { return k.ID() == body.KeyID })
	if i < 0 || keys[i].Role() != trust.RoleOperator {
		return "", fmt.Errorf("%w: the roster names no operator key %s", pause.ErrUnauthenticated, body.KeyID)
	}
	key := keys[i]
	if err := key.Verify(operators.PausePayloadType, env.Payload, env.Signatures[0].Signature); err != nil {
		return "", fmt.Errorf("%w: %w", pause.ErrUnauthenticated, err)
	}
	if !slices.ContainsFunc(key.Grants(), func(g trust.Grant) bool {
		return g.Tenant == tenant && g.Project == project && g.Level >= trust.LevelOperate
	}) {
		return "", fmt.Errorf("%w: %s", pause.ErrUnauthorized, oneLine(key.Subject()))
	}
	return key.Subject(), nil
}

// signedPauseVerdict is pauseFile's verdict over what signedPauseFile found.
func (d *examination) signedPauseVerdict() (string, string, string) {
	path := filepath.ToSlash(d.cfg.Resolve(d.cfg.Pause.File))
	entries, signer, err := d.signedPauseFile()
	if err != nil {
		return verdictFail, "pause", fmt.Sprintf("%s cannot be taken under the signed authority: %s; run refuses to start on it", path, oneLine(err.Error()))
	}
	d.pauseEntries = entries
	stray := strayEntries(entries, targetsOf(d.cfg, nil, false))
	shares := d.printCoverage(entries, stray)
	state := pause.Clear
	if len(entries) > 0 {
		state = pause.Paused
	}
	return verdictOK, "pause", fmt.Sprintf("%s is %s: %d entry(ies), %d naming a provider this configuration does not have; %s; %s; read every %s",
		path, state, len(entries), len(stray), signer, shares, d.cfg.Pause.PollInterval)
}
