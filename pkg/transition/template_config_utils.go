package transition

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	DefaultDefault = "default_default"

	DefaultStatefulSfdcnet = "default_stateful-sfdcnet"
	EmailinfraEaas         = "emailinfra_eaas"
	SolrService            = "solr-service_solr-service"
	Vagabond               = "vagabond_vagabond"

	DefaultRedisSfdcnet    = "default_redis-sfdcnet"
	Caas                   = "cache-as-a-service_caas"
	CaasPc                 = "cache-as-a-service_caaspc"
	DefaultExternalService = "default_external-service"
	DefaultZkSfdcnet       = "default_zk-sfdcnet"

	CoreOnSamCoreapp = "core-on-sam_coreapp"
	CoreOnSamDapp    = "core-on-sam_dapp"
	HsrCoreHsr       = "hsr_core-hsr"

	GenericServiceFilters      = "generic_service-filters"
	HawkingServiceFilters      = "hawking_service-filters"
	ProtocolFilters            = "protocol_filters"
	RateLimitingServiceFilters = "unified-engagement_rate-limiting-service"

	ManagedByCopilotValue = "istio-copilot"
)

// namespace
const (
	MeshControlPlaneNamespace = "mesh-control-plane"
)

// filter suffixes
const (
	RedisFilterSuffix           = "ext-svc-redis"
	StarttlsFilterSuffix        = "ext-svc-starttls"
	MaxConnDurationFilterSuffix = "max-conn-duration"
	ZkFilterSuffix              = "zookeeper-proxy-filter"
	RedisOpsTimeoutFilterSuffix = "redis-ops-timeout-filter"
	ThriftFilterSuffix          = "thrift-filter"
	RatelimitFilterSuffix       = "rate-limit-filter"
)

// Additional Service Filters Suffix
const (
	GenericAuthorityFilterSuffix   = "authority"
	GenericGzipFilterSuffix        = "gzip"
	GenericJwtFilterSuffix         = ""
	GenericPassthroughFilterSuffix = "source-ip-passthrough"
	GenericSqlServerFilterSuffix   = ""
	GenericRateLimitFilterSuffix   = "rate-limit-filter"
	HawkingGzipFilterSuffix        = "gzip-filter"
	HawkingRatelimitFilterSuffix   = "rate-limit-filter"
)

// Annotations
const (
	ExternalServiceConfigAnnotation = "routing.mesh.sfdc.net/external-service"
	RedisOpsTimeoutAnnotation       = "routing.mesh.sfdc.net/redis-ops-timeout"
	RedisOpsTimeoutFilterNamespace  = "core-on-sam"
	SfproxyNamespace                = "sfproxy"
	isFdLevel                       = "is_fd_level"
	UnifiedEngagementFd             = "unified-engagement"
	ServiceTemplateAnnotation       = "routing.mesh.sfdc.net/service-templates"
	ManagedByCopilotLabel           = "mesh.sfdc.net/managed-by"
	TypeLabel                       = "mesh.sfdc.net/type"
	VersionLabel                    = "mesh.sfdc.net/version"
)

var (
	RlsExcludedNamespaces = []string{
		"mesh-control-plane", "service-mesh", "sfproxy",
		"authz-opa-webhook", "collection-webhook", "collection-webhook-test", "cts", "discovery", "dnr-collect",
		"dva-system", "iac", "kaas", "kaas-webhook", "kube-node-lease", "kube-public", "kube-system", "madkub-injector-watchdog",
		"madkub-webhook", "opa", "sam-system", "sfdc-system", "stampy-webhook", "stride-eks-components", "system", "vault-injection",
		"vault-webhook",
	}
)

var (
	allowedServiceFiltersForTemplateType = map[string][]string{
		GenericServiceFilters: {"authority", "gzip", "jwt", "passthrough", "sql-server", "ratelimit"},
		HawkingServiceFilters: {"gzip", "ratelimit"},
	}
	additionalTemplateTypes = []string{GenericServiceFilters, HawkingServiceFilters, ProtocolFilters, RateLimitingServiceFilters}
)

// configInfo represents a config object generated for a given template type
type configInfo struct {
	getName      func(obj *unstructured.Unstructured) (string, error)
	getNamespace func(obj *unstructured.Unstructured) (string, error)
	gvr          schema.GroupVersionResource
}

