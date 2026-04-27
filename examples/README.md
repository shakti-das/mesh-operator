# examples

Small, self-contained examples of how to feed mesh-operator.

```
examples/
  templates/     # Jsonnet templates the operator renders per service
  use-cases/     # sample Service + MeshOperator manifests
```

## 1. Render locally (no cluster needed)

The fastest smoke test: feed a sample service + the templates through the
real rendering pipeline and print the Istio CRs that would be applied.

```bash
go run ./cmd/mesh-operator render \
  --template-paths ./examples/templates \
  --service-path ./examples/use-cases/basic/service.yaml \
  -p
```

Expected output is a JSON document with a `virtualservice` and
`destinationrule` array, each containing one object for the `orders` service.

The sample Service pins a specific template via the
`routing.mesh.io/template: default/default` annotation, so template
selection is explicit. To exercise label-based selection instead, drop the
annotation, name your template directory after the service's app label, and
pass `--template-selector-labels app`.

## 2. Run against a real cluster

Requires a local Kubernetes cluster plus `istioctl`.

```bash
# Install prerequisites (once)
brew install minikube istioctl     # or: brew install kind istioctl

# 1. Spin up a cluster
minikube start -p mop --kubernetes-version=v1.30.2
# or:  kind create cluster --name mop

# 2. Install Istio
istioctl install --set profile=demo -y

# 3. Install mesh-operator CRDs
kubectl apply -f crds/

# 4. Create the demo namespace and apply the sample CRs
kubectl create ns demo
kubectl apply -f examples/use-cases/basic/

# 5. Run mesh-operator against the cluster (it reads ~/.kube/config)
go run ./cmd/mesh-operator server \
  --config-namespace=istio-system \
  --template-paths=./examples/templates \
  --template-selector-labels=app \
  --leader-elect=false

# 6. In another terminal, confirm Istio config was generated
kubectl get virtualservice,destinationrule -A
kubectl get meshoperator -n demo -o yaml
```

To tear down:

```bash
kubectl delete -f examples/use-cases/basic/
minikube delete -p mop            # or: kind delete cluster --name mop
```

## Using richer templates

For blue/green, canary, multi-cluster, and external-service patterns browse
`pkg/testdata/end-to-end-templates/` — the tree exercised by the operator's
own golden-file tests. Copy the bits you need into your own templates/ tree.
