function(service, context) [{
  apiVersion: "networking.istio.io/v1alpha3",
  kind: "DestinationRule",
  metadata: {
    name: service.metadata.name,
    namespace: service.metadata.namespace,
  },
  spec: {
    host: service.metadata.name + "." + service.metadata.namespace + ".svc.cluster.local",
    trafficPolicy: {
      tls: { mode: "ISTIO_MUTUAL" },
    },
  },
}]
