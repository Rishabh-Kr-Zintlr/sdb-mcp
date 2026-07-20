# sdb-mcp

A STDIO [Model Context Protocol](https://modelcontextprotocol.io) (MCP) server
that exposes the `sdb.zintlr.com` API — authentication and MongoDB data access —
as MCP tools.

> **Status:** scaffold only. Directory structure is in place; handlers are not
> yet implemented.

## Layout

```
.
├── cmd/
│   └── sdb-mcp/        # main package — process entry point
├── internal/
│   ├── config/         # runtime configuration (base URL, credentials, env)
│   ├── client/         # HTTP client for the sdb.zintlr.com API
│   ├── server/         # MCP server construction + STDIO transport wiring
│   └── tools/          # MCP tool definitions & handlers, grouped by domain
│       ├── auth/       # login, me            (/api/auth/*)
│       └── mongodb/    # databases, collections, schema, db-stat, query (/api/mongodb/*)
├── go.mod
└── scripts/
    └── build.py        # cross-compile to dist/ for windows, mac, linux
```

## Planned tools

| Tool                    | Upstream endpoint                 |
| ----------------------- | --------------------------------- |
| `auth_login`            | `POST /api/auth/login`            |
| `auth_me`               | `GET  /api/auth/me`               |
| `mongodb_databases`     | `/api/mongodb/databases`          |
| `mongodb_collections`   | `/api/mongodb/collections/{db}`   |
| `mongodb_schema`        | `POST /api/mongodb/schema`        |
| `mongodb_db_stat`       | `/api/mongodb/db-stat`            |
| `mongodb_query`         | `/api/mongodb/query`              |

## Build

Cross-compiles for Windows, macOS, and Linux (amd64 + arm64) into `dist/`:

```sh
python scripts/build.py            # all targets
python scripts/build.py linux      # only Linux
python scripts/build.py --clean    # wipe dist/ first
```
