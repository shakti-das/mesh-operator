# mesh-operator

A Kubernetes operator that generates Istio routing configuration from a small set
of domain-specific CRDs. It reconciles `MeshOperator`, `MeshServiceMetadata`,
`ClusterTrafficPolicy`, `TrafficShardingPolicy`, and `SidecarConfig` resources
into `VirtualService`, `DestinationRule`, `ServiceEntry`, `Sidecar`, and
`EnvoyFilter` objects using Jsonnet templates.

This project is an Istio-ecosystem friendly port of the operator that
Salesforce has been running in production. The upstream design keeps feature
parity with the internal version while removing organization-specific hooks;
anything Salesforce-specific (Falcon, FMT metadata cache, internal hostnames)
has been scrubbed.

## Status

Early bootstrap. APIs, CRDs, and flag names may move around. Track progress in
the GitHub issues.

## Build

Requires Go 1.24+.

```bash
go build ./...
go test ./...
```

## Run locally

```bash
go run ./cmd/mesh-operator server \
  --templates-location=./examples/templates \
  --config-namespace=istio-system
```

See `go run ./cmd/mesh-operator server --help` for the full flag set. The
operator also exposes:

- `go run ./cmd/mesh-operator render ...` – render the Istio configuration for a
  service from a local file tree without talking to Kubernetes (useful for
  PR-time golden-file testing).
- `go run ./cmd/mesh-operator mutate ...` – run only the mutating admission
  webhook.

## CRDs

Custom resource definitions live under `crds/`. Apply them before starting the
operator:

```bash
kubectl apply -f crds/
```

The operator watches:

- `MeshOperator` (mesh.io) – the top-level control object. Selects services by
  label, names the templates that generate their Istio config, and defines the
  label/annotation aliases the templates receive in their rendering context.
- `MeshServiceMetadata` (mesh.io) – overlay metadata for a service.
- `ClusterTrafficPolicy` (mesh.io) – cluster-local traffic policy applied to a
  service.
- `TrafficShardingPolicy` (mesh.io) – policy that shards traffic across
  workload slices.
- `SidecarConfig` (mesh.io) – sidecar-specific configuration.

## Templates

Templates are Jsonnet files under a directory tree. Each file exports an Istio
resource (or array of resources). The operator feeds each template a rendering
context containing the service object, any policy objects, and the aliases
declared on the selecting `MeshOperator`.

A minimal example:

```bash
examples/templates/
  default/
    default/
      virtualservice.jsonnet
      destinationrule.jsonnet
```

Run `go run ./cmd/mesh-operator render --templates ./examples/templates ...`
against a sample service YAML to see what would be emitted.

## Repository layout

- `cmd/mesh-operator/` – binary entry point (`go build ./cmd/mesh-operator`).
- `pkg/cmd/` – cobra commands: `server`, `render`, `mutate`.
- `controllers/` – controller-runtime reconcilers for each CRD.
- `pkg/controllers_api/` – controller managers, workqueues, dispatch.
- `pkg/resources/` – Istio CR construction helpers.
- `pkg/templating/` – Jsonnet engine wrapper and template loaders.
- `pkg/kube/`, `pkg/kube_test/` – Kubernetes client helpers.
- `pkg/generated/` – generated clientset / informers / listers for the `mesh.io`
  CRDs.
- `common/pkg/` – shared infrastructure (logging, metrics, k8s webhook,
  dynamic routing, HTTP helpers).
- `crds/` – CustomResourceDefinition manifests.
- `examples/` – minimal templates, use-cases, and gold files.

## Contributing

PRs and issues welcome. Before submitting a change:

```bash
go build ./...
go vet ./...
go test ./...
```

## License

Apache License 2.0. See [LICENSE](./LICENSE).
