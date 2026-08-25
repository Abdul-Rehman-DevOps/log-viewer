# Log Viewer

**Version:** `0.6.0`  
**Author:** [Abdul Rehman](https://abdulrehman.cz/)  
**GitHub:** [Abdul-Rehman-DevOps](https://github.com/Abdul-Rehman-DevOps)  
**Repository:** [github.com/Abdul-Rehman-DevOps/log-viewer](https://github.com/Abdul-Rehman-DevOps/log-viewer)  
**License:** [MIT](LICENSE)

Kubernetes-native log viewer: live multi-pod streams, Admin-scoped discovery, viewer authentication, ConfigMap-backed multi-replica config, and optional **S3 history** for the last **3 days**.

Published image: [`abdulrehman770/log-viewer:0.6.0`](https://hub.docker.com/r/abdulrehman770/log-viewer)

---

## Features

### Viewer (`/`)
- Live logs for **Deployments**, **StatefulSets**, **DaemonSets**, and **Jobs** (kinds Admin-controlled)
- **History** mode: pick any From/To window within the last 3 days (S3 archive)
- Dynamic discovery via the Kubernetes API (namespaces + workloads)
- Multi-pod stream with source badges, timestamps (PKT), and level indicators
- ANSI preserved; structured coloring for JSON, logfmt (`key=value`), and plain text
- Level highlights: `ERROR` / `WARN` / `INFO` / `LOG` / `DEBUG`
- Auto-follow on workload select; scroll for history; jump-to-latest
- Themes: **Dark** / **Light** (log pane always dark)
- Browser zoom for text size; **Learn more** modal with author links
- **Problems** tab: CrashLoopBackOff, Failed, ImagePullBackOff, and related error pods
- Scoped to Admin-allowed workloads / namespaces only
- Header **alert** icon highlights when any allowed workload has failing pods
- Streams **previous container** logs automatically for crash-loop cases

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
- Optional **S3 archival** (collector + retention cleanup)
- Structured access / auth / stream logs

---

## Surfaces

| Surface | Path | Purpose |
|---------|------|---------|
| Viewer | `/` | Live + History workload logs + Problems |
| Login | `/login` | Viewer sign-in |
| Setup | `/setup` | Until first viewer user exists |
| Admin | `/admin` | Filters, users, display |
| Health | `/api/admin/health` | Liveness / version |
| Unhealthy pods | `/api/unhealthy-pods` | CrashLoop / Failed pods (auth + Admin scope) |
| Log history | `/api/logs/history` | S3-backed range query (auth) |
| Archive status | `/api/archive/status` | Whether S3 history is enabled |

---

## Deploy (with S3 history)

### 1. Create an S3 bucket

```bash
aws s3 mb s3://YOUR_LOG_VIEWER_BUCKET --region us-east-1
```

Recommended lifecycle rule (matches app retention):

```json
{
  "Rules": [{
    "ID": "log-viewer-3d",
    "Status": "Enabled",
    "Filter": { "Prefix": "log-viewer/" },
    "Expiration": { "Days": 3 }
  }]
}
```

IAM needs at least: `s3:PutObject`, `s3:GetObject`, `s3:ListBucket`, `s3:DeleteObject` on that bucket/prefix. Prefer **IRSA** (EKS) over static keys.

### 2. Helm install / upgrade

From the `log-viewer` repo root (where `charts/` lives):

```bash
helm upgrade --install log-viewer ./charts/log-viewer \
  --namespace log-viewer \
  --create-namespace \
  --set image.repository=abdulrehman770/log-viewer \
  --set image.tag=0.6.0 \
  --set image.pullPolicy=Always \
  --set admin.password='CHANGE_ME_STRONG' \
  --set s3.enabled=true \
  --set s3.bucket=YOUR_LOG_VIEWER_BUCKET \
  --set s3.region=us-east-1 \
  --set s3.prefix=log-viewer \
  --set s3.retentionDays=3 \
  --set s3.intervalSec=120
```

**IRSA (recommended on EKS)** — annotate the ServiceAccount and omit static keys:

```bash
helm upgrade --install log-viewer ./charts/log-viewer \
  --namespace log-viewer \
  --create-namespace \
  --set image.tag=0.6.0 \
  --set image.pullPolicy=Always \
  --set admin.password='CHANGE_ME_STRONG' \
  --set s3.enabled=true \
  --set s3.bucket=YOUR_LOG_VIEWER_BUCKET \
  --set s3.region=us-east-1 \
  --set serviceAccount.annotations."eks\.amazonaws\.com/role-arn"=arn:aws:iam::ACCOUNT:role/log-viewer-s3
```

**Static AWS keys** (dev only):

```bash
helm upgrade --install log-viewer ./charts/log-viewer \
  --namespace log-viewer \
  --create-namespace \
  --set image.tag=0.6.0 \
  --set admin.password='CHANGE_ME_STRONG' \
  --set s3.enabled=true \
  --set s3.bucket=YOUR_LOG_VIEWER_BUCKET \
  --set s3.region=us-east-1 \
  --set s3.accessKeyId=AKIA... \
  --set s3.secretAccessKey='...'
```

Or use an existing secret with keys `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`:

```bash
--set s3.existingSecret=my-aws-secret
```

### 3. Open the UI

```bash
kubectl -n log-viewer port-forward svc/log-viewer 8080:8080
```

| Endpoint | Role |
|----------|------|
| `http://localhost:8080/` | Viewer (Live / History) |
| `http://localhost:8080/admin` | Admin |

Configure namespaces / apps / kinds and at least one enabled viewer user under `/admin`, then authenticate at `/login`.

In the viewer: select a workload → **History** → set **From** / **To** (within last 3 days) → **Load**.

> **Note:** History is filled **going forward** after S3 is enabled (plus a short kubelet backfill on first collect). Objects stay in S3 for the retention window even after **Pods** or **Deployments** are deleted — use **History** mode to browse archived workloads by date/time.

### Deploy without S3 (live only)

```bash
helm upgrade --install log-viewer ./charts/log-viewer \
  --namespace log-viewer \
  --create-namespace \
  --set image.repository=abdulrehman770/log-viewer \
  --set image.tag=0.6.0 \
  --set image.pullPolicy=Always \
  --set admin.password='CHANGE_ME_STRONG'
```

---

## Build & push

```bash
docker build -t YOUR_REGISTRY/log-viewer:0.6.0 .
docker push YOUR_REGISTRY/log-viewer:0.6.0
```

Point the chart at your registry with `image.repository` / `image.tag`, or use the published image above.

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
| `LOG_VIEWER_S3_ENABLED` | `false` | Enable S3 archival + History UI |
| `LOG_VIEWER_S3_BUCKET` | _(required if enabled)_ | Target bucket |
| `LOG_VIEWER_S3_REGION` | `us-east-1` | AWS region |
| `LOG_VIEWER_S3_PREFIX` | `log-viewer` | Key prefix |
| `LOG_VIEWER_S3_RETENTION_DAYS` | `3` | App-side cleanup window |
| `LOG_VIEWER_S3_INTERVAL_SEC` | `120` | Collector interval |
| `LOG_VIEWER_S3_BACKFILL_SEC` | `3600` | First-collect kubelet window |
| `LOG_VIEWER_S3_ENDPOINT` | _(empty)_ | Custom endpoint (MinIO, etc.) |
| `LOG_VIEWER_S3_FORCE_PATH_STYLE` | `false` | Path-style S3 (MinIO) |
| `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` | _(optional)_ | Static credentials; prefer IRSA |

### Helm (summary)

| Key | Notes |
|-----|--------|
| `image.repository` / `image.tag` | Container image (`0.6.0`) |
| `replicaCount` | Default `2` (ConfigMap sync; PVC off by default) |
| `admin.password` / `admin.existingSecret` | Bootstrap Admin credentials |
| `s3.enabled` / `s3.bucket` / `s3.region` | S3 history |
| `s3.retentionDays` | Default `3` |
| `s3.accessKeyId` / `secretAccessKey` / `existingSecret` | Credentials (or IRSA) |
| `serviceAccount.annotations` | IRSA role ARN |
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
Browser ──► Log Viewer (Go) ──► Kubernetes API (live logs)
                 │
                 ├─ /           viewer UI (Live + History)
                 ├─ /admin      admin UI
                 ├─ /api/*      auth, workloads, log stream / history
                 ├─ PVC /data   config.json
                 ├─ ConfigMap   multi-replica sync (optional)
                 └─ S3          last 3 days archived chunks (optional)
```

- **Module:** `github.com/Abdul-Rehman-DevOps/log-viewer`
- **Entry:** `cmd/log-viewer`
- **Packages:** `internal/viewer`, `internal/admin`, `internal/auth`, `internal/config`, `internal/k8s`, `internal/syncer`, `internal/archive`

Discovery is request-time against the API, constrained by Admin filters and ServiceAccount RBAC. The archive collector snapshots allowed workloads on an interval and stores gzipped JSONL under `{prefix}/v1/{ns}/{kind}/{workload}/{pod}/{date}/…`.

---

## Local development

Requires Go **1.22+** and cluster credentials.

```bash
export LOG_VIEWER_ADMIN_PASSWORD=devadmin
export LOG_VIEWER_DATA=./tmp-data
export LOG_VIEWER_ASSETS=./branding
export KUBECONFIG=~/.kube/config

# Optional S3 history (MinIO or AWS)
export LOG_VIEWER_S3_ENABLED=true
export LOG_VIEWER_S3_BUCKET=log-viewer-dev
export LOG_VIEWER_S3_REGION=us-east-1
export AWS_ACCESS_KEY_ID=...
export AWS_SECRET_ACCESS_KEY=...

go run ./cmd/log-viewer -listen :8080
```

---

## Operations

```bash
kubectl -n log-viewer logs -f deploy/log-viewer --prefix
```

Notable lines: startup / idle timeout, `s3 archive enabled|disabled`, `s3 archive tick`, `viewer login ok`, `admin login ok`, `session timeout`, `workloads: returned=…`, `logs stream start|end`, `logs history`, HTTP access.

```bash
helm upgrade log-viewer ./charts/log-viewer -n log-viewer --reuse-values \
  --set image.tag=0.6.0 \
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
- Prefer IRSA / workload identity for S3; avoid embedding long-lived keys in values
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
internal/archive/        S3 collector + history query
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
