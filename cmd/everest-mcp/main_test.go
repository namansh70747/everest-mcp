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

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/namansh70747/everest-mcp/internal/mcpserver"
)

func TestBearerToken(t *testing.T) {
	t.Parallel()
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	if got := bearerToken(r); got != "" {
		t.Errorf("no header: got %q, want empty", got)
	}
	r.Header.Set("Authorization", "Bearer abc.def")
	if got := bearerToken(r); got != "abc.def" {
		t.Errorf("got %q, want abc.def", got)
	}
	r.Header.Set("Authorization", "bearer xyz") // case-insensitive scheme
	if got := bearerToken(r); got != "xyz" {
		t.Errorf("got %q, want xyz", got)
	}
}

func TestHealthz(t *testing.T) {
	t.Parallel()
	h := newHTTPHandler("http://unused", "", mcpserver.Config{})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.String() != "ok" {
		t.Errorf("healthz = %d %q", rec.Code, rec.Body.String())
	}
}

// TestHTTPTransportForwardsUserToken proves the governance property of the
// in-cluster mode: the per-request bearer token is the one used for the
// OpenEverest API call, so the host enforces RBAC for that specific user.
func TestHTTPTransportForwardsUserToken(t *testing.T) {
	t.Parallel()

	var seenToken string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenToken = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		_, _ = w.Write([]byte(`{"items":[{"name":"local","server":"https://k8s.local"}]}`))
	}))
	defer api.Close()

	gw := httptest.NewServer(newHTTPHandler(api.URL, "", mcpserver.Config{DefaultCluster: "local"}))
	defer gw.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Connect an MCP client over Streamable HTTP, presenting the user's token.
	transport := &mcp.StreamableClientTransport{
		Endpoint:   gw.URL,
		HTTPClient: &http.Client{Transport: tokenInjector{base: http.DefaultTransport, token: "alice-jwt"}},
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "t", Version: "0"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_clusters"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), "local") {
		t.Errorf("unexpected output: %s", b)
	}
	if seenToken != "alice-jwt" {
		t.Errorf("OpenEverest API saw token %q, want the user's token %q", seenToken, "alice-jwt")
	}
}

// tokenInjector adds a bearer token to every outgoing request, simulating the
// OpenEverest plugin proxy forwarding the acting user's identity.
type tokenInjector struct {
	base  http.RoundTripper
	token string
}

func (t tokenInjector) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(r)
}
