local metadata = import "lib/metadata.jsonnet";

function(serviceentry, context) [{
  apiVersion: "networking.istio.io/v1alpha3",
  kind: "VirtualService",
  metadata: {},
  spec: {}
}]
