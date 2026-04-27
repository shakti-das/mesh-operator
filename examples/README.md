# examples

Small, self-contained examples of how to feed mesh-operator.

```
examples/
  templates/     # Jsonnet templates the operator renders per service
  use-cases/     # sample service + policy manifests for `mop render`
```

## Rendering a sample

```bash
go run ./cmd/mesh-operator render \
  --templates ./examples/templates \
  --service ./examples/use-cases/basic/service.yaml
```

This prints the Istio resources mesh-operator would apply for the sample
service using the `default/default` template. Use this as the starting point
for building your own templates; copy `templates/default/default/*.jsonnet`
and edit as needed.

For a richer set of templates with blue/green, canary, multi-cluster, and
external-service patterns, browse `pkg/testdata/end-to-end-templates/`, which
is the tree exercised by the operator's own golden-file tests.
