// Package mongodb implements the read-only MongoDB data-access MCP tools (list
// databases, list collections, database stats, collection schema, find-style
// query, and aggregation pipelines) backed by the sdb.zintlr.com /api/mongodb
// endpoints.
package mongodb

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/client"
	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/tools"
)

const (
	defaultSampleSize = 100
	defaultQueryLimit = 20
	// aggregateSoftCap bounds how many bytes of aggregate result data are
	// returned inline before the tool substitutes a "narrow the pipeline" note.
	aggregateSoftCap = 512 * 1024
)

// ---- Input types ------------------------------------------------------------

type databaseInput struct {
	Database string `json:"database" jsonschema:"the database name, e.g. ZINTLR_PROD"`
}

type schemaInput struct {
	Database   string `json:"database" jsonschema:"the database name"`
	Collection string `json:"collection" jsonschema:"the collection name"`
	SampleSize *int   `json:"sampleSize,omitempty" jsonschema:"number of documents to sample when inferring the schema (default 100)"`
}

type dbStatsInput struct {
	Database  string `json:"database" jsonschema:"the database name"`
	BustCache bool   `json:"bustCache,omitempty" jsonschema:"recompute stats instead of using the server cache"`
}

type queryInput struct {
	Database   string         `json:"database" jsonschema:"the database name"`
	Collection string         `json:"collection" jsonschema:"the collection name"`
	Filter     map[string]any `json:"filter,omitempty" jsonschema:"MongoDB find filter as Extended JSON; query operators only (no aggregation stages)"`
	Projection map[string]any `json:"projection,omitempty" jsonschema:"fields to include (1) or exclude (0)"`
	Sort       map[string]any `json:"sort,omitempty" jsonschema:"sort spec mapping field to 1 (asc) or -1 (desc)"`
	Limit      *int           `json:"limit,omitempty" jsonschema:"maximum documents to return (default 20; capped by the server)"`
	Skip       int            `json:"skip,omitempty" jsonschema:"documents to skip, for pagination"`
}

type aggregateInput struct {
	Database   string           `json:"database" jsonschema:"the database name"`
	Collection string           `json:"collection" jsonschema:"the collection name"`
	Pipeline   []map[string]any `json:"pipeline" jsonschema:"aggregation pipeline: an array of stage objects in Extended JSON"`
}

// ---- Registration -----------------------------------------------------------

