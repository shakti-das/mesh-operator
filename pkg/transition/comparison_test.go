package transition

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	"github.com/istio-ecosystem/mesh-operator/pkg/templating"

	"github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"

	metricstesting "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics/testing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"

	"istio.io/api/networking/v1alpha3"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"

	commonmetrics "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	wrapperspb "google.golang.org/protobuf/types/known/wrapperspb"
	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	istiosecurityv1beta1 "istio.io/client-go/pkg/apis/security/v1beta1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	objectName        = "test-service"
	pCell             = "cell01"
	pServiceNameLabel = "test-svc"
	objectNsName      = "test-ns"
	clusterName       = "primary"
	pServiceName      = "zookeeper"
)

var (
	baseTemplateType = "default/default"

	serviceTypedObject = &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.ServiceKind.Kind,
			APIVersion: constants.ServiceResource.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            objectName,
			Namespace:       objectNsName,
			ResourceVersion: "1",
			Annotations:     map[string]string{},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{Name: "grpc", Port: 7443}, {Name: "http", Port: 80},
			},
		},
		Status: v1.ServiceStatus{},
	}

	serviceUsingZkTemplate = &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.ServiceKind.Kind,
			APIVersion: constants.ServiceResource.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            objectName,
			Namespace:       objectNsName,
			ResourceVersion: "1",
			Annotations: map[string]string{
				"routing.mesh.io/template": DefaultZkSet,
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{Name: "grpc", Port: 7443}, {Name: "http", Port: 80},
			},
		},
		Status: v1.ServiceStatus{},
	}

	serviceUsingZkTemplateWithLabel = &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.ServiceKind.Kind,
			APIVersion: constants.ServiceResource.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            objectName,
			Namespace:       objectNsName,
			ResourceVersion: "1",
			Annotations: map[string]string{
				"routing.mesh.io/template": DefaultZkSet,
			},
			Labels: map[string]string{
				"p_servicename": pServiceName,
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{Name: "grpc", Port: 7443}, {Name: "http", Port: 80},
			},
		},
		Status: v1.ServiceStatus{},
	}

	serviceUsingRedisOpsTimeout = &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.ServiceKind.Kind,
			APIVersion: constants.ServiceResource.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            objectName,
			Namespace:       objectNsName,
			ResourceVersion: "1",
			Annotations: map[string]string{
				"routing.mesh.io/template": DefaultRedisSet,
				RedisOpsTimeoutAnnotation:  "100ms",
			},
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{Name: "grpc", Port: 7443}, {Name: "http", Port: 80},
			},
		},
		Status: v1.ServiceStatus{},
	}

	serviceWithThriftProtocolPort = &v1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.ServiceKind.Kind,
			APIVersion: constants.ServiceResource.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      objectName,
			Namespace: objectNsName,
		},
		Spec: v1.ServiceSpec{
			Ports: []v1.ServicePort{
				{Name: "grpc", Port: 7443}, {Name: "thrift-port", Port: 80},
			},
		},
	}

	externalServiceEntry = createServiceEntry(objectNsName, objectName, nil, map[string]string{"mesh.io.example.com/managed-by": "copilot", "p_servicename": pServiceNameLabel})
	extSeWithWithFdLevel = createServiceEntry(objectNsName, objectName, map[string]string{ExternalServiceConfigAnnotation: "{ \"is_fd_level\" : \"true\"}"}, map[string]string{"mesh.io.example.com/managed-by": "copilot", "p_servicename": pServiceNameLabel})

	serviceObject, _                                  = kube.ObjectToUnstructured(serviceTypedObject)
	externalSeObject, _                               = kube.ObjectToUnstructured(externalServiceEntry)
	externalSeObjectWithFdLevel, _                    = kube.ObjectToUnstructured(extSeWithWithFdLevel)
	serviceObjectZK, _                                = kube.ObjectToUnstructured(serviceUsingZkTemplate)
	serviceObjectZKWithLabel, _                       = kube.ObjectToUnstructured(serviceUsingZkTemplateWithLabel)
	serviceObjectRedisOpsTimeout, _                   = kube.ObjectToUnstructured(serviceUsingRedisOpsTimeout)
	serviceObjectWithThriftProtocol, _                = kube.ObjectToUnstructured(serviceWithThriftProtocolPort)
	serviceObjectWithGenericServiceAuthorityFilter    = kube_test.NewServiceBuilder(objectName, objectNsName).SetAnnotations(map[string]string{ServiceTemplateAnnotation: "{\"generic/service-filters\":[\"authority\"]}"}).GetServiceAsUnstructuredObject()
	serviceObjectWithAuthorityAndWasmFilter           = kube_test.NewServiceBuilder(objectName, objectNsName).SetAnnotations(map[string]string{ServiceTemplateAnnotation: "{\"generic/service-filters\":[\"authority\", \"wasm\"]}"}).GetServiceAsUnstructuredObject()
	serviceObjectWithGenericServiceSqlServerFilter    = kube_test.NewServiceBuilder(objectName, objectNsName).SetAnnotations(map[string]string{ServiceTemplateAnnotation: "{\"generic/service-filters\":[\"sql-server\"]}"}).GetServiceAsUnstructuredObject()
	serviceObjectWithGenericJwtFilter                 = kube_test.NewServiceBuilder(objectName, objectNsName).SetAnnotations(map[string]string{ServiceTemplateAnnotation: "{\"generic/service-filters\":[\"jwt\"]}"}).GetServiceAsUnstructuredObject()
	serviceObjectWithGenericRateLimitFilter           = kube_test.NewServiceBuilder(objectName, objectNsName).SetAnnotations(map[string]string{ServiceTemplateAnnotation: "{\"generic/service-filters\":[\"ratelimit\"]}"}).GetServiceAsUnstructuredObject()
	serviceObjectWithGenericPassthroughFilter         = kube_test.NewServiceBuilder(objectName, objectNsName).SetAnnotations(map[string]string{ServiceTemplateAnnotation: "{\"generic/service-filters\":[\"passthrough\"]}"}).GetServiceAsUnstructuredObject()
	svcObjectWithThriftPortAndServiceFilterAnnotation = kube_test.NewServiceBuilder(objectName, objectNsName).SetPorts([]v1.ServicePort{{Name: "grpc", Port: 7443}, {Name: "thrift-port", Port: 80}}).SetAnnotations(map[string]string{ServiceTemplateAnnotation: "{\"generic/service-filters\":[\"authority\"]}"}).GetServiceAsUnstructuredObject()
	serviceObjectWithCell                             = kube_test.NewServiceBuilder(objectName, objectNsName).SetPorts([]v1.ServicePort{{Name: "grpc", Port: 7443}}).SetLabels(map[string]string{"p_cell": pCell, "p_servicename": objectName}).GetServiceAsUnstructuredObject()
	rlsExcludedObjectWithCell                         = kube_test.NewServiceBuilder(objectName, "service-mesh").SetPorts([]v1.ServicePort{{Name: "grpc", Port: 7443}}).SetLabels(map[string]string{"p_cell": pCell, "p_servicename": objectName}).GetServiceAsUnstructuredObject()
	lbTypeService                                     = kube_test.NewServiceBuilder(objectName, objectName).SetPorts([]v1.ServicePort{{Name: "grpc", Port: 7443}}).SetLabels(map[string]string{"p_cell": pCell, "p_servicename": objectName}).SetType(v1.ServiceTypeLoadBalancer).GetServiceAsUnstructuredObject()
	ipExternalService                                 = kube_test.NewServiceBuilder(objectName, objectName).SetPorts([]v1.ServicePort{{Name: "grpc", Port: 7443}}).SetLabels(map[string]string{"p_cell": pCell, "p_servicename": objectName}).SetExternalName("8.8.8.8").GetServiceAsUnstructuredObject()

	copilotMainVirtualService = setLabels(
		createCopilotMainVirtualService(objectName, true, 80, 7443),
		map[string]string{
			ManagedByCopilotLabel: "istio-copilot",
		},
	)

	rootVirtualServiceWithoutHttpRouteName = setLabels(
		createCopilotMainVirtualService(objectName, false, 80, 7443),
		map[string]string{
			ManagedByCopilotLabel: "istio-copilot",
		},
	)

	copilotVirtualServiceGrpc = setLabels(
		createVirtualServiceWithDefaultSpec(buildDelegateName(objectName, 7443), 7443),
		map[string]string{
			ManagedByCopilotLabel: "istio-copilot",
		},
	)
	copilotVirtualServiceHttp = setLabels(
		createVirtualServiceWithDefaultSpec(buildDelegateName(objectName, 80), 80),
		map[string]string{
			ManagedByCopilotLabel: "istio-copilot",
		},
	)
	copilotDestinationRule = createCopilotDestinationRule()

	mopVirtualServiceGrpc = createVirtualServiceWithDefaultSpec(objectName, 7443)

	statefulServiceEntry = createServiceEntry(objectNsName, "test-sts-0", nil, map[string]string{"mesh.io.example.com/managed-by": "copilot"})
	statefulVs           = createVirtualServiceWithDefaultSpec("test-sts-0", 123)
	statefulDr           = NewDestinationRuleBuilder("test-sts-0", objectNsName).
				SetLabels(map[string]string{"mesh.io.example.com/managed-by": "copilot"}).Build()

	defaultCopilotGeneratedObjects = []runtime.Object{
		copilotMainVirtualService, copilotVirtualServiceGrpc, copilotVirtualServiceHttp, copilotDestinationRule,
	}
	defaultMopGeneratedObjects = []runtime.Object{
		copilotMainVirtualService, copilotVirtualServiceGrpc, copilotVirtualServiceHttp, copilotDestinationRule,
	}

	redisFilter                = createEnvoyFilter(objectName+"-"+RedisFilterSuffix, objectNsName)
	redisFilterInMcpNs         = createEnvoyFilter(objectName+"--"+objectNsName+"--"+RedisFilterSuffix, MeshControlPlaneNamespace)
	maxConnectionFilterInMcpNs = createEnvoyFilter(objectName+"--"+objectNsName+"--"+MaxConnDurationFilterSuffix, MeshControlPlaneNamespace)
	starttlsFilter             = createEnvoyFilter(objectName+"-"+StarttlsFilterSuffix, objectNsName)
	zkFilter                   = createEnvoyFilter(objectName+"-"+objectName+"-"+ZkFilterSuffix, objectNsName)
	zkFilterWithPServiceName   = createEnvoyFilter(pServiceName+"-"+objectName+"-"+ZkFilterSuffix, objectNsName)
	redisOpsTimeoutFilter      = createEnvoyFilter(objectName+"-"+RedisOpsTimeoutFilterSuffix, RedisOpsTimeoutFilterNamespace)
	thriftFilter               = createEnvoyFilter(objectName+"-"+ThriftFilterSuffix, objectNsName)
	genericAuthorityFilter     = createEnvoyFilter(objectName+"-"+GenericAuthorityFilterSuffix, objectNsName)
	genericJwtFilter           = createRequestAuthenticationResource(objectName, objectNsName)
	genericRateLimitFilter     = createEnvoyFilter(objectName+"-"+GenericRateLimitFilterSuffix, objectNsName)
	genericPassthroughFilter   = createEnvoyFilter(objectName+"-"+GenericPassthroughFilterSuffix, objectNsName)
	wasmFilter                 = createEnvoyFilter(objectName+"-"+"wasm", objectNsName)
	rateLimitingFilter         = createEnvoyFilter(pCell+"-"+objectName+"-"+RatelimitFilterSuffix, objectNsName)

	sqlServerServiceEntry                      = createServiceEntry(objectNsName, objectName, map[string]string{"routing.mesh.io/enabled": "false"}, nil)
	sqlServerSeWithoutRoutingEnabledAnnotation = createServiceEntry(objectNsName, objectName, nil, nil)
)

