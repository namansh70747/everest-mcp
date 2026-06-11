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

// Command everest-fake is a tiny stand-in for the OpenEverest v1 API. It serves
// realistic, static responses so you can run and demo the MCP gateway WITHOUT a
// real Kubernetes cluster. It is NOT OpenEverest and enforces no real security —
// it exists only for local development, demos, and tests.
//
// By default it simulates a read-only user: write requests (POST) return 403,
// which is what powers the gateway's governance demo. Pass --admin to make
// writes succeed.
//
//	go run ./cmd/everest-fake --addr :8899
//	go run ./cmd/everest-mcp-demo --everest-url http://127.0.0.1:8899 \
//	  --token demo --cluster local --namespace team-alpha --allow-writes --storage s3
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"time"
)

var (
	reNamespaces = regexp.MustCompile(`/namespaces$`)
	reInstances  = regexp.MustCompile(`/instances$`)
	reInstance   = regexp.MustCompile(`/instances/[^/]+$`)
	reBackups    = regexp.MustCompile(`/backups$`)
	reConnection = regexp.MustCompile(`/instances/[^/]+/connection$`)
)

func main() {
	addr := flag.String("addr", ":8899", "listen address")
	admin := flag.Bool("admin", false, "simulate an admin (writes succeed) instead of a read-only user (writes 403)")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path

		if r.Method == http.MethodPost {
			if reBackups.MatchString(p) {
				if *admin {
					write(w, 201, `{"metadata":{"name":"orders-prod-adhoc","namespace":"team-alpha"},"spec":{"instanceName":"orders-prod","storageName":"s3-backups"},"status":{"state":"Starting"}}`)
					return
				}
				write(w, 403, `{"message":"user alice@example.com is not allowed to create backups in team-alpha"}`)
				return
			}
			write(w, 404, `{"message":"not found"}`)
			return
		}

		switch {
		case p == "/v1/plugins/context":
			write(w, 200, `{"user":"alice@example.com","groups":["dev"],"namespaces":["team-alpha","team-bravo"]}`)
		case p == "/v1/clusters":
			write(w, 200, `{"items":[{"name":"local","server":"https://kubernetes.default.svc"}]}`)
		case reNamespaces.MatchString(p):
			write(w, 200, `["team-alpha","team-bravo"]`)
		case reInstances.MatchString(p):
			write(w, 200, `{"items":[
				{"metadata":{"name":"orders-prod","namespace":"team-alpha"},"spec":{"provider":"postgresql","version":"16"},"status":{"phase":"Ready","version":"16","conditions":[{"type":"Ready","status":"True","reason":"AllComponentsReady","message":"all components are ready"}]}},
				{"metadata":{"name":"sessions-cache","namespace":"team-alpha"},"spec":{"provider":"psmdb","version":"7.0"},"status":{"phase":"Initializing","conditions":[{"type":"Ready","status":"False","reason":"WaitingForPrimary","message":"waiting for replica set primary"}]}}
			]}`)
		case reConnection.MatchString(p):
			write(w, 200, `{"type":"postgresql","provider":"postgresql","host":"orders-prod.team-alpha.svc","port":"5432","username":"app","password":"REDACTED-in-demo","uri":"postgres://app@orders-prod.team-alpha.svc:5432/app"}`)
		case reInstance.MatchString(p):
			write(w, 200, `{"metadata":{"name":"orders-prod","namespace":"team-alpha"},"spec":{"provider":"postgresql","version":"16"},"status":{"phase":"Ready","version":"16","conditions":[{"type":"Ready","status":"True","reason":"AllComponentsReady","message":"all components are ready"}]}}`)
		case reBackups.MatchString(p):
			write(w, 200, `{"items":[
				{"metadata":{"name":"orders-prod-20260610-0300","namespace":"team-alpha"},"status":{"state":"Succeeded","size":"412Mi","completedAt":"2026-06-10T03:00:00Z"}},
				{"metadata":{"name":"orders-prod-20260609-0300","namespace":"team-alpha"},"status":{"state":"Succeeded","size":"408Mi","completedAt":"2026-06-09T03:00:00Z"}}
			]}`)
		case p == "/v1/events":
			serveEvents(w, r)
		default:
			write(w, 404, `{"message":"not found: `+p+`"}`)
		}
	})

	role := "read-only user (writes denied)"
	if *admin {
		role = "admin (writes allowed)"
	}
	log.Printf("everest-fake listening on %s — simulating %s. NOT a real OpenEverest.", *addr, role)
	srv := &http.Server{Addr: *addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(srv.ListenAndServe())
}

// serveEvents emits one sample SSE event then holds the connection open, so the
// gateway's event consumer has something to read in a demo.
func serveEvents(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	fl, ok := w.(http.Flusher)
	if !ok {
		return
	}
	fmt.Fprint(w, ": connected\n\n")
	fmt.Fprint(w, "id: 1001\nevent: database-cluster.ready\n"+
		`data: {"resourceVersion":"1001","type":"database-cluster.ready","namespace":"team-alpha","resource":{"kind":"Instance","name":"orders-prod","uid":"u-1"},"newState":{"phase":"Ready"}}`+"\n\n")
	fl.Flush()
	<-r.Context().Done()
}

func write(w http.ResponseWriter, code int, body string) {
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}
