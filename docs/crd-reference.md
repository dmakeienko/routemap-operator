# Routemap CRD Reference

**API group:** `routemaps.github.com`  
**Version:** `v1alpha1`  
**Scope:** Namespaced

---

## Spec

```yaml
spec:
  displayName: ""            # Dashboard title; defaults to .metadata.name
  namespaces: {}             # Which namespaces to scan (see below)
  sources: ["Ingress","HTTPRoute"]  # Resource kinds to discover
  healthCheck: {}            # Optional active probing (see below)
  auth: {}                   # Optional OIDC protection (see below)
```

### `spec.namespaces` — NamespaceSelector

Exactly one strategy applies; `watchAll` takes priority when set.

| Field | Type | Description |
|---|---|---|
| `watchAll` | bool | Scan every namespace in the cluster. |
| `names` | `[]string` | Explicit list of namespaces. |
| `selector` | `metav1.LabelSelector` | Match namespaces by label. |

If none of the three fields is set, the operator scans only the namespace where the `Routemap` CR lives.

### `spec.sources` — SourceKind[]

Accepted values: `Ingress`, `HTTPRoute`.  
Default: `["Ingress","HTTPRoute"]`.

### `spec.healthCheck` — HealthCheckSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Activates periodic probing. |
| `annotationKey` | string | `routemap.github.com/healthcheck` | Annotation read from Ingress/HTTPRoute to find the health path. |
| `interval` | Duration | `30s` | Time between probe cycles. |
| `timeout` | Duration | `5s` | Per-request timeout. |
| `expectedStatus` | int32 | `200` | HTTP status code treated as healthy. |

Health probing happens only for endpoints that have a non-empty `HealthPath`, i.e. the Ingress or HTTPRoute must carry the annotation.

### `spec.auth` — AuthSpec

| Field | Type | Description |
|---|---|---|
| `issuerURL` | string | OIDC issuer URL (must expose `/.well-known/openid-configuration`). |
| `clientID` | string | OAuth2 client ID. |
| `clientSecretRef.name` | string | Name of the Secret holding the client secret. |
| `clientSecretRef.key` | string | Key inside the Secret. |
| `redirectURL` | string | Callback URL registered with the IdP (must end with `/{ns}/{name}/callback`). |

See [oidc.md](oidc.md) for full OIDC setup instructions.

---

## Status

| Field | Description |
|---|---|
| `observedGeneration` | `.metadata.generation` processed in the last reconcile. |
| `discoveredEndpoints` | Total normalised endpoints currently in the store. |
| `healthyEndpoints` | Endpoints with `HealthState=Healthy` (0 when health check is disabled). |
| `dashboardURL` | In-cluster path, e.g. `http://<pod-ip>:9090/default/my-routemap`. |
| `conditions` | Standard `metav1.Condition` list. Currently sets the `Ready` type. |

### `kubectl get routemap` columns

```
NAME           ENDPOINTS   HEALTHY   READY   AGE
my-routemap    12          10        True    5m
```

---

## Endpoint health annotation

Add this annotation to any `Ingress` or `HTTPRoute` you want probed:

```yaml
metadata:
  annotations:
    routemap.github.com/healthcheck: /healthz
```

The value is the path the health checker will GET. Override the annotation key via `spec.healthCheck.annotationKey`.

---

## Minimal example

```yaml
apiVersion: routemaps.github.com/v1alpha1
kind: Routemap
metadata:
  name: my-app
  namespace: default
spec:
  displayName: "My Application"
  sources:
    - Ingress
```

## Full example

```yaml
apiVersion: routemaps.github.com/v1alpha1
kind: Routemap
metadata:
  name: platform-routes
  namespace: routemap-system
spec:
  displayName: "Platform Services"
  namespaces:
    names:
      - default
      - staging
      - production
  sources:
    - Ingress
    - HTTPRoute
  healthCheck:
    enabled: true
    annotationKey: routemap.github.com/healthcheck
    interval: 30s
    timeout: 5s
    expectedStatus: 200
  auth:
    issuerURL: https://accounts.google.com
    clientID: my-client-id
    clientSecretRef:
      name: routemap-oidc-secret
      key: client-secret
    redirectURL: https://routemap.example.com/routemap-system/platform-routes/callback
```
