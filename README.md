# sdb-mcp

A STDIO [Model Context Protocol](https://modelcontextprotocol.io) (MCP) server
that exposes the `sdb.zintlr.com` API — authentication and **read-only** MongoDB
data access — as MCP tools, built on the official
[`go-sdk`](https://github.com/modelcontextprotocol/go-sdk).

The server logs in with a username/password from the environment, holds the
session token in memory, and refreshes it automatically. It exposes seven
read-only tools; there are no write paths, and aggregation pipelines that would
write data or execute code are rejected before they are sent.

See [PLAN.md](PLAN.md) for the full API contract and design notes.

## Tools

| Tool                    | Upstream endpoint                 | Purpose |
| ----------------------- | --------------------------------- | ------- |
| `sdb_whoami`            | `GET  /api/auth/me`               | Identity the server acts as (admin / analytics flags). |
| `sdb_list_databases`    | `GET  /api/mongodb/databases`     | Databases with on-disk size. |
| `sdb_list_collections`  | `GET  /api/mongodb/collections/{db}` | Collection names in a database. |
| `sdb_collection_schema` | `POST /api/mongodb/schema`        | Inferred field types from a document sample. |
| `sdb_database_stats`    | `POST /api/mongodb/db-stats`      | Per-collection storage / document-count stats (NDJSON). |
| `sdb_query`             | `POST /api/mongodb/query`         | Find-style query (filter, projection, sort, paging). |
| `sdb_aggregate`         | `POST /api/mongodb/aggregate`     | Read-only aggregation pipeline. |

Filters, projections, sort specs, and pipelines are authored as MongoDB
[Extended JSON](https://www.mongodb.com/docs/manual/reference/mongodb-extended-json/)
and passed through verbatim.

## Configuration

Configuration is read from the environment. `SDB_USERNAME` and `SDB_PASSWORD`
are required; the rest have defaults.

| Variable | Required | Default | Purpose |
| --- | --- | --- | --- |
| `SDB_USERNAME` | ✅ | — | Login username. |
| `SDB_PASSWORD` | ✅ | — | Login password. |
| `SDB_BASE_URL` | — | `https://sdb.zintlr.com` | Upstream base URL. |
| `SDB_HTTP_TIMEOUT` | — | `30s` | Per-request timeout (Go duration). |
| `SDB_QUERY_MAX_LIMIT` | — | `100` | Hard cap on `sdb_query` `limit`. |
| `SDB_USER_AGENT` | — | `sdb-mcp/<version> (+github.com/Rishabh-Kr-Zintlr/sdb-mcp)` | Outbound User-Agent. |

Credentials come only from the environment and are never logged. The server
communicates over stdin/stdout, so all diagnostics go to stderr.

## Install

Requires Go 1.26+ (older Go 1.21+ toolchains will download the required
version automatically).

```sh
go install github.com/Rishabh-Kr-Zintlr/sdb-mcp/cmd/sdb-mcp@latest
```

This places an `sdb-mcp` binary in `$(go env GOPATH)/bin` (usually
`~/go/bin`, or `%USERPROFILE%\go\bin` on Windows). Make sure that directory is
on your `PATH`. Pin a release with `@v0.1.0` instead of `@latest`.

## Build

Cross-compiles for Windows, macOS, and Linux (amd64 + arm64) into `dist/`,
injecting the version into the binary:

```sh
python scripts/build.py                    # all targets, default version
python scripts/build.py linux              # only Linux
python scripts/build.py --clean            # wipe dist/ first
python scripts/build.py --version 0.2.0    # set the injected version
```

## Run

Development:

```sh
SDB_USERNAME=you SDB_PASSWORD=secret go run ./cmd/sdb-mcp
```

MCP client (e.g. Claude Desktop / Claude Code) — point `command` at the
installed binary (or a built one from `dist/`) and supply credentials via `env`.
If the client doesn't inherit your shell `PATH`, use the absolute path, e.g.
`/home/you/go/bin/sdb-mcp`:

```json
{
  "mcpServers": {
    "sdb": {
      "command": "sdb-mcp",
      "env": {
        "SDB_USERNAME": "you",
        "SDB_PASSWORD": "secret"
      }
    }
  }
}
```

## Test

```sh
go test ./...
```

Tests run entirely against in-process HTTP servers and an in-memory MCP
transport — no live calls. They cover login and `Set-Cookie` parsing, the clean
header policy, proactive (JWT `exp`) and reactive (`401`) token refresh, `429`
backoff, NDJSON parsing, Extended-JSON round-tripping, the aggregation
write-stage guard, and `limit` clamping.

## Layout

```
.
├── cmd/sdb-mcp/          # main package — process entry point, version var
├── internal/
│   ├── config/           # env-sourced configuration + validation
│   ├── client/           # HTTP client: transport, auth lifecycle, endpoints, errors
│   ├── server/           # MCP server construction + STDIO transport
│   └── tools/            # MCP tool definitions & handlers
│       ├── auth/         # sdb_whoami
│       └── mongodb/      # list/schema/stats/query/aggregate tools
├── scripts/build.py      # cross-compile to dist/
├── go.mod
└── PLAN.md               # API contract and design
```