func TestMeshConfigComparer(t *testing.T) {
	testCases := []struct {
		name                              string
		object                            *unstructured.Unstructured
		metadata                          map[string]string
		copilotGeneratedObjects           []runtime.Object
		mopGeneratedObjects               []*unstructured.Unstructured
		templateType                      string
		enableCopilotToMopTransition      bool
		transitionTemplatesOwnedByCopilot []string
		additionalObjects                 []*unstructured.Unstructured
		transitionDisabled                bool
		expectedMetrics                   []TransitionMetrics
		expectedSuccess                   bool
		expectedConfig                    []*unstructured.Unstructured
	}{
		{
			name:                              "Test happy path",
			object:                            serviceObject,
			templateType:                      DefaultDefault,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/default"},
			copilotGeneratedObjects:           defaultCopilotGeneratedObjects,
			mopGeneratedObjects:               convertToUnstructured(t, defaultMopGeneratedObjects...),
			expectedConfig:                    convertToUnstructured(t, defaultMopGeneratedObjects...),
			expectedSuccess:                   true,
		},
		{
			name:                              "Test mop missing configs",
			object:                            serviceObject,
			templateType:                      DefaultDefault,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/default"},
			copilotGeneratedObjects:           defaultCopilotGeneratedObjects,
			mopGeneratedObjects:               convertToUnstructured(t, copilotVirtualServiceGrpc, copilotDestinationRule),
			expectedConfig:                    convertToUnstructured(t, copilotVirtualServiceGrpc, copilotDestinationRule),
			expectedMetrics:                   []TransitionMetrics{MopTransitionMissingConfigs},
			expectedSuccess:                   false,
		},
		{
			name:                              "Test only one delegate found",
			object:                            serviceObject,
			templateType:                      DefaultDefault,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/default"},
			copilotGeneratedObjects: []runtime.Object{
				copilotDestinationRule,
				copilotMainVirtualService,
				copilotVirtualServiceGrpc,
				createVirtualServiceWithDefaultSpec(buildDelegateName("other-delegate-name", 80), 80),
			},
			mopGeneratedObjects: convertToUnstructured(t, copilotMainVirtualService, copilotVirtualServiceGrpc, copilotVirtualServiceHttp, copilotDestinationRule),
			expectedConfig:      convertToUnstructured(t, copilotMainVirtualService, copilotVirtualServiceGrpc, copilotDestinationRule),
			expectedMetrics:     []TransitionMetrics{MopTransitionExtraConfigs},
			expectedSuccess:     false,
		},
		{
			name:                              "Test only main vs found",
			object:                            serviceObject,
			templateType:                      DefaultDefault,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/default"},
			copilotGeneratedObjects: []runtime.Object{
				copilotMainVirtualService,
			},
			mopGeneratedObjects: convertToUnstructured(t, copilotMainVirtualService, copilotVirtualServiceGrpc, copilotVirtualServiceHttp, copilotDestinationRule),
			expectedConfig:      convertToUnstructured(t, copilotMainVirtualService),
			expectedMetrics:     []TransitionMetrics{MopTransitionExtraConfigs},
			expectedSuccess:     false,
		},
		{
			name:                              "Test different virtualservice spec from copilot",
			object:                            serviceObject,
			templateType:                      DefaultDefault,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/default"},
			copilotGeneratedObjects:           defaultCopilotGeneratedObjects,
			mopGeneratedObjects: convertToUnstructured(t,
				copilotVirtualServiceGrpc,
				func() *istiov1alpha3.VirtualService {
					vs := newLabeledVirtualService(buildDelegateName(objectName, 80))
					vs.Spec.Hosts = []string{
						"test-service1.test-ns.svc.cluster.local",
						"test-service1.test-ns.svc.mesh.io",
					}
					vs.Spec.Gateways = []string{"mesh"}
					vs.Spec.Http = []*v1alpha3.HTTPRoute{{Match: []*v1alpha3.HTTPMatchRequest{{Port: 80}}}}
					return vs
				}(),
				copilotDestinationRule),
			expectedConfig:  convertToUnstructured(t, copilotVirtualServiceGrpc, copilotDestinationRule),
			expectedMetrics: []TransitionMetrics{MopTransitionDifferentConfigSpec},
			expectedSuccess: false,
		},
		{
			name:                              "Test different destinationrule spec from copilot",
			object:                            serviceObject,
			templateType:                      DefaultDefault,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/default"},
			copilotGeneratedObjects:           defaultCopilotGeneratedObjects,
			mopGeneratedObjects: convertToUnstructured(t,
				copilotVirtualServiceGrpc,
				copilotVirtualServiceHttp,
				NewDestinationRuleBuilder(objectName, objectNsName).SetTrafficPolicy(
					&v1alpha3.TrafficPolicy{
						LoadBalancer: &v1alpha3.LoadBalancerSettings{
							LbPolicy: &v1alpha3.LoadBalancerSettings_Simple{
								Simple: v1alpha3.LoadBalancerSettings_LEAST_CONN,
							},
						},
					}).Build()),
			expectedConfig:  convertToUnstructured(t, copilotVirtualServiceGrpc, copilotVirtualServiceHttp),
			expectedMetrics: []TransitionMetrics{MopTransitionDifferentConfigSpec},
			expectedSuccess: false,
		},
		{
			name:                              "Test unknown mesh-operator config",
			object:                            serviceObject,
			templateType:                      DefaultDefault,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/default"},
			copilotGeneratedObjects:           defaultCopilotGeneratedObjects,
			mopGeneratedObjects: convertToUnstructured(t,
				copilotVirtualServiceGrpc,
				newLabeledVirtualService("other-vs"),
				copilotDestinationRule),
			expectedConfig:  convertToUnstructured(t, copilotVirtualServiceGrpc, copilotDestinationRule),
			expectedMetrics: []TransitionMetrics{MopTransitionUnknownConfigGenerated},
			expectedSuccess: false,
		},
		{
			name:                              "Test deprecated copilot template config",
			object:                            serviceObject,
			templateType:                      DefaultDefault,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp"},
			copilotGeneratedObjects:           []runtime.Object{},
			mopGeneratedObjects:               convertToUnstructured(t, mopVirtualServiceGrpc),
			transitionDisabled:                true,
			expectedMetrics: []TransitionMetrics{
				MopTransitionDeprecatedCopilotTemplate},
			expectedSuccess: true,
			expectedConfig:  convertToUnstructured(t, mopVirtualServiceGrpc),
		},
		{
			name:                              "Test mismatched version label",
			object:                            serviceObject,
			templateType:                      DefaultDefault,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/default"},
			copilotGeneratedObjects: []runtime.Object{
				setLabels(
					copilotMainVirtualService,
					map[string]string{
						ManagedByCopilotLabel: "istio-copilot",
						VersionLabel:          "v100",
					},
				),
			},
			mopGeneratedObjects: convertToUnstructured(t,
				setLabels(
					copilotMainVirtualService,
					map[string]string{
						ManagedByCopilotLabel: "v200",
					},
				),
			),
			expectedMetrics: []TransitionMetrics{MopTransitionDifferentConfigSpec},
			expectedSuccess: false,
			expectedConfig:  []*unstructured.Unstructured{},
		},
		{
			name:                              "Test mismatched type label",
			object:                            serviceObject,
			templateType:                      DefaultDefault,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/default"},
			copilotGeneratedObjects: []runtime.Object{
				setLabels(
					createVirtualServiceWithDefaultSpec(objectName, 7443),
					map[string]string{
						ManagedByCopilotLabel: "istio-copilot",
						TypeLabel:             "vanilla",
					},
				),
			},
			mopGeneratedObjects: convertToUnstructured(t,
				setLabels(
					createVirtualServiceWithDefaultSpec(objectName, 7443),
					map[string]string{
						TypeLabel: "chocolate",
					},
				),
			),
			expectedMetrics: []TransitionMetrics{MopTransitionDifferentConfigSpec},
			expectedSuccess: false,
			expectedConfig:  []*unstructured.Unstructured{},
		},
		{
			name:                              "Test mesh.io.example.com/* and routing.mesh.io.example.com/* labels propagated",
			object:                            serviceObject,
			templateType:                      DefaultDefault,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/default"},
			copilotGeneratedObjects: []runtime.Object{
				setLabels(copilotMainVirtualService,
					map[string]string{
						"mesh.io.example.com/managed-by":            "istio-copilot",
						"mesh.io.example.com/some-other-label":      "other-value",
						"routing.mesh.io.example.com/multi-cluster": "true",
					}),
			},
			mopGeneratedObjects: convertToUnstructured(t, setLabels(copilotMainVirtualService, map[string]string{})),
			expectedSuccess:     true,
			expectedConfig: convertToUnstructured(t, setLabels(copilotMainVirtualService,
				map[string]string{
					"mesh.io.example.com/managed-by":            "istio-copilot",
					"mesh.io.example.com/some-other-label":      "other-value",
					"routing.mesh.io.example.com/multi-cluster": "true",
				})),
		},
		{
			name:                              "Test stateful compare",
			object:                            serviceObject,
			templateType:                      DefaultStatefulSet,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/stateful"},
			metadata:                          map[string]string{"statefulSetReplicas": "1", "statefulSetReplicaName": "test-sts"},
			copilotGeneratedObjects:           []runtime.Object{copilotMainVirtualService, statefulVs, statefulDr, statefulServiceEntry},
			mopGeneratedObjects:               convertToUnstructured(t, copilotMainVirtualService, statefulVs, statefulDr, statefulServiceEntry),
			expectedSuccess:                   true,
			expectedConfig:                    convertToUnstructured(t, copilotMainVirtualService, statefulVs, statefulDr, statefulServiceEntry),
		},
		{
			name:                              "Test additionalObjects exclusion (Transition Success)",
			object:                            serviceObjectWithThriftProtocol,
			templateType:                      DefaultStatefulSet,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/stateful"},
			metadata:                          map[string]string{"statefulSetReplicas": "1", "statefulSetReplicaName": "test-sts"},
			copilotGeneratedObjects:           []runtime.Object{copilotMainVirtualService, statefulVs, statefulDr, statefulServiceEntry},
			mopGeneratedObjects:               convertToUnstructured(t, copilotMainVirtualService, statefulVs, statefulDr, statefulServiceEntry),
			additionalObjects:                 convertToUnstructured(t, thriftFilter),
			expectedSuccess:                   true,
			expectedConfig:                    convertToUnstructured(t, copilotMainVirtualService, statefulVs, statefulDr, statefulServiceEntry, thriftFilter),
		},
		{
			name:                              "Test stateful compare (emailinfra)",
			object:                            serviceObject,
			templateType:                      EmailinfraEaas,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "emailinfra/eaas"},
			metadata:                          map[string]string{"statefulSetReplicas": "1", "statefulSetReplicaName": "test-sts"},
			copilotGeneratedObjects:           []runtime.Object{copilotMainVirtualService, statefulVs, statefulDr, statefulServiceEntry},
			mopGeneratedObjects:               convertToUnstructured(t, copilotMainVirtualService, statefulVs, statefulDr, statefulServiceEntry),
			expectedSuccess:                   true,
			expectedConfig:                    convertToUnstructured(t, copilotMainVirtualService, statefulVs, statefulDr, statefulServiceEntry),
		},
		{
			name:                              "Test non matching SE",
			object:                            serviceObject,
			templateType:                      DefaultStatefulSet,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/stateful"},
			metadata:                          map[string]string{"statefulSetReplicas": "1"},
			copilotGeneratedObjects:           []runtime.Object{copilotMainVirtualService, statefulServiceEntry},
			mopGeneratedObjects:               convertToUnstructured(t, copilotMainVirtualService, createServiceEntry(objectNsName, "other-se", nil, map[string]string{"mesh.io.example.com/managed-by": "copilot"})),
			expectedSuccess:                   false,
			expectedConfig:                    convertToUnstructured(t, copilotMainVirtualService),
		},
		{
			name:                              "Test default_external-service template - remove HttpRouteName",
			object:                            externalSeObject,
			templateType:                      DefaultExternalService,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/external-service"},
			copilotGeneratedObjects:           []runtime.Object{copilotMainVirtualService, copilotDestinationRule, redisFilter, starttlsFilter},
			mopGeneratedObjects:               convertToUnstructured(t, rootVirtualServiceWithoutHttpRouteName, copilotDestinationRule, redisFilter, starttlsFilter),
			expectedSuccess:                   true,
			expectedConfig:                    convertToUnstructured(t, rootVirtualServiceWithoutHttpRouteName, copilotDestinationRule, redisFilter, starttlsFilter),
		},
		{
			name:                              "Test default_external-service template - MaxConnectionFilterLookupInMcpNamespace",
			object:                            externalSeObject,
			templateType:                      DefaultExternalService,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/external-service"},
			copilotGeneratedObjects:           []runtime.Object{copilotMainVirtualService, copilotDestinationRule, maxConnectionFilterInMcpNs},
			mopGeneratedObjects:               convertToUnstructured(t, rootVirtualServiceWithoutHttpRouteName, copilotDestinationRule, maxConnectionFilterInMcpNs),
			expectedSuccess:                   true,
			expectedConfig:                    convertToUnstructured(t, rootVirtualServiceWithoutHttpRouteName, copilotDestinationRule, maxConnectionFilterInMcpNs),
		},
		{
			name:                              "Test default_external-service template - RedisFilterLookupInMcpNamespace",
			object:                            externalSeObjectWithFdLevel,
			templateType:                      DefaultExternalService,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/external-service"},
			copilotGeneratedObjects:           []runtime.Object{copilotMainVirtualService, copilotDestinationRule, redisFilterInMcpNs},
			mopGeneratedObjects:               convertToUnstructured(t, rootVirtualServiceWithoutHttpRouteName, copilotDestinationRule, redisFilterInMcpNs),
			expectedSuccess:                   true,
			expectedConfig:                    convertToUnstructured(t, rootVirtualServiceWithoutHttpRouteName, copilotDestinationRule, redisFilterInMcpNs),
		},
		{
			name:                              "Test default_zookeeper template - ZkFilter",
			object:                            serviceObjectZK,
			templateType:                      DefaultZkSet,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/zookeeper"},
			copilotGeneratedObjects:           []runtime.Object{copilotMainVirtualService, copilotDestinationRule, zkFilter},
			mopGeneratedObjects:               convertToUnstructured(t, copilotMainVirtualService, copilotDestinationRule, zkFilter),
			expectedSuccess:                   true,
			expectedConfig:                    convertToUnstructured(t, copilotMainVirtualService, copilotDestinationRule, zkFilter),
		},
		{
			name:                              "Test default_zookeeper template - ZkFilter with p_servicename label",
			object:                            serviceObjectZKWithLabel,
			templateType:                      DefaultZkSet,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/zookeeper"},
			copilotGeneratedObjects:           []runtime.Object{copilotMainVirtualService, copilotDestinationRule, zkFilterWithPServiceName},
			mopGeneratedObjects:               convertToUnstructured(t, copilotMainVirtualService, copilotDestinationRule, zkFilterWithPServiceName),
			expectedSuccess:                   true,
			expectedConfig:                    convertToUnstructured(t, copilotMainVirtualService, copilotDestinationRule, zkFilterWithPServiceName),
		},
		{
			name:                              "Test default_redis template - redis ops timeout filter",
			object:                            serviceObjectRedisOpsTimeout,
			templateType:                      DefaultRedisSet,
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "default/redis"},
			copilotGeneratedObjects:           []runtime.Object{copilotMainVirtualService, copilotDestinationRule, redisOpsTimeoutFilter},
			mopGeneratedObjects:               convertToUnstructured(t, copilotMainVirtualService, copilotDestinationRule, redisOpsTimeoutFilter),
			expectedSuccess:                   true,
			expectedConfig:                    convertToUnstructured(t, copilotMainVirtualService, copilotDestinationRule, redisOpsTimeoutFilter),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			client := kube_test.NewKubeClientBuilder().
				AddDynamicClientObjects(tc.copilotGeneratedObjects...).
				Build()
			registry := prometheus.NewRegistry()
			comparer := NewMeshConfigComparer(
				zaptest.NewLogger(t).Sugar(),
				client,
				registry,
				clusterName,
			)

			metricsLabels := map[string]string{
				commonmetrics.ResourceNameLabel:       objectName,
				commonmetrics.TargetResourceNameLabel: objectName,
				commonmetrics.TargetKindLabel:         tc.object.GetKind(),
				commonmetrics.NamespaceLabel:          objectNsName,
				commonmetrics.ClusterLabel:            clusterName,
				commonmetrics.ResourceKind:            tc.object.GetKind(),
				commonmetrics.TemplateTypeLabel:       tc.templateType}

			mopGeneratedConfig := templating.UnflattenConfig(tc.mopGeneratedObjects, tc.templateType)

			if tc.additionalObjects != nil {
				mopGeneratedConfig.Config[tc.templateType+"_addon"] = tc.additionalObjects
			}

			enableCopilotToMopTransitionOriginalValue := EnableCopilotToMopTransition
			transitionTemplatesOwnedByCopilotOriginalValue := TransitionTemplatesOwnedByCopilot

			EnableCopilotToMopTransition = tc.enableCopilotToMopTransition
			TransitionTemplatesOwnedByCopilot = tc.transitionTemplatesOwnedByCopilot
			defer func() {
				TransitionTemplatesOwnedByCopilot = transitionTemplatesOwnedByCopilotOriginalValue
				EnableCopilotToMopTransition = enableCopilotToMopTransitionOriginalValue
			}()

			actualConfig, err := comparer.DiffWithCopilotConfig(mopGeneratedConfig, tc.object, tc.metadata)

			assert.Nil(t, err)

			// assert metrics
			for _, metric := range tc.expectedMetrics {
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, metric.GetName(),
					metricsLabels, 1)
			}

			assert.Equal(t, len(tc.expectedConfig), len(actualConfig.FlattenConfig()))
			assert.ElementsMatchf(t, tc.expectedConfig, actualConfig.FlattenConfig(), "")

			if tc.transitionDisabled {
				metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, MeshConfigTotal.GetName(), metricsLabels, 0)
			} else {
				metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, MeshConfigTotal.GetName(), metricsLabels, 1)
			}

			if tc.expectedSuccess {
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, MopTransitionSuccess.GetName(), metricsLabels, 1)
				metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, MeshConfigTransitionErrors.GetName(), metricsLabels, 0)
			} else {
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, MopTransitionSuccess.GetName(), metricsLabels, 0)
				metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, MeshConfigTransitionErrors.GetName(), metricsLabels, 1)
			}
		})
	}
}

