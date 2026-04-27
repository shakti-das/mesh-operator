local metadata = import "lib/metadata.jsonnet";

function(mop, filter, service=null, context) [{
  apiVersion: "networking.istio.io/v1alpha3",
  kind: "EnvoyFilter",
  metadata: {},
  spec: {}
}]
