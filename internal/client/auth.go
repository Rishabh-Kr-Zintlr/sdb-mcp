package client

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

// authCookieName is the only cookie this client sends or stores. Every other
// cookie in the browser captures is unrelated session noise and is ignored.
const authCookieName = "auth-token"

// authRefreshSkew is how far before expiry a token is proactively refreshed.
const authRefreshSkew = 60 * time.Second

// fallbackTokenTTL is used when neither the JWT nor the Set-Cookie carries a
// usable expiry. Reactive 401 handling covers any premature expiry.
const fallbackTokenTTL = time.Hour

// userAgentTransport is an http.RoundTripper that stamps a fixed User-Agent and
// the CSRF Origin/Referer headers on every outbound request. It is the single
// choke point for all traffic, keeping the outbound header set clean: only
// these headers plus the per-request Accept/Content-Type/Cookie are sent — none
// of the browser's sec-ch-ua*/sec-fetch-*/cache-control noise.
//
// Origin and Referer are required: the server enforces CSRF and rejects
// requests missing them with HTTP 403 ("CSRF: missing Origin/Referer header").
type userAgentTransport struct {
	userAgent string
	origin    string // e.g. https://sdb.zintlr.com
	referer   string // e.g. https://sdb.zintlr.com/
	base      http.RoundTripper
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone before mutating, per RoundTripper contract.
	r := req.Clone(req.Context())
	r.Header.Set("User-Agent", t.userAgent)
	if t.origin != "" {
		r.Header.Set("Origin", t.origin)
	}
	if t.referer != "" {
		r.Header.Set("Referer", t.referer)
	}
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(r)
}

// authManager owns the login credentials and the in-memory session token. It
// logs in lazily, caches the token until its expiry, refreshes proactively
// before expiry, and can be invalidated to force a reactive re-login.
type authManager struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client

	mu    sync.Mutex
	token string
	exp   time.Time
}

func newAuthManager(baseURL, username, password string, hc *http.Client) *authManager {
	return &authManager{
		baseURL:    baseURL,
		username:   username,
		password:   password,
		httpClient: hc,
	}
}

// ensureFresh guarantees a usable token, refreshing if missing or near expiry.
// It serializes concurrent callers so at most one login is in flight; callers
// that arrive during a login observe the freshly stored token.
func (a *authManager) ensureFresh(ctx context.Context) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token != "" && time.Now().Before(a.exp.Add(-authRefreshSkew)) {
		return a.token, nil
	}
	if err := a.loginLocked(ctx); err != nil {
		return "", err
	}
	return a.token, nil
}

// invalidate clears the cached token, forcing the next ensureFresh to re-login.
// Called after an upstream 401/403.
func (a *authManager) invalidate() {
	a.mu.Lock()
	a.token = ""
	a.exp = time.Time{}
	a.mu.Unlock()
}

// loginLocked performs POST /api/auth/login and stores the resulting token and
// expiry. The caller must hold a.mu.
func (a *authManager) loginLocked(ctx context.Context) error {
	body, err := json.Marshal(map[string]string{
		"username": a.username,
		"password": a.password,
	})
	if err != nil {
		return fmt.Errorf("marshal login body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return newAPIError(resp.StatusCode, respBody)
	}

	cookie := findCookie(resp.Cookies(), authCookieName)
	if cookie == nil || cookie.Value == "" {
		return fmt.Errorf("login succeeded but no %s cookie was returned", authCookieName)
	}

	a.token = cookie.Value
	a.exp = tokenExpiry(cookie, time.Now())
	return nil
}

// findCookie returns the cookie with the given name, or nil.
func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// tokenExpiry determines when the token expires, preferring the JWT `exp`
// claim, then the cookie Max-Age, then a conservative fallback.
func tokenExpiry(cookie *http.Cookie, now time.Time) time.Time {
	if exp, ok := parseJWTExp(cookie.Value); ok {
		return exp
	}
	if cookie.MaxAge > 0 {
		return now.Add(time.Duration(cookie.MaxAge) * time.Second)
	}
	return now.Add(fallbackTokenTTL)
}

// parseJWTExp extracts the `exp` (seconds since epoch) claim from a JWT without
// verifying its signature. It returns ok=false if the claim is absent or the
// token is malformed.
func parseJWTExp(token string) (time.Time, bool) {
	parts := splitN(token, '.', 3)
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Tolerate padded tokens.
		if payload, err = base64.URLEncoding.DecodeString(parts[1]); err != nil {
			return time.Time{}, false
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Exp == 0 {
		return time.Time{}, false
	}
	return time.Unix(claims.Exp, 0), true
}

// splitN splits s on sep into at most n parts, without allocating a full
// strings.Split when the JWT has the expected three segments.
func splitN(s string, sep byte, n int) []string {
	parts := make([]string, 0, n)
	start := 0
	for i := 0; i < len(s) && len(parts) < n-1; i++ {
		if s[i] == sep {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}
