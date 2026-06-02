# Dashboard

Each `Routemap` CR gets its own dashboard page served by the operator at:

```
http://<operator-pod>:9090/{namespace}/{routemap-name}
```

## Features

- **Endpoint cards** — one card per discovered endpoint, grouped by namespace by default.
- **Health indicators** — coloured dot per card:
  - Green — `Healthy` (last probe passed)
  - Red — `Unhealthy` (probe failed or returned unexpected status)
  - Grey — `Unknown` (health check not yet run)
  - Yellow — `Pending` (probe in flight)
  - Light grey — `NoCheck` (no `HealthPath` annotation on this endpoint)
- **Client-side filtering** — filter by namespace, health status, or source type (Ingress / HTTPRoute) without a page reload.
- **Auto-refresh** — the JSON API endpoint is polled every 30 s; the page updates without a full reload.
- **Self-contained** — the HTML template is embedded in the binary (`//go:embed`); no external CDN calls.

## Routes

| Path | Description |
|---|---|
| `/{ns}/{name}` | Full dashboard page (HTML). |
| `/{ns}/{name}/api/endpoints` | JSON array of current endpoints for this Routemap. |
| `/{ns}/{name}/callback` | OIDC redirect callback (only active when `spec.auth` is set). |
| `/healthz` | Liveness probe — returns `200 OK`. |

## JSON endpoint schema

`GET /{ns}/{name}/api/endpoints`

```json
[
  {
    "routemapKey":  {"namespace": "default", "name": "my-routemap"},
    "sourceKind":   "Ingress",
    "sourceRef":    {"namespace": "default", "name": "my-app"},
    "host":         "my-app.example.com",
    "path":         "/api",
    "pathType":     "Prefix",
    "backend":      "my-app-svc:8080",
    "tls":          false,
    "healthPath":   "/healthz",
    "health":       "Healthy",
    "lastProbe":    "2026-06-02T12:34:56Z"
  }
]
```

## OIDC protection

Add `spec.auth` to the Routemap CR to require login before viewing the dashboard. See [oidc.md](oidc.md).

## Disabling the dashboard

Pass `--enable-dashboard=false` to the operator binary to skip starting the HTTP server entirely.
