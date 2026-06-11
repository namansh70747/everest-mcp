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

//go:build e2e

// Package main e2e test: builds the everest-mcp binary, runs it as a real MCP
// server over stdio (via CommandTransport), backed by a mock OpenEverest API,
// and drives it with a genuine MCP client. Run with: go test -tags e2e ./...
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestEndToEndOverStdio(t *testing.T) {
	// Mock OpenEverest API.
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/clusters":
			_, _ = w.Write([]byte(`{"items":[{"name":"local","server":"https://k8s.local"}]}`))
		case strings.HasSuffix(r.URL.Path, "/namespaces/team-a/instances"):
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"orders","namespace":"team-a"},"spec":{"provider":"postgresql","version":"16"},"status":{"phase":"Ready"}}]}`))
		case r.URL.Path == "/v1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			<-r.Context().Done() // hold the stream open
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	// Build the binary.
	bin := filepath.Join(t.TempDir(), "everest-mcp")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// Run it as an MCP server over stdio.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.Command(bin, "serve", "--everest-url", api.URL, "--token", "tkn", "--cluster", "local")
	transport := &mcp.CommandTransport{Command: cmd}

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect to binary: %v", err)
	}
	defer session.Close()

	// 1. The expected read-only tools are present; write tools are not.
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range tools.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"whoami", "list_clusters", "list_instances", "get_instance_health"} {
		if !names[want] {
			t.Errorf("missing tool %q", want)
		}
	}
	if names["create_backup"] {
		t.Error("create_backup must be absent without --allow-writes")
	}

	// 2. list_instances returns the mocked instance.
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_instances",
		Arguments: map[string]any{"namespace": "team-a"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), "orders") || !strings.Contains(string(b), "postgresql") {
		t.Errorf("unexpected output: %s", b)
	}

	// 3. The clusters resource reads live.
	rr, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: "everest://clusters"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(rr.Contents) == 0 || !strings.Contains(rr.Contents[0].Text, "local") {
		t.Errorf("clusters resource did not contain the mocked cluster: %+v", rr.Contents)
	}
}
