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

package everest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a minimal OpenEverest v1 API client. It is safe for concurrent use.
//
// Authentication uses a bearer token (Authorization: Bearer <token>), matching
// the OpenEverest API contract. The token can be supplied directly (e.g. from
// `everestctl token reset`) or obtained via Login.
type Client struct {
	baseURL string // e.g. http://localhost:8080  (the /v1 prefix is added per call)
	token   string
	http    *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithToken sets a pre-obtained bearer token.
func WithToken(token string) Option {
	return func(c *Client) { c.token = strings.TrimSpace(token) }
}

// WithHTTPClient overrides the underlying http.Client.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) { c.http = h }
}

// NewClient creates a client for the given OpenEverest base URL
// (scheme + host, without the /v1 suffix).
func NewClient(baseURL string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// Token returns the currently configured bearer token (may be empty).
func (c *Client) Token() string { return c.token }

// BaseURL returns the configured base URL.
func (c *Client) BaseURL() string { return c.baseURL }

// Login exchanges username/password for a bearer token via POST /v1/session
// and stores it on the client for subsequent calls.
func (c *Client) Login(ctx context.Context, username, password string) error {
	var out sessionToken
	err := c.do(ctx, http.MethodPost, "/v1/session", userCredentials{Username: username, Password: password}, &out)
	if err != nil {
		return err
	}
	if out.Token == "" {
		return fmt.Errorf("login succeeded but no token returned")
	}
	c.token = out.Token
	return nil
}

// PluginContext returns the current identity and accessible namespaces.
func (c *Client) PluginContext(ctx context.Context) (*PluginContext, error) {
	var out PluginContext
	if err := c.do(ctx, http.MethodGet, "/v1/plugins/context", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListClusters returns all clusters managed by OpenEverest.
func (c *Client) ListClusters(ctx context.Context) (*ClusterList, error) {
	var out ClusterList
	if err := c.do(ctx, http.MethodGet, "/v1/clusters", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListNamespaces returns the namespaces managed by OpenEverest in a cluster.
func (c *Client) ListNamespaces(ctx context.Context, cluster string) ([]string, error) {
	var out []string
	p := fmt.Sprintf("/v1/clusters/%s/namespaces", url.PathEscape(cluster))
	if err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ListInstances returns all database instances in a namespace.
func (c *Client) ListInstances(ctx context.Context, cluster, namespace string) (*InstanceList, error) {
	var out InstanceList
	p := fmt.Sprintf("/v1/clusters/%s/namespaces/%s/instances", url.PathEscape(cluster), url.PathEscape(namespace))
	if err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetInstance returns a single database instance.
func (c *Client) GetInstance(ctx context.Context, cluster, namespace, instance string) (*Instance, error) {
	var out Instance
	p := fmt.Sprintf("/v1/clusters/%s/namespaces/%s/instances/%s",
		url.PathEscape(cluster), url.PathEscape(namespace), url.PathEscape(instance))
	if err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListBackups returns the backups for a database instance.
func (c *Client) ListBackups(ctx context.Context, cluster, namespace, instance string) (*BackupList, error) {
	var out BackupList
	p := fmt.Sprintf("/v1/clusters/%s/namespaces/%s/instances/%s/backups",
		url.PathEscape(cluster), url.PathEscape(namespace), url.PathEscape(instance))
	if err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetConnection returns the connection details (incl. credentials) for an
// instance. The caller is responsible for gating this behind an explicit opt-in.
func (c *Client) GetConnection(ctx context.Context, cluster, namespace, instance string) (*ConnectionDetails, error) {
	var out ConnectionDetails
	p := fmt.Sprintf("/v1/clusters/%s/namespaces/%s/instances/%s/connection",
		url.PathEscape(cluster), url.PathEscape(namespace), url.PathEscape(instance))
	if err := c.do(ctx, http.MethodGet, p, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateBackup triggers a backup of an instance. This is a write operation and
// must be gated by the server's --allow-writes flag.
func (c *Client) CreateBackup(ctx context.Context, cluster, namespace string, spec BackupSpec) (*Backup, error) {
	var out Backup
	body := Backup{Spec: spec}
	p := fmt.Sprintf("/v1/clusters/%s/namespaces/%s/backups", url.PathEscape(cluster), url.PathEscape(namespace))
	if err := c.do(ctx, http.MethodPost, p, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// do performs an HTTP request against the OpenEverest API, marshaling the
// request body (if any) and unmarshaling the JSON response into out (if any).
// Non-2xx responses are turned into a descriptive error, decoding the API's
// Error envelope when present.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		body = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(data))
		var apiErr apiError
		if json.Unmarshal(data, &apiErr) == nil && apiErr.Message != "" {
			msg = apiErr.Message
		}
		return &APIError{Status: resp.StatusCode, Message: msg, Method: method, Path: path}
	}

	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// APIError represents a non-2xx response from the OpenEverest API.
type APIError struct {
	Status  int
	Message string
	Method  string
	Path    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("everest API %s %s: status %d", e.Method, e.Path, e.Status)
	}
	return fmt.Sprintf("everest API %s %s: status %d: %s", e.Method, e.Path, e.Status, e.Message)
}
