# Running everest-mcp against a real OpenEverest

A manual runbook for standing up OpenEverest locally, provisioning a database,
and driving it with the gateway — including the issues encountered on a real
macOS run and how they were resolved.

There are two ways to stand up OpenEverest:

- **Option 1 — Tilt full stack**: builds and runs the API server, controller, and
  the database operators/providers, so databases provision to `Ready`. Heavier
  first-time setup (clone 3 repos).
- **Option 2 — `make deploy-all`**: faster; brings up the API server and
  controller (enough to drive the gateway against real resources), but does not
  install the DB operators, so instances stay unprovisioned.

> To try the gateway without a cluster, run `./scripts/demo-local.sh` against the
> bundled fake (see [DEMO.md](DEMO.md)).

---

## 0. One-time machine prerequisites

```sh
# Go module verification — fixes "invalid GOSUMDB: malformed verifier id".
# (The correct value is sum.golang.org, NOT sum.golang.google.org.)
go env -w GOSUMDB=sum.golang.org
# Also remove any wrong "export GOSUMDB=sum.golang.google.org" from ~/.zshrc.

# Tools (macOS / Homebrew). Docker Desktop must be running.
brew install k3d kind helm kubectl pnpm
brew install tilt-dev/tap/tilt mkcert    # tilt + mkcert only needed for Option 1
```

