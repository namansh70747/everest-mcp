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

// Package mcpserver builds the Model Context Protocol server that exposes
// OpenEverest database operations as MCP tools, resources, and prompts.
//
// Every operation is executed against the OpenEverest v1 API using the acting
// user's bearer token, so the platform re-checks each call against that user's
// RBAC policy. The server cannot escalate beyond the caller's permissions: this
// is the governance guarantee, enforced by the host, not by this code.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/namansh70747/everest-mcp/internal/events"
	"github.com/namansh70747/everest-mcp/internal/everest"
)

// Config configures the MCP server.
type Config struct {
	// DefaultCluster is used when a tool call omits the cluster argument.
	DefaultCluster string
	// AllowWrites enables mutating tools (e.g. create_backup). Off by default.
	AllowWrites bool
	// AllowConnectionDetails enables the get_connection tool, which returns
	// database credentials. Off by default for safety.
	AllowConnectionDetails bool
	// Version is reported to MCP clients.
	Version string
}

// Server wires the OpenEverest client and event consumer into an MCP server.
type Server struct {
	cfg    Config
	client *everest.Client
	mcp    *mcp.Server
}

func boolPtr(b bool) *bool { return &b }

// New builds an MCP server with all tools, resources, and prompts registered.
func New(client *everest.Client, cfg Config) *Server {
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	impl := &mcp.Implementation{
		Name:    "everest-mcp",
		Title:   "OpenEverest MCP Gateway",
		Version: cfg.Version,
	}
	opts := &mcp.ServerOptions{
		Instructions: "Operate OpenEverest-managed databases (PostgreSQL, MongoDB/PSMDB, " +
			"MySQL/PXC) on Kubernetes. All actions run under the connecting user's " +
			"OpenEverest RBAC; calls the user is not permitted to make are rejected by " +
			"the platform. Use list_clusters and list_namespaces to discover scope, " +
			"then list_instances / get_instance for state.",
	}
	s := &Server{cfg: cfg, client: client, mcp: mcp.NewServer(impl, opts)}
	s.registerTools()
	s.registerResources()
	s.registerPrompts()
	return s
}

// MCP returns the underlying *mcp.Server (for transport wiring / tests).
func (s *Server) MCP() *mcp.Server { return s.mcp }

// Run serves the MCP server over stdio until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.mcp.Run(ctx, &mcp.StdioTransport{})
}

// cluster resolves the effective cluster name for a tool call.
func (s *Server) cluster(arg string) (string, error) {
	if arg != "" {
		return arg, nil
	}
	if s.cfg.DefaultCluster != "" {
		return s.cfg.DefaultCluster, nil
	}
	return "", fmt.Errorf("no cluster specified and no default cluster configured; call list_clusters and pass 'cluster'")
}

// ---- Tool input/output types ------------------------------------------------

type clusterArg struct {
	Cluster string `json:"cluster,omitempty" jsonschema:"OpenEverest cluster name; omit to use the configured default"`
}

type namespaceArg struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"OpenEverest cluster name; omit to use the configured default"`
	Namespace string `json:"namespace" jsonschema:"the namespace to query"`
}

type instanceArg struct {
	Cluster   string `json:"cluster,omitempty" jsonschema:"OpenEverest cluster name; omit to use the configured default"`
	Namespace string `json:"namespace" jsonschema:"the namespace the instance lives in"`
	Instance  string `json:"instance" jsonschema:"the database instance name"`
}

type createBackupArg struct {
	Cluster         string `json:"cluster,omitempty" jsonschema:"OpenEverest cluster name; omit to use the configured default"`
	Namespace       string `json:"namespace" jsonschema:"the namespace of the instance to back up"`
	InstanceName    string `json:"instanceName" jsonschema:"the database instance to back up"`
	StorageName     string `json:"storageName" jsonschema:"the BackupStorage to write the backup to"`
	BackupClassName string `json:"backupClassName,omitempty" jsonschema:"optional BackupClass defining how the backup runs"`
}

// instanceSummary is a model-friendly projection of an Instance.
type instanceSummary struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Provider  string `json:"provider"`
	Version   string `json:"version"`
	Phase     string `json:"phase"`
}

type instancesOut struct {
	Cluster   string            `json:"cluster"`
	Namespace string            `json:"namespace"`
	Count     int               `json:"count"`
	Instances []instanceSummary `json:"instances"`
}

type namespacesOut struct {
	Cluster    string   `json:"cluster"`
	Namespaces []string `json:"namespaces"`
}

type healthOut struct {
	Name       string              `json:"name"`
	Phase      string              `json:"phase"`
	Healthy    bool                `json:"healthy"`
	Version    string              `json:"version"`
	Conditions []everest.Condition `json:"conditions,omitempty"`
}

type backupSummary struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Size        string `json:"size,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
}

