# Log Viewer

**Version:** `0.5.3`  
**Author:** [Abdul Rehman](https://abdulrehman.cz/)  
**GitHub:** [Abdul-Rehman-DevOps](https://github.com/Abdul-Rehman-DevOps)  
**Repository:** [github.com/Abdul-Rehman-DevOps/log-viewer](https://github.com/Abdul-Rehman-DevOps/log-viewer)  
**License:** [MIT](LICENSE)

Open-source **Kubernetes log viewer** for any cluster. Live multi-pod streaming, Admin-controlled access, viewer logins, and branding — without shipping your logs to a third-party SaaS.

---

## Features

### Viewer (`/`)
- Live logs for **Deployments**, **StatefulSets**, **DaemonSets**, and **Jobs** (kinds are Admin-controlled)
- Dynamic discovery from the live cluster (namespaces + workloads via the Kubernetes API)
- Multi-pod stream with colored source badges, timestamps, and level indicators
- ANSI colors preserved (NestJS / terminal colors render in the log pane)
- Structured log coloring: JSON, logfmt (`key=value`), and plain text
- Level highlighting for `ERROR` / `WARN` / `INFO` / `LOG` / `DEBUG`
- Timestamps shown in **PKT**
- Always live on workload select (no Start/Stop); scroll up for history; jump-to-latest control
- Independent sidebar scroller for long workload lists
- Themes: **Dark** and **Light** (log pane stays dark for readability)
- Use browser zoom (`Ctrl` `+` / `Ctrl` `-`) to adjust text size
- **Learn more** modal with author links (portfolio / GitHub / project)

### Auth & sessions
- Viewer users managed in Admin (bcrypt-hashed passwords)
- First-run **setup gate**: create at least one viewer user before logs open
- Admin panel protected by `LOG_VIEWER_ADMIN_PASSWORD`
- **Idle timeout: 10 minutes** of no activity (sliding session — activity refreshes it)
- Process restart invalidates existing sessions
- On timeout / restart, viewer and admin show a clear re-login message

### Admin (`/admin`)
- Namespace **include** or **exclude** mode (lists namespaces live from the cluster)
- Optional app pins (`namespace/Kind/name`); empty pins = all apps in allowed namespaces
- Workload kinds toggles + Select all / Unselect all
- Viewer users: generate strong password, show/hide, copy
- Display title editable
- **About / branding is read-only** (locked to author details)
- **Save & apply now** — config persists and syncs across replicas via ConfigMap
- Banner when pinned apps are limiting the live viewer

### Platform
- In-cluster Kubernetes client (ServiceAccount + ClusterRole)
- Deployment → ReplicaSet → Pod ownership (avoids mixing CronJob/worker pods)
- PVC for config + optional ConfigMap sync for multi-replica
- Structured pod logs: login, timeouts, workloads, log streams, HTTP access

---

## Surfaces

| Surface | Path | Purpose |
|---------|------|---------|
| Viewer | `/` | Live workload logs |
| Login | `/login` | Viewer sign-in |
| Setup | `/setup` | Shown until the first viewer user exists |
| Admin | `/admin` | Namespaces, apps, kinds, users, display |
| Health | `/api/admin/health` | Simple health / version JSON |

---

## Quick start (Helm)

```bash
helm upgrade --install log-viewer ./charts/log-viewer \
  --namespace log-viewer \
  --create-namespace \
  --set image.repository=YOUR_REGISTRY/log-viewer \
  --set image.tag=0.5.3 \
  --set image.pullPolicy=Always \
  --set admin.password='CHANGE_ME_STRONG'
```

Port-forward (if Ingress is off):

```bash
kubectl -n log-viewer port-forward svc/log-viewer 8080:8080
```

Then open:
- Viewer: http://localhost:8080/
- Admin: http://localhost:8080/admin

**First login flow**
1. Open `/admin` with the bootstrap admin password
2. Create at least one **enabled** viewer user (generate + copy password)
3. Choose namespaces / apps / kinds → **Save & apply now**
4. Sign in at `/login` and stream logs

**Admin filter rules**
- **Namespaces** control which namespaces are in scope
- **Pinned apps** (Apps tab), if any, further limit the viewer to only those apps
- Clear all pins + Save to show every app in the selected namespaces

---

## Build & push image

```bash
docker build -t YOUR_REGISTRY/log-viewer:0.5.3 .
docker push YOUR_REGISTRY/log-viewer:0.5.3
```

Example (ECR):

```bash
docker build -t 390866253661121.dkr.ecr.us-east-2.amazonaws.com/log-viewer/logger:latest .
docker push 390866253661121.dkr.ecr.us-east-2.amazonaws.com/log-viewer/logger:latest
```

---

## Configuration

### Environment variables

| Variable | Default | Description |
|----------|---------|-------------|
| `LOG_VIEWER_LISTEN` | `:8080` | HTTP listen address |
| `LOG_VIEWER_DATA` | `/data` | Persistent data directory |
| `LOG_VIEWER_CONFIG` | `$DATA/config.json` | Settings file path |
| `LOG_VIEWER_ASSETS` | `/etc/log-viewer/branding` | Static assets (logo, etc.) |
| `LOG_VIEWER_ADMIN_PASSWORD` | _(empty)_ | Admin panel password (empty = open admin, **dev only**) |
| `LOG_VIEWER_SESSION_SECRET` | falls back to admin password | Signs session cookies |
| `LOG_VIEWER_CONFIGMAP` | _(empty)_ | ConfigMap name for multi-replica sync |
| `POD_NAMESPACE` | _(empty)_ | Namespace of the ConfigMap (set by Helm downward API) |
| `KUBECONFIG` | in-cluster | Optional local kubeconfig |

### Helm values (high level)

| Key | Notes |
|-----|--------|
| `image.repository` / `image.tag` | Container image |
| `replicaCount` | Default `4` (uses ConfigMap sync; PVC off by default) |
| `admin.password` | Bootstrap Admin password |
| `admin.existingSecret` | Optional existing Secret instead of chart Secret |
| `seedConfig` | Initial config when PVC has no `config.json` yet |
| `rbac.clusterWide` | ClusterRole for listing namespaces + reading pods/logs |
| `persistence.*` | PVC for `/data` |
| `ingress.*` | Optional Ingress |
| `resources` | CPU/memory requests & limits |

See [`charts/log-viewer/values.yaml`](charts/log-viewer/values.yaml) for the full list.

### Runtime config (Admin)

Stored on the PVC as `config.json` and synced to ConfigMap when enabled:

- `mode`: `include` \| `exclude`
- `include` / `exclude`: namespace lists
- `allowedWorkloads`: pinned apps (`dev/Deployment/api`); empty = all apps in allowed namespaces
- `workloads`: which kinds to show
- `users`: viewer accounts (hashes only persisted)
- `title`: viewer header title
- Branding fields exist in config but **cannot be changed from Admin** (server-locked)

---

## Architecture

```
Browser ──► Log Viewer (Go) ──► Kubernetes API
                 │
                 ├─ /           viewer UI (embedded HTML/JS)
                 ├─ /admin      admin UI
                 ├─ /api/*      auth + workloads + log stream
                 ├─ PVC /data   config.json
                 └─ ConfigMap   multi-replica sync (optional)
```

- **Module:** `github.com/Abdul-Rehman-DevOps/log-viewer`
- **Entry:** `cmd/log-viewer`
- **Packages:** `internal/viewer`, `internal/admin`, `internal/auth`, `internal/config`, `internal/k8s`, `internal/syncer`

Namespaces and workloads are discovered **dynamically** from the cluster API on each request (subject to Admin filters and ServiceAccount RBAC).

---

## Local development

Needs Go **1.22+** and cluster access (`KUBECONFIG` or in-cluster).

```bash
export LOG_VIEWER_ADMIN_PASSWORD=devadmin
export LOG_VIEWER_DATA=./tmp-data
export LOG_VIEWER_ASSETS=./branding
export KUBECONFIG=~/.kube/config

go run ./cmd/log-viewer -listen :8080
```

Open http://localhost:8080/admin then create a viewer user.

---

## Operations

### Pod logs

```bash
kubectl -n log-viewer logs -f deploy/log-viewer --prefix
```

Useful log lines (prefix `log-viewer `):

- startup / listen / idle timeout
- `viewer login ok` / `admin login ok`
- `session timeout`
- `workloads: returned=…`
- `logs stream start|end`
- `METHOD /path status duration`

### Upgrade

```bash
helm upgrade log-viewer ./charts/log-viewer \
  --namespace log-viewer \
  --set image.repository=YOUR_REGISTRY/log-viewer \
  --set image.tag=0.5.3 \
  --set image.pullPolicy=Always \
  --reuse-values
```

### Uninstall

```bash
helm uninstall log-viewer -n log-viewer
# optional: delete PVC / namespace if you want a clean wipe
```

---

## Security notes

- Set a strong `admin.password` in production
- Prefer `admin.existingSecret` over plaintext values in Git
- Viewer passwords are stored as **bcrypt** hashes
- Sessions are HMAC-signed cookies; idle expiry is **10 minutes**
- Cluster RBAC can list pods and stream logs in selected namespaces — scope cluster access carefully
- Branding (author links) is intentionally not editable from Admin

---

## Project layout

```
cmd/log-viewer/          main binary
internal/viewer/         viewer UI + APIs
internal/admin/          admin UI + APIs
internal/auth/           sessions / cookies
internal/config/         settings store
internal/k8s/            workloads + log streaming
internal/syncer/         ConfigMap sync
charts/log-viewer/       Helm chart
branding/                logo + assets
Dockerfile
```

---

## Links

- Portfolio: https://abdulrehman.cz/
- GitHub profile: https://github.com/Abdul-Rehman-DevOps
- This project: https://github.com/Abdul-Rehman-DevOps/log-viewer

---

## License

MIT © 2026 Abdul Rehman — see [LICENSE](LICENSE).
