# Deployment

## Prerequisites

- Kubernetes ≥ 1.26
- `kubectl` configured against your cluster
- Helm 3 (recommended) or kustomize
- (Optional) [Gateway API CRDs](https://gateway-api.sigs.k8s.io/guides/#installing-gateway-api) if you want HTTPRoute discovery

---

## Install with Helm (recommended)

```bash
# Install CRDs + operator in one step
helm install routemap-operator ./charts/routemap-operator \
  --namespace routemap-system \
  --create-namespace
```

The Helm chart deploys:

- The operator `Deployment` with leader election enabled by default
- `ClusterRole` / `ClusterRoleBinding` with all required RBAC
- `ServiceAccount`
- A `ClusterIP` Service named `<release>-dashboard` on port 9090 for the dashboard

### Helm values

| Value | Default | Description |
|---|---|---|
| `image.repository` | `dmakeienko12/routemap-operator` | Container image repository |
| `image.tag` | chart `appVersion` | Image tag; defaults to the chart's `appVersion` |
| `replicaCount` | `1` | Number of operator replicas |
| `leaderElect` | `true` | Enable leader election (required for `replicaCount > 1`) |
| `dashboard.enabled` | `true` | Run the dashboard HTTP server |
| `dashboard.bindAddress` | `:9090` | Address the dashboard listens on inside the pod |
| `dashboard.service.enabled` | `true` | Create a Service for the dashboard |
| `dashboard.service.type` | `ClusterIP` | Service type (`ClusterIP`, `NodePort`, `LoadBalancer`) |
| `dashboard.service.port` | `9090` | Service port |
| `resources.limits.cpu` | `500m` | CPU limit |
| `resources.limits.memory` | `128Mi` | Memory limit |

Override values at install time:

```bash
helm install routemap-operator ./charts/routemap-operator \
  --namespace routemap-system \
  --create-namespace \
  --set image.tag=v0.2.0 \
  --set dashboard.service.type=LoadBalancer
```

---

## Install with kustomize

```bash
# Install CRDs
make install
# or:
kubectl apply -f config/crd/bases/

# Build and push the image
make docker-build docker-push IMG=ghcr.io/dmakeienko/routemap-operator:latest

# Deploy
make deploy IMG=ghcr.io/dmakeienko/routemap-operator:latest
```

> **Note:** The kustomize deployment does not create a dashboard Service by default.
> Add one manually — see [Accessing the dashboard](#accessing-the-dashboard) below.

---

## Create your first Routemap

```bash
# Using the sample from the repo
kubectl apply -f config/samples/routemaps_v1alpha1_routemap.yaml

# Or the example from routemap.yaml
kubectl apply -f routemap.yaml
```

Verify:

```bash
kubectl get routemap -A
# NAME               ENDPOINTS   HEALTHY   READY   AGE
# routemap-sample    4           4         True    30s

kubectl describe routemap routemap-sample -n default
```

---

## Accessing the dashboard

The dashboard URL has the form:

```text
http://<service-or-host>:9090/{namespace}/{routemap-name}
```

### Helm install — port-forward (quickest)

```bash
kubectl port-forward -n routemap-system \
  svc/routemap-operator-dashboard 9090:9090
```

Then open: `http://localhost:9090/default/routemap-sample`

### Helm install — via Ingress

The Helm chart creates a `ClusterIP` Service automatically. Add an Ingress on top:

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: routemap-dashboard
  namespace: routemap-system
spec:
  rules:
    - host: routemap.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: routemap-operator-dashboard   # <release>-dashboard
                port:
                  number: 9090
```

Then open: `https://routemap.example.com/default/routemap-sample`

### kustomize install — manual Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: routemap-dashboard
  namespace: routemap-system
spec:
  selector:
    app.kubernetes.io/name: routemap-operator
  ports:
    - port: 9090
      targetPort: 9090
```

### Development — run locally

```bash
make install   # install CRDs
make run       # run operator against current kubeconfig
```

Open: `http://localhost:9090/{namespace}/{routemap-name}`

---

## Operator flags

All flags are passed to the manager binary. With Helm, configure them via `values.yaml`; with kustomize, edit the `args` array in `config/manager/manager.yaml`.

| Flag | Default | Description |
|---|---|---|
| `--dashboard-bind-address` | `:9090` | Address the dashboard HTTP server listens on. |
| `--enable-dashboard` | `true` | Set to `false` to disable the dashboard entirely. |
| `--health-probe-bind-address` | `:8081` | Kubernetes liveness/readiness probe address. |
| `--metrics-bind-address` | `0` | Prometheus metrics address. Use `:8443` (HTTPS) or `:8080` (HTTP). |
| `--metrics-secure` | `true` | Serve metrics over HTTPS. |
| `--leader-elect` | `false` | Enable leader election (required for multi-replica deployments). |
| `--enable-http2` | `false` | Enable HTTP/2 on metrics and webhook servers. |
| `--zap-log-level` | `info` | Log verbosity (`debug`, `info`, `error`). |

---

## Multi-replica / HA

Set `leaderElect: true` (Helm) or `--leader-elect` (kustomize). The health checker only runs on the elected leader, preventing duplicate probes. The dashboard and reconciler run on all replicas.

---

## RBAC

The operator requires the following permissions:

| Resource | Verbs |
|---|---|
| `networking.k8s.io/ingresses` | get, list, watch |
| `gateway.networking.k8s.io/httproutes` | get, list, watch |
| `core/namespaces` | get, list, watch |
| `core/secrets` | get |
| `routemaps.github.com/routemaps` | get, list, watch, create, update, patch, delete |
| `routemaps.github.com/routemaps/status` | get, update, patch |
| `routemaps.github.com/routemaps/finalizers` | update |

Regenerate after changing kubebuilder markers:

```bash
make manifests
```

---

## Prometheus metrics

Enable the metrics endpoint:

```bash
--metrics-bind-address=:8080 --metrics-secure=false
```

Available metric:

| Metric | Labels | Description |
|---|---|---|
| `routemap_endpoint_health_probes_total` | `routemap`, `host`, `result` | Counter of health probes by outcome (`Healthy`/`Unhealthy`). |

---

## Uninstall

```bash
# Helm
helm uninstall routemap-operator -n routemap-system
kubectl delete -f config/crd/bases/   # removes all Routemap objects!

# kustomize
make undeploy    # remove operator Deployment and RBAC
make uninstall   # remove CRDs (deletes all Routemap objects!)
```