func TestAddonConfigComparer(t *testing.T) {

	sqlServerServiceEntry.SetLabels(map[string]string{"mesh.io.example.com/managed-by": "istio-copilot"})

	nonMatchingThriftFilter := createEnvoyFilter(objectName+"-"+ThriftFilterSuffix, objectNsName)
	nonMatchingThriftFilter.Spec.ConfigPatches = []*v1alpha3.EnvoyFilter_EnvoyConfigObjectPatch{
		{ApplyTo: v1alpha3.EnvoyFilter_FILTER_CHAIN},
	}
	nonMatchingGenericAuthorityFilter := createEnvoyFilter(objectName+"-"+GenericAuthorityFilterSuffix, objectNsName)
	nonMatchingGenericAuthorityFilter.Spec.ConfigPatches = []*v1alpha3.EnvoyFilter_EnvoyConfigObjectPatch{
		{ApplyTo: v1alpha3.EnvoyFilter_FILTER_CHAIN},
	}
	nonMatchingRLSFilter := createEnvoyFilter(pCell+"-"+objectName+"-"+RatelimitFilterSuffix, objectNsName)
	nonMatchingRLSFilter.Spec.ConfigPatches = []*v1alpha3.EnvoyFilter_EnvoyConfigObjectPatch{
		{ApplyTo: v1alpha3.EnvoyFilter_FILTER_CHAIN},
	}
	unifiedEngagement := "unified-engagement"
	testCases := []struct {
		name                              string
		object                            *unstructured.Unstructured
		mopAddon                          []*unstructured.Unstructured
		objectsInCluster                  []runtime.Object
		enableCopilotToMopTransition      bool
		transitionTemplatesOwnedByCopilot []string
		expectedAddonConfig               []*unstructured.Unstructured
		expectedMetrics                   []TransitionMetrics
		metricTemplate                    string
		expectSuccess                     bool
		expectFailure                     bool
		fdType                            string
	}{
		{
			name:   "No addons",
			object: serviceObject,
		},
		{
			name:                "Unknown addons preserved",
			object:              serviceObject,
			mopAddon:            []*unstructured.Unstructured{objectToUnstructured(t, redisFilter)},
			expectedAddonConfig: []*unstructured.Unstructured{objectToUnstructured(t, redisFilter)},
		},
		{
			name:                              "Thrift: deprecated",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp"},
			object:                            serviceObjectWithThriftProtocol,
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, thriftFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{objectToUnstructured(t, thriftFilter)},
		},
		{
			name:                              "Thrift: no copilot filter",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "protocol/filters"},
			object:                            serviceObjectWithThriftProtocol,
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, thriftFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{},
			expectedMetrics:                   []TransitionMetrics{MopTransitionExtraConfigs},
			metricTemplate:                    ProtocolFilters,
			expectFailure:                     true,
		},
		{
			name:                              "Thrift: no mop filter",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "protocol/filters"},
			object:                            serviceObjectWithThriftProtocol,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, thriftFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{},
			expectedMetrics:                   []TransitionMetrics{MopTransitionMissingConfigs},
			metricTemplate:                    ProtocolFilters,
			expectFailure:                     true,
		},
		{
			name:                              "Thrift: no filters found, but should",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "protocol/filters"},
			object:                            serviceObjectWithThriftProtocol,
			objectsInCluster:                  []runtime.Object{},
			expectedAddonConfig:               []*unstructured.Unstructured{},
			expectedMetrics:                   []TransitionMetrics{MopTransitionMissingConfigs},
			metricTemplate:                    ProtocolFilters,
			expectFailure:                     true,
		},
		{
			name:                              "Thrift: filters don't match",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "protocol/filters"},
			object:                            serviceObjectWithThriftProtocol,
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, nonMatchingThriftFilter)},
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, thriftFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{},
			expectedMetrics:                   []TransitionMetrics{MopTransitionDifferentConfigSpec},
			metricTemplate:                    ProtocolFilters,
			expectFailure:                     true,
		},
		{
			name:                              "Thrift: filters match",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "protocol/filters"},
			object:                            serviceObjectWithThriftProtocol,
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, thriftFilter)},
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, thriftFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{objectToUnstructured(t, thriftFilter)},
			expectedMetrics:                   []TransitionMetrics{MopTransitionSuccess},
			metricTemplate:                    ProtocolFilters,
			expectSuccess:                     true,
		},
		{
			name:                              "Thrift: unrelated filters preserved",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "protocol/filters"},
			object:                            serviceObjectWithThriftProtocol,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, thriftFilter)},
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, zkFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{objectToUnstructured(t, zkFilter)},
			expectedMetrics:                   []TransitionMetrics{MopTransitionMissingConfigs},
			metricTemplate:                    ProtocolFilters,
			expectFailure:                     true,
		},
		{
			name:                              "GenericServiceFilter: ExtraConfig",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "generic/service-filters"},
			object:                            serviceObjectWithGenericServiceAuthorityFilter,
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, genericAuthorityFilter)},
			expectedMetrics:                   []TransitionMetrics{MopTransitionExtraConfigs},
			metricTemplate:                    GenericServiceFilters,
			expectFailure:                     true,
		},
		{
			name:                              "GenericServiceFilter - svc contains thrift port along with service template annotation ",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "generic/service-filters"},
			object:                            svcObjectWithThriftPortAndServiceFilterAnnotation,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, thriftFilter)},
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, genericAuthorityFilter), objectToUnstructured(t, thriftFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{objectToUnstructured(t, thriftFilter)},
			expectedMetrics:                   []TransitionMetrics{MopTransitionExtraConfigs},
			metricTemplate:                    GenericServiceFilters,
			expectFailure:                     true,
		},
		{
			name:                              "GenericServiceFilter: MissingConfig",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "generic/service-filters", "protocol/filters"},
			object:                            svcObjectWithThriftPortAndServiceFilterAnnotation,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, genericAuthorityFilter)},
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, thriftFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{},
			expectedMetrics:                   []TransitionMetrics{MopTransitionMissingConfigs},
			metricTemplate:                    GenericServiceFilters,
			expectFailure:                     true,
		},
		{
			name:                              "GenericServiceFilter: DifferentSpec",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "generic/service-filters"},
			object:                            svcObjectWithThriftPortAndServiceFilterAnnotation,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, nonMatchingGenericAuthorityFilter)},
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, genericAuthorityFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{},
			expectedMetrics:                   []TransitionMetrics{MopTransitionDifferentConfigSpec},
			metricTemplate:                    GenericServiceFilters,
			expectFailure:                     true,
		},
		{
			name:                              "GenericServiceFilter: TransitionSuccess",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "generic/service-filters"},
			object:                            serviceObjectWithGenericServiceAuthorityFilter,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, genericAuthorityFilter)},
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, genericAuthorityFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{objectToUnstructured(t, genericAuthorityFilter)},
			expectedMetrics:                   []TransitionMetrics{MopTransitionSuccess},
			metricTemplate:                    GenericServiceFilters,
			expectSuccess:                     true,
		},
		{
			name:                              "GenericServiceFilter - Skip wasm takeover",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "generic/service-filters"},
			object:                            serviceObjectWithAuthorityAndWasmFilter,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, genericAuthorityFilter), objectToUnstructured(t, wasmFilter)},
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, genericAuthorityFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{objectToUnstructured(t, genericAuthorityFilter)},
			expectedMetrics:                   []TransitionMetrics{MopTransitionSuccess},
			metricTemplate:                    GenericServiceFilters,
			expectSuccess:                     true,
		},
		{
			name:                              "GenericServiceFilter - sql-server Transition Success",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "generic/service-filters"},
			object:                            serviceObjectWithGenericServiceSqlServerFilter,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, sqlServerServiceEntry)},
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, sqlServerServiceEntry)},
			expectedAddonConfig:               []*unstructured.Unstructured{objectToUnstructured(t, sqlServerServiceEntry)},
			expectedMetrics:                   []TransitionMetrics{MopTransitionSuccess},
			metricTemplate:                    GenericServiceFilters,
			expectSuccess:                     true,
		},
		{
			name:                              "GenericServiceFilter - Special case of sql-server resource, Extra config",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "generic/service-filters"},
			object:                            serviceObjectWithGenericServiceSqlServerFilter,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, sqlServerSeWithoutRoutingEnabledAnnotation)},
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, sqlServerServiceEntry)},
			expectedMetrics:                   []TransitionMetrics{MopTransitionExtraConfigs},
			metricTemplate:                    GenericServiceFilters,
			expectFailure:                     true,
		},
		{
			name:                              "GenericServiceFilter - jwt Filter, Transition Success",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "generic/service-filters"},
			object:                            serviceObjectWithGenericJwtFilter,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, genericJwtFilter)},
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, genericJwtFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{objectToUnstructured(t, genericJwtFilter)},
			expectedMetrics:                   []TransitionMetrics{MopTransitionSuccess},
			metricTemplate:                    GenericServiceFilters,
			expectSuccess:                     true,
		},
		{
			name:                              "GenericServiceFilter - ratelimit filter, Transition Success",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "generic/service-filters"},
			object:                            serviceObjectWithGenericRateLimitFilter,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, genericRateLimitFilter)},
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, genericRateLimitFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{objectToUnstructured(t, genericRateLimitFilter)},
			expectedMetrics:                   []TransitionMetrics{MopTransitionSuccess},
			metricTemplate:                    GenericServiceFilters,
			expectSuccess:                     true,
		},
		{
			name:                              "GenericServiceFilter - passthrough filter, Transition Success",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "generic/service-filters"},
			object:                            serviceObjectWithGenericPassthroughFilter,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, genericPassthroughFilter)},
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, genericPassthroughFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{objectToUnstructured(t, genericPassthroughFilter)},
			expectedMetrics:                   []TransitionMetrics{MopTransitionSuccess},
			metricTemplate:                    GenericServiceFilters,
			expectSuccess:                     true,
		},
		{
			name:                              "RateLimitingServiceFilter - unified-engagement fdType, Transition Success",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "unified-engagement/rate-limiting-service"},
			object:                            serviceObjectWithCell,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, rateLimitingFilter)},
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, rateLimitingFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{objectToUnstructured(t, rateLimitingFilter)},
			expectedMetrics:                   []TransitionMetrics{MopTransitionSuccess},
			metricTemplate:                    RateLimitingServiceFilters,
			fdType:                            unifiedEngagement,
			expectSuccess:                     true,
		},
		{
			name:                              "RateLimitingServiceFilter: MissingConfig",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "unified-engagement/rate-limiting-service"},
			object:                            serviceObjectWithCell,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, rateLimitingFilter)},
			mopAddon:                          []*unstructured.Unstructured{},
			expectedAddonConfig:               []*unstructured.Unstructured{},
			expectedMetrics:                   []TransitionMetrics{MopTransitionMissingConfigs},
			metricTemplate:                    RateLimitingServiceFilters,
			fdType:                            unifiedEngagement,
			expectFailure:                     true,
		},
		{
			name:                              "RateLimitingServiceFilter: DifferentSpec",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "unified-engagement/rate-limiting-service"},
			object:                            serviceObjectWithCell,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, nonMatchingRLSFilter)},
			mopAddon:                          []*unstructured.Unstructured{objectToUnstructured(t, rateLimitingFilter)},
			expectedAddonConfig:               []*unstructured.Unstructured{},
			expectedMetrics:                   []TransitionMetrics{MopTransitionDifferentConfigSpec},
			metricTemplate:                    RateLimitingServiceFilters,
			fdType:                            unifiedEngagement,
			expectFailure:                     true,
		},
		{
			name:                              "RatelimitingServiceFilter: Excluded namespace",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "unified-engagement/rate-limiting-service"},
			object:                            rlsExcludedObjectWithCell,
			objectsInCluster:                  []runtime.Object{},
			mopAddon:                          []*unstructured.Unstructured{},
			expectedAddonConfig:               []*unstructured.Unstructured{},
			fdType:                            unifiedEngagement,
			expectSuccess:                     false,
			expectFailure:                     false,
		},
		{
			name:                              "RatelimitingServiceFilter: No p_cell",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "unified-engagement/rate-limiting-service"},
			object:                            serviceObject,
			objectsInCluster:                  []runtime.Object{objectToUnstructured(t, rateLimitingFilter)},
			mopAddon:                          []*unstructured.Unstructured{},
			expectedAddonConfig:               []*unstructured.Unstructured{},
			fdType:                            unifiedEngagement,
			expectSuccess:                     false,
			expectFailure:                     false,
		},
		{
			name:                              "RatelimitingServiceFilter: LB type",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "unified-engagement/rate-limiting-service"},
			object:                            lbTypeService,
			objectsInCluster:                  []runtime.Object{},
			mopAddon:                          []*unstructured.Unstructured{},
			expectedAddonConfig:               []*unstructured.Unstructured{},
			fdType:                            unifiedEngagement,
			expectSuccess:                     false,
			expectFailure:                     false,
		},
		{
			name:                              "RatelimitingServiceFilter: external name IP",
			enableCopilotToMopTransition:      true,
			transitionTemplatesOwnedByCopilot: []string{"example-coreapp/coreapp", "unified-engagement/rate-limiting-service"},
			object:                            ipExternalService,
			objectsInCluster:                  []runtime.Object{},
			mopAddon:                          []*unstructured.Unstructured{},
			expectedAddonConfig:               []*unstructured.Unstructured{},
			fdType:                            unifiedEngagement,
			expectSuccess:                     false,
			expectFailure:                     false,
		},
	}

	// We add an arbitrary object to the config with expectation that DiffAdditionalTemplates doesn't make modifications to unrelated objects
	someOtherObject := &unstructured.Unstructured{}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {

			enableCopilotToMopTransitionOriginalValue := EnableCopilotToMopTransition
			transitionTemplatesOwnedByCopilotOriginalValue := TransitionTemplatesOwnedByCopilot

			EnableCopilotToMopTransition = tc.enableCopilotToMopTransition
			TransitionTemplatesOwnedByCopilot = tc.transitionTemplatesOwnedByCopilot
			defer func() {
				TransitionTemplatesOwnedByCopilot = transitionTemplatesOwnedByCopilotOriginalValue
				EnableCopilotToMopTransition = enableCopilotToMopTransitionOriginalValue
			}()

			features.FDType = tc.fdType
			client := kube_test.NewKubeClientBuilder().
				AddDynamicClientObjects(tc.objectsInCluster...).
				Build()
			registry := prometheus.NewRegistry()
			comparer := NewMeshConfigComparer(
				zaptest.NewLogger(t).Sugar(),
				client,
				registry,
				clusterName,
			)

			originalConfig := templating.GeneratedConfig{
				Config: map[string][]*unstructured.Unstructured{
					"some-template":            {someOtherObject},
					"some_base-template_addon": tc.mopAddon,
				},
				TemplateType: "some_base-template",
			}
			actualConfig := comparer.DiffAdditionalTemplates(originalConfig, tc.object)

			assert.Equal(t, "some_base-template", actualConfig.TemplateType)
			assert.ElementsMatchf(t, originalConfig.Config["some-template"], actualConfig.Config["some-template"], "")
			assert.ElementsMatchf(t, tc.expectedAddonConfig, actualConfig.Config["some_base-template_addon"], "")

			metricsLabels := map[string]string{
				commonmetrics.ResourceKind:            "Service",
				commonmetrics.ResourceNameLabel:       objectName,
				commonmetrics.TargetResourceNameLabel: objectName,
				commonmetrics.TargetKindLabel:         "Service",
				commonmetrics.NamespaceLabel:          objectNsName,
				commonmetrics.ClusterLabel:            clusterName,
				commonmetrics.TemplateTypeLabel:       tc.metricTemplate}

			for _, metric := range tc.expectedMetrics {
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, metric.GetName(), metricsLabels, 1)
			}

			if tc.expectSuccess {
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, MopTransitionSuccess.GetName(), metricsLabels, 1)
				metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, MeshConfigTransitionErrors.GetName(), metricsLabels, 0)
			}
			if tc.expectFailure {
				metricstesting.AssertEqualsCounterValueWithLabel(t, registry, MopTransitionSuccess.GetName(), metricsLabels, 0)
				metricstesting.AssertEqualsGaugeValueWithLabel(t, registry, MeshConfigTransitionErrors.GetName(), metricsLabels, 1)
			}

		})
	}
}

