package transition

import (
	"context"
	"encoding/json"
	"fmt"

	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"github.com/prometheus/client_golang/prometheus"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	blueColor  = "blue"
	greenColor = "green"

	casamNamespace = "core-on-sam"
	hsrNamespace   = "hsr"
)

var routingContextResource = schema.GroupVersionResource{Group: "mesh.sfdc.net", Version: "v1", Resource: "routingcontexts"}

var casamNamespaces = []string{casamNamespace, hsrNamespace}

func InitCasamOverlays(baseLogger *zap.SugaredLogger, primaryClient, client kube.Client, metricsRegistry *prometheus.Registry, clusterName string, svc *corev1.Service) {

	if !EnableInitCasamMops {
		return
	}

	logger := baseLogger.With("cluster", clusterName, "namespace", svc.Namespace, "service", svc.Name)

	// Check if it's casam NS
	if !common.SliceContains(casamNamespaces, svc.Namespace) {
		return
	}

	// Fetch RC if exists (note that RCs live in the primary cluster)
	rc, err := primaryClient.Dynamic().Resource(routingContextResource).Namespace(svc.Namespace).Get(context.TODO(), svc.Name, metav1.GetOptions{})
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			logger.Errorw("failed to fetch routing context", "error", err)
			raiseMetric(metricsRegistry, svc, clusterName, CasamInitOverlaysMopError)
		}
		return
	}

	// Check if MOP already exists (note that MOPs live in the same cluster as the service)
	_, err = client.MopApiClient().MeshV1alpha1().MeshOperators(svc.Namespace).Get(context.TODO(), composeCasamMopName(svc.Name), metav1.GetOptions{})
	if err == nil {
		logger.Infof("casam overlay MOP already exists")
		return
	}

	if !k8serrors.IsNotFound(err) {
		logger.Errorw("can't fetch casam overlay MOP", "error", err)
		raiseMetric(metricsRegistry, svc, clusterName, CasamInitOverlaysMopError)
		return
	}

	// CreateMop
	err = createCasamMop(client, svc, rc)
	if err != nil {
		logger.Errorw("failed to create casam overlay MOP", "error", err)
		raiseMetric(metricsRegistry, svc, clusterName, CasamInitOverlaysMopError)
	} else {
		logger.Info("created initial casam overlay MOP")
		raiseMetric(metricsRegistry, svc, clusterName, CasamInitOverlaysMopSuccess)
	}
}

func createCasamMop(
	client kube.Client,
	svc *corev1.Service,
	rc *unstructured.Unstructured) error {
	overlays, err := composeOverlays(svc.Namespace, svc.Name, rc)
	if err != nil {
		return err
	}

	// Create and persist MOP
	selector, err := composeServiceSelector(svc)
	if err != nil {
		return err
	}
	mop := v1alpha1.MeshOperator{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: svc.Namespace,
			Name:      composeCasamMopName(svc.Name),
			Labels: map[string]string{
				constants.MeshIoManagedByLabel: "mesh-operator",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: constants.ServiceResource.GroupVersion().String(),
					Kind:       constants.ServiceKind.Kind,
					Name:       svc.GetName(),
					UID:        svc.GetUID(),
				},
			},
		},
		Spec: v1alpha1.MeshOperatorSpec{
			ServiceSelector: selector,
			Overlays:        overlays,
		},
	}
	_, err = client.MopApiClient().MeshV1alpha1().MeshOperators(svc.Namespace).Create(context.TODO(), &mop, metav1.CreateOptions{})
	return err
}

func composeCasamMopName(serviceName string) string {
	return fmt.Sprintf("rus--%s", serviceName)
}

func composeServiceSelector(svc *corev1.Service) (map[string]string, error) {
	selector := map[string]string{}
	pCell, exists := svc.Labels["p_cell"]
	if !exists {
		return nil, fmt.Errorf("missing p_cell label")
	}
	pServicename, exists := svc.Labels["p_servicename"]
	if !exists {
		return nil, fmt.Errorf("missing p_servicename label")
	}
	if pServiceInstance, exists := svc.Labels["p_service_instance"]; exists {
		selector["p_service_instance"] = pServiceInstance
	}
	selector["p_cell"] = pCell
	selector["p_servicename"] = pServicename

	return selector, nil
}

