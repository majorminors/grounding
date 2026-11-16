package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/spf13/cobra"

	"btr.mt/grounding/internal/auth"
	"btr.mt/grounding/internal/backends"
	"btr.mt/grounding/internal/config"
	"btr.mt/grounding/internal/tools"
)

var (
	transport string
	addr      string
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start MCP server",
	Long:  `Starts the grounding MCP server. Use --transport to choose stdio (default) or http.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		s := server.NewMCPServer(
			"grounding",
			version,
			server.WithToolCapabilities(true),
			// The tool list is shaped per session: public callers don't see
			// full-tier tools, and full-tier callers see the descriptions
			// that name the paid backends and what they cost. The context
			// carries the tier resolved per request (HTTP) or the full-tier
			// default (stdio).
			server.WithToolFilter(func(ctx context.Context, listed []mcp.Tool) []mcp.Tool {
				tier := auth.GetTier(ctx)
				visible := make([]mcp.Tool, 0, len(listed))
				for _, tool := range listed {
					definition, known := tools.Lookup(tool.Name)
					if known && definition.RequiresAuth && tier == auth.TierPublic {
						continue
					}
					if known {
						tool.Description = definition.DescriptionForTier(tier)
					}
					visible = append(visible, tool)
				}
				return visible
			}),
		)

		var researchLedger *tools.ResearchLedger
		if servingResearchSession() {
			if transport != "stdio" {
				return fmt.Errorf("a research session is served over stdio only")
			}
			var err error
			if researchLedger, err = newResearchSessionLedger(); err != nil {
				return err
			}
		}

		for _, tool := range tools.All() {
			if tool.Handler == nil || !toolIsServedInThisSession(tool.Name) {
				continue
			}

			var mcpTool mcp.Tool
			if len(tool.InputSchema.Properties) > 0 {
				schemaJSON, err := json.Marshal(tool.InputSchema)
				if err != nil {
					return err
				}
				mcpTool = mcp.NewToolWithRawSchema(tool.Name, tool.Description, schemaJSON)
			} else {
				mcpTool = mcp.NewTool(tool.Name, mcp.WithDescription(tool.Description))
			}
			mcpTool.Annotations = annotationsFor(tool)

			handler := tool.Handler
			if researchLedger != nil {
				handler = researchSessionToolHandler(researchLedger, researchSessionLedgerFile, handler)
			}
			if transport == "http" {
				handler = publicTierCallLimitedHandler(tool.Name, publicTierCallLimiter, handler)
				handler = loggedToolHandler(tool.Name, handler)
			}
			s.AddTool(mcpTool, server.ToolHandlerFunc(handler))
		}

		switch transport {
		case "stdio":
			return server.ServeStdio(s)
		case "http":
			cfg := config.LoadConfig()
			backends.EnableResponseCache(0)
			// Fail closed: with no token every HTTP caller would resolve to
			// the full tier and get downloads and the paid backends.
			if cfg.AuthToken == "" {
				return fmt.Errorf("refusing to serve HTTP without GROUNDING_AUTH_TOKEN: every caller would get full access; set the token (or use --transport stdio for local use)")
			}
			mcpHandler := server.NewStreamableHTTPServer(s,
				server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
					ctx = withClientAddress(ctx, clientAddressOfRequest(r))
					return auth.WithTier(ctx, auth.ResolveTier(cfg.AuthToken, r))
				}),
			)
			mux := http.NewServeMux()
			mux.Handle("/mcp", mcpHandler)
			mux.HandleFunc("/health", handleHealth)
			mux.HandleFunc("/llms.txt", makeLlmsTxtHandler(cfg.AuthToken))
			mux.HandleFunc("/", makeLandingHandler(cfg.AuthToken))
			log.Printf("Starting MCP server on %s", addr)
			return http.ListenAndServe(addr, mux)
		default:
			return fmt.Errorf("unknown transport %q (use stdio or http)", transport)
		}
	},
}

// publicTierCallLimiter is shared by every tool, so the allowance is
// per caller, not per caller per tool.
var publicTierCallLimiter = newPublicTierCallLimiterByClientAddress(
	publicTierCallBurstPerClient,
	publicTierCallRefillIntervalPerClient,
	publicTierIdleClientForgottenAfter,
)

// annotationsFor states every hint explicitly. mcp.NewTool defaults to
// readOnlyHint false and destructiveHint true, and NewToolWithRawSchema
// sends none, so without this every tool looks like it might change
// something and clients such as an agent harness run calls one at a time and
// ask before each. All tools reach external services (openWorldHint).
// Nothing is destructive, and no tool writes on the server:
// find_download_link returns a link (only the command line's --save-to
// writes a file, named for the document).
func annotationsFor(tool tools.ToolDef) mcp.ToolAnnotation {
	return mcp.ToolAnnotation{
		Title:           tool.Title,
		ReadOnlyHint:    mcp.ToBoolPtr(tool.ReadOnly),
		DestructiveHint: mcp.ToBoolPtr(false),
		IdempotentHint:  mcp.ToBoolPtr(tool.ReadOnly),
		OpenWorldHint:   mcp.ToBoolPtr(true),
	}
}

// loggedToolHandler writes one line per tool call so the server's
// journal answers "who is calling what, and how slowly" without a
// metrics stack. Arguments are not logged; queries can be personal.
// A call whose client stopped waiting (its request's context
// cancelled) is client-cancelled, whatever the tool then returned: a
// tool-error there is the cancellation's doing, not the tool's.
func loggedToolHandler(name string, handler tools.HandlerFunc) tools.HandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		result, err := handler(ctx, request)
		outcome := "ok"
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			outcome = "client-cancelled"
		case err != nil:
			outcome = "error"
		case result != nil && result.IsError:
			outcome = "tool-error"
		}
		log.Printf("tool=%s tier=%s outcome=%s duration=%s",
			name, auth.GetTier(ctx), outcome, time.Since(start).Truncate(time.Millisecond))
		return result, err
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok","version":"` + version + `"}`))
}

func init() {
	serveCmd.Flags().StringVar(&transport, "transport", "stdio", "Transport type: stdio or http")
	serveCmd.Flags().StringVar(&addr, "addr", ":9471", "Listen address for HTTP transport")
	rootCmd.AddCommand(serveCmd)
}
