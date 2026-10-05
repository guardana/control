package transportcause

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"strconv"
	"syscall"
)

// The causes that name no peer, the same for every caller.
const (
	Canceled = "the request was canceled"
	Closed   = "the connection closed before the answer ended"
	Socket   = "failed"
)

// Words are the causes a caller words itself, since they name its peer.
type Words struct {
	// Withheld is the cause of a failure no error type names.
	Withheld string
	// Deadline is the cause of a deadline or a timeout.
	Deadline string
}

// Of words err by its type alone.
func (w Words) Of(err error) string {
	var op *net.OpError
	var timeout net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &timeout) && timeout.Timeout():
		return w.Deadline
	case errors.Is(err, context.Canceled):
		return Canceled
	case errors.As(err, &op):
		return socketCause(op)
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return Closed
	}
	if cause := tlsCause(err); cause != "" {
		return cause
	}
	return w.Withheld
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
	return text + ": " + Socket
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
