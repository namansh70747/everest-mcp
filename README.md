# everest-mcp — OpenEverest MCP Gateway

[![CI](https://github.com/namansh70747/everest-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/namansh70747/everest-mcp/actions/workflows/ci.yml)

A [Model Context Protocol](https://modelcontextprotocol.io) server that lets MCP
clients (Claude Desktop, Cursor, Claude Code) operate
[OpenEverest](https://github.com/openeverest/openeverest)-managed databases
(PostgreSQL, MongoDB/PSMDB, MySQL/PXC).

Every tool call is made against the OpenEverest v1 API with the connecting
user's bearer token, so OpenEverest re-checks it against that user's RBAC policy.
The gateway does not implement its own authorization — a call the user is not
permitted to make is rejected by the host. Mutating tools and
credential-returning tools are disabled by default.

It is packaged as an OpenEverest [generic plugin](https://github.com/openeverest/openeverest/blob/main/docs/process/generic-plugins-design.md)
and built on primitives the host already provides: the `/v1/plugins/{name}/*`
proxy, the `/v1/events` SSE stream, and the Casbin RBAC engine.

## Tools

Read-only unless noted:

| Tool | Description |
|------|-------------|
| `whoami` | Connecting user identity and accessible namespaces (`/v1/plugins/context`). |
| `list_clusters` | Kubernetes clusters managed by OpenEverest. |
| `list_namespaces` | Namespaces managed in a cluster. |
| `list_instances` | Database instances in a namespace (provider, version, phase). |
| `get_instance` | Full details of one instance. |
| `get_instance_health` | Phase, readiness, and status conditions. |
| `list_backups` | Backups of an instance (state, size, completion time). |
| `get_connection` | Connection details including credentials. Requires `--allow-connection-details`. |
| `create_backup` | Trigger a backup. Requires `--allow-writes`; still RBAC-checked by the host. |

Resources (read on demand, with update notifications driven by `/v1/events`):

- `everest://clusters`
- `everest://instances/{cluster}/{namespace}`

Prompt: `diagnose-instance` — a guided health check for an instance.

## Architecture

```text
MCP client (Claude Desktop / Cursor / Claude Code)
        │  MCP over stdio or Streamable HTTP
        ▼
   everest-mcp ── tool call ──► OpenEverest /v1 API   (RBAC checked per call)
        │
        └── GET /v1/events (SSE) ──► resource update notifications
```

- Desktop: run over stdio and point an MCP client at the binary.
- In-cluster: run as the plugin backend `Service`; the OpenEverest API proxies
  `/v1/plugins/everest-mcp/*` to it and forwards the per-user identity.

## Quick start (Claude Desktop)

```sh
go build -o everest-mcp ./cmd/everest-mcp
```

Get a token (`everestctl token reset`) and add to `claude_desktop_config.json`:

```json
{
  "mcpServers": {
    "openeverest": {
      "command": "/absolute/path/to/everest-mcp",
      "args": ["serve", "--everest-url", "http://localhost:8080", "--cluster", "main"],
      "env": { "EVEREST_TOKEN": "your-token" }
    }
  }
}
```

Restart Claude Desktop, then ask it to list your database instances. The gateway
can also log in with `--username`/`--password` instead of a token.

### Flags

| Flag | Env | Default | Purpose |
|------|-----|---------|---------|
| `--everest-url` | `EVEREST_URL` | `http://localhost:8080` | OpenEverest API base URL (no `/v1`). |
| `--token` | `EVEREST_TOKEN` | — | Bearer token. |
| `--username` / `--password` | `EVEREST_USERNAME` / `EVEREST_PASSWORD` | — | Password login. |
| `--cluster` | `EVEREST_CLUSTER` | — | Default cluster for calls that omit it. |
| `--allow-writes` | — | `false` | Enable `create_backup`. |
| `--allow-connection-details` | — | `false` | Enable `get_connection`. |
| `--watch-events` | — | `true` | Subscribe to `/v1/events` for resource updates (stdio mode). |
| `--http` | `EVEREST_MCP_HTTP` | — | Serve over Streamable HTTP (e.g. `:8080`) instead of stdio. |

### In-cluster (Streamable HTTP)

With `--http :8080` the gateway serves Streamable HTTP behind the plugin proxy.
Each session uses the bearer token on its own request — the identity the proxy
forwards — so RBAC is enforced per user. A `/healthz` endpoint is exposed for
readiness probes.

```sh
./everest-mcp --http :8080 --everest-url http://everest.everest-system:8080 --cluster main
```

## Install as a plugin

```sh
everestctl plugin install oci://ghcr.io/namansh70747/everest-mcp:0.1.0
everestctl plugin enable everest-mcp -n <namespace>
```

See [deploy/manifest.yaml](deploy/manifest.yaml) for the `Plugin` and
`PluginInstallation` resources.

## Try it without a cluster

A small fake OpenEverest (`cmd/everest-fake`) serves static responses so you can
run the gateway end to end without Kubernetes. It is for development and tests
only and enforces no real security.

```sh
./scripts/demo-local.sh           # read-only identity: create_backup is denied
./scripts/demo-local.sh --admin   # admin identity: create_backup succeeds
```

See [docs/DEMO.md](docs/DEMO.md) for a recorded run and Claude Desktop steps, and
[docs/FULL_SETUP.md](docs/FULL_SETUP.md) for running against a real cluster.

## Development

```sh
make test            # unit + in-memory MCP tests
make e2e             # also runs the end-to-end stdio test (-tags e2e)
make vet
make build
```

## License

Apache 2.0 — see [LICENSE](LICENSE).
