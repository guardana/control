package otel

import (
	"log/slog"
	"net/http"
)

// NewReceiverWithin is NewReceiver answering 413 over maxBytes instead of
// MaxRequestBytes, so a test goes over the bound with small records.
func NewReceiverWithin(sink Sink, log *slog.Logger, maxBytes int64) (http.Handler, error) {
	h, err := NewReceiver(sink, log)
	if err != nil {
		return nil, err
	}
	h.(*receiver).maxBytes = maxBytes
	return h, nil
}