// Register adds all MongoDB read-only tools to the server.
func Register(s *mcp.Server, c *client.Client) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}

	mcp.AddTool(s, &mcp.Tool{
		Name: "sdb_list_databases",
		Description: "List the MongoDB databases available on the server, with each " +
			"database's on-disk size (bytes) and whether it is empty. Takes no arguments.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ tools.NoArgs) (*mcp.CallToolResult, any, error) {
		dbs, err := c.Databases(ctx)
		if err != nil {
			return nil, nil, err
		}
		return tools.JSONResult(dbs)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "sdb_list_collections",
		Description: "List the collection names in a database.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in databaseInput) (*mcp.CallToolResult, any, error) {
		cols, err := c.Collections(ctx, in.Database)
		if err != nil {
			return nil, nil, err
		}
		return tools.JSONResult(map[string]any{
			"database":    in.Database,
			"count":       len(cols),
			"collections": cols,
		})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "sdb_collection_schema",
		Description: `Infer a collection's schema by sampling documents. Returns, per field: the ` +
			`name, inferred BSON type, occurrence count/frequency, whether nulls were seen, and ` +
			`sample values (as Extended JSON). Use this to discover field names and types before ` +
			`querying. sampleSize defaults to 100.`,
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in schemaInput) (*mcp.CallToolResult, any, error) {
		sampleSize := defaultSampleSize
		if in.SampleSize != nil && *in.SampleSize > 0 {
			sampleSize = *in.SampleSize
		}
		res, err := c.Schema(ctx, in.Database, in.Collection, sampleSize)
		if err != nil {
			return nil, nil, err
		}
		return tools.JSONResult(res)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "sdb_database_stats",
		Description: "Return per-collection storage statistics for a database: storage size, data " +
			"size, document count, average document size, index count, and total index size. " +
			"Includes a meta summary with the total collection count. Set bustCache to recompute.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in dbStatsInput) (*mcp.CallToolResult, any, error) {
		res, err := c.DBStats(ctx, in.Database, in.BustCache)
		if err != nil {
			return nil, nil, err
		}
		return tools.JSONResult(res)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "sdb_query",
		Description: `Run a read-only find query against a collection and return matching documents.

Filter/projection/sort are MongoDB Extended JSON. Only query operators are allowed (the server
rejects aggregation stage operators like $group here — use sdb_aggregate for those).

Example:
  filter:     {"provider": 1, "created_at": {"$gte": {"$date": "2026-07-15T00:00:00.000Z"}}}
  projection: {"call_number": 1, "prospect_type": 1, "_id": 0}
  sort:       {"_id": -1}
  limit:      20
  skip:       0

The response includes the documents (Extended JSON), the total match count, and pagination
metadata. limit defaults to 20 and is capped by the server; the applied value is reported.`,
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in queryInput) (*mcp.CallToolResult, any, error) {
		limit := defaultQueryLimit
		if in.Limit != nil {
			limit = *in.Limit
		}
		if limit < 1 {
			limit = 1
		}
		if max := c.QueryMaxLimit(); limit > max {
			limit = max
		}
		skip := in.Skip
		if skip < 0 {
			skip = 0
		}

		req := client.QueryRequest{
			Database:   in.Database,
			Collection: in.Collection,
			Limit:      limit,
			Skip:       skip,
		}
		var err error
		if req.Filter, err = rawObject(in.Filter, "filter"); err != nil {
			return nil, nil, err
		}
		if req.Projection, err = rawObject(in.Projection, "projection"); err != nil {
			return nil, nil, err
		}
		if req.Sort, err = rawObject(in.Sort, "sort"); err != nil {
			return nil, nil, err
		}

		res, err := c.Query(ctx, req)
		if err != nil {
			return nil, nil, err
		}
		return tools.JSONResult(struct {
			Data         json.RawMessage `json:"data"`
			Count        int64           `json:"count"`
			MaxPage      int64           `json:"maxPage"`
			RowsPerPage  int64           `json:"rowsPerPage"`
			AppliedLimit int             `json:"appliedLimit"`
			AppliedSkip  int             `json:"appliedSkip"`
		}{
			Data:         res.Data,
			Count:        res.Count,
			MaxPage:      res.MaxPage,
			RowsPerPage:  res.RowsPerPage,
			AppliedLimit: limit,
			AppliedSkip:  skip,
		})
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "sdb_aggregate",
		Description: `Run a read-only MongoDB aggregation pipeline and return the result documents.

pipeline is an array of Extended JSON stage objects. Supported stages include $match, $project,
$sort, $limit, $skip, $count, $group, $unwind, $addFields, and $lookup.

This tool is strictly read-only: pipelines containing write or code-execution stages
($out, $merge, $function, $accumulator, $where, $graphLookup, $unionWith) are rejected before
being sent. End large pipelines with a $limit stage to bound the result size.

Example:
  pipeline: [{"$group": {"_id": "$prospect_type", "n": {"$sum": 1}}}, {"$sort": {"n": -1}}]`,
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in aggregateInput) (*mcp.CallToolResult, any, error) {
		pipeline, err := json.Marshal(in.Pipeline)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal pipeline: %w", err)
		}
		res, err := c.Aggregate(ctx, client.AggregateRequest{
			Database:   in.Database,
			Collection: in.Collection,
			Pipeline:   pipeline,
		})
		if err != nil {
			return nil, nil, err
		}
		if len(res.Data) > aggregateSoftCap {
			return tools.JSONResult(map[string]any{
				"count":     res.Count,
				"truncated": true,
				"note": fmt.Sprintf("result data is %d bytes, exceeding the %d-byte inline limit; "+
					"add a $limit stage or narrow the pipeline (filter earlier with $match, project fewer fields)",
					len(res.Data), aggregateSoftCap),
			})
		}
		return tools.JSONResult(res)
	})
}

// rawObject marshals an optional Extended JSON object argument. A nil map (the
// argument was omitted) yields a nil RawMessage so the field is left off the
// upstream request rather than sent as JSON null.
func rawObject(m map[string]any, name string) (json.RawMessage, error) {
	if m == nil {
		return nil, nil
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal %s: %w", name, err)
	}
	return b, nil
}
