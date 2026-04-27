function(service, rollout, context)
[{
  op: "replace",
  path: "/spec/strategy",
  value: if std.objectHas(context, "isOcmManagedRollout") && context.isOcmManagedRollout == "true" then "ocm-canary" else "canary"
}]
