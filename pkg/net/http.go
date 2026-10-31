package net

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"jmpeax.com/sec/monica/internal/logging"
	"jmpeax.com/sec/monica/pkg/request"
)

const (
	// DefaultTimeout bounds a whole request, including reading the body.
	DefaultTimeout = 30 * time.Second
	// DefaultUserAgent is sent when the request sets no User-Agent.
	DefaultUserAgent = "monica/0.1.0"
)

// Client sends parsed requests. It wraps one *http.Client so connections are
// reused across requests.
type Client struct {
	http    *http.Client
	timeout time.Duration
}

// Options configure a Client.
type Options struct {
	// Timeout bounds each request (zero means none); a request's own Timeout
	// overrides it.
	Timeout time.Duration
	// NoFollowRedirects returns 3xx responses instead of following them.
	NoFollowRedirects bool
	// InsecureSkipVerify disables TLS certificate verification. Opt-in only,
	// for self-signed development servers.
	InsecureSkipVerify bool
}

// NewClient returns a Client configured by opts.
func NewClient(opts Options) *Client {
	hc := &http.Client{}
	if opts.NoFollowRedirects {
		hc.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	if opts.InsecureSkipVerify {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- explicit user opt-in
		hc.Transport = tr
	}
	return &Client{http: hc, timeout: opts.Timeout}
}

var defaultClient = NewClient(Options{Timeout: DefaultTimeout})

// HTTPRequest sends request with the shared default client.
func HTTPRequest(request *request.Request, headerOnly bool) (*HTTPResponse, error) {
	return defaultClient.Do(request, headerOnly)
}

// Do sends request and returns its response. With headerOnly the body is
// discarded.
func (c *Client) Do(request *request.Request, headerOnly bool) (*HTTPResponse, error) {
	if request == nil {
		return nil, errors.New("nil request")
	}
	timeout := c.timeout
	if request.Timeout > 0 {
		timeout = request.Timeout
	}
	ctx := context.Background()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel() // after the body has been read
	}
	req, err := http.NewRequestWithContext(ctx, request.Method, request.URL, strings.NewReader(request.Body))
	if err != nil {
		return nil, fmt.Errorf("creating HTTP request: %w", err)
	}
	for k, vs := range request.Headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	// net/http ignores Header["Host"]; the Host field is what is sent.
	if host := request.Headers.Get("Host"); host != "" {
		req.Host = host
		req.Header.Del("Host")
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", DefaultUserAgent)
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("sending HTTP request: timed out after %s: %w", timeout, err)
	}
	if err != nil {
		return nil, fmt.Errorf("sending HTTP request: %w", err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			logging.Log.Debug().Err(err).Msg("Failed to close response body")
		}
	}()
	response := &HTTPResponse{
		Status:     resp.Proto + " " + resp.Status,
		StatusCode: resp.StatusCode,
		Headers:    resp.Header,
	}
	if !headerOnly {
		bodyBytes, err := io.ReadAll(resp.Body)
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("reading response body: timed out after %s: %w", timeout, err)
		}
		if err != nil {
			return nil, fmt.Errorf("reading response body: %w", err)
		}
		response.Body = string(bodyBytes)
	}
	response.Duration = time.Since(start)
	logging.Log.Debug().Msgf("%s %s -> %d in %s", request.Method, request.URL, resp.StatusCode, response.Duration)
	return response, nil
}
