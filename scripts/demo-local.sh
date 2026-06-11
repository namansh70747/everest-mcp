#!/usr/bin/env bash
# Copyright (C) 2026 The everest-mcp Contributors
# Licensed under the Apache License, Version 2.0.
#
# One-shot local demo: starts the fake OpenEverest, builds the gateway, runs the
# scripted MCP client against it, then cleans up. No Kubernetes cluster needed.
#
#   ./scripts/demo-local.sh           # read-only user: the write is DENIED
#   ./scripts/demo-local.sh --admin   # admin user: the write SUCCEEDS
set -euo pipefail
cd "$(dirname "$0")/.."

ADMIN_FLAG=""
[ "${1:-}" = "--admin" ] && ADMIN_FLAG="--admin"

echo "==> building gateway + fake"
go build -o everest-mcp ./cmd/everest-mcp
go build -o everest-fake ./cmd/everest-fake

echo "==> starting fake OpenEverest on :8899 ($ADMIN_FLAG)"
./everest-fake --addr :8899 $ADMIN_FLAG &
FAKE_PID=$!
trap 'kill $FAKE_PID 2>/dev/null || true' EXIT
sleep 1

echo "==> running scripted demo (gateway driven over stdio, like Claude Desktop)"
echo
go run ./cmd/everest-mcp-demo \
  --everest-url http://127.0.0.1:8899 \
  --token demo-token \
  --cluster local \
  --namespace team-alpha \
  --allow-writes --storage s3-backups
