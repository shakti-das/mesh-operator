// Minimal VirtualService template.
//
// The operator invokes this function once per matched service. It receives
// the raw Service object and a rendering context containing policies and
// aliases declared on the selecting MeshOperator.
function(service, context) [{
  apiVersion: "networking.istio.io/v1alpha3",
  kind: "VirtualService",
  metadata: {
    name: service.metadata.name,
    namespace: service.metadata.namespace,
  },
  spec: {
    hosts: [service.metadata.name + "." + service.metadata.namespace + ".svc.cluster.local"],
    http: [{
      route: [{
        destination: {
          host: service.metadata.name + "." + service.metadata.namespace + ".svc.cluster.local",
        },
      }],
    }],
  },
}]
