# Development

## Prerequisites

- Go 1.24+
- `make`
- `kubectl` + a local cluster (kind, minikube, k3d)
- Docker (for image builds)

## Repository layout

```
.
├── api/v1alpha1/          CRD types, deepcopy, scheme registration
├── cmd/main.go            Entrypoint — wires manager, store, dashboard, health checker
├── config/
│   ├── crd/               Generated CRD manifests
│   ├── default/           Kustomize base overlay
│   ├── manager/           Deployment manifest
│   └── rbac/              Generated ClusterRole
├── internal/
│   ├── controller/        Kubebuilder reconciler
│   ├── dashboard/         HTTP server, HTML template, OIDC middleware
│   ├── health/            Async health probe runnable
│   ├── inventory/         Ingress/HTTPRoute → Endpoint mappers
│   └── store/             Thread-safe in-memory store
└── docs/                  This documentation
```

## Common make targets

```bash
make help           # Print all targets with descriptions

make generate       # Regenerate DeepCopy methods (run after editing types)
make manifests      # Regenerate CRD YAML and RBAC from kubebuilder markers
make fmt            # go fmt ./...
make vet            # go vet ./...
make lint           # golangci-lint run
make lint-fix       # golangci-lint run --fix
make test           # Run all unit + envtest tests with coverage
make build          # go build ./...
make run            # Run operator against current kubeconfig
make install        # kubectl apply CRDs
make uninstall      # kubectl delete CRDs
make deploy         # kustomize build + kubectl apply
make undeploy       # kubectl delete the operator resources
make docker-build   # Build container image (set IMG=...)
make docker-push    # Push image
```

## Running tests

Tests require envtest binaries:

```bash
make test
```

`make test` calls `setup-envtest` which downloads the binaries to `bin/k8s/` automatically. Running `go test ./...` directly will fail without the binaries.

### Coverage

```
internal/store:       100%
internal/inventory:    72%
internal/health:       67%
internal/controller:   53%
internal/dashboard:    36%
```

### Linting

The project uses golangci-lint v2 with a custom `logcheck` plugin. Run:

```bash
make lint-fix   # fixes auto-fixable issues
make lint       # check-only
```

The `modernize/newexpr` rule is disabled because it incorrectly rewrites generic pointer helpers (`ptrOf[T any](v T) *T`).

## Adding a new field to the CRD

1. Edit [api/v1alpha1/routemap_types.go](../api/v1alpha1/routemap_types.go) — add the field with kubebuilder markers.
2. Run `make generate manifests`.
3. Add validation/usage in the controller or inventory packages.
4. Add or update tests.

## Architecture decisions

### In-memory store

The store is the single shared mutable state. The reconciler writes to it; the dashboard and health checker read from it. `sync.RWMutex` provides safe concurrent access. No external cache (Redis, etcd) is needed — the reconciler will repopulate the store from the API server on pod restart.

### HTTPRoute as optional dependency

The operator does not declare a hard dependency on the Gateway API. At startup, `SetupWithManager` probes the REST mapper for `gateway.networking.k8s.io/v1/httproutes`. If absent the `HTTPRouteAvailable` flag stays false and the controller skips HTTPRoute list calls and watch registration. This means a single binary works on clusters with or without Gateway API installed.

### Health checker leader-gating

The checker implements `NeedLeaderElection() bool { return true }`. controller-runtime will not call `Start` on non-leader replicas. This prevents N replicas from probing the same endpoints N times simultaneously.

### OIDC session tokens without server state

Storing sessions in a database or cache would introduce operational complexity. Instead, the session payload is signed with an in-process HMAC key and the expiry is embedded in the token. The trade-off is that sessions are invalidated on pod restart, which is acceptable for a developer-facing tool.
