package mcp

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// The causes logText words a failure by when its text is not printed.
const (
	causeWithheld = "the transport failed; its text is withheld, since it can quote the endpoint or what the upstream sent"
	causeDeadline = "the upstream did not answer in time"
	causeCanceled = "the request was canceled"
	causeClosed   = "the connection closed before the answer ended"
	causeSocket   = "failed"
)

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
		return uerr.Op + ": " + transportCause(uerr.Err)
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

// transportCause words the error under a url.Error by its type alone.
func transportCause(err error) string {
	var op *net.OpError
	var timeout net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return causeDeadline
	case errors.Is(err, context.Canceled):
		return causeCanceled
	case errors.As(err, &op):
		return socketCause(op)
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return causeClosed
	}
	if cause := tlsCause(err); cause != "" {
		return cause
	}
	return causeWithheld
}

// socketCause is a socket's failure by its operation, the address it was on
// and the system's error number.
func socketCause(op *net.OpError) string {
	text := op.Op
	if op.Net != "" {
		text += " " + op.Net
	}
	if op.Addr != nil {
		text += " " + op.Addr.String()
	}
	var dns *net.DNSError
	var sys *os.SyscallError
	var errno syscall.Errno
	switch {
	case errors.As(op.Err, &dns) && dns.IsNotFound:
		return text + ": no such host"
	case errors.As(op.Err, &sys) && errors.As(sys.Err, &errno):
		return text + ": " + sys.Syscall + ": " + errno.Error()
	case errors.As(op.Err, &errno):
		return text + ": " + errno.Error()
	}
	if cause := tlsCause(op.Err); cause != "" {
		return text + ": " + cause
	}
	return text + ": " + causeSocket
}

// tlsCause names a TLS or certificate failure by its type, or is empty: a
// certificate's error quotes the names the peer's certificate holds.
func tlsCause(err error) string {
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	var verify *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	var alert tls.AlertError
	switch {
	case errors.As(err, &unknown):
		return "the certificate is signed by an unknown authority"
	case errors.As(err, &host):
		return "the certificate is not valid for the host"
	case errors.As(err, &invalid):
		return "the certificate is not valid"
	case errors.As(err, &verify):
		return "the certificate did not verify"
	case errors.As(err, &record):
		return "the peer did not answer in TLS"
	case errors.As(err, &alert):
		return "TLS alert " + strconv.Itoa(int(alert))
	}
	return ""
}
