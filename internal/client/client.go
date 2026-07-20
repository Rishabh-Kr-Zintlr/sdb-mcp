// Package client is a thin HTTP client for the sdb.zintlr.com API. It owns
// request construction, authentication/session handling, and response decoding
// used by the MCP tool handlers.
//
// The client is strictly read-only: it exposes identity, database/collection
// discovery, schema sampling, stats, find-style queries, and aggregation
// pipelines. Aggregation write/code stages are rejected before a request is
// sent (see ValidatePipeline).
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/config"
)

// maxResponseBytes caps how much of any response body is read into memory. The
// read paths are bounded (query by limit, db-stats by collection count); this
// is a safety valve against a pathologically large aggregate result.
const maxResponseBytes = 64 << 20 // 64 MiB

// defaultBackoff is used for a 429 when no Retry-After header is present. It is
// a var (not const) so tests can shorten it.
var defaultBackoff = time.Second

// Client is a read-only HTTP client for the sdb.zintlr.com API.
type Client struct {
	cfg        *config.Config
	httpClient *http.Client
	auth       *authManager
}

// New constructs a Client from configuration. It does not perform any network
// I/O; login happens lazily on the first authenticated call.
func New(cfg *config.Config) *Client {
	origin, referer := originAndReferer(cfg.BaseURL)
	httpClient := &http.Client{
		Timeout: cfg.HTTPTimeout,
		Transport: &userAgentTransport{
			userAgent: cfg.UserAgent,
			origin:    origin,
			referer:   referer,
			base:      http.DefaultTransport,
		},
	}
	return &Client{
		cfg:        cfg,
		httpClient: httpClient,
		auth:       newAuthManager(cfg.BaseURL, cfg.Username, cfg.Password, httpClient),
	}
}

// QueryMaxLimit reports the configured hard cap on find-query limits.
func (c *Client) QueryMaxLimit() int { return c.cfg.QueryMaxLimit }

// do sends an authenticated request and returns the response body for a 2xx
// status. It handles a single reactive re-login on 401/403 and a single
// backoff+retry on 429; any other non-2xx status becomes an *APIError.
func (c *Client) do(ctx context.Context, method, path string, reqBody []byte, accept string) ([]byte, error) {
	authRetried := false
	rateRetried := false

	for {
		token, err := c.auth.ensureFresh(ctx)
		if err != nil {
			return nil, err
		}

		status, header, body, err := c.send(ctx, method, path, reqBody, accept, token)
		if err != nil {
			return nil, err
		}

		switch {
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			if authRetried {
				return nil, newAPIError(status, body)
			}
			authRetried = true
			c.auth.invalidate()

		case status == http.StatusTooManyRequests:
			if rateRetried {
				return nil, newAPIError(status, body)
			}
			rateRetried = true
			if err := sleepBackoff(ctx, retryAfter(header)); err != nil {
				return nil, err
			}

		case status >= 200 && status < 300:
			return body, nil

		default:
			return nil, newAPIError(status, body)
		}
	}
}

// send performs one HTTP round-trip, attaching the auth cookie and the clean
// header set, and returns the status, response headers, and (bounded) body.
func (c *Client) send(ctx context.Context, method, path string, reqBody []byte, accept, token string) (int, http.Header, []byte, error) {
	var r io.Reader
	if reqBody != nil {
		r = bytes.NewReader(reqBody)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.cfg.BaseURL+path, r)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("build request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if accept == "" {
		accept = "application/json"
	}
	req.Header.Set("Accept", accept)
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: token})

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("read response body: %w", err)
	}
	return resp.StatusCode, resp.Header, body, nil
}

// getJSON issues a GET and decodes the JSON envelope into out (a pointer),
// verifying the "success" flag.
func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	body, err := c.do(ctx, http.MethodGet, path, nil, "application/json")
	if err != nil {
		return err
	}
	return decodeEnvelope(body, out)
}

// postJSON marshals reqBody, issues a POST, and decodes the JSON envelope into
// out (a pointer), verifying the "success" flag.
func (c *Client) postJSON(ctx context.Context, path string, reqBody any, out any) error {
	b, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	body, err := c.do(ctx, http.MethodPost, path, b, "application/json")
	if err != nil {
		return err
	}
	return decodeEnvelope(body, out)
}

// decodeEnvelope verifies {"success":true} and unmarshals the full body into
// out. A "success":false body becomes an *APIError.
func decodeEnvelope(body []byte, out any) error {
	var probe struct {
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return fmt.Errorf("decode response envelope: %w", err)
	}
	if !probe.Success {
		return newAPIError(http.StatusOK, body)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// retryAfter derives a backoff duration from a Retry-After header, supporting
// the delay-seconds form. Absent or unparseable, it returns the default.
func retryAfter(header http.Header) time.Duration {
	if header == nil {
		return defaultBackoff
	}
	if secs, ok := parseIntHeader(header.Get("Retry-After")); ok && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return defaultBackoff
}

// sleepBackoff waits for d or until ctx is cancelled.
func sleepBackoff(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		d = defaultBackoff
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// parseIntHeader is a small helper for Retry-After (seconds form).
func parseIntHeader(v string) (int, bool) {
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// originAndReferer derives the CSRF Origin (scheme://host) and Referer
// (Origin + "/") from the base URL. Both are empty if the URL is unusable.
func originAndReferer(baseURL string) (origin, referer string) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", ""
	}
	origin = u.Scheme + "://" + u.Host
	return origin, origin + "/"
}