type serviceFilterInfo struct {
	getName func(obj *unstructured.Unstructured) string
	gvr     schema.GroupVersionResource
	kind    string
}

var (
	// configListByTemplateType contains config information for template types that produce a constant number of configs.
	configListByTemplateType    = buildConfigListMap()
	additionalServiceFilterInfo = map[string]serviceFilterInfo{
		"generic_service-filters_authority": {
			getName: func(obj *unstructured.Unstructured) string {
				return getResourceName(obj, GenericAuthorityFilterSuffix, "")
			},
			gvr:  constants.EnvoyFilterResource,
			kind: "EnvoyFilter",
		},
		"generic_service-filters_gzip": {
			getName: func(obj *unstructured.Unstructured) string {
				return getResourceName(obj, GenericGzipFilterSuffix, "")
			},
			gvr:  constants.EnvoyFilterResource,
			kind: "EnvoyFilter",
		},
		"generic_service-filters_jwt": {
			getName: func(obj *unstructured.Unstructured) string {
				return getResourceName(obj, GenericJwtFilterSuffix, "")
			},
			gvr:  constants.RequestAuthenticationResource,
			kind: "RequestAuthentication",
		},
		"generic_service-filters_passthrough": {
			getName: func(obj *unstructured.Unstructured) string {
				return getResourceName(obj, GenericPassthroughFilterSuffix, "")
			},
			gvr:  constants.EnvoyFilterResource,
			kind: "EnvoyFilter",
		},
		"generic_service-filters_sql-server": {
			getName: func(obj *unstructured.Unstructured) string {
				return getResourceName(obj, GenericSqlServerFilterSuffix, "")
			},
			gvr:  constants.ServiceEntryResource,
			kind: "ServiceEntry",
		},
		"generic_service-filters_ratelimit": {
			getName: func(obj *unstructured.Unstructured) string {
				return getResourceName(obj, GenericRateLimitFilterSuffix, "")
			},
			gvr:  constants.EnvoyFilterResource,
			kind: "EnvoyFilter",
		},
		"hawking_service-filters_gzip": {
			getName: func(obj *unstructured.Unstructured) string {
				return getResourceName(obj, HawkingGzipFilterSuffix, "z")
			},
			gvr:  constants.EnvoyFilterResource,
			kind: "EnvoyFilter",
		},
		"hawking_service-filters_ratelimit": {
			getName: func(obj *unstructured.Unstructured) string {
				return getResourceName(obj, HawkingRatelimitFilterSuffix, "")
			},
			gvr:  constants.EnvoyFilterResource,
			kind: "EnvoyFilter",
		},
	}
)

