package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// ClientOption configures an HTTP Client.
type ClientOption func(*clientConfig)

type clientConfig struct {
	timeout   time.Duration
	transport http.RoundTripper
}

// WithClientTimeout sets the overall request timeout.
func WithClientTimeout(d time.Duration) ClientOption {
	return func(c *clientConfig) {
		c.timeout = d
	}
}

// WithCustomTransport sets a custom base http.RoundTripper.
func WithCustomTransport(rt http.RoundTripper) ClientOption {
	return func(c *clientConfig) {
		c.transport = rt
	}
}

// Client wraps http.Client with OpenTelemetry tracing instrumentation.
type Client struct {
	*http.Client
}

// NewClient constructs an HTTP client instrumented with OpenTelemetry tracing.
func NewClient(opts ...ClientOption) *Client {
	cfg := clientConfig{
		timeout: 10 * time.Second,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	baseTransport := cfg.transport
	if baseTransport == nil {
		baseTransport = &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}

	tracedTransport := otelhttp.NewTransport(baseTransport)

	return &Client{
		Client: &http.Client{
			Timeout:   cfg.timeout,
			Transport: tracedTransport,
		},
	}
}

// Get executes an HTTP GET request with context.
func (c *Client) Get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("httpx client: failed to create request: %w", err)
	}
	return c.Do(req)
}

// PostJSON marshals body to JSON, sets the Content-Type header, and executes a POST request.
func (c *Client) PostJSON(ctx context.Context, url string, body any) (*http.Response, error) {
	buf := new(bytes.Buffer)
	if body != nil {
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return nil, fmt.Errorf("httpx client: failed to encode json body: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, buf)
	if err != nil {
		return nil, fmt.Errorf("httpx client: failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	return c.Do(req)
}
