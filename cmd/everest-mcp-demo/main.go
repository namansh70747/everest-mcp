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

// Command everest-mcp-demo is a scripted MCP client that drives the everest-mcp
// gateway over stdio — exactly the way Claude Desktop / Cursor would — and runs
// a sequence of real tool calls against a live OpenEverest, printing each call
// and its result. Use it to verify the gateway end-to-end and to produce a
// reproducible terminal demo.
//
//	go build -o everest-mcp ./cmd/everest-mcp
//	go run ./cmd/everest-mcp-demo \
//	  --everest-url "$EVEREST_URL" --token "$EVEREST_TOKEN" \
//	  --cluster local --namespace team-alpha
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	var (
		bin         = flag.String("bin", "./everest-mcp", "path to the everest-mcp gateway binary")
		everestURL  = flag.String("everest-url", env("EVEREST_URL", "http://localhost:8080"), "OpenEverest API base URL")
		token       = flag.String("token", os.Getenv("EVEREST_TOKEN"), "bearer token; or set EVEREST_TOKEN")
		cluster     = flag.String("cluster", env("EVEREST_CLUSTER", "local"), "cluster name")
		namespace   = flag.String("namespace", "", "namespace to inspect (required for instance calls)")
		allowWrites = flag.Bool("allow-writes", false, "also attempt create_backup (shows RBAC allow/deny)")
		storage     = flag.String("storage", "", "BackupStorage name for the create_backup attempt")
		backupClass = flag.String("backup-class", "default", "BackupClass name for the create_backup attempt")
	)
	flag.Parse()
	if *token == "" {
		die("provide --token or EVEREST_TOKEN")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	args := []string{"serve", "--everest-url", *everestURL, "--token", *token, "--cluster", *cluster, "--watch-events=false"}
	if *allowWrites {
		args = append(args, "--allow-writes")
	}
	cmd := exec.Command(*bin, args...)
	cmd.Stderr = os.Stderr // surface the gateway's startup log

	session, err := mcp.NewClient(&mcp.Implementation{Name: "everest-mcp-demo", Version: "0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		die(fmt.Sprintf("connect to gateway %q: %v", *bin, err))
	}
	defer session.Close()

	fmt.Println("# OpenEverest MCP Gateway — live demo")
	fmt.Printf("# gateway=%s everest=%s cluster=%s\n\n", *bin, *everestURL, *cluster)

	// Discover the tool surface the agent sees.
	if tools, err := session.ListTools(ctx, nil); err == nil {
		fmt.Print("$ tools available: ")
		for i, t := range tools.Tools {
			if i > 0 {
				fmt.Print(", ")
			}
			fmt.Print(t.Name)
		}
		fmt.Print("\n\n")
	}

	call(ctx, session, "whoami", nil)
	call(ctx, session, "list_clusters", nil)

	if *namespace == "" {
		fmt.Println("\n# (pass --namespace to run instance/backup calls)")
		return
	}

	call(ctx, session, "list_namespaces", map[string]any{})
	out := call(ctx, session, "list_instances", map[string]any{"namespace": *namespace})

	// Drill into the first instance, if any.
	if name := firstInstanceName(out); name != "" {
		call(ctx, session, "get_instance_health", map[string]any{"namespace": *namespace, "instance": name})
		call(ctx, session, "list_backups", map[string]any{"namespace": *namespace, "instance": name})

		if *allowWrites && *storage != "" {
			fmt.Println("\n# --- governance check: attempt a write; the host enforces RBAC ---")
			call(ctx, session, "create_backup", map[string]any{
				"namespace": *namespace, "instanceName": name,
				"storageName": *storage, "backupClassName": *backupClass,
			})
		}
	} else {
		fmt.Printf("\n# no instances in namespace %q to drill into\n", *namespace)
	}
}

// call runs one tool and prints the request and result in a transcript style.
func call(ctx context.Context, s *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	argJSON, _ := json.Marshal(args)
	fmt.Printf("$ call %s %s\n", name, string(argJSON))
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		fmt.Printf("  transport error: %v\n\n", err)
		return nil
	}
	if res.IsError {
		fmt.Printf("  DENIED: %s\n\n", text(res))
		return res
	}
	if res.StructuredContent != nil {
		b, _ := json.MarshalIndent(res.StructuredContent, "  ", "  ")
		fmt.Printf("  OK %s\n\n", string(b))
	} else {
		fmt.Printf("  OK %s\n\n", text(res))
	}
	return res
}

func firstInstanceName(res *mcp.CallToolResult) string {
	if res == nil || res.StructuredContent == nil {
		return ""
	}
	b, _ := json.Marshal(res.StructuredContent)
	var out struct {
		Instances []struct {
			Name string `json:"name"`
		} `json:"instances"`
	}
	if json.Unmarshal(b, &out) == nil && len(out.Instances) > 0 {
		return out.Instances[0].Name
	}
	return ""
}

func text(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return "(no text content)"
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func die(msg string) {
	fmt.Fprintln(os.Stderr, "everest-mcp-demo:", msg)
	os.Exit(1)
}
