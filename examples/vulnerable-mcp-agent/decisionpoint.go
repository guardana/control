package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/guardana/control/internal/loopback"
)

// evaluationPath is the access evaluation endpoint under an identifier with
// no path of its own.
const evaluationPath = "/access/v1/evaluation"

// decisionPoint answers its published metadata and holds every question
// until the asker gives up, which is how a decision point that is up but not
// deciding looks from the plane.
type decisionPoint struct {
	identifier string
	journal    *journal
}

// serveDecisionPoint binds addr, which has to be a loopback IP literal, and
// serves the decision point there until stop is called. The identifier is
// the address it bound, as its metadata publishes it.
func serveDecisionPoint(addr string, j *journal) (identifier string, stop func(), err error) {
	if err := loopback.Check(addr); err != nil {
		return "", nil, err
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		return "", nil, err
	}
	dp := &decisionPoint{identifier: "http://" + ln.Addr().String(), journal: j}
	srv := &http.Server{Handler: dp.handler(), ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return dp.identifier, func() { _ = srv.Close() }, nil
}

func (dp *decisionPoint) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/authzen-configuration", dp.metadata)
	mux.HandleFunc("POST "+evaluationPath, dp.evaluation)
	return mux
}

func (dp *decisionPoint) metadata(w http.ResponseWriter, _ *http.Request) {
	body, err := json.Marshal(map[string]string{
		"policy_decision_point":      dp.identifier,
		"access_evaluation_endpoint": dp.identifier + evaluationPath,
	})
	if err != nil {
		http.Error(w, "the metadata could not be encoded", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// evaluation records the question and never answers it: it returns only once
// the asker has gone, having written nothing.
func (dp *decisionPoint) evaluation(w http.ResponseWriter, r *http.Request) {
	if err := dp.journal.record(entry{Asked: evaluationPath}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	<-r.Context().Done()
}
