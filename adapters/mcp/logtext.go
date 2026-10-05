package mcp

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"

	"github.com/guardana/control/internal/transportcause"
)

// The causes logText words a failure by when its text is not printed.
const (
	causeWithheld = "the transport failed; its text is withheld, since it can quote the endpoint or what the upstream sent"
	causeDeadline = "the upstream did not answer in time"
	causeCanceled = transportcause.Canceled
	causeClosed   = transportcause.Closed
	causeSocket   = transportcause.Socket
)

var causes = transportcause.Words{Withheld: causeWithheld, Deadline: causeDeadline}

// logText is err as a log line carries it: a transport's failure by the
// operation and a cause chosen from the error's type, a wire error by its
// code alone, since its message is the upstream's own text, and the adapter's
// own error by its own words. No other error's text is printed: the HTTP
// client quotes a malformed answer's bytes, which can echo the request line
// and its Authorization header, and the library words some failures with the
// endpoint and the session id the upstream chose.
func logText(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		return uerr.Op + ": " + causes.Of(uerr.Err)
	}
	var werr *jsonrpc.Error
	if errors.As(err, &werr) {
		return "JSON-RPC error " + strconv.FormatInt(werr.Code, 10)
	}
	var own Error
	if errors.As(err, &own) {
		return own.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return causeDeadline
	}
	return causeWithheld
}