func buildConfigListMap() map[string][]configInfo {
	result := map[string][]configInfo{
		DefaultExternalService: {
			{
				gvr: constants.EnvoyFilterResource,
				getName: func(obj *unstructured.Unstructured) (string, error) {
					configNamespaceLookUp, err := getRedisFilterNamespace(obj)
					configName := getEnvoyFilterName(obj, RedisFilterSuffix, configNamespaceLookUp)
					if err != nil {
						return "", err
					}
					return configName, nil
				},
				getNamespace: getRedisFilterNamespace,
			}, // redisenvoy.jsonnet
			{
				gvr: constants.EnvoyFilterResource,
				getName: func(object *unstructured.Unstructured) (string, error) {
					return getEnvoyFilterName(object, MaxConnDurationFilterSuffix, MeshControlPlaneNamespace), nil
				},
				getNamespace: func(_ *unstructured.Unstructured) (string, error) {
					return MeshControlPlaneNamespace, nil
				},
			}, // max-connection-duration.jsonnet
			{
				gvr: constants.EnvoyFilterResource,
				getName: func(object *unstructured.Unstructured) (string, error) {
					return defaultGetName(object.GetName(), StarttlsFilterSuffix), nil
				},
				getNamespace: defaultGetNamespace,
			}, // starttls.jsonnet
		},
		DefaultZkSfdcnet: {
			{
				gvr: constants.EnvoyFilterResource,
				getName: func(object *unstructured.Unstructured) (string, error) {
					return getZkFilterName(object), nil
				},
				getNamespace: defaultGetNamespace,
			}, // zkfilter.jsonnet
		},
		DefaultRedisSfdcnet: {
			// this envoy filter only exists when the service object has RedisOpsTimeoutAnnotation set on it
			{
				gvr: constants.EnvoyFilterResource,
				getName: func(object *unstructured.Unstructured) (string, error) {
					return defaultGetName(object.GetName(), RedisOpsTimeoutFilterSuffix), nil
				},
				getNamespace: func(_ *unstructured.Unstructured) (string, error) {
					return RedisOpsTimeoutFilterNamespace, nil
				},
			}, // envoyfilter.jsonnet
		},
		CoreOnSamCoreapp: {
			// ingress-vs
			{
				gvr:          constants.VirtualServiceResource,
				getNamespace: defaultGetNamespace,
				getName: func(svc *unstructured.Unstructured) (string, error) {
					return getResourceNameFromMetadata(svc, "ingress"), nil
				},
			},
			// internal-header
			{
				gvr:          constants.EnvoyFilterResource,
				getNamespace: defaultGetNamespace,
				getName: func(svc *unstructured.Unstructured) (string, error) {
					return getResourceNameFromMetadata(svc, "internal-header"), nil
				},
			},
			// header-casing
			{
				gvr:          constants.EnvoyFilterResource,
				getNamespace: defaultGetNamespace,
				getName: func(svc *unstructured.Unstructured) (string, error) {
					return getResourceNameFromMetadata(svc, "header-casing"), nil
				},
			},
			// header-casing (sfproxy)
			{
				gvr: constants.EnvoyFilterResource,
				getNamespace: func(obj *unstructured.Unstructured) (string, error) {
					return SfproxyNamespace, nil
				},
				getName: func(svc *unstructured.Unstructured) (string, error) {
					return getResourceNameFromMetadata(svc, "header-casing"), nil
				},
			},
			// sidecar
			{
				gvr:          constants.SidecarResource,
				getNamespace: defaultGetNamespace,
				getName: func(svc *unstructured.Unstructured) (string, error) {
					return getResourceNameFromMetadata(svc, ""), nil
				},
			},
			// sidecar - bootstrap
			{
				gvr:          constants.SidecarResource,
				getNamespace: defaultGetNamespace,
				getName: func(svc *unstructured.Unstructured) (string, error) {
					if cell, found := svc.GetLabels()["p_cell"]; found {
						return cell + "-casam-bootstrap", nil
					} else {
						// If no p_cell - just return something
						return "undefined", nil
					}
				},
			},
		},
		HsrCoreHsr: {
			// header-casing
			{
				gvr:          constants.EnvoyFilterResource,
				getNamespace: defaultGetNamespace,
				getName: func(svc *unstructured.Unstructured) (string, error) {
					return getResourceNameFromMetadata(svc, "header-casing"), nil
				},
			},
			// header-casing (sfproxy)
			{
				gvr: constants.EnvoyFilterResource,
				getNamespace: func(obj *unstructured.Unstructured) (string, error) {
					return SfproxyNamespace, nil
				},
				getName: func(svc *unstructured.Unstructured) (string, error) {
					return getResourceNameFromMetadata(svc, "header-casing"), nil
				},
			},
			// sidecar
			{
				gvr:          constants.SidecarResource,
				getNamespace: defaultGetNamespace,
				getName: func(svc *unstructured.Unstructured) (string, error) {
					return getResourceNameFromMetadata(svc, ""), nil
				},
			},
			// sidecar - bootstrap
			{
				gvr:          constants.SidecarResource,
				getNamespace: defaultGetNamespace,
				getName: func(svc *unstructured.Unstructured) (string, error) {
					if cell, found := svc.GetLabels()["p_cell"]; found {
						return cell + "-casam-bootstrap", nil
					} else {
						return "undefined", nil
					}
				},
			},
		},
	}

	// Core-on-sam/dapp has the same objects
	result[CoreOnSamDapp] = result[CoreOnSamCoreapp]

	// Symlinks
	result[Caas] = result[DefaultRedisSfdcnet]
	result[CaasPc] = result[DefaultExternalService]

	return result
}

func defaultGetName(objName string, suffix string) string {
	return fmt.Sprintf("%s-%s", objName, suffix)
}