func TestOwnerReferences(t *testing.T) {
	var (
		mopOwnerReference = metav1.OwnerReference{
			APIVersion: v1alpha1.ApiVersion,
			Kind:       v1alpha1.MsmKind.Kind,
			Name:       objectName,
			UID:        "fake-uid-msm-owner-ref",
		}
		serviceOwnerReference = metav1.OwnerReference{
			APIVersion: constants.ServiceResource.GroupVersion().String(),
			Kind:       constants.ServiceKind.Kind,
			Name:       objectName,
			UID:        "fake-service-owner-ref-uid",
		}
		comparedObject = copilotMainVirtualService
	)
	testCases := []struct {
		name              string
		existingOwnerRefs []metav1.OwnerReference
		mopOwnerRefs      []metav1.OwnerReference
		expectedOwnerRefs []metav1.OwnerReference
	}{
		{
			name:              "Existing service owner ref for copilot",
			existingOwnerRefs: []metav1.OwnerReference{serviceOwnerReference},
			expectedOwnerRefs: []metav1.OwnerReference{serviceOwnerReference, mopOwnerReference},
		},
		{
			name:              "No existing owner refs for copilot",
			existingOwnerRefs: []metav1.OwnerReference{},
			expectedOwnerRefs: []metav1.OwnerReference{mopOwnerReference},
		},
		{
			name:              "Nil owner refs for copilot",
			existingOwnerRefs: nil,
			expectedOwnerRefs: []metav1.OwnerReference{mopOwnerReference},
		},
		{
			name:              "Pre-existing MSM owner ref",
			existingOwnerRefs: []metav1.OwnerReference{mopOwnerReference},
			expectedOwnerRefs: []metav1.OwnerReference{mopOwnerReference},
		},
		{
			name:              "Existing service and MSM owner ref",
			existingOwnerRefs: []metav1.OwnerReference{mopOwnerReference, serviceOwnerReference},
			expectedOwnerRefs: []metav1.OwnerReference{mopOwnerReference, serviceOwnerReference},
		},
		{
			name:              "Multiple MOP owner refs",
			existingOwnerRefs: []metav1.OwnerReference{serviceOwnerReference},
			mopOwnerRefs:      []metav1.OwnerReference{serviceOwnerReference, mopOwnerReference},
			expectedOwnerRefs: []metav1.OwnerReference{serviceOwnerReference, mopOwnerReference},
		},
		{
			name:              "No MOP owner refs",
			existingOwnerRefs: []metav1.OwnerReference{serviceOwnerReference},
			mopOwnerRefs:      []metav1.OwnerReference{},
			expectedOwnerRefs: []metav1.OwnerReference{serviceOwnerReference},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mopConfigList := convertToUnstructured(t, comparedObject)
			if tc.mopOwnerRefs == nil {
				mopConfigList[0].SetOwnerReferences([]metav1.OwnerReference{mopOwnerReference})
			} else {
				mopConfigList[0].SetOwnerReferences(tc.mopOwnerRefs)
			}

			comparedObject.SetOwnerReferences(tc.existingOwnerRefs)

			client := kube_test.NewKubeClientBuilder().
				AddDynamicClientObjects([]runtime.Object{comparedObject}...).
				Build()

			registry := prometheus.NewRegistry()
			comparer := NewMeshConfigComparer(
				zaptest.NewLogger(t).Sugar(),
				client,
				registry,
				clusterName,
			)

			mopConfig := templating.UnflattenConfig(mopConfigList, baseTemplateType)

			actualConfig, err := comparer.DiffWithCopilotConfig(mopConfig, serviceObject, map[string]string{})
			assert.Nil(t, err)

			actualOwnerReferences := actualConfig.FlattenConfig()[0].GetOwnerReferences()
			assert.EqualValues(t, tc.expectedOwnerRefs, actualOwnerReferences)
		})
	}
}

