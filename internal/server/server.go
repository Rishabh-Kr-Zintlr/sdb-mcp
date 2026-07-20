// Package server constructs the MCP server, registers the available tools, and
// serves requests over the STDIO transport (stdin/stdout).
package server

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/client"
	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/config"
	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/tools/auth"
	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/tools/mongodb"
)

const instructions = `sdb-mcp exposes read-only access to the sdb.zintlr.com MongoDB data API.

Typical flow: sdb_whoami to confirm identity, then sdb_list_databases and
sdb_list_collections to discover data, sdb_collection_schema to learn a
collection's fields, then sdb_query (find) or sdb_aggregate (pipelines) to read.
sdb_database_stats reports sizes and document counts.

Filters, projections, sort specs, and pipelines are MongoDB Extended JSON. The
server is strictly read-only; aggregation write/code stages are rejected.`

// New constructs the MCP server with the HTTP client and all tools registered.
func New(cfg *config.Config, version string) *mcp.Server {
	c := client.New(cfg)
	srv := mcp.NewServer(
		&mcp.Implementation{Name: "sdb-mcp", Version: version},
		&mcp.ServerOptions{Instructions: instructions},
	)
	auth.Register(srv, c)
	mongodb.Register(srv, c)
	return srv
}

// Run builds the server and serves it over STDIO until ctx is cancelled or the
// client disconnects (stdin closes).
func Run(ctx context.Context, cfg *config.Config, version string) error {
	return New(cfg, version).Run(ctx, &mcp.StdioTransport{})
}