func defaultGetNamespace(object *unstructured.Unstructured) (string, error) {
	return object.GetNamespace(), nil
}

func (ci *configInfo) getConfigObjectNameAndNamespace(object *unstructured.Unstructured) (string, string, error) {
	name, err := ci.getName(object)
	if err != nil {
		return "", "", err
	}
	namespace, err := ci.getNamespace(object)
	if err != nil {
		return "", "", err
	}
	return name, namespace, nil
}

func getEnvoyFilterName(object *unstructured.Unstructured, filterSuffix string, filterNamespace string) string {
	if filterNamespace == MeshControlPlaneNamespace || filterSuffix == MaxConnDurationFilterSuffix {
		return object.GetName() + "--" + object.GetNamespace() + "--" + filterSuffix
	}
	return object.GetName() + "-" + filterSuffix
}

func getResourceNameFromMetadata(object *unstructured.Unstructured, suffix string) string {
	pCell, hasCell := object.GetLabels()["p_cell"]
	pServiceName, hasServiceName := object.GetLabels()["p_servicename"]
	msi, hasMsi := object.GetLabels()["mesh_service_instance"]
	if hasMsi {
		pServiceName += "-" + msi
	}
	suffixToUse := "-" + suffix
	if len(suffix) == 0 {
		return object.GetName()
	}

	if hasCell {
		return pCell + "-" + pServiceName + suffixToUse
	} else if hasServiceName {
		return pServiceName + suffixToUse
	} else {
		return object.GetName() + suffixToUse
	}
}

func getZkFilterName(service *unstructured.Unstructured) string {
	var filterName, meshServiceInstanceSuffix string
	filterSuffix := service.GetName() + "-" + ZkFilterSuffix
	labels := service.GetLabels()
	if meshServiceInstance, ok := labels["mesh_service_instance"]; ok {
		meshServiceInstanceSuffix = "-" + meshServiceInstance
	}
	pServiceName := labels["p_servicename"]
	if pCell, ok := labels["p_cell"]; ok {
		filterName = pCell + "-" + pServiceName + meshServiceInstanceSuffix + "-" + filterSuffix
	} else if pServiceName != "" {
		filterName = pServiceName + meshServiceInstanceSuffix + "-" + filterSuffix
	} else {
		filterName = service.GetName() + "-" + filterSuffix
	}
	return filterName
}

func getRedisFilterNamespace(serviceEntry *unstructured.Unstructured) (string, error) {
	defaultNamespace := serviceEntry.GetNamespace()
	mcpNamespace := "mesh-control-plane"
	annotations := serviceEntry.GetAnnotations()
	if annotations == nil {
		return defaultNamespace, nil
	}
	extSvcConfig := map[string]string{}
	val, exists := annotations[ExternalServiceConfigAnnotation]
	if exists {
		err := json.Unmarshal([]byte(val), &extSvcConfig)
		if err != nil {
			return "", err
		}
	}
	isFdLevelVal := extSvcConfig[isFdLevel]
	if isFdLevelVal == "true" {
		return mcpNamespace, nil
	}
	return defaultNamespace, nil
}

func getKey(obj *unstructured.Unstructured) string {
	return fmt.Sprintf("%s/%s/%s/%s", obj.GetAPIVersion(), obj.GetKind(), obj.GetNamespace(), obj.GetName())
}

func isTemplateDeprecatedByCopilot(templateType string) bool {
	if EnableCopilotToMopTransition {
		templateType = strings.Replace(templateType, constants.TemplateKeyDelimiter, constants.TemplateNameSeparator, -1)
		templateTypeOwnedByCopilot := common.SliceContains(TransitionTemplatesOwnedByCopilot, templateType)
		return !templateTypeOwnedByCopilot
	}
	return false
}

// shouldHaveProtocolFilter - checks, whether protocol filter should be present for service
func shouldHaveProtocolFilter(object *unstructured.Unstructured) bool {
	ports, found, _ := unstructured.NestedSlice(object.Object, "spec", "ports")
	if found {
		for _, port := range ports {
			portObject, ok := port.(map[string]interface{})
			if ok {
				portName, portNameFound, _ := unstructured.NestedString(portObject, "name")
				if portNameFound && strings.HasPrefix(portName, "thrift") {
					return true
				}
			}
		}
	}
	return false
}