**Free port 5000** (k3d's dev registry). macOS **AirPlay Receiver** holds it:
System Settings → General → AirDrop & Handoff → **AirPlay Receiver = Off**
(or change `hostPort: "5000"` in `openeverest/dev/k3d_config*.yaml`).

---

## Option 1 — Full stack with Tilt (databases reach Ready)

This is the path OpenEverest devs use; it brings up the operators that actually
provision databases.

### 1.1 Clone the companion repos

```sh
cd ~/src   # or wherever you keep repos
git clone https://github.com/percona/everest-operator
git clone -b v2 https://github.com/openeverest/helm-charts
git clone https://github.com/openeverest/provider-percona-server-mongodb
```

### 1.2 Point OpenEverest at them

```sh
cd ~/openeverest
cp dev/.env.example dev/.env        # then edit dev/.env:
#   EVEREST_OPERATOR_DIR=/abs/path/to/everest-operator
#   EVEREST_CHART_DIR=/abs/path/to/helm-charts/charts/everest
#   PSMDB_PROVIDER_CHART_DIR=/abs/path/to/provider-percona-server-mongodb/charts/provider-percona-server-mongodb
cp dev/config.yaml.example dev/config.yaml   # set DB namespaces to create (e.g. "everest")
```

### 1.3 Bring it up

```sh
make dev-up        # creates k3d "everest-dev" cluster + runs Tilt; builds & loads ALL images
# Watch the Tilt UI (it prints a localhost URL) until every resource is green.
```

The API/UI is at **http://localhost:8080**. Default admin password is whatever
the chart sets (often `admin`; check the Tilt logs or the `everest-accounts`
secret). Then jump to **§3 Create a database**.

Teardown: `make dev-down` (keep cluster) / `make dev-destroy` (delete cluster).

---

## Option 2 — CI-style deploy (API + controller only)

What was actually run end-to-end here. Gets the gateway working against real CR
data quickly; instances won't provision without the operators (see §2.4).

### 2.1 Create the cluster

```sh
cd ~/openeverest
make k3d-cluster-up                 # k3d cluster "everest-server-test"
# IMPORTANT: make sure this is your CURRENT context before deploying, or the
# install lands in whatever context was active:
kubectl config use-context k3d-everest-server-test
```

### 2.2 Build + install

```sh
export GOSUMDB=sum.golang.org
make deploy-all DB_NAMESPACES=everest    # builds UI + server image, installs Everest, creates ns "everest"
```

This builds `ghcr.io/openeverest/openeverest-dev:0.0.0` (server **and**
controller run from this one image) and sets admin password to **`admin`**.

### 2.3 Two gotchas this path hits

**(a) The fresh image must be in the cluster that runs Everest.** `deploy-all`
imports the image into the **k3d** cluster. If Everest installed into a different
cluster (e.g. a `kind` one because that was the current context), load it there:

```sh
kind load docker-image ghcr.io/openeverest/openeverest-dev:0.0.0 --name <kind-cluster>
kubectl rollout restart deploy/everest-server deploy/everest-controller -n everest-system
```

Symptom of a stale image: `/v1/clusters` returns `{"message":"no matching
operation was found"}` (an old API). After loading the fresh image it returns
`{"items":[{"name":"main",...}]}`.

**(b) `expose` may fail** with `nodePort 30080 already allocated`. Harmless —
skip the NodePort and just port-forward:

```sh
kubectl port-forward -n everest-system svc/everest 8080:8080
```

### 2.4 To make databases provision under Option 2 (operators)

`deploy-all` does **not** build/install the DB operators. To get there you must
build and load the operator + catalog images and install them, e.g.:

```sh
make build-controller-debug docker-build-controller   # if present; see `make help`
# build/load the operator & catalog images (openeverest-operator-dev / -catalog-dev),
# kind/k3d-load them, then install the catalog + operator subscriptions.
```

This is exactly the wiring Tilt automates — if you need provisioning, prefer
**Option 1**.

---

## 3. Verify the API and create a database

```sh
# Token (admin/admin):
export EVEREST_TOKEN=$(curl -s -X POST http://localhost:8080/v1/session \
  -H 'Content-Type: application/json' -d '{"username":"admin","password":"admin"}' \
  | sed 's/.*"token":"//;s/".*//')

# Discover the cluster name (usually "main"):
curl -s http://localhost:8080/v1/clusters -H "Authorization: Bearer $EVEREST_TOKEN"
```

Create a database the proper way via the **UI** at http://localhost:8080
(login, choose engine + namespace `everest`, submit) and wait for `Ready`. Or
apply a minimal CR for a list-only demo (won't reach Ready without operators):

```sh
kubectl apply -f - <<'YAML'
apiVersion: core.openeverest.io/v1alpha1
kind: Instance
metadata: { name: orders-prod, namespace: everest }
spec: { provider: postgresql, version: "16" }
YAML
```

---

## 4. Build and run the gateway against the live API

```sh
cd ~/everest-mcp
go build -o everest-mcp ./cmd/everest-mcp

# Scripted terminal demo (uses a token):
go run ./cmd/everest-mcp-demo \
  --everest-url http://localhost:8080 --token "$EVEREST_TOKEN" \
  --cluster main --namespace everest

# Or run the server for Claude Desktop (stdio). claude_desktop_config.json:
#   "openeverest": {
#     "command": "/abs/path/everest-mcp",
#     "args": ["serve","--everest-url","http://localhost:8080","--cluster","main"],
#     "env": { "EVEREST_TOKEN": "<token>" } }
```

In-cluster (Streamable HTTP) mode behind the plugin proxy:

```sh
./everest-mcp --http :8080 --everest-url http://everest.everest-system:8080 --cluster main
```

---

## 5. Governance demo — the RBAC denial (read-only user)

The headline ("an agent can't exceed the connecting user's RBAC") needs a
**non-admin** identity. The mechanism is already proven by
`go test ./...` (`TestRBACDenialSurfacesAsToolError`) and `./scripts/demo-local.sh`.
To show it **live**:

1. Enable RBAC and add a read-only policy. OpenEverest stores Casbin policy in
   the `everest-rbac-settings` ConfigMap in `everest-system`. Add roles/grants
   that allow `read` on `database-clusters`/`backups` but **not** `create`.
2. Give the demo user a token. With the built-in account model the simplest
   route is an **OIDC** provider (configure it in Everest settings) issuing a
   token for a user bound to the read-only role; alternatively create a second
   account if your build supports `everestctl accounts create`.
3. Run the gateway with that user's token and `--allow-writes`, then ask it to
   `create_backup`. OpenEverest returns `403`; the gateway surfaces it as a tool
   error — enforced by the platform, not the plugin.

Until OIDC/extra accounts are configured, demo this step with
`./scripts/demo-local.sh` (the bundled fake denies the read-only user's write),
which produces the identical agent-visible behavior.

---

## 6. Troubleshooting quick reference

| Symptom | Cause / fix |
|---|---|
| `invalid GOSUMDB: malformed verifier id` | `go env -w GOSUMDB=sum.golang.org`; fix `~/.zshrc` export. |
| k3d create fails binding `:5000` | macOS AirPlay Receiver; disable it or change the registry `hostPort`. |
| Install landed in the wrong cluster | `kubectl config use-context <target>` **before** `make deploy-all`. |
| `/v1/clusters` → "no matching operation" | Stale server image; `kind/k3d load` the fresh `openeverest-dev:0.0.0` and restart `everest-server`. |
| `expose` → nodePort already allocated | Skip it; `kubectl port-forward -n everest-system svc/everest 8080:8080`. |
| `everest-controller` CrashLoopBackOff | Its image isn't in the cluster; load it and restart the deployment. |
| Instances never reach `Ready` | DB operators not installed (Option 2). Use Option 1 (Tilt). |

---

## 7. Teardown

```sh
# Option 1
cd ~/openeverest && make dev-destroy
# Option 2
cd ~/openeverest && make undeploy && make k3d-cluster-down
# Stop a port-forward
pkill -f "port-forward.*everest"
```
