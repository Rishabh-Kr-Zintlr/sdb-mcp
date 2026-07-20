package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/config"
)

// ---- helpers ----------------------------------------------------------------

func testConfig(baseURL string) *config.Config {
	return &config.Config{
		Username:      "alice",
		Password:      "secret",
		BaseURL:       baseURL,
		HTTPTimeout:   5 * time.Second,
		QueryMaxLimit: 100,
		UserAgent:     "sdb-mcp-test/1",
	}
}

// makeJWT builds an unsigned-but-well-formed JWT carrying the given exp.
func makeJWT(exp int64) string {
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(fmt.Sprintf(`{"id":"1","username":"alice","iat":1,"exp":%d}`, exp)))
	return hdr + "." + payload + ".sig"
}

func setAuthCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name: authCookieName, Value: token, MaxAge: 86400, Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func futureToken() string { return makeJWT(time.Now().Add(24 * time.Hour).Unix()) }

// ---- tests ------------------------------------------------------------------

func TestLoginCookieAndTokenReuse(t *testing.T) {
	var mu sync.Mutex
	var loginCount, meCount int
	token := futureToken()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		loginCount++
		mu.Unlock()
		setAuthCookie(w, token)
		io.WriteString(w, `{"success":true}`)
	})
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		meCount++
		mu.Unlock()
		c, err := r.Cookie(authCookieName)
		if err != nil || c.Value != token {
			t.Errorf("me: missing/wrong auth cookie: %v (%q)", err, cookieValue(c))
		}
		io.WriteString(w, `{"success":true,"user":{"id":"1","username":"alice","isAuthenticated":true,"isAdmin":false,"canViewAnalytics":true}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(testConfig(srv.URL))
	ctx := context.Background()

	u, err := c.Me(ctx)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	if u.Username != "alice" || !u.CanViewAnalytics {
		t.Errorf("unexpected user: %+v", u)
	}
	if _, err := c.Me(ctx); err != nil {
		t.Fatalf("second Me: %v", err)
	}

	if loginCount != 1 {
		t.Errorf("loginCount = %d, want 1 (token should be reused)", loginCount)
	}
	if meCount != 2 {
		t.Errorf("meCount = %d, want 2", meCount)
	}
}

func TestHeaderPolicy(t *testing.T) {
	token := futureToken()
	var gotHeader http.Header

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		setAuthCookie(w, token)
		io.WriteString(w, `{"success":true}`)
	})
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		io.WriteString(w, `{"success":true,"user":{"username":"alice"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(testConfig(srv.URL))
	if _, err := c.Me(context.Background()); err != nil {
		t.Fatalf("Me: %v", err)
	}

	if got := gotHeader.Get("User-Agent"); got != "sdb-mcp-test/1" {
		t.Errorf("User-Agent = %q, want sdb-mcp-test/1", got)
	}
	if got := gotHeader.Get("Accept"); got != "application/json" {
		t.Errorf("Accept = %q, want application/json", got)
	}
	if got := gotHeader.Get("Cookie"); got != authCookieName+"="+token {
		t.Errorf("Cookie = %q, want only the auth-token", got)
	}
	// CSRF headers must be present and derived from the base URL.
	if got := gotHeader.Get("Origin"); got != srv.URL {
		t.Errorf("Origin = %q, want %q", got, srv.URL)
	}
	if got, want := gotHeader.Get("Referer"), srv.URL+"/"; got != want {
		t.Errorf("Referer = %q, want %q", got, want)
	}
	// Browser noise headers must never be sent.
	for _, h := range []string{
		"Sec-Ch-Ua", "Sec-Ch-Ua-Mobile", "Sec-Ch-Ua-Platform",
		"Sec-Fetch-Dest", "Sec-Fetch-Mode", "Sec-Fetch-Site",
		"Accept-Language", "Cache-Control", "Pragma", "Priority",
	} {
		if v := gotHeader.Get(h); v != "" {
			t.Errorf("stripped header %s was sent: %q", h, v)
		}
	}
}

func TestProactiveRefreshOnNearExpiry(t *testing.T) {
	var mu sync.Mutex
	var loginCount int

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		loginCount++
		mu.Unlock()
		// Token already inside the refresh skew window, so it is treated as stale.
		setAuthCookie(w, makeJWT(time.Now().Add(30*time.Second).Unix()))
		io.WriteString(w, `{"success":true}`)
	})
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true,"user":{"username":"alice"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(testConfig(srv.URL))
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if _, err := c.Me(ctx); err != nil {
			t.Fatalf("Me #%d: %v", i, err)
		}
	}
	if loginCount != 2 {
		t.Errorf("loginCount = %d, want 2 (near-expiry token should be refreshed each call)", loginCount)
	}
}