type backupsOut struct {
	Instance string          `json:"instance"`
	Count    int             `json:"count"`
	Backups  []backupSummary `json:"backups"`
}

func summarize(in everest.Instance) instanceSummary {
	return instanceSummary{
		Name:      in.Metadata.Name,
		Namespace: in.Metadata.Namespace,
		Provider:  in.Spec.Provider,
		Version:   firstNonEmpty(in.Status.Version, in.Spec.Version),
		Phase:     in.Status.Phase,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// ---- Tool registration ------------------------------------------------------

func (s *Server) registerTools() {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "whoami",
		Description: "Return the connecting user's identity and the namespaces they can access (from /v1/plugins/context).",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, *everest.PluginContext, error) {
		pc, err := s.client.PluginContext(ctx)
		if err != nil {
			return nil, nil, err
		}
		return nil, pc, nil
	})

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_clusters",
		Description: "List all Kubernetes clusters managed by OpenEverest.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, *everest.ClusterList, error) {
		cl, err := s.client.ListClusters(ctx)
		if err != nil {
			return nil, nil, err
		}
		return nil, cl, nil
	})

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_namespaces",
		Description: "List the namespaces managed by OpenEverest in a cluster.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in clusterArg) (*mcp.CallToolResult, *namespacesOut, error) {
		cluster, err := s.cluster(in.Cluster)
		if err != nil {
			return nil, nil, err
		}
		ns, err := s.client.ListNamespaces(ctx, cluster)
		if err != nil {
			return nil, nil, err
		}
		return nil, &namespacesOut{Cluster: cluster, Namespaces: ns}, nil
	})

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_instances",
		Description: "List database instances (clusters) in a namespace, with provider, version, and phase.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in namespaceArg) (*mcp.CallToolResult, *instancesOut, error) {
		cluster, err := s.cluster(in.Cluster)
		if err != nil {
			return nil, nil, err
		}
		list, err := s.client.ListInstances(ctx, cluster, in.Namespace)
		if err != nil {
			return nil, nil, err
		}
		out := &instancesOut{Cluster: cluster, Namespace: in.Namespace, Count: len(list.Items)}
		for _, it := range list.Items {
			out.Instances = append(out.Instances, summarize(it))
		}
		return nil, out, nil
	})

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "get_instance",
		Description: "Get full details of a single database instance.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in instanceArg) (*mcp.CallToolResult, *everest.Instance, error) {
		cluster, err := s.cluster(in.Cluster)
		if err != nil {
			return nil, nil, err
		}
		inst, err := s.client.GetInstance(ctx, cluster, in.Namespace, in.Instance)
		if err != nil {
			return nil, nil, err
		}
		return nil, inst, nil
	})

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "get_instance_health",
		Description: "Summarize the health of a database instance: phase, readiness, and status conditions.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in instanceArg) (*mcp.CallToolResult, *healthOut, error) {
		cluster, err := s.cluster(in.Cluster)
		if err != nil {
			return nil, nil, err
		}
		inst, err := s.client.GetInstance(ctx, cluster, in.Namespace, in.Instance)
		if err != nil {
			return nil, nil, err
		}
		out := &healthOut{
			Name:       inst.Metadata.Name,
			Phase:      inst.Status.Phase,
			Healthy:    inst.Status.Phase == "Ready",
			Version:    firstNonEmpty(inst.Status.Version, inst.Spec.Version),
			Conditions: inst.Status.Conditions,
		}
		return nil, out, nil
	})

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "list_backups",
		Description: "List the backups of a database instance, with state, size, and completion time.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in instanceArg) (*mcp.CallToolResult, *backupsOut, error) {
		cluster, err := s.cluster(in.Cluster)
		if err != nil {
			return nil, nil, err
		}
		list, err := s.client.ListBackups(ctx, cluster, in.Namespace, in.Instance)
		if err != nil {
			return nil, nil, err
		}
		out := &backupsOut{Instance: in.Instance, Count: len(list.Items)}
		for _, b := range list.Items {
			bs := backupSummary{Name: b.Metadata.Name, State: b.Status.State, Size: b.Status.Size}
			if b.Status.CompletedAt != nil {
				bs.CompletedAt = b.Status.CompletedAt.Format("2006-01-02T15:04:05Z07:00")
			}
			out.Backups = append(out.Backups, bs)
		}
		return nil, out, nil
	})

	if s.cfg.AllowConnectionDetails {
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name: "get_connection",
			Description: "Get connection details for an instance. WARNING: returns credentials. " +
				"Only enabled when the gateway is started with --allow-connection-details.",
			Annotations: readOnly,
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in instanceArg) (*mcp.CallToolResult, *everest.ConnectionDetails, error) {
			cluster, err := s.cluster(in.Cluster)
			if err != nil {
				return nil, nil, err
			}
			cd, err := s.client.GetConnection(ctx, cluster, in.Namespace, in.Instance)
			if err != nil {
				return nil, nil, err
			}
			return nil, cd, nil
		})
	}

	if s.cfg.AllowWrites {
		mcp.AddTool(s.mcp, &mcp.Tool{
			Name: "create_backup",
			Description: "Trigger a backup of a database instance. This is a write operation; it " +
				"succeeds only if the connecting user has create permission on backups (RBAC enforced by OpenEverest).",
			Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: boolPtr(false), IdempotentHint: false},
		}, func(ctx context.Context, _ *mcp.CallToolRequest, in createBackupArg) (*mcp.CallToolResult, *everest.Backup, error) {
			cluster, err := s.cluster(in.Cluster)
			if err != nil {
				return nil, nil, err
			}
			b, err := s.client.CreateBackup(ctx, cluster, in.Namespace, everest.BackupSpec{
				InstanceName:    in.InstanceName,
				StorageName:     in.StorageName,
				BackupClassName: in.BackupClassName,
			})
			if err != nil {
				return nil, nil, err
			}
			return nil, b, nil
		})
	}
}

