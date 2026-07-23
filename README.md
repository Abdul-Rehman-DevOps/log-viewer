# Log Viewer

**Version:** `0.5.3`  
**Author:** [Abdul Rehman](https://abdulrehman.cz/)  
**GitHub:** [Abdul-Rehman-DevOps](https://github.com/Abdul-Rehman-DevOps)  
**Repository:** [github.com/Abdul-Rehman-DevOps/log-viewer](https://github.com/Abdul-Rehman-DevOps/log-viewer)  
**License:** [MIT](LICENSE)

Kubernetes-native log viewer: live multi-pod streams, Admin-scoped discovery, viewer authentication, and ConfigMap-backed multi-replica config. Logs stay in-cluster — no third-party shipping.

Published image: [`abdulrehman770/log-viewer:latest`](https://hub.docker.com/r/abdulrehman770/log-viewer)

---

## Features

### Viewer (`/`)
- Live logs for **Deployments**, **StatefulSets**, **DaemonSets**, and **Jobs** (kinds Admin-controlled)
- Dynamic discovery via the Kubernetes API (namespaces + workloads)
- Multi-pod stream with source badges, timestamps (PKT), and level indicators
- ANSI preserved; structured coloring for JSON, logfmt (`key=value`), and plain text
- Level highlights: `ERROR` / `WARN` / `INFO` / `LOG` / `DEBUG`
- Auto-follow on workload select; scroll for history; jump-to-latest
- Themes: **Dark** / **Light** (log pane always dark)
- Browser zoom for text size; **Learn more** modal with author links

### Auth & sessions
- Viewer users in Admin (bcrypt hashes)
- Setup gate until at least one viewer user exists
- Admin gated by `LOG_VIEWER_ADMIN_PASSWORD`
- Sliding idle TTL **8h**; epoch shared across replicas (`LOG_VIEWER_SESSION_EPOCH` or secret-derived)
- Clear timeout / invalid-session responses for UI re-login

### Admin (`/admin`)
- Namespace include/exclude (live cluster list)
- Optional app pins (`namespace/Kind/name`); empty = all apps in allowed namespaces
- Workload kind toggles; viewer user password tools
- Editable display title; branding fields locked to author
- **Save & apply now** — PVC + ConfigMap sync across replicas

### Platform
- In-cluster client (ServiceAccount + ClusterRole)
- Deployment → ReplicaSet → Pod ownership resolution
- Optional PVC; ConfigMap sync for multi-replica
- Structured access / auth / stream logs

---

## Surfaces

| Surface | Path | Purpose |
|---------|------|---------|
| Viewer | `/` | Live workload logs |
| Login | `/login` | Viewer sign-in |
| Setup | `/setup` | Until first viewer user exists |
| Admin | `/admin` | Filters, users, display |
| Health | `/api/admin/health` | Liveness / version |

---

## Deploy

```bash
helm upgrade --install log-viewer ./charts/log-viewer \
  --namespace log-viewer \
  --create-namespace \
  --set image.repository=abdulrehman770/log-viewer \
  --set image.tag=latest \
  --set image.pullPolicy=Always \
  --set admin.password='CHANGE_ME_STRONG'
```

```bash
kubectl -n log-viewer port-forward svc/log-viewer 8080:8080
```

| Endpoint | Role |
|----------|------|
| `http://localhost:8080/` | Viewer |
| `http://localhost:8080/admin` | Admin |

Configure namespaces / apps / kinds and at least one enabled viewer user under `/admin`, then authenticate at `/login`. Namespace filters set scope; pinned apps (if any) further restrict the workload list.

---

## Build

```bash
docker build -t YOUR_REGISTRY/log-viewer:0.5.3 .
docker push YOUR_REGISTRY/log-viewer:0.5.3
```

Point the chart at your registry with `image.repository` / `image.tag`, or use the published image in **Deploy** above.

---

## Configuration

### Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `LOG_VIEWER_LISTEN` | `:8080` | Listen address |
| `LOG_VIEWER_DATA` | `/data` | Data directory |
| `LOG_VIEWER_CONFIG` | `$DATA/config.json` | Config path |
| `LOG_VIEWER_ASSETS` | `/etc/log-viewer/branding` | Static assets |
| `LOG_VIEWER_ADMIN_PASSWORD` | _(empty)_ | Admin password (empty = open admin, **dev only**) |
| `LOG_VIEWER_SESSION_SECRET` | admin password fallback | Cookie HMAC key |
| `LOG_VIEWER_SESSION_EPOCH` | derived from session secret | Shared gen; bump to invalidate all sessions |
| `LOG_VIEWER_CONFIGMAP` | _(empty)_ | Multi-replica ConfigMap name |
| `POD_NAMESPACE` | _(empty)_ | ConfigMap namespace (downward API) |
| `KUBECONFIG` | in-cluster | Optional local kubeconfig |

### Helm (summary)

| Key | Notes |
|-----|--------|
| `image.repository` / `image.tag` | Container image |
| `replicaCount` | Default `4` (ConfigMap sync; PVC off by default) |
| `admin.password` / `admin.existingSecret` | Bootstrap Admin credentials |
| `seedConfig` | Initial config when `/data/config.json` is absent |
| `rbac.clusterWide` | ClusterRole for namespaces / pods / logs |
| `persistence.*` | PVC for `/data` |
| `ingress.*` | Optional Ingress |
| `resources` | Requests / limits |

Full list: [`charts/log-viewer/values.yaml`](charts/log-viewer/values.yaml).

### Runtime config

Persisted as `config.json` (and ConfigMap when enabled): `mode`, `include` / `exclude`, `allowedWorkloads`, `workloads`, `users` (hashes only), `title`. Branding fields are server-locked.

---

## Architecture

```
Browser ──► Log Viewer (Go) ──► Kubernetes API
                 │
                 ├─ /           viewer UI (embedded)
                 ├─ /admin      admin UI
                 ├─ /api/*      auth, workloads, log stream
                 ├─ PVC /data   config.json
                 └─ ConfigMap   multi-replica sync (optional)
```

- **Module:** `github.com/Abdul-Rehman-DevOps/log-viewer`
- **Entry:** `cmd/log-viewer`
- **Packages:** `internal/viewer`, `internal/admin`, `internal/auth`, `internal/config`, `internal/k8s`, `internal/syncer`

Discovery is request-time against the API, constrained by Admin filters and ServiceAccount RBAC.

---

## Local development

Requires Go **1.22+** and cluster credentials.

```bash
export LOG_VIEWER_ADMIN_PASSWORD=devadmin
export LOG_VIEWER_DATA=./tmp-data
export LOG_VIEWER_ASSETS=./branding
export KUBECONFIG=~/.kube/config

go run ./cmd/log-viewer -listen :8080
```

---

## Operations

```bash
kubectl -n log-viewer logs -f deploy/log-viewer --prefix
```

Notable lines: startup / idle timeout, `viewer login ok`, `admin login ok`, `session timeout`, `workloads: returned=…`, `logs stream start|end`, HTTP access.

```bash
helm upgrade log-viewer ./charts/log-viewer -n log-viewer --reuse-values \
  --set image.pullPolicy=Always
```

```bash
helm uninstall log-viewer -n log-viewer
```

---

## Security

- Strong `admin.password` (prefer `admin.existingSecret` in GitOps)
- Viewer passwords: bcrypt; sessions: HMAC cookies, **8h** sliding idle
- RBAC can list pods and stream logs — scope ClusterRole carefully
- Branding is not Admin-editable by design

---

## Layout

```
cmd/log-viewer/          binary
internal/viewer/         viewer UI + API
internal/admin/          admin UI + API
internal/auth/           sessions
internal/config/         settings store
internal/k8s/            workloads + streams
internal/syncer/         ConfigMap sync
charts/log-viewer/       Helm chart
branding/                assets
Dockerfile
```

---

## Links

- https://abdulrehman.cz/
- https://github.com/Abdul-Rehman-DevOps/log-viewer
- https://hub.docker.com/r/abdulrehman770/log-viewer

---

## License

MIT © 2026 Abdul Rehman — see [LICENSE](LICENSE).
