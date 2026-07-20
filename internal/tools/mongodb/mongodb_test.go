package mongodb_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/client"
	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/config"
	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/tools/mongodb"
)

// newSession wires the mongodb tools onto an in-memory MCP server/client pair
// backed by the given upstream test server.
func newSession(t *testing.T, upstream string) *mcp.ClientSession {
	t.Helper()
	cfg := &config.Config{
		Username: "alice", Password: "secret", BaseURL: upstream,
		HTTPTimeout: 5 * time.Second, QueryMaxLimit: 100, UserAgent: "sdb-mcp-test/1",
	}
	c := client.New(cfg)

	srv := mcp.NewServer(&mcp.Implementation{Name: "sdb-mcp", Version: "test"}, nil)
	mongodb.Register(srv, c)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cli := mcp.NewClient(&mcp.Implementation{Name: "client", Version: "test"}, nil)
	sess, err := cli.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func loginHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "auth-token", Value: "t.t.t", MaxAge: 86400, Path: "/"})
	io.WriteString(w, `{"success":true}`)
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if len(res.Content) == 0 {
		t.Fatal("tool result had no content")
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("content[0] is %T, want *mcp.TextContent", res.Content[0])
	}
	return tc.Text
}

func TestQueryClampsLimitAndPassesExtendedJSON(t *testing.T) {
	var gotLimit int
	var gotFilter map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", loginHandler)
	mux.HandleFunc("/api/mongodb/query", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Filter map[string]any `json:"filter"`
			Limit  int            `json:"limit"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotLimit = body.Limit
		gotFilter = body.Filter
		io.WriteString(w, `{"success":true,"data":[{"_id":{"$oid":"abc"}}],"count":1,"maxPage":1,"rowsPerPage":20}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	sess := newSession(t, srv.URL)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "sdb_query",
		Arguments: map[string]any{
			"database":   "ZINTLR_PROD",
			"collection": "tracker_cdr",
			"filter":     map[string]any{"created_at": map[string]any{"$gte": map[string]any{"$date": "2026-07-15T18:30:00.000Z"}}},
			"limit":      9999, // over the cap
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", resultText(t, res))
	}

	// Upstream must have received the clamped limit, not 9999.
	if gotLimit != 100 {
		t.Errorf("upstream limit = %d, want 100 (clamped)", gotLimit)
	}
	// Extended JSON must survive the map round-trip verbatim.
	date, _ := gotFilter["created_at"].(map[string]any)["$gte"].(map[string]any)["$date"].(string)
	if date != "2026-07-15T18:30:00.000Z" {
		t.Errorf("upstream filter lost Extended JSON $date: %+v", gotFilter)
	}

	// Tool output must report the applied limit.
	var out struct {
		Count        int64 `json:"count"`
		AppliedLimit int   `json:"appliedLimit"`
	}
	if err := json.Unmarshal([]byte(resultText(t, res)), &out); err != nil {
		t.Fatalf("decode tool output: %v", err)
	}
	if out.AppliedLimit != 100 {
		t.Errorf("appliedLimit = %d, want 100", out.AppliedLimit)
	}
	if out.Count != 1 {
		t.Errorf("count = %d, want 1", out.Count)
	}
}

func TestAggregateWriteStageIsRejected(t *testing.T) {
	var upstreamHit bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", loginHandler)
	mux.HandleFunc("/api/mongodb/aggregate", func(w http.ResponseWriter, r *http.Request) {
		upstreamHit = true
		io.WriteString(w, `{"success":true,"data":[],"count":0}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	sess := newSession(t, srv.URL)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "sdb_aggregate",
		Arguments: map[string]any{
			"database":   "ZINTLR_PROD",
			"collection": "tracker_cdr",
			"pipeline":   []any{map[string]any{"$out": "stolen"}},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected tool error for $out pipeline, got: %s", resultText(t, res))
	}
	if !strings.Contains(resultText(t, res), "$out") {
		t.Errorf("error text = %q, want mention of $out", resultText(t, res))
	}
	if upstreamHit {
		t.Error("write-stage pipeline reached the upstream; guard should block before sending")
	}
}

func TestAggregateHappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", loginHandler)
	mux.HandleFunc("/api/mongodb/aggregate", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"success":true,"data":[{"_id":{"$numberInt":"2"},"n":{"$numberInt":"52"}}],"count":1}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	sess := newSession(t, srv.URL)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "sdb_aggregate",
		Arguments: map[string]any{
			"database":   "ZINTLR_PROD",
			"collection": "tracker_cdr",
			"pipeline":   []any{map[string]any{"$group": map[string]any{"_id": "$prospect_type", "n": map[string]any{"$sum": 1}}}},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %s", resultText(t, res))
	}
	if !strings.Contains(resultText(t, res), "$numberInt") {
		t.Errorf("result lost Extended JSON: %s", resultText(t, res))
	}
}
