// Package auth implements the authentication-related MCP tools backed by the
// sdb.zintlr.com /api/auth endpoints. Login itself is handled internally by the
// client as part of the session lifecycle and is not exposed as a tool; the
// only agent-facing tool here is whoami.
package auth

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/client"
	"github.com/Rishabh-Kr-Zintlr/sdb-mcp/internal/tools"
)

// Register adds the auth tools to the server.
func Register(s *mcp.Server, c *client.Client) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "sdb_whoami",
		Description: "Return the identity this server is authenticated as, including " +
			"whether the account is an admin and whether it can view analytics. " +
			"Takes no arguments. Useful to confirm access before running queries.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ tools.NoArgs) (*mcp.CallToolResult, any, error) {
		user, err := c.Me(ctx)
		if err != nil {
			return nil, nil, err
		}
		return tools.JSONResult(user)
	})
}
