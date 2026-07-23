# Log Viewer
### by Abdul Rehman

Open-source **Kubernetes log viewer** for any cluster — with an Admin panel that controls everything in realtime.

## What you get

| Surface | URL | Purpose |
|---------|-----|---------|
| Viewer | `/` | Deployments & StatefulSets (configurable) + live logs |
| Admin | `/admin` | Namespaces, workloads, users, title — apply instantly |
| Login | `/login` | End-user login when auth is enabled in Admin |

- **No nodes** in the UI — only workloads you enable in Admin (Deployments / StatefulSets by default)
- **Users managed in Admin** — enable auth and add viewers; no separate hardcoded simple-user flow
- **Realtime apply** — Save in Admin updates visibility immediately
- Branding: **Log Viewer** (custom logo), not third-party UI chrome

## Deploy

```bash
helm upgrade --install log-viewer ./charts/log-viewer \
  --namespace log-viewer --create-namespace \
  --set image.repository=YOUR_REGISTRY/log-viewer \
  --set image.tag=latest \
  --set admin.password=change-me
```

```bash
kubectl -n log-viewer port-forward svc/log-viewer 8083:8080
# Admin:  http://localhost:8083/admin
# Viewer: http://localhost:8083/
```

## License

MIT — see [LICENSE](LICENSE).
