# routemap-operator — Overview

routemap-operator is a Kubernetes operator that **discovers HTTP endpoints** from `Ingress` and `HTTPRoute` resources across one or more namespaces, optionally health-checks them, and serves a **per-Routemap web dashboard** showing the live endpoint inventory.

## How it works

```
┌────────────────────────────────────────────────────────────────┐
│  Kubernetes API                                                │
│  ┌──────────────┐  ┌──────────────────────────────────────┐   │
│  │   Routemap   │  │  Ingress / HTTPRoute (watched NSes)  │   │
│  │     CRD      │  └────────────────┬─────────────────────┘   │
│  └──────┬───────┘                   │                         │
└─────────┼─────────────────────────── ┼ ────────────────────────┘
          │  reconcile                 │ list+watch
          ▼                            ▼
   ┌──────────────┐          ┌──────────────────┐
   │  Controller  │─────────▶│  In-memory Store │
   └──────────────┘  writes  └────────┬─────────┘
                                      │ reads
              ┌───────────────────────┤
              │                       │
              ▼                       ▼
   ┌─────────────────────┐   ┌─────────────────┐
   │  Health Checker     │   │  Dashboard HTTP │
   │  (leader-only)      │   │  Server :9090   │
   └─────────────────────┘   └─────────────────┘
```

### Reconciliation loop

1. A `Routemap` CR is created or updated.
2. The controller resolves the target namespace set (all / explicit list / label selector).
3. It lists all `Ingress` and/or `HTTPRoute` objects in those namespaces and normalises them into a flat list of `Endpoint` records.
4. The result is written to an **in-memory store** keyed by the Routemap's `namespace/name`.
5. The CR's `.status` is updated with endpoint counts and the dashboard URL.

### Health checker

- Runs as a separate goroutine, gated on **leader election** — only one replica probes at a time.
- Polls the store every 10 s, starts/stops per-Routemap probe loops based on `healthCheck.enabled`.
- For each endpoint that has a `HealthPath` (via annotation), issues an HTTP GET and compares the response code to `expectedStatus`.
- Writes `Healthy` / `Unhealthy` / `Unknown` back to the store via `UpdateEndpointHealth`.
- Emits the Prometheus counter `routemap_endpoint_health_probes_total{routemap, host, result}`.

### Dashboard server

- An HTTP server bound to `:9090` (configurable).
- Routes: `/{ns}/{name}` — dashboard page, `/{ns}/{name}/api/endpoints` — JSON API, `/{ns}/{name}/callback` — OIDC callback.
- Renders a single-page HTML dashboard with no external CDN dependencies.
- Supports **optional OIDC authentication** per Routemap (see [oidc.md](oidc.md)).
- Serves all Routemaps from the same process; the store isolates data per key.

### HTTPRoute graceful degradation

At startup the controller probes the REST mapper for the Gateway API CRD. If it is not installed, HTTPRoute discovery is silently skipped; no error is surfaced. This lets you run the operator on clusters that only have `networking.k8s.io/v1/Ingress`.

## Component map

| Package | Purpose |
|---|---|
| `api/v1alpha1` | CRD schema, deepcopy, GroupVersionKind registration |
| `internal/inventory` | Ingress → Endpoint, HTTPRoute → Endpoint mappers; namespace resolver |
| `internal/store` | Thread-safe in-memory store bridging reconciler ↔ dashboard/health |
| `internal/controller` | Kubebuilder reconciler, watches, finalizer, status updates |
| `internal/dashboard` | HTTP server, HTML template, OIDC middleware |
| `internal/health` | Async health probe runnable |
| `cmd/main.go` | Wires everything into a controller-runtime Manager |
