# Demo

Two ways to run the gateway against data: a scriptable terminal demo and an
interactive Claude Desktop session. Both exercise the same behavior — read
database state, then attempt a write that RBAC may deny.

## Video

[Watch the demo (Google Drive)](https://drive.google.com/drive/folders/1SjwLMbS1AV8kH8IqbjOwmg4xHCjh7hr2?usp=drive_link).
Claude Desktop reads a live database, then is blocked from a write by RBAC.

## Recorded cast

[`everest-mcp-demo.cast`](everest-mcp-demo.cast) is an asciinema recording of a
live run (reads against a `Ready` MongoDB, then a `403` on a write under a
read-only policy).

```sh
asciinema play docs/everest-mcp-demo.cast    # replay in a terminal
asciinema upload docs/everest-mcp-demo.cast  # shareable link
agg docs/everest-mcp-demo.cast demo.gif      # render a GIF
```

## Terminal demo

`cmd/everest-mcp-demo` is a small MCP client that launches the gateway over
stdio (as Claude Desktop does) and runs a fixed sequence of tool calls.

```sh
go build -o everest-mcp ./cmd/everest-mcp
go run ./cmd/everest-mcp-demo \
  --everest-url "$EVEREST_URL" --token "$EVEREST_TOKEN" \
  --cluster main --namespace <your-namespace> \
  --allow-writes --storage s3-backups --backup-class default
```

### Output against a real cluster

Captured against an OpenEverest 2.x stack (server, controller, everest-operator,
and the Percona Server for MongoDB provider) with a `Ready` MongoDB instance and
a read-only RBAC policy applied to the acting user:

```text
$ call whoami null
  OK { "user": "admin", "namespaces": ["default","everest"] }

$ call list_clusters null
  OK { "items": [ { "name": "main", "server": "https://kubernetes.default.svc" } ] }

$ call list_instances {"namespace":"everest"}
  OK { "cluster": "main", "count": 1,
       "instances": [ { "name": "orders-prod", "provider": "percona-server-mongodb", "version": "8.0.12", "phase": "Ready" } ] }

$ call get_instance_health {"instance":"orders-prod","namespace":"everest"}
  OK { "name": "orders-prod", "phase": "Ready", "healthy": true, "version": "8.0.12",
       "conditions": [ { "type": "ConnectionDetailsReady", "status": "True", "reason": "Available" } ] }

$ call create_backup {"instanceName":"orders-prod","namespace":"everest","storageName":"s3-backups","backupClassName":"default"}
  DENIED: everest API POST /v1/clusters/main/namespaces/everest/backups:
          status 403: insufficient permissions for performing the operation
```

Reads succeed; the write returns `403` because the user's RBAC policy does not
grant `create` on backups. With an admin token (or full RBAC) the same
`create_backup` call succeeds. To reproduce the read-only policy, set the
`everest-rbac` ConfigMap `enabled: "true"` with a role that grants only `read`
(see [FULL_SETUP.md](FULL_SETUP.md) §5).

### Output against the bundled fake (no cluster)

`./scripts/demo-local.sh` runs the same sequence against `cmd/everest-fake`,
which denies writes to a read-only identity (`--admin` makes them succeed):

```text
$ call list_instances {"namespace":"team-alpha"}
  OK { "count": 1, "instances": [ { "name": "orders-prod", "provider": "postgresql", "version": "16", "phase": "Ready" } ] }

$ call create_backup {"instanceName":"orders-prod","namespace":"team-alpha","storageName":"s3-backups"}
  DENIED: status 403: user alice@example.com is not allowed to create backups in team-alpha
```

## Claude Desktop

1. Build the binary and note its absolute path:

   ```sh
   go build -o everest-mcp ./cmd/everest-mcp && pwd
   ```

2. Add to `claude_desktop_config.json` (add `--allow-writes` if you want the
   `create_backup` tool exposed):

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

3. Restart Claude Desktop and confirm the `openeverest` tools appear, then ask it
   to list your database instances and report the health of one.

Note: `EVEREST_TOKEN` from `/v1/session` is short-lived; use `--username` and
`--password` args if you want the gateway to log in itself.
