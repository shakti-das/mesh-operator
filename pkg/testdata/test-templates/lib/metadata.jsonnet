{
  buildMetadataFromService(service)::
    {
      namespace: service.metadata.namespace,
      name: service.metadata.name,
      labels: if std.objectHas(service.metadata, "labels") then service.metadata.labels else {}
    },
}
