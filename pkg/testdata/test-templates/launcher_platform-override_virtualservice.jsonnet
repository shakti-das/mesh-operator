local metadata = import "lib/metadata.jsonnet";

function(service, context) [{
  apiVersion: "networking.istio.io/v1alpha3",
  kind: "VirtualService",
  metadata: metadata.buildMetadataFromService(service),
  spec: "bad spec template",
  badTemplate: { bad: "123", bad: "456"}
}]
