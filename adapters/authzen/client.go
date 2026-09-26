package authzen

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"time"

	controlv1 "github.com/guardana/control/api/gen/go/guardana/control/v1"
	"github.com/guardana/control/internal/core"
)

// Error is a refusal by the client, matched with errors.Is.
type Error string

// Error returns the refusal's text.
func (e Error) Error() string { return string(e) }

// ErrInvalidOptions is a New whose options cannot reach a decision point
// safely.
const ErrInvalidOptions Error = "authzen: invalid options"

const (
	evaluationPath = "/access/v1/evaluation"
	// maxAnswerBytes bounds what is read of an answer; a longer one is
	// refused.
	maxAnswerBytes = 64 << 10
	maxHeaderBytes = 64 << 10
)

// Client asks one decision point. New is the only way to make a usable one;
// the zero value answers every question as unavailable.
type Client struct {
	cfg       config
	headers   map[string]string
	timeout   time.Duration
	slots     chan struct{}
	transport *http.Transport
	http      *http.Client
}

// New checks opts and returns a client. It contacts nothing.
func New(opts Options) (*Client, error) {
	cfg, err := opts.check()
	if err != nil {
		return nil, err
	}
	transport := &http.Transport{
		DialContext:            (&net.Dialer{Timeout: opts.Timeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: opts.RootCAs},
		ForceAttemptHTTP2:      true,
		MaxIdleConnsPerHost:    opts.InFlight,
		IdleConnTimeout:        90 * time.Second,
		MaxResponseHeaderBytes: maxHeaderBytes,
	}
	if cfg.proxy != nil {
		transport.Proxy = http.ProxyURL(cfg.proxy)
	}
	headers := make(map[string]string, len(opts.Headers))
	for k, v := range opts.Headers {
		headers[k] = v
	}
	return &Client{
		cfg:       cfg,
		headers:   headers,
		timeout:   opts.Timeout,
		slots:     make(chan struct{}, opts.InFlight),
		transport: transport,
		http: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Identifier is the decision point's identifier as configured, for the
// decision that consulted its answer.
func (c *Client) Identifier() string {
	if c == nil {
		return ""
	}
	return c.cfg.raw
}

// Ask asks the decision point about env once, within the options' timeout and
// never past ctx's deadline. Whatever it cannot do is an unanswered state
// naming its cause: an envelope without the identifiers mapping version 1
// needs, a client nobody built, an ask past the in-flight bound or no answer
// are unavailable; the deadline is a timeout; a 200 this client will not read
// is refused.
func (c *Client) Ask(ctx context.Context, env *controlv1.ActionEnvelope) core.External {
	if c == nil || c.http == nil {
		return core.ExternalUnavailable()
	}
	body, ok := mapV1(env)
	if !ok {
		return core.ExternalUnavailable()
	}
	select {
	case c.slots <- struct{}{}:
	default:
		return core.ExternalUnavailable()
	}
	defer func() { <-c.slots }()
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	return c.post(ctx, body, env.GetRequestId())
}

// post sends one question and reads its answer.
func (c *Client) post(ctx context.Context, body []byte, requestID string) core.External {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.endpoint, bytes.NewReader(body))
	if err != nil {
		return core.ExternalUnavailable()
	}
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Request-ID", requestID)
	resp, err := c.http.Do(req)
	if err != nil {
		return failed(ctx)
	}
	defer resp.Body.Close() //nolint:errcheck // the answer is read; nothing to lose on close
	if ctx.Err() != nil {
		// The transport can hand back a response that arrived after ctx
		// ended, such as one the server wrote on seeing the connection
		// close; that is no answer.
		return failed(ctx)
	}
	if resp.StatusCode != http.StatusOK {
		return core.ExternalUnavailable()
	}
	if !isJSON(resp.Header.Values("Content-Type")) || !echoes(resp.Header.Values("X-Request-ID"), requestID) {
		return core.ExternalAnswerRefused()
	}
	text, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes+1))
	switch {
	case err != nil || ctx.Err() != nil:
		// A body read to its end after the deadline is no answer within it;
		// the transport can report a body the deadline cut as ending cleanly.
		return failed(ctx)
	case len(text) > maxAnswerBytes:
		return core.ExternalAnswerRefused()
	}
	return readAnswer(text, c.cfg.listed)
}

// failed names why no answer came back: the deadline, or anything else.
func failed(ctx context.Context) core.External {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return core.ExternalTimeout()
	}
	return core.ExternalUnavailable()
}

// echoes reports whether the answer returned exactly the identifier sent,
// once.
func echoes(values []string, sent string) bool {
	return len(values) == 1 && values[0] == sent
}
