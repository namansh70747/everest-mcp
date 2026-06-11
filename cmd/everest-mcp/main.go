// Copyright (C) 2026 The everest-mcp Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Command everest-mcp runs the OpenEverest MCP Gateway: an MCP server that
// exposes OpenEverest database operations to AI agents over stdio, under the
// connecting user's RBAC.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/namansh70747/everest-mcp/internal/events"
	"github.com/namansh70747/everest-mcp/internal/everest"
	"github.com/namansh70747/everest-mcp/internal/mcpserver"
)

// version is overridable at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	fs := flag.NewFlagSet("everest-mcp", flag.ExitOnError)
	var (
		everestURL    = fs.String("everest-url", envOr("EVEREST_URL", "http://localhost:8080"), "OpenEverest API base URL (without /v1)")
		token         = fs.String("token", os.Getenv("EVEREST_TOKEN"), "bearer token (e.g. from `everestctl token reset`); or set EVEREST_TOKEN")
		username      = fs.String("username", os.Getenv("EVEREST_USERNAME"), "username for password login (alternative to --token)")
		password      = fs.String("password", os.Getenv("EVEREST_PASSWORD"), "password for password login")
		defaultClus   = fs.String("cluster", os.Getenv("EVEREST_CLUSTER"), "default cluster name for tool calls that omit it")
		allowWrites   = fs.Bool("allow-writes", false, "enable mutating tools (create_backup); still RBAC-gated by OpenEverest")
		allowConnDeux = fs.Bool("allow-connection-details", false, "enable get_connection, which returns DB credentials")
		watchEvents   = fs.Bool("watch-events", true, "subscribe to /v1/events and push live resource updates (stdio mode)")
		httpAddr      = fs.String("http", os.Getenv("EVEREST_MCP_HTTP"), "if set (e.g. :8080), serve MCP over Streamable HTTP instead of stdio (in-cluster plugin mode)")
	)
	// Accept an optional leading "serve" subcommand for ergonomics
	// (`everestctl plugin run mcp -- serve ...`).
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "serve" {
		args = args[1:]
	}
	if err := fs.Parse(args); err != nil {
		fail(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := mcpserver.Config{
		DefaultCluster:         *defaultClus,
		AllowWrites:            *allowWrites,
		AllowConnectionDetails: *allowConnDeux,
		Version:                version,
	}

	if *httpAddr != "" {
		serveHTTP(ctx, *httpAddr, *everestURL, *token, cfg)
		return
	}

	// stdio mode: a single identity for the whole process.
	if *token == "" && (*username == "" || *password == "") {
		fail(fmt.Errorf("provide --token (or EVEREST_TOKEN), or --username and --password for login"))
	}

	client := everest.NewClient(*everestURL, everest.WithToken(*token))
	if *token == "" {
		if err := client.Login(ctx, *username, *password); err != nil {
			fail(fmt.Errorf("login: %w", err))
		}
	}

	srv := mcpserver.New(client, cfg)

	if *watchEvents {
		consumer := events.NewConsumer(*everestURL, client.Token(), func(evt events.Event) {
			srv.OnEvent(ctx, evt)
		})
		go consumer.Run(ctx, nil, nil)
	}

	// Log to stderr so we don't corrupt the stdio MCP transport on stdout.
	fmt.Fprintf(os.Stderr, "everest-mcp %s: serving MCP over stdio (everest=%s, writes=%v)\n",
		version, *everestURL, *allowWrites)

	if err := srv.Run(ctx); err != nil && ctx.Err() == nil {
		fail(err)
	}
}

// serveHTTP runs the gateway as an in-cluster plugin backend over Streamable
// HTTP. Each request is authenticated independently: the bearer token on the
// incoming request (forwarded by the OpenEverest plugin proxy as the acting
// user's identity) is used for that session's OpenEverest calls, so RBAC is
// enforced per user. The fallbackToken is used only when a request carries no
// Authorization header (e.g. local testing).
func serveHTTP(ctx context.Context, addr, everestURL, fallbackToken string, cfg mcpserver.Config) {
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           newHTTPHandler(everestURL, fallbackToken, cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutCtx)
	}()

	fmt.Fprintf(os.Stderr, "everest-mcp %s: serving MCP over Streamable HTTP on %s (everest=%s, writes=%v)\n",
		version, addr, everestURL, cfg.AllowWrites)
	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fail(err)
	}
}

// newHTTPHandler builds the in-cluster plugin backend handler. Each MCP session
// is bound to the bearer token on its request, so OpenEverest enforces RBAC per
// acting user. fallbackToken is used only when a request carries no token.
func newHTTPHandler(everestURL, fallbackToken string, cfg mcpserver.Config) http.Handler {
	getServer := func(r *http.Request) *mcp.Server {
		tok := bearerToken(r)
		if tok == "" {
			tok = fallbackToken
		}
		if tok == "" {
			return nil // SDK serves 400 Bad Request
		}
		client := everest.NewClient(everestURL, everest.WithToken(tok))
		return mcpserver.New(client, cfg).MCP()
	}

	mux := http.NewServeMux()
	mux.Handle("/", mcp.NewStreamableHTTPHandler(getServer, nil))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return mux
}

// bearerToken extracts the token from an Authorization: Bearer header.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "everest-mcp:", err)
	os.Exit(1)
}
