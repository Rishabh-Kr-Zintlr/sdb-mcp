package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// ---- Response types ---------------------------------------------------------

// User is the authenticated identity from GET /api/auth/me.
type User struct {
	ID               string `json:"id"`
	Username         string `json:"username"`
	IsAuthenticated  bool   `json:"isAuthenticated"`
	IsAdmin          bool   `json:"isAdmin"`
	CanViewAnalytics bool   `json:"canViewAnalytics"`
}

// Database is one entry from GET /api/mongodb/databases.
type Database struct {
	Name       string `json:"name"`
	SizeOnDisk int64  `json:"sizeOnDisk"`
	Empty      bool   `json:"empty"`
}

// DBStatsMeta is the leading _meta record of the db-stats NDJSON stream.
type DBStatsMeta struct {
	Total     int  `json:"total"`
	Truncated bool `json:"truncated"`
}

// CollectionStats is one per-collection record from db-stats.
type CollectionStats struct {
	Name            string `json:"name"`
	StorageSize     int64  `json:"storageSize"`
	DataSize        int64  `json:"dataSize"`
	DocumentCount   int64  `json:"documentCount"`
	AvgDocumentSize int64  `json:"avgDocumentSize"`
	IndexCount      int    `json:"indexCount"`
	TotalIndexSize  int64  `json:"totalIndexSize"`
}

// DBStatsResult is the parsed db-stats NDJSON stream.
type DBStatsResult struct {
	Meta        DBStatsMeta       `json:"meta"`
	Collections []CollectionStats `json:"collections"`
}

// SchemaField describes one field inferred by POST /api/mongodb/schema. The
// sampled values are kept as raw Extended JSON for full fidelity.
type SchemaField struct {
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Count        int64           `json:"count"`
	Frequency    float64         `json:"frequency"`
	HasNull      bool            `json:"hasNull"`
	Values       json.RawMessage `json:"values"`
	SampleValues json.RawMessage `json:"sampleValues"`
}

// SchemaResult is the data payload of POST /api/mongodb/schema.
type SchemaResult struct {
	Fields         []SchemaField `json:"fields"`
	TotalDocuments int64         `json:"totalDocuments"`
	SampleSize     int           `json:"sampleSize"`
}

// QueryResult is the payload of POST /api/mongodb/query. Data holds the result
// documents as raw Extended JSON.
type QueryResult struct {
	Data        json.RawMessage `json:"data"`
	Count       int64           `json:"count"`
	MaxPage     int64           `json:"maxPage"`
	RowsPerPage int64           `json:"rowsPerPage"`
}

// AggregateResult is the payload of POST /api/mongodb/aggregate.
type AggregateResult struct {
	Data  json.RawMessage `json:"data"`
	Count int64           `json:"count"`
}

// ---- Request types ----------------------------------------------------------

// QueryRequest is the body for POST /api/mongodb/query. Filter/Projection/Sort
// are raw Extended JSON passed through verbatim; empty ones are omitted.
type QueryRequest struct {
	Database   string          `json:"database"`
	Collection string          `json:"collection"`
	Filter     json.RawMessage `json:"filter,omitempty"`
	Projection json.RawMessage `json:"projection,omitempty"`
	Sort       json.RawMessage `json:"sort,omitempty"`
	Limit      int             `json:"limit"`
	Skip       int             `json:"skip"`
}

// AggregateRequest is the body for POST /api/mongodb/aggregate. Pipeline is a
// raw Extended JSON array of stages.
type AggregateRequest struct {
	Database   string          `json:"database"`
	Collection string          `json:"collection"`
	Pipeline   json.RawMessage `json:"pipeline"`
}

// ---- Endpoint methods -------------------------------------------------------

// Me returns the authenticated identity.
func (c *Client) Me(ctx context.Context) (*User, error) {
	var env struct {
		User User `json:"user"`
	}
	if err := c.getJSON(ctx, "/api/auth/me", &env); err != nil {
		return nil, err
	}
	return &env.User, nil
}

