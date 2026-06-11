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

// Package everest contains a thin, dependency-light client for the OpenEverest
// v1 HTTP API. The types here intentionally model only the fields the MCP
// gateway surfaces; they mirror the canonical schemas in
// github.com/openeverest/openeverest/v2/client (crds.gen.go) without importing
// the full host module and its Kubernetes/controller-runtime dependency tree.
package everest

import "time"

// Cluster is a Kubernetes cluster managed by OpenEverest.
// Mirrors components.schemas.Cluster in api/openapi/http-api.yaml.
type Cluster struct {
	Name   string `json:"name"`
	Server string `json:"server"`
}

// ClusterList is the response of GET /v1/clusters.
type ClusterList struct {
	Items []Cluster `json:"items"`
}

// ObjectMeta is the subset of Kubernetes metadata we read.
type ObjectMeta struct {
	Name              string    `json:"name"`
	Namespace         string    `json:"namespace,omitempty"`
	UID               string    `json:"uid,omitempty"`
	CreationTimestamp time.Time `json:"creationTimestamp,omitempty"`
}

// Condition mirrors metav1.Condition.
type Condition struct {
	Type               string    `json:"type"`
	Status             string    `json:"status"`
	Reason             string    `json:"reason,omitempty"`
	Message            string    `json:"message,omitempty"`
	LastTransitionTime time.Time `json:"lastTransitionTime,omitempty"`
}

// InstanceSpec is the subset of InstanceSpec we surface.
type InstanceSpec struct {
	Provider string `json:"provider,omitempty"`
	Version  string `json:"version,omitempty"`
}

// InstanceStatus is the subset of InstanceStatus we surface.
// Phase is one of: Pending, Provisioning, Initializing, Ready, Updating,
// Terminating, Failed, Restoring, Suspending, Suspended, Resuming.
type InstanceStatus struct {
	Phase      string      `json:"phase,omitempty"`
	Version    string      `json:"version,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// Instance is a database instance (the OpenEverest term for a database cluster).
type Instance struct {
	Metadata ObjectMeta     `json:"metadata"`
	Spec     InstanceSpec   `json:"spec"`
	Status   InstanceStatus `json:"status,omitempty"`
}

// InstanceList is the response of GET .../instances.
type InstanceList struct {
	Items []Instance `json:"items"`
}

// BackupSpec mirrors the subset of BackupSpec we create/read.
type BackupSpec struct {
	BackupClassName string `json:"backupClassName,omitempty"`
	InstanceName    string `json:"instanceName"`
	StorageName     string `json:"storageName"`
	DeletionPolicy  string `json:"deletionPolicy,omitempty"`
}

// BackupStatus mirrors the subset of BackupStatus we surface.
type BackupStatus struct {
	State       string      `json:"state,omitempty"`
	StartedAt   *time.Time  `json:"startedAt,omitempty"`
	CompletedAt *time.Time  `json:"completedAt,omitempty"`
	Size        string      `json:"size,omitempty"`
	Conditions  []Condition `json:"conditions,omitempty"`
}

// Backup is a database backup.
type Backup struct {
	Metadata ObjectMeta   `json:"metadata"`
	Spec     BackupSpec   `json:"spec"`
	Status   BackupStatus `json:"status,omitempty"`
}

// BackupList is the response of GET .../backups.
type BackupList struct {
	Items []Backup `json:"items"`
}

// ConnectionDetails mirrors InstanceConnectionDetails. NOTE: this contains
// credentials and is only fetched when explicitly enabled by the operator.
type ConnectionDetails struct {
	Type     string `json:"type,omitempty"`
	Provider string `json:"provider,omitempty"`
	Host     string `json:"host,omitempty"`
	Port     string `json:"port,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	URI      string `json:"uri,omitempty"`
}

// PluginContext is the response of GET /v1/plugins/context.
type PluginContext struct {
	User       string   `json:"user"`
	Groups     []string `json:"groups,omitempty"`
	Namespaces []string `json:"namespaces,omitempty"`
}

// apiError mirrors components.schemas.Error.
type apiError struct {
	Message string `json:"message"`
}

// userCredentials is the POST /v1/session request body.
type userCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// sessionToken is the POST /v1/session response body.
type sessionToken struct {
	Token string `json:"token"`
}