// ---- Resources --------------------------------------------------------------

func (s *Server) registerResources() {
	s.mcp.AddResource(&mcp.Resource{
		URI:         "everest://clusters",
		Name:        "clusters",
		Description: "All Kubernetes clusters managed by OpenEverest (live).",
		MIMEType:    "application/json",
	}, func(ctx context.Context, _ *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		cl, err := s.client.ListClusters(ctx)
		if err != nil {
			return nil, err
		}
		return jsonResource("everest://clusters", cl)
	})

	// Template: everest://instances/{cluster}/{namespace}
	s.mcp.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: "everest://instances/{cluster}/{namespace}",
		Name:        "instances",
		Description: "Database instances in a given cluster/namespace (live).",
		MIMEType:    "application/json",
	}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		cluster, namespace, ok := parseInstancesURI(req.Params.URI)
		if !ok {
			return nil, fmt.Errorf("invalid instances URI: %s", req.Params.URI)
		}
		list, err := s.client.ListInstances(ctx, cluster, namespace)
		if err != nil {
			return nil, err
		}
		return jsonResource(req.Params.URI, list)
	})
}

// parseInstancesURI extracts cluster and namespace from
// everest://instances/{cluster}/{namespace}.
func parseInstancesURI(uri string) (cluster, namespace string, ok bool) {
	const prefix = "everest://instances/"
	if !strings.HasPrefix(uri, prefix) {
		return "", "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(uri, prefix), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func jsonResource(uri string, v any) (*mcp.ReadResourceResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{{
			URI:      uri,
			MIMEType: "application/json",
			Text:     string(b),
		}},
	}, nil
}

// ---- Live updates -----------------------------------------------------------

// OnEvent notifies subscribed MCP clients that an instance-related resource
// changed, so they re-read the latest state. Wire this as the events.Consumer
// handler. It is a no-op for event types that don't map to a resource.
func (s *Server) OnEvent(ctx context.Context, evt events.Event) {
	switch evt.Type {
	case events.DatabaseClusterCreated, events.DatabaseClusterReady,
		events.DatabaseClusterUpdated, events.DatabaseClusterDeleted,
		events.DatabaseClusterFailed, events.InstanceCreated, events.InstanceDeleted:
		// Notify the clusters list and any per-namespace instance lists.
		_ = s.mcp.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: "everest://clusters"})
		if evt.Namespace != "" {
			// We cannot know the cluster from the event alone; notify the default.
			if s.cfg.DefaultCluster != "" {
				uri := fmt.Sprintf("everest://instances/%s/%s", s.cfg.DefaultCluster, evt.Namespace)
				_ = s.mcp.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: uri})
			}
		}
	}
}

// ---- Prompts ----------------------------------------------------------------

func (s *Server) registerPrompts() {
	s.mcp.AddPrompt(&mcp.Prompt{
		Name:        "diagnose-instance",
		Description: "Diagnose the health of a database instance and suggest next steps.",
		Arguments: []*mcp.PromptArgument{
			{Name: "namespace", Description: "namespace of the instance", Required: true},
			{Name: "instance", Description: "instance name", Required: true},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		ns := req.Params.Arguments["namespace"]
		inst := req.Params.Arguments["instance"]
		text := fmt.Sprintf(
			"Diagnose the OpenEverest database instance %q in namespace %q.\n"+
				"1. Call get_instance_health to read its phase and conditions.\n"+
				"2. Call list_backups to check recent backup success.\n"+
				"3. If the phase is Failed or a condition is not satisfied, explain the likely "+
				"cause and the safest remediation. Do not perform any write action unless the "+
				"user explicitly asks.", inst, ns)
		return &mcp.GetPromptResult{
			Description: "Database instance diagnosis",
			Messages: []*mcp.PromptMessage{{
				Role:    "user",
				Content: &mcp.TextContent{Text: text},
			}},
		}, nil
	})
}
