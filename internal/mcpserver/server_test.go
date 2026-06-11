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

package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/namansh70747/everest-mcp/internal/everest"
)

// connect spins up the MCP server backed by the given OpenEverest mock and
// returns a connected MCP client session.
func connect(t *testing.T, cfg Config, everestHandler http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	api := httptest.NewServer(everestHandler)
	t.Cleanup(api.Close)

	client := everest.NewClient(api.URL, everest.WithToken("tkn"))
	srv := New(client, cfg)

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.MCP().Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestListInstancesTool(t *testing.T) {
	t.Parallel()
	cs := connect(t, Config{DefaultCluster: "local"}, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/namespaces/team-a/instances") {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"orders","namespace":"team-a"},"spec":{"provider":"postgresql"},"status":{"phase":"Ready"}}]}`))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_instances",
		Arguments: map[string]any{"namespace": "team-a"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error: %+v", res.Content)
	}
	var out instancesOut
	mustUnmarshalStructured(t, res, &out)
	if out.Count != 1 || out.Instances[0].Name != "orders" || out.Instances[0].Provider != "postgresql" {
		t.Errorf("unexpected output: %+v", out)
	}
}

// TestRBACDenialSurfacesAsToolError is the governance guarantee: when the
// OpenEverest host denies a write, the tool call returns an error result the
// model can see — the gateway never bypasses RBAC.
func TestRBACDenialSurfacesAsToolError(t *testing.T) {
	t.Parallel()
	cs := connect(t, Config{DefaultCluster: "local", AllowWrites: true}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"user not allowed to create backups"}`))
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "create_backup",
		Arguments: map[string]any{"namespace": "team-a", "instanceName": "orders", "storageName": "s3"},
	})
	if err != nil {
		t.Fatalf("CallTool transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected tool error result for RBAC denial, got success")
	}
	if txt := contentText(res); !strings.Contains(txt, "not allowed") && !strings.Contains(txt, "403") {
		t.Errorf("error text %q does not mention the denial", txt)
	}
}

// TestWriteToolHiddenByDefault verifies create_backup is not registered unless
// --allow-writes is set.
func TestWriteToolHiddenByDefault(t *testing.T) {
	t.Parallel()
	cs := connect(t, Config{DefaultCluster: "local"}, func(w http.ResponseWriter, _ *http.Request) {})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		if tool.Name == "create_backup" {
			t.Error("create_backup should be hidden when AllowWrites is false")
		}
	}
}

func mustUnmarshalStructured(t *testing.T, res *mcp.CallToolResult, v any) {
	t.Helper()
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
}

func contentText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}