// Databases lists the accessible databases.
func (c *Client) Databases(ctx context.Context) ([]Database, error) {
	var env struct {
		Data []Database `json:"data"`
	}
	if err := c.getJSON(ctx, "/api/mongodb/databases", &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

// Collections lists the collection names in a database.
func (c *Client) Collections(ctx context.Context, database string) ([]string, error) {
	if database == "" {
		return nil, errors.New("database is required")
	}
	var env struct {
		Data []string `json:"data"`
	}
	path := "/api/mongodb/collections/" + url.PathEscape(database)
	if err := c.getJSON(ctx, path, &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

// Schema samples a collection and returns its inferred field shapes.
func (c *Client) Schema(ctx context.Context, database, collection string, sampleSize int) (*SchemaResult, error) {
	if database == "" || collection == "" {
		return nil, errors.New("database and collection are required")
	}
	reqBody := map[string]any{
		"database":   database,
		"collection": collection,
		"sampleSize": sampleSize,
	}
	var env struct {
		Data SchemaResult `json:"data"`
	}
	if err := c.postJSON(ctx, "/api/mongodb/schema", reqBody, &env); err != nil {
		return nil, err
	}
	return &env.Data, nil
}

// Query runs a find-style query. Filter/Projection/Sort must be valid Extended
// JSON objects (or nil); the caller is responsible for clamping Limit.
func (c *Client) Query(ctx context.Context, req QueryRequest) (*QueryResult, error) {
	if req.Database == "" || req.Collection == "" {
		return nil, errors.New("database and collection are required")
	}
	var out QueryResult
	if err := c.postJSON(ctx, "/api/mongodb/query", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Aggregate runs an aggregation pipeline. The pipeline is validated against the
// write/code-stage denylist before the request is sent.
func (c *Client) Aggregate(ctx context.Context, req AggregateRequest) (*AggregateResult, error) {
	if req.Database == "" || req.Collection == "" {
		return nil, errors.New("database and collection are required")
	}
	if err := ValidatePipeline(req.Pipeline); err != nil {
		return nil, err
	}
	var out AggregateResult
	if err := c.postJSON(ctx, "/api/mongodb/aggregate", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DBStats returns per-collection storage statistics. The upstream response is
// an application/x-ndjson stream, parsed line by line.
func (c *Client) DBStats(ctx context.Context, database string, bustCache bool) (*DBStatsResult, error) {
	if database == "" {
		return nil, errors.New("database is required")
	}
	reqBody, err := json.Marshal(map[string]any{
		"database":  database,
		"bustCache": bustCache,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	body, err := c.do(ctx, http.MethodPost, "/api/mongodb/db-stats", reqBody, "application/x-ndjson")
	if err != nil {
		return nil, err
	}
	return parseDBStats(body)
}

// parseDBStats parses the NDJSON db-stats stream: a leading {"_meta":{…}}
// record followed by one JSON object per collection.
func parseDBStats(body []byte) (*DBStatsResult, error) {
	res := &DBStatsResult{}
	sc := bufio.NewScanner(bytes.NewReader(body))
	// Individual collection records are small, but give the scanner generous
	// headroom so a large record never trips bufio.ErrTooLong.
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)

	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}

		// A mid-stream error is reported as a success:false envelope.
		var errProbe struct {
			Success *bool  `json:"success"`
			Error   string `json:"error"`
		}
		if json.Unmarshal(line, &errProbe) == nil && errProbe.Success != nil && !*errProbe.Success {
			return nil, &APIError{Status: http.StatusOK, Message: errProbe.Error}
		}

		// The _meta record precedes the per-collection records.
		var metaProbe struct {
			Meta *DBStatsMeta `json:"_meta"`
		}
		if json.Unmarshal(line, &metaProbe) == nil && metaProbe.Meta != nil {
			res.Meta = *metaProbe.Meta
			continue
		}

		var cs CollectionStats
		if err := json.Unmarshal(line, &cs); err != nil {
			return nil, fmt.Errorf("parse db-stats line: %w", err)
		}
		res.Collections = append(res.Collections, cs)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan db-stats stream: %w", err)
	}
	return res, nil
}

// ---- Aggregation safety guard ----------------------------------------------

// deniedAggregationOperators are pipeline operators that write data or execute
// server-side code. They are blocked client-side so this server stays strictly
// read-only, independent of the upstream allow-list.
var deniedAggregationOperators = map[string]bool{
	"$out":         true,
	"$merge":       true,
	"$function":    true,
	"$accumulator": true,
	"$where":       true,
	"$graphLookup": true,
	"$unionWith":   true,
}

// ValidatePipeline verifies that pipeline is a non-empty JSON array of stages
// containing no write or code-execution operator. The scan is recursive, so a
// denied operator nested inside an expression is also caught.
func ValidatePipeline(pipeline json.RawMessage) error {
	if len(bytes.TrimSpace(pipeline)) == 0 {
		return errors.New("pipeline is required")
	}
	var v any
	if err := json.Unmarshal(pipeline, &v); err != nil {
		return fmt.Errorf("invalid pipeline JSON: %w", err)
	}
	arr, ok := v.([]any)
	if !ok {
		return errors.New("pipeline must be a JSON array of stages")
	}
	if len(arr) == 0 {
		return errors.New("pipeline must contain at least one stage")
	}
	if op := findDeniedOperator(v); op != "" {
		return fmt.Errorf("aggregation operator %q is not allowed: this server is read-only and blocks write/code stages", op)
	}
	return nil
}

// findDeniedOperator returns the first denied operator key found anywhere in v,
// or "" if none.
func findDeniedOperator(v any) string {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if deniedAggregationOperators[k] {
				return k
			}
			if op := findDeniedOperator(val); op != "" {
				return op
			}
		}
	case []any:
		for _, e := range t {
			if op := findDeniedOperator(e); op != "" {
				return op
			}
		}
	}
	return ""
}