func findConfigObjectByName(config []*unstructured.Unstructured, name string) (int, *unstructured.Unstructured) {
	for i, obj := range config {
		if obj.GetName() == name {
			return i, obj
		}
	}

	return -1, nil
}

// The config generated from templates won't have rollouts-pod-template-hash label.
// Hence, even after deleting the label here, the DR created in the cluster should still have the label owing to upsert.
func removeArgoUpdatedSubsetLabel(obj *unstructured.Unstructured) {
	annotations := obj.GetAnnotations()
	isUpdatedByArgo := false
	if annotations != nil {
		_, isUpdatedByArgo = obj.GetAnnotations()["argo-rollouts.argoproj.io/managed-by-rollouts"]
	}
	if isUpdatedByArgo && obj.GetKind() == "DestinationRule" {
		spec := obj.Object["spec"].(map[string]interface{})
		subsets := spec["subsets"]
		if subsets == nil {
			return
		}
		for _, subset := range subsets.([]interface{}) {
			s := subset.(map[string]interface{})
			lbl := s["labels"].(map[string]interface{})
			delete(lbl, "rollouts-pod-template-hash")
		}
	}
}

// for external services, remove http route names if present in copilot generated VS
// some of the ext svc rendered via copilot uses default/default which includes http route name whereas
// MOP generated VS uses default/external-service template which don't contain http route name
func preProcessExtSvcVs(results []*unstructured.Unstructured) error {
	for _, vs := range results {
		httpRoutes, found, err := unstructured.NestedSlice(vs.Object, "spec", "http")
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		for _, httpRoute := range httpRoutes {
			var httpRouteAsMap = httpRoute.(map[string]interface{})
			unstructured.RemoveNestedField(httpRouteAsMap, "name")
		}
		err = unstructured.SetNestedSlice(vs.Object, httpRoutes, "spec", "http")
		if err != nil {
			return nil
		}
	}
	return nil
}

func getLoggableKeys(configIndex map[string]*unstructured.Unstructured) string {
	var keys []string
	for key := range configIndex {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func shouldHaveAdditionalServiceTemplates(service *unstructured.Unstructured, additionalTemplateType string) (bool, []string, error) {
	annotations := service.GetAnnotations()
	if annotations == nil {
		return false, nil, nil
	}
	serviceTemplates := make(map[string][]string)
	val, exists := annotations[ServiceTemplateAnnotation]
	if exists {
		err := json.Unmarshal([]byte(val), &serviceTemplates)
		if err != nil {
			return false, nil, err
		}
	}
	if filterList, ok := serviceTemplates[strings.Replace(additionalTemplateType, "_", "/", -1)]; ok && len(filterList) > 0 {
		return true, filterList, nil
	}
	return false, nil, nil
}

func getResourceName(service *unstructured.Unstructured, suffix string, prefix string) string {
	if suffix == "" {
		return service.GetName()
	}
	var filterName, meshServiceInstanceSuffix string
	labels := service.GetLabels()
	if meshServiceInstance, ok := labels["mesh_service_instance"]; ok {
		meshServiceInstanceSuffix = "-" + meshServiceInstance
	}
	pServiceName := labels["p_servicename"]
	if pCell, ok := labels["p_cell"]; ok {
		filterName = pCell + "-" + pServiceName + meshServiceInstanceSuffix + "-" + suffix
	} else if pServiceName != "" {
		filterName = pServiceName + meshServiceInstanceSuffix + "-" + suffix
	} else {
		filterName = service.GetName() + "-" + suffix
	}
	if prefix != "" {
		return prefix + "-" + filterName
	}
	return filterName
}

func TakenOverByMeshOp(labels map[string]string) bool {
	var managedByMop bool
	var managedByCopilot bool
	if val, ok := labels[constants.MeshIoManagedByLabel]; ok {
		if val == "mesh-operator" {
			managedByMop = true
		}
	}
	if val, ok := labels[ManagedByCopilotLabel]; ok {
		if val == ManagedByCopilotValue {
			managedByCopilot = true
		}
	}
	return managedByMop && !managedByCopilot
}