func createCopilotDestinationRule() *istiov1alpha3.DestinationRule {
	return NewDestinationRuleBuilder(objectName, objectNsName).SetLabels(map[string]string{ManagedByCopilotLabel: "istio-copilot"}).Build()
}

func createCopilotMainVirtualService(name string, httpRouteNameEnabled bool, delegatePorts ...uint32) *istiov1alpha3.VirtualService {
	vs := &istiov1alpha3.VirtualService{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.VirtualServiceKind.Kind,
			APIVersion: constants.VirtualServiceResource.GroupVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: objectNsName,
		},
	}
	vs.Spec.Hosts = []string{
		"test-service.test-ns.svc.cluster.local",
		"test-service.test-ns.svc.mesh.io",
	}
	vs.Spec.Gateways = []string{"mesh"}

	for _, port := range delegatePorts {
		httpRoute := v1alpha3.HTTPRoute{
			Match: []*v1alpha3.HTTPMatchRequest{{Port: port}},
			Delegate: &v1alpha3.Delegate{
				Name: buildDelegateName(name, port),
			},
		}
		if httpRouteNameEnabled {
			httpRoute.Name = "http-" + fmt.Sprint(port)
		}
		vs.Spec.Http = append(vs.Spec.Http, &httpRoute)
	}

	return vs
}

