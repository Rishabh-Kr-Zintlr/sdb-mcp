// Command sdb-mcp is a STDIO Model Context Protocol (MCP) server that exposes
// the sdb.zintlr.com API (authentication and read-only MongoDB data access) as
// MCP tools.
//
// Configuration comes from the environment (see internal/config): SDB_USERNAME
// and SDB_PASSWORD are required. The server communicates over stdin/stdout, so
// all diagnostics are written to stderr.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/config"
	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/server"
)

// version is the build version, overridable at link time via
// -ldflags "-X main.version=...". When left at "dev", resolveVersion falls
// back to the module version recorded by `go install module@version`.
var version = "dev"

// resolveVersion returns the link-time version if set, otherwise the main
// module's version from the embedded build info (e.g. "v0.1.0" for
// `go install ...@v0.1.0`), otherwise "dev".
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func main() {
	version = resolveVersion()

	cfg, err := config.Load(version)
	if err != nil {
		fmt.Fprintf(os.Stderr, "sdb-mcp: configuration error: %v\n", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := server.Run(ctx, cfg, version); err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintf(os.Stderr, "sdb-mcp: %v\n", err)
		os.Exit(1)
	}
}
