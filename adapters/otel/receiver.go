package otel

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
)

// ErrNoSink is a receiver built with nothing to keep what it accepts.
const ErrNoSink Error = "otel: no sink for the records a receiver accepts"

// ErrSinkRefused is what a sink's error matches, with errors.Is, when the sink
// will never keep the events it was handed, as against not keeping them now.
// The receiver answers it with 400, as it answers a request it cannot read.
const ErrSinkRefused Error = "otel: the sink refuses the records for good"

// Sink keeps the events a receiver accepted. Append returns nil only once
// every event it was handed is durable, because the answer the receiver gives
// on a nil lets the sender release them.
type Sink interface {
	Append(ctx context.Context, events []*controlv1.Event) error
}

// NewReceiver returns the handler of OTLP/HTTP logs requests in the JSON
// encoding that hands the events ReadRequest reads to sink, and answers:
//
//   - 200 with an empty body once sink.Append returned nil, which is what
//     lets the exporter release the records;
//   - 400 for a request ReadRequest refuses or the sink refuses with
//     ErrSinkRefused, and 413 for one over MaxRequestBytes, which the
//     exporter answers by halving the batch and in the end quarantining the
//     record it cannot deliver;
//   - 503 when the sink fails otherwise, which the exporter answers by sending the same
//     records again, and which fills the plane's spool while it lasts;
//   - 405 for a method other than POST, and 415 for a body that is not
//     application/json in UTF-8, or that carries a content coding.
//
// No answer carries a body, and a log line names a refusal without quoting
// the request.
func NewReceiver(sink Sink, log *slog.Logger) (http.Handler, error) {
	if sink == nil {
		return nil, ErrNoSink
	}
	if log == nil {
		log = slog.Default()
	}
	return &receiver{sink: sink, log: log, maxBytes: MaxRequestBytes}, nil
}

type receiver struct {
	sink Sink
	log  *slog.Logger
	// maxBytes is MaxRequestBytes; a test lowers it to go over it cheaply.
	maxBytes int64
}

func (h *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if why := unsupported(r.Header); why != "" {
		h.log.Warn("otel: request refused", "status", http.StatusUnsupportedMediaType, "why", why)
		w.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, h.maxBytes))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		h.refuse(w, http.StatusRequestEntityTooLarge, ErrRequestTooLarge)
		return
	case err != nil:
		h.refuse(w, http.StatusBadRequest, err)
		return
	}
	events, err := ReadRequest(body)
	switch {
	case errors.Is(err, ErrRequestTooLarge):
		h.refuse(w, http.StatusRequestEntityTooLarge, err)
		return
	case err != nil:
		h.refuse(w, http.StatusBadRequest, err)
		return
	}
	if len(events) > 0 {
		err := h.sink.Append(r.Context(), events)
		if errors.Is(err, ErrSinkRefused) {
			h.refuse(w, http.StatusBadRequest, err)
			return
		}
		if err != nil {
			h.log.Error("otel: the records could not be kept; the sender will send them again",
				"status", http.StatusServiceUnavailable, "records", len(events), "error", err.Error())
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (h *receiver) refuse(w http.ResponseWriter, status int, err error) {
	h.log.Warn("otel: request refused", "status", status, "why", err.Error())
	w.WriteHeader(status)
}

// unsupported says why a request's headers name a body this receiver does not
// read, or nothing when they name JSON in UTF-8 with no content coding.
func unsupported(header http.Header) string {
	if coding := header.Get("Content-Encoding"); coding != "" && !strings.EqualFold(coding, "identity") {
		return "the body carries a content coding"
	}
	media, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	switch {
	case err != nil:
		return "the content type does not parse"
	case media != "application/json":
		return "the content type is not application/json"
	}
	for name, value := range params {
		if name != "charset" || !strings.EqualFold(value, "utf-8") {
			return "the content type carries a parameter other than charset=utf-8"
		}
	}
	return ""
}