func createVirtualServiceWithDefaultSpec(name string, port uint32) *istiov1alpha3.VirtualService {
	vs := newLabeledVirtualService(name)
	vs.Spec.Hosts = []string{
		"test-service.test-ns.svc.cluster.local",
		"test-service.test-ns.svc.mesh.io",
	}
	vs.Spec.Gateways = []string{"mesh"}
	vs.Spec.Http = []*v1alpha3.HTTPRoute{{Match: []*v1alpha3.HTTPMatchRequest{{Port: port}}}}
	return vs
}

// newLabeledVirtualService returns a VirtualService skeleton with the labels
// and annotations the comparison tests expect. Callers populate Spec fields
// directly on the returned pointer to avoid copying the VirtualService Spec
// by value, which carries a protobuf lock.
func newLabeledVirtualService(name string) *istiov1alpha3.VirtualService {
	return &istiov1alpha3.VirtualService{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.VirtualServiceKind.Kind,
			APIVersion: constants.VirtualServiceResource.GroupVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   objectNsName,
			Labels:      map[string]string{"mesh.io.example.com/managed-by": "copilot"},
			Annotations: map[string]string{},
		},
	}
}

func buildDelegateName(serviceName string, port uint32) string {
	return fmt.Sprintf(serviceName+"-%d", port)
}