func composeOverlays(svcNamespace, svcName string, rc *unstructured.Unstructured) ([]v1alpha1.Overlay, error) {
	liveVersion, found, _ := unstructured.NestedString(rc.Object, "spec", "liveTrafficAppVersion")
	if !found {
		return nil, fmt.Errorf("live version not in RC")
	}
	testVersion, found, _ := unstructured.NestedString(rc.Object, "spec", "testTrafficAppVersion")
	if !found {
		return nil, fmt.Errorf("test version not in RC")
	}

	overlays := []v1alpha1.Overlay{}

	if svcNamespace == hsrNamespace {
		overlays = append(overlays, composeHsrDrOverlay(svcName, liveVersion, testVersion))
	} else {
		overlays = append(overlays, composeDrOverlay(svcName, liveVersion, testVersion))
		overlays = append(overlays, composeVsOverlaysForColor(svcName, blueColor, liveVersion, testVersion)...)
		overlays = append(overlays, composeVsOverlaysForColor(svcName, greenColor, liveVersion, testVersion)...)
	}
	return overlays, nil
}

func composeVsOverlaysForColor(serviceName, color, liveVersion, testVersion string) []v1alpha1.Overlay {
	overlays := []v1alpha1.Overlay{}
	if color == liveVersion {
		overlays = append(overlays, composeRouteOverlay(serviceName, "casam_readiness_"+color, "live", 200))
	} else {
		overlays = append(overlays, composeRouteOverlay(serviceName, "casam_readiness_"+color, "test", 500))
	}

	if color == testVersion {
		overlays = append(overlays, composeRouteOverlay(serviceName, "casam_test_"+color, "test", 200))
	} else {
		overlays = append(overlays, composeRouteOverlay(serviceName, "casam_test_"+color, "live", 500))
	}
	return overlays
}

func composeDrOverlay(svcName string, liveVersion string, testVersion string) v1alpha1.Overlay {
	drOverlayBytes, _ := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"subsets": []interface{}{
				map[string]interface{}{
					"name": "live",
					"labels": map[string]interface{}{
						"version": liveVersion,
					},
				},
				map[string]interface{}{
					"name": "sticky-live",
					"labels": map[string]interface{}{
						"version": liveVersion,
					},
				},
				map[string]interface{}{
					"name": "test",
					"labels": map[string]interface{}{
						"version": testVersion,
					},
				},
				map[string]interface{}{
					"name": "sticky-test",
					"labels": map[string]interface{}{
						"version": testVersion,
					},
				},
			},
		},
	})

	return v1alpha1.Overlay{
		Kind: constants.DestinationRuleKind.Kind,
		Name: svcName,
		StrategicMergePatch: runtime.RawExtension{
			Raw: drOverlayBytes,
		},
	}
}

func composeHsrDrOverlay(svcName string, liveVersion string, testVersion string) v1alpha1.Overlay {
	drOverlayBytes, _ := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"subsets": []interface{}{
				map[string]interface{}{
					"name": "live",
					"labels": map[string]interface{}{
						"version": liveVersion,
					},
				},
				map[string]interface{}{
					"name": "test",
					"labels": map[string]interface{}{
						"version": testVersion,
					},
				},
			},
		},
	})

	return v1alpha1.Overlay{
		Kind: constants.DestinationRuleKind.Kind,
		Name: svcName,
		StrategicMergePatch: runtime.RawExtension{
			Raw: drOverlayBytes,
		},
	}
}
func composeRouteOverlay(svcName, routeName, subset string, httpStatus int) v1alpha1.Overlay {
	routeOverlayBytes, _ := json.Marshal(map[string]interface{}{
		"spec": map[string]interface{}{
			"http": []interface{}{
				map[string]interface{}{
					"name": routeName,
					"fault": map[string]interface{}{
						"abort": map[string]interface{}{
							"httpStatus": httpStatus,
						},
					},
					"route": []interface{}{
						map[string]interface{}{
							"destination": map[string]interface{}{
								"host":   svcName,
								"subset": subset,
								"port": map[string]interface{}{
									"number": 7442,
								},
							},
						},
					},
				},
			},
		},
	})

	return v1alpha1.Overlay{
		Kind: constants.VirtualServiceKind.Kind,
		StrategicMergePatch: runtime.RawExtension{
			Raw: routeOverlayBytes,
		},
	}
}

func raiseMetric(metricsRegistry *prometheus.Registry, svc *corev1.Service, clusterName, metricName string) {
	labels := commonmetrics.GetLabelsForK8sResource(constants.ServiceKind.Kind, clusterName, svc.Namespace, svc.Name)
	commonmetrics.GetOrRegisterCounterWithLabels(metricName, labels, metricsRegistry).Inc()
}
