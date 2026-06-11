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

package events

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestStreamParsesSSEFrames verifies the consumer parses the exact SSE frame
// format the OpenEverest host writes:
//
//	id: <rv>\nevent: <type>\ndata: <json>\n\n
func TestStreamParsesSSEFrames(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		fmt.Fprint(w, ": connected\n\n")
		fmt.Fprint(w, "id: 100\nevent: database-cluster.created\n"+
			`data: {"resourceVersion":"100","type":"database-cluster.created","namespace":"team-a","resource":{"kind":"Instance","name":"orders","uid":"u1"},"newState":{"phase":"Pending"}}`+"\n\n")
		fmt.Fprint(w, "id: 101\nevent: database-cluster.failed\n"+
			`data: {"resourceVersion":"101","type":"database-cluster.failed","namespace":"team-a","resource":{"kind":"Instance","name":"orders","uid":"u1"},"newState":{"phase":"Failed"}}`+"\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	got := make(chan Event, 4)
	c := NewConsumer(srv.URL, "tkn", func(e Event) { got <- e })

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	go c.Run(ctx, []string{string(DatabaseClusterCreated)}, []string{"team-a"})

	want := []struct {
		typ   Type
		phase string
	}{
		{DatabaseClusterCreated, "Pending"},
		{DatabaseClusterFailed, "Failed"},
	}
	for i, w := range want {
		select {
		case e := <-got:
			if e.Type != w.typ {
				t.Errorf("event %d type = %q, want %q", i, e.Type, w.typ)
			}
			if e.NewState.Phase != w.phase {
				t.Errorf("event %d phase = %q, want %q", i, e.NewState.Phase, w.phase)
			}
			if e.Resource.Name != "orders" {
				t.Errorf("event %d resource name = %q, want orders", i, e.Resource.Name)
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for event %d", i)
		}
	}
}