func setLabels(vs *istiov1alpha3.VirtualService, labels map[string]string) *istiov1alpha3.VirtualService {
	result := vs.DeepCopy()
	result.SetLabels(labels)
	return result
}

func convertToUnstructured(t *testing.T, objects ...runtime.Object) []*unstructured.Unstructured {
	var convertedObjects []*unstructured.Unstructured

	for _, obj := range objects {
		convertedObjects = append(convertedObjects, objectToUnstructured(t, obj))
	}
	return convertedObjects
}

func objectToUnstructured(t *testing.T, object interface{}) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}

	byteData, err := json.Marshal(object)
	require.NoError(t, err)

	err = obj.UnmarshalJSON(byteData)
	require.NoError(t, err)
	return obj
}

type DestinationRuleBuilder struct {
	dr *istiov1alpha3.DestinationRule
}

func NewDestinationRuleBuilder(name string, namespace string) *DestinationRuleBuilder {
	builder := &DestinationRuleBuilder{
		dr: &istiov1alpha3.DestinationRule{
			TypeMeta: metav1.TypeMeta{
				Kind:       constants.DestinationRuleKind.Kind,
				APIVersion: constants.DestinationRuleResource.GroupVersion().String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: v1alpha3.DestinationRule{
				Host: strings.Join([]string{name, namespace, "svc.cluster.local"}, "."),
				TrafficPolicy: &v1alpha3.TrafficPolicy{
					LoadBalancer: &v1alpha3.LoadBalancerSettings{
						LocalityLbSetting: &v1alpha3.LocalityLoadBalancerSetting{
							Enabled: &wrapperspb.BoolValue{
								Value: true,
							},
						},
					},
				},
			},
		},
	}
	return builder
}

func (d *DestinationRuleBuilder) SetLabels(labels map[string]string) *DestinationRuleBuilder {
	d.dr.SetLabels(labels)
	return d
}

func (d *DestinationRuleBuilder) SetAnnotations(annotations map[string]string) *DestinationRuleBuilder {
	d.dr.SetAnnotations(annotations)
	return d
}

func (d *DestinationRuleBuilder) SetSubsets(subsets []*v1alpha3.Subset) *DestinationRuleBuilder {
	d.dr.Spec.Subsets = subsets
	return d
}

func (d *DestinationRuleBuilder) SetTrafficPolicy(trafficPolicy *v1alpha3.TrafficPolicy) *DestinationRuleBuilder {
	d.dr.Spec.TrafficPolicy = trafficPolicy
	return d
}

func (d *DestinationRuleBuilder) Build() *istiov1alpha3.DestinationRule {
	return d.dr
}

func createServiceEntry(namespace, name string, annotations map[string]string, labels map[string]string) *istiov1alpha3.ServiceEntry {
	return &istiov1alpha3.ServiceEntry{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.ServiceEntryKind.Kind,
			APIVersion: constants.ServiceEntryResource.GroupVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   namespace,
			Name:        name,
			Labels:      labels,
			Annotations: annotations,
		},
	}
}

