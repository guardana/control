package recovery

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/guardana/control/internal/gatewayconfig"
	"github.com/guardana/control/internal/pause"
	"github.com/guardana/control/internal/policystate"
	"github.com/guardana/control/internal/runs"
)

// killPoint, when a test sets it, is called before each write a restore
// makes, with the class and what the write is; an error it returns stops
// the restore there, as a crash would.
var killPoint func(step string) error

// Report is what a restore did, class by class, and the operator's files
// the backup holds that a restore never writes.
type Report struct {
	Done []string
	// PutBack names, by key, a signed file the backup holds a copy of: an
	// operator who wants it again copies it back, and a plane takes it only
	// where the raised floor still does.
	PutBack []string
}

// Restore raises the state cfg configures to what the backup in dir holds,
// so no floor ends below the higher of the two, and closes every run at now.
// It refuses, before it writes anything: a backup readBackup refuses; a
// plane that keeps a stop list, with ErrStops; and a plane holding the
// spool's or the hold journal's lock, which it then holds to the end. Under
// the signed authority it writes only what the plane's account owns: the
// floors and the runs' state/. The spool, the hold journal, the approvals
// store and every key are never written, so nothing held resumes.
func Restore(ctx context.Context, cfg *gatewayconfig.Config, dir string, now time.Time) (_ Report, err error) {
	if now.IsZero() {
		return Report{}, errors.New("recovery: the clock reads zero")
	}
	m, err := readBackup(cfg, dir)
	if err != nil {
		return Report{}, err
	}
	held := map[string]bool{}
	for _, e := range m.Entries {
		held[e.Class] = true
	}
	if cfg.Reaction.Stops != "" || held[ClassStops] {
		return Report{}, fmt.Errorf("%w: reaction.stops; restore a plane without one, or wait for the build that carries the list", ErrStops)
	}
	release, err := holdPlane(cfg)
	if err != nil {
		return Report{}, err
	}
	defer func() { err = errors.Join(err, release()) }()
	r := restorer{ctx: ctx, cfg: cfg, dir: dir, now: now, held: held}
	for _, c := range classes {
		if err := r.class(c); err != nil {
			return r.out, fmt.Errorf("%s: %w", c.key, err)
		}
	}
	return r.out, nil
}

type restorer struct {
	ctx  context.Context
	cfg  *gatewayconfig.Config
	dir  string
	now  time.Time
	held map[string]bool
	out  Report
}

func (r *restorer) step(c class) func(string) error {
	return func(what string) error {
		if killPoint == nil {
			return nil
		}
		return killPoint(c.name + ": " + what)
	}
}

// class restores one class: from the backup's copy where it holds one, and
// the runs whether or not it does, since every run closes at a restore.
func (r *restorer) class(c class) error {
	if !r.restores(c) {
		return nil
	}
	live, err := filepath.Abs(r.cfg.Resolve(c.value(r.cfg)))
	if err != nil {
		return err
	}
	from := filepath.Join(r.dir, filepath.FromSlash(c.name))
	step := r.step(c)
	signed := r.cfg.Approvals.Authority == gatewayconfig.AuthoritySigned
	var did string
	switch c.name {
	case ClassPlaneFloor:
		var got policystate.Restored
		got, err = policystate.RestorePlane(r.ctx, live, from, r.now, step)
		did = fmt.Sprintf("%d floors stamped at the restore", len(got.Written))
	case ClassRouteFloor:
		var got policystate.Restored
		got, err = policystate.RestoreRoute(r.ctx, live, from, step)
		did = fmt.Sprintf("%d route floors raised", len(got.Written))
	case ClassAuthorityFloor:
		var got policystate.Restored
		got, err = policystate.RestoreAuthority(r.ctx, live, from, step)
		did = fmt.Sprintf("%d authority floors raised", len(got.Written))
	case ClassPauseFloor:
		var got policystate.Restored
		got, err = policystate.RestorePause(r.ctx, live, from, r.cfg.TenantID, r.cfg.ProjectID, step)
		did = fmt.Sprintf("%d pause floor raised", len(got.Written))
	case ClassRuns:
		did, err = r.runs(live, from, signed, step)
	case ClassPause:
		did, err = r.pause(live, from, signed, step)
	}
	if err != nil {
		return err
	}
	r.out.Done = append(r.out.Done, c.key+": "+did)
	return nil
}

// restores reports whether a restore touches class c: configured, under an
// authority it applies to, and held by the backup, or the runs, which close
// whether or not it holds them.
func (r *restorer) restores(c class) bool {
	if c.value(r.cfg) == "" || !c.applies(r.cfg.Approvals.Authority) {
		return false
	}
	return r.held[c.name] || c.name == ClassRuns
}

func (r *restorer) runs(live, from string, signed bool, step func(string) error) (string, error) {
	if !r.held[ClassRuns] {
		from = ""
	}
	if signed {
		got, err := runs.RestoreSigned(r.ctx, live, from, step)
		if from != "" {
			r.out.PutBack = append(r.out.PutBack, "runs.dir: records/")
		}
		return fmt.Sprintf("%d closes carried, %d states joined", got.Carried, got.States), err
	}
	got, err := runs.RestoreUnsigned(r.ctx, live, from, r.now, step)
	return fmt.Sprintf("%d records closed, %d states joined", got.Closed, got.States), err
}

func (r *restorer) pause(live, from string, signed bool, step func(string) error) (string, error) {
	if signed {
		r.out.PutBack = append(r.out.PutBack, "pause.file")
		return "left to an operator; its floor is raised", nil
	}
	raw, err := readListed(filepath.Join(from, filepath.Base(live)), pause.MaxFileBytes)
	if err != nil {
		return "", err
	}
	if err := step("merge"); err != nil {
		return "", err
	}
	n, err := pause.Merge(r.ctx, live, raw)
	return fmt.Sprintf("%d entries added", n), err
}
