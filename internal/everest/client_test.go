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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestListInstances(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/v1/clusters/local/namespaces/team-a/instances"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tkn" {
			t.Errorf("auth header = %q, want %q", got, "Bearer tkn")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"orders","namespace":"team-a"},"spec":{"provider":"postgresql","version":"16"},"status":{"phase":"Ready"}}]}`))
	})

	c := NewClient(srv.URL, WithToken("tkn"))
	list, err := c.ListInstances(context.Background(), "local", "team-a")
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("got %d instances, want 1", len(list.Items))
	}
	in := list.Items[0]
	if in.Metadata.Name != "orders" || in.Spec.Provider != "postgresql" || in.Status.Phase != "Ready" {
		t.Errorf("unexpected instance: %+v", in)
	}
}

func TestCreateBackupSurfacesForbidden(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"user not allowed to create backups"}`))
	})

	c := NewClient(srv.URL, WithToken("readonly"))
	_, err := c.CreateBackup(context.Background(), "local", "team-a", BackupSpec{InstanceName: "orders", StorageName: "s3"})

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %v", err)
	}
	if apiErr.Status != http.StatusForbidden {
		t.Errorf("status = %d, want 403", apiErr.Status)
	}
	if apiErr.Message != "user not allowed to create backups" {
		t.Errorf("message = %q", apiErr.Message)
	}
}

func TestLoginStoresToken(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/session" || r.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"token":"jwt-123"}`))
	})

	c := NewClient(srv.URL)
	if err := c.Login(context.Background(), "admin", "pw"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	if c.Token() != "jwt-123" {
		t.Errorf("token = %q, want %q", c.Token(), "jwt-123")
	}
}