func Test_removeArgoSpecificLabels(t *testing.T) {
	testCases := []struct {
		name         string
		configObject *unstructured.Unstructured
	}{
		{
			"No subsets in the DestinationRule",
			objectToUnstructured(t, NewDestinationRuleBuilder(objectName, objectNsName).SetAnnotations(
				map[string]string{"argo-rollouts.argoproj.io/managed-by-rollouts": objectName}).Build()),
		},
		{
			"No subset with Argo label",
			objectToUnstructured(t, NewDestinationRuleBuilder(objectName, objectNsName).SetAnnotations(
				map[string]string{"argo-rollouts.argoproj.io/managed-by-rollouts": objectName}).SetSubsets([]*v1alpha3.Subset{{
				Name:   "test",
				Labels: map[string]string{"test": "test"},
			}}).Build()),
		},
		{
			"Single subset with Argo label",
			objectToUnstructured(t, NewDestinationRuleBuilder(objectName, objectNsName).SetAnnotations(
				map[string]string{"argo-rollouts.argoproj.io/managed-by-rollouts": objectName}).SetSubsets([]*v1alpha3.Subset{{
				Name:   "test",
				Labels: map[string]string{"test": "test", "rollouts-pod-template-hash": "6f87c5d5d5"},
			}}).Build()),
		},
		{
			"Multiple subsets with Argo label",
			objectToUnstructured(t, NewDestinationRuleBuilder(objectName, objectNsName).SetAnnotations(
				map[string]string{"argo-rollouts.argoproj.io/managed-by-rollouts": objectName}).SetSubsets([]*v1alpha3.Subset{{
				Name:   "test1",
				Labels: map[string]string{"test1": "test1", "rollouts-pod-template-hash": "6f87c5d5d5"},
			}, {
				Name:   "test2",
				Labels: map[string]string{"test2": "test2", "rollouts-pod-template-hash": "8df5isk878"},
			}}).Build()),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			removeArgoUpdatedSubsetLabel(tc.configObject)
			spec := tc.configObject.Object["spec"].(map[string]interface{})
			subsets := spec["subsets"]
			if subsets != nil {
				for _, subset := range subsets.([]interface{}) {
					s := subset.(map[string]interface{})
					labels := s["labels"].(map[string]interface{})
					assert.NotContains(t, labels, "rollouts-pod-template-hash")
				}
			}
		})
	}
}

func createEnvoyFilter(name string, namespace string) *istiov1alpha3.EnvoyFilter {
	filter := istiov1alpha3.EnvoyFilter{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.EnvoyFilterKind.Kind,
			APIVersion: constants.EnvoyFilterResource.GroupVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
	filter.SetLabels(map[string]string{"mesh.io.example.com/managed-by": "copilot"})
	return &filter
}

func createRequestAuthenticationResource(name string, namespace string) *istiosecurityv1beta1.RequestAuthentication {
	requestAuthentication := istiosecurityv1beta1.RequestAuthentication{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.RequestAuthenticationKind.Kind,
			APIVersion: constants.RequestAuthenticationResource.GroupVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
	requestAuthentication.SetLabels(map[string]string{"mesh.io.example.com/managed-by": "copilot"})
	return &requestAuthentication
}