func TestReactiveReloginOn401(t *testing.T) {
	var mu sync.Mutex
	var loginCount, meCount int

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		loginCount++
		mu.Unlock()
		setAuthCookie(w, futureToken())
		io.WriteString(w, `{"success":true}`)
	})
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		meCount++
		n := meCount
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, `{"success":false,"error":"expired"}`)
			return
		}
		io.WriteString(w, `{"success":true,"user":{"username":"alice"}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(testConfig(srv.URL))
	if _, err := c.Me(context.Background()); err != nil {
		t.Fatalf("Me: %v", err)
	}
	if loginCount != 2 {
		t.Errorf("loginCount = %d, want 2 (initial + reactive re-login)", loginCount)
	}
	if meCount != 2 {
		t.Errorf("meCount = %d, want 2 (401 then retry)", meCount)
	}
}

func TestReactive401GivesUpAfterOneRetry(t *testing.T) {
	var mu sync.Mutex
	var loginCount, meCount int

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		loginCount++
		mu.Unlock()
		setAuthCookie(w, futureToken())
		io.WriteString(w, `{"success":true}`)
	})
	mux.HandleFunc("/api/auth/me", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		meCount++
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"success":false,"error":"nope"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(testConfig(srv.URL))
	_, err := c.Me(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("Me error = %v, want APIError status 401", err)
	}
	if loginCount != 2 {
		t.Errorf("loginCount = %d, want 2 (initial + single retry)", loginCount)
	}
	if meCount != 2 {
		t.Errorf("meCount = %d, want 2", meCount)
	}
}

func TestDBStatsNDJSON(t *testing.T) {
	var gotAccept, gotBody string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		setAuthCookie(w, futureToken())
		io.WriteString(w, `{"success":true}`)
	})
	mux.HandleFunc("/api/mongodb/db-stats", func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/x-ndjson")
		io.WriteString(w, `{"_meta":{"total":3,"truncated":false}}`+"\n"+
			`{"name":"a","storageSize":100,"dataSize":200,"documentCount":5,"avgDocumentSize":40,"indexCount":2,"totalIndexSize":80}`+"\n"+
			`{"name":"b","storageSize":10,"dataSize":20,"documentCount":0,"avgDocumentSize":0,"indexCount":1,"totalIndexSize":8}`+"\n"+
			`{"name":"c","storageSize":1,"dataSize":2,"documentCount":1,"avgDocumentSize":2,"indexCount":1,"totalIndexSize":4}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(testConfig(srv.URL))
	res, err := c.DBStats(context.Background(), "ZINTLR_STAGE", false)
	if err != nil {
		t.Fatalf("DBStats: %v", err)
	}
	if gotAccept != "application/x-ndjson" {
		t.Errorf("Accept = %q, want application/x-ndjson", gotAccept)
	}
	if !strings.Contains(gotBody, `"database":"ZINTLR_STAGE"`) || !strings.Contains(gotBody, `"bustCache":false`) {
		t.Errorf("request body = %q, missing database/bustCache", gotBody)
	}
	if res.Meta.Total != 3 || res.Meta.Truncated {
		t.Errorf("meta = %+v, want {3 false}", res.Meta)
	}
	if len(res.Collections) != 3 {
		t.Fatalf("collections = %d, want 3", len(res.Collections))
	}
	if res.Collections[0].Name != "a" || res.Collections[0].DocumentCount != 5 || res.Collections[0].IndexCount != 2 {
		t.Errorf("collection[0] = %+v", res.Collections[0])
	}
}

