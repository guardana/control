package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	observev1 "github.com/guardana/control/api/gen/go/guardana/control/observe/v1alpha1"
	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/coverage"
	"github.com/guardana/control/internal/observe"
	"github.com/guardana/control/internal/runs"
	"github.com/guardana/control/internal/supervise"
	"google.golang.org/protobuf/encoding/protojson"
)

// readRun reads the opened run's record. A local run's id, a run no record
// holds and a child run are refused: a child's events carry its own id, so
// supervising one alone would judge part of a task as the whole.
func readRun(dir, id string) (supervise.Run, error) {
	if !observe.ValidRunID(id) {
		return supervise.Run{}, refusedInput("run", id, supervise.ErrRunID)
	}
	admin, err := runs.OpenAdmin(dir)
	if err != nil {
		return supervise.Run{}, refusedInput("runs", dir, err)
	}
	rec, found, err := findRecord(admin, id)
	if err = errors.Join(err, admin.Close()); err != nil {
		return supervise.Run{}, refusedInput("runs", dir, err)
	}
	switch {
	case !found:
		return supervise.Run{}, refusedInput("run", id, errors.New("no record of it in the runs directory"))
	case rec.Parent != "":
		return supervise.Run{}, refusedInput("run", id, errors.New("a child run is not supervised on its own"))
	}
	return supervise.Run{ID: rec.ID, Tenant: rec.Who.TenantID, Closed: rec.Closed()}, nil
}

// findRecord is id's record, and false when a complete listing holds none. A
// listing the bound stopped cannot say a run is absent.
func findRecord(admin *runs.Admin, id string) (runs.Record, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), runsLockWait)
	defer cancel()
	l, err := admin.List(ctx, runsListBound)
	if err != nil {
		return runs.Record{}, false, err
	}
	for _, r := range l.Records {
		if r.ID == id {
			return r, true, nil
		}
	}
	if !l.Complete {
		return runs.Record{}, false, fmt.Errorf("%w; it stops at %d runs", errIncomplete, runsListBound)
	}
	return runs.Record{}, false, nil
}

// readSupervisedExport reads an export as coverage does, then takes its
// events from the same bytes: coverage.ReadExport judges the export and keeps
// no event.
func readSupervisedExport(path string) (supervise.Export, error) {
	raw, err := readExportFile(path)
	if err != nil {
		return supervise.Export{}, err
	}
	x, err := coverage.ReadExport(bytes.NewReader(raw))
	if err != nil {
		return supervise.Export{}, refusedInput("evidence", path, err)
	}
	events, err := exportEvents(raw)
	if err != nil {
		return supervise.Export{}, refusedInput("evidence", path, err)
	}
	return supervise.Export{Events: events, Whole: x.Whole, NotWhole: x.NotWhole}, nil
}

// exportEvents decodes the event records of an export coverage.ReadExport
// accepted. Bytes after the last newline are a line cut short, which that
// reader does not read either.
func exportEvents(raw []byte) ([]*controlv1.Event, error) {
	lines := bytes.Split(raw, []byte("\n"))
	var out []*controlv1.Event
	for _, line := range lines[:len(lines)-1] {
		var rec struct {
			Type  string          `json:"type"`
			Event json.RawMessage `json:"event"`
		}
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, err
		}
		if rec.Type != "event" {
			continue
		}
		ev := &controlv1.Event{}
		if err := protojson.Unmarshal(rec.Event, ev); err != nil {
			return nil, errors.New("an event does not decode")
		}
		out = append(out, ev)
	}
	return out, nil
}

// readSupervisedSource reads a source as coverage does: a descriptor that
// does not exist is reported absent, and a log directory with no log yet
// gives a source never heard.
func readSupervisedSource(descriptor, logDir string) (supervise.Source, bool, error) {
	src, present, err := readSource(descriptor, logDir)
	if err != nil || !present {
		return supervise.Source{}, present, err
	}
	out := supervise.Source{
		SourceID:         src.Descriptor.GetSourceId(),
		HeartbeatSeconds: src.Descriptor.GetHeartbeatSeconds(),
	}
	out.LastHeard, out.Heard = lastHeard(out.SourceID, src.Records)
	for _, r := range src.Records {
		if o := r.GetObservation(); o != nil {
			out.Observations = append(out.Observations, o)
		}
	}
	return out, true, nil
}

// lastHeard is the newest event time an import report of the source names,
// each taken no later than its report's receive time, as coverage computes
// it; a report without both times does not count.
func lastHeard(sourceID string, records []*observev1.Record) (time.Time, bool) {
	var last time.Time
	heard := false
	for _, r := range records {
		rep := r.GetImportReport()
		latest, received := rep.GetLatestEventTime(), rep.GetReceivedTime()
		if rep.GetSource().GetSourceId() != sourceID || !latest.IsValid() || !received.IsValid() {
			continue
		}
		t := latest.AsTime()
		if rt := received.AsTime(); t.After(rt) {
			t = rt
		}
		if !heard || t.After(last) {
			last, heard = t, true
		}
	}
	return last, heard
}

// unreadSources names each --source the run could not be judged against: a
// descriptor that does not exist, by its path, and each source
// supervise.Evaluate judged never heard or silent, by its id.
func unreadSources(in supervise.Input, res *supervise.Result) []string {
	var out []string
	for _, path := range in.SourcesNotRead {
		out = append(out, "source "+oneLine(path)+" absent")
	}
	for _, id := range res.NeverHeard {
		out = append(out, "source "+oneLine(id)+" never heard")
	}
	for _, id := range res.Silent {
		out = append(out, "source "+oneLine(id)+" silent")
	}
	return out
}
