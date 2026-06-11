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

// Package events consumes the OpenEverest lifecycle event stream
// (GET /v1/events, Server-Sent Events) and maintains a small in-memory view of
// recent events. The envelope mirrors github.com/openeverest/openeverest/v2
// pkg/events/types.go exactly.
package events

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Type is an event type. Values match pkg/events.Type in the host.
type Type string

// Event type constants — kept in sync with pkg/events/types.go.
const (
	DatabaseClusterCreated Type = "database-cluster.created"
	DatabaseClusterReady   Type = "database-cluster.ready"
	DatabaseClusterUpdated Type = "database-cluster.updated"
	DatabaseClusterDeleted Type = "database-cluster.deleted"
	DatabaseClusterFailed  Type = "database-cluster.failed"
	BackupStarted          Type = "backup.started"
	BackupCompleted        Type = "backup.completed"
	BackupFailed           Type = "backup.failed"
	RestoreStarted         Type = "restore.started"
	RestoreCompleted       Type = "restore.completed"
	RestoreFailed          Type = "restore.failed"
	InstanceCreated        Type = "instance.created"
	InstanceDeleted        Type = "instance.deleted"
)

// ResourceRef mirrors pkg/events.ResourceRef.
type ResourceRef struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	UID     string `json:"uid"`
	Engine  string `json:"engine,omitempty"`
	Version string `json:"version,omitempty"`
}

// StateSnapshot mirrors pkg/events.StateSnapshot.
type StateSnapshot struct {
	Phase string `json:"phase,omitempty"`
}

// Actor mirrors pkg/events.Actor.
type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Event mirrors pkg/events.Event — the JSON envelope streamed over SSE.
type Event struct {
	ResourceVersion string        `json:"resourceVersion"`
	Type            Type          `json:"type"`
	OccurredAt      time.Time     `json:"occurredAt"`
	Namespace       string        `json:"namespace"`
	Resource        ResourceRef   `json:"resource"`
	PrevState       StateSnapshot `json:"prevState,omitempty"`
	NewState        StateSnapshot `json:"newState,omitempty"`
	Actor           Actor         `json:"actor,omitempty"`
}

// Handler is invoked for each event as it arrives.
type Handler func(Event)

// Consumer holds an open SSE connection to /v1/events and invokes a handler
// for each event it receives.
type Consumer struct {
	baseURL string
	token   string
	http    *http.Client
	handler Handler
}

// NewConsumer creates an event consumer for the given OpenEverest base URL.
func NewConsumer(baseURL, token string, handler Handler) *Consumer {
	return &Consumer{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{}, // no timeout: the SSE stream is long-lived
		handler: handler,
	}
}

// Run connects and streams events until ctx is cancelled. On stream drop it
// reconnects with exponential backoff (capped), mirroring the kube-watch
// restart pattern the host documents. Run blocks; callers typically `go Run`.
func (c *Consumer) Run(ctx context.Context, types, namespaces []string) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		err := c.stream(ctx, types, namespaces)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// transient: wait and reconnect.
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

// stream opens one SSE connection and reads frames until it closes or errors.
func (c *Consumer) stream(ctx context.Context, types, namespaces []string) error {
	u := c.baseURL + "/v1/events"
	q := []string{}
	if len(types) > 0 {
		q = append(q, "types="+strings.Join(types, ","))
	}
	if len(namespaces) > 0 {
		q = append(q, "namespaces="+strings.Join(namespaces, ","))
	}
	if len(q) > 0 {
		u += "?" + strings.Join(q, "&")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("events stream: status %d", resp.StatusCode)
	}

	// Parse SSE frames. The host writes:
	//   id: <resourceVersion>\nevent: <type>\ndata: <json>\n\n
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	var dataLines []string
	flush := func() {
		if len(dataLines) == 0 {
			return
		}
		raw := strings.Join(dataLines, "\n")
		dataLines = dataLines[:0]
		var evt Event
		if err := json.Unmarshal([]byte(raw), &evt); err != nil {
			return // skip malformed frame
		}
		if c.handler != nil {
			c.handler(evt)
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "": // frame boundary
			flush()
		case strings.HasPrefix(line, ":"): // comment / keep-alive
			continue
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		default:
			// id:/event: lines — ignored; data carries the full envelope.
		}
	}
	flush()
	return scanner.Err()
}