func TestQueryExtendedJSONRoundTrip(t *testing.T) {
	filter := json.RawMessage(`{"created_at":{"$gte":{"$date":"2026-07-15T18:30:00.000Z"}}}`)
	var gotFilter json.RawMessage
	var gotLimit int

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		setAuthCookie(w, futureToken())
		io.WriteString(w, `{"success":true}`)
	})
	mux.HandleFunc("/api/mongodb/query", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Filter json.RawMessage `json:"filter"`
			Limit  int             `json:"limit"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode query body: %v", err)
		}
		gotFilter = body.Filter
		gotLimit = body.Limit
		io.WriteString(w, `{"success":true,"data":[{"_id":{"$oid":"6a59dea244cc9189f4775182"}}],"count":1,"maxPage":1,"rowsPerPage":20}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(testConfig(srv.URL))
	res, err := c.Query(context.Background(), QueryRequest{
		Database: "ZINTLR_PROD", Collection: "tracker_cdr", Filter: filter, Limit: 20,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !jsonEqual(t, gotFilter, filter) {
		t.Errorf("filter sent = %s, want %s (verbatim Extended JSON)", gotFilter, filter)
	}
	if gotLimit != 20 {
		t.Errorf("limit sent = %d, want 20", gotLimit)
	}
	if res.Count != 1 {
		t.Errorf("count = %d, want 1", res.Count)
	}
	if !strings.Contains(string(res.Data), "$oid") {
		t.Errorf("response data lost Extended JSON: %s", res.Data)
	}
}

func TestAPIErrorOnOperatorAllowList(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		setAuthCookie(w, futureToken())
		io.WriteString(w, `{"success":true}`)
	})
	mux.HandleFunc("/api/mongodb/query", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"success":false,"error":"Unsupported or unsafe operator: $group"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(testConfig(srv.URL))
	_, err := c.Query(context.Background(), QueryRequest{Database: "D", Collection: "C", Limit: 10})
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want *APIError", err)
	}
	if apiErr.Status != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", apiErr.Status)
	}
	if !strings.Contains(apiErr.Message, "Unsupported or unsafe operator") {
		t.Errorf("message = %q, want operator allow-list text", apiErr.Message)
	}
}

func TestQueryReactive429Backoff(t *testing.T) {
	orig := defaultBackoff
	defaultBackoff = time.Millisecond
	defer func() { defaultBackoff = orig }()

	var mu sync.Mutex
	var qCount int
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		setAuthCookie(w, futureToken())
		io.WriteString(w, `{"success":true}`)
	})
	mux.HandleFunc("/api/mongodb/query", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		qCount++
		n := qCount
		mu.Unlock()
		if n == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"success":false,"error":"slow down"}`)
			return
		}
		io.WriteString(w, `{"success":true,"data":[],"count":0,"maxPage":0,"rowsPerPage":20}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := New(testConfig(srv.URL))
	if _, err := c.Query(context.Background(), QueryRequest{Database: "D", Collection: "C", Limit: 10}); err != nil {
		t.Fatalf("Query: %v", err)
	}
	if qCount != 2 {
		t.Errorf("query attempts = %d, want 2 (429 then retry)", qCount)
	}
}

func TestValidatePipeline(t *testing.T) {
	tests := []struct {
		name     string
		pipeline string
		wantErr  bool
		wantOp   string
	}{
		{"valid group", `[{"$group":{"_id":"$t","n":{"$sum":1}}}]`, false, ""},
		{"valid multi", `[{"$match":{"x":1}},{"$sort":{"x":-1}},{"$limit":10}]`, false, ""},
		{"out blocked", `[{"$match":{"x":1}},{"$out":"copy"}]`, true, "$out"},
		{"merge blocked", `[{"$merge":{"into":"x"}}]`, true, "$merge"},
		{"nested function blocked", `[{"$match":{"$expr":{"$function":{"body":"f","args":[],"lang":"js"}}}}]`, true, "$function"},
		{"where blocked", `[{"$match":{"$where":"this.x>1"}}]`, true, "$where"},
		{"empty array", `[]`, true, ""},
		{"not an array", `{"$match":{}}`, true, ""},
		{"empty input", ``, true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePipeline(json.RawMessage(tc.pipeline))
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantOp != "" && (err == nil || !strings.Contains(err.Error(), tc.wantOp)) {
				t.Errorf("error = %v, want mention of %s", err, tc.wantOp)
			}
		})
	}
}

func TestParseJWTExp(t *testing.T) {
	exp := time.Now().Add(time.Hour).Truncate(time.Second)
	got, ok := parseJWTExp(makeJWT(exp.Unix()))
	if !ok || !got.Equal(exp) {
		t.Errorf("parseJWTExp = (%v, %v), want (%v, true)", got, ok, exp)
	}
	if _, ok := parseJWTExp("not-a-jwt"); ok {
		t.Errorf("parseJWTExp(garbage) ok = true, want false")
	}
}

func TestTokenExpiryFallsBackToCookieMaxAge(t *testing.T) {
	now := time.Now()
	// A token with no parseable exp claim should fall back to the cookie Max-Age.
	cookie := &http.Cookie{Name: authCookieName, Value: "opaque-not-a-jwt", MaxAge: 3600}
	got := tokenExpiry(cookie, now)
	if want := now.Add(3600 * time.Second); got.Sub(want).Abs() > time.Second {
		t.Errorf("tokenExpiry = %v, want ~%v", got, want)
	}
}

// ---- small test utilities ---------------------------------------------------

func cookieValue(c *http.Cookie) string {
	if c == nil {
		return ""
	}
	return c.Value
}

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}
