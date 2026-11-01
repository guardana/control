package otel

import (
	"log/slog"
	"net/http"
	"time"
)

// NewReceiverWithin is NewReceiver answering 413 over maxBytes instead of
// MaxRequestBytes, so a test goes over the bound with small records.
func NewReceiverWithin(sink Sink, log *slog.Logger, cred Credential, maxBytes int64) (http.Handler, error) {
	h, err := NewReceiver(sink, log, cred)
	if err != nil {
		return nil, err
	}
	h.(*receiver).maxBytes = maxBytes
	return h, nil
}

// SetReceiverClock makes the receiver h read its clock from now, so a test
// moves past the bound on its 401 log lines.
func SetReceiverClock(h http.Handler, now func() time.Time) {
	r := h.(*receiver)
	r.refused.mu.Lock()
	defer r.refused.mu.Unlock()
	r.refused.now = now
}

// CauseWithheld is the cause a log line gives a transport failure whose text
// is not printed.
const CauseWithheld = causeWithheld
