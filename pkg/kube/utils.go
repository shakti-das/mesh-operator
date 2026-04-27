package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	k8slabels "k8s.io/apimachinery/pkg/labels"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/retry"

	"context"
	"reflect"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"

	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	scheme = runtime.NewScheme()
)

type BuildClientFromKubeConfigHandler func(logger *zap.SugaredLogger, clusterName string, kubeConfig []byte, rescanPeriod time.Duration, selector string, informerCacheSyncTimeoutSeconds int, metricsRegistry *prometheus.Registry, qps float32, burst int) (Client, error)

// Reference: https://github.com/istio/istio/blob/177cd70457863588e4712923d12663eae4edaa2a/pkg/kube/util.go#L51
// TODO shakti-das Update during multi-cluster support.
// BuildClientCmd builds a client cmd config from a kubeconfig filepath and context.
// It overrides the current context with the one provided (empty to use default).
//
// This is a modified version of k8s.io/client-go/tools/clientcmd/BuildConfigFromFlags with the
// difference that it loads default configs if not running in-cluster.
func BuildClientCmd(kubeconfig, context string, overrides ...func(*clientcmd.ConfigOverrides)) clientcmd.ClientConfig {
	if kubeconfig != "" {
		info, err := os.Stat(kubeconfig)
		if err != nil || info.Size() == 0 {
			// If the specified kubeconfig doesn't exists / empty file / any other error
			// from file stat, fall back to default
			kubeconfig = ""
		}
	}

	// Config loading rules:
	// 1. kubeconfig if it not empty string
	// 2. Config(s) in KUBECONFIG environment variable
	// 3. In cluster config if running in-cluster
	// 4. Use $HOME/.kube/config
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	loadingRules.DefaultClientConfig = &clientcmd.DefaultClientConfig
	loadingRules.ExplicitPath = kubeconfig
	configOverrides := &clientcmd.ConfigOverrides{
		ClusterDefaults: clientcmd.ClusterDefaults,
		CurrentContext:  context,
	}

	for _, fn := range overrides {
		fn(configOverrides)
	}

	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)
}

func CreateEventRecorder(kubeClient kubernetes.Interface) record.EventRecorder {
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: kubeClient.CoreV1().Events("")})
	eventRecorder := eventBroadcaster.NewRecorder(scheme, corev1.EventSource{Component: constants.MeshOperatorName})
	return eventRecorder
}

// BuildClientsFromConfig creates kube.Clients from the provided kubeconfig.
func BuildClientsFromConfig(logger *zap.SugaredLogger, clusterName string, kubeConfig []byte, rescanPeriod time.Duration, selector string, informerCacheSyncTimeoutSeconds int, metricsRegistry *prometheus.Registry, qps float32, burst int) (Client, error) {
	if len(kubeConfig) == 0 {
		return nil, errors.New("kubeconfig is empty")
	}

	rawConfig, err := clientcmd.Load(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("kubeconfig cannot be loaded: %v", err)
	}

	if err := clientcmd.Validate(*rawConfig); err != nil {
		return nil, fmt.Errorf("kubeconfig is not valid: %v", err)
	}

	clientConfig := clientcmd.NewDefaultClientConfig(*rawConfig, &clientcmd.ConfigOverrides{})
	config, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed due to client config error: %v", err)
	}

	// Apply client-side throttling settings for remote cluster
	config.QPS = qps
	config.Burst = burst
	logger.Infof("Remote cluster %s Kubernetes client configured with QPS: %.1f, Burst: %d", clusterName, qps, burst)

	var tweakListOptionsFunc func(options *v1.ListOptions)
	if len(selector) > 0 {
		tweakListOptionsFunc, err = GetTweakListOptionsFunc(selector)
		if err != nil {
			return nil, fmt.Errorf("failed to parse label selector: %v", err)
		}
	}
	clients, err := NewClientWithTweakListOption(logger, clusterName, config, rescanPeriod, tweakListOptionsFunc, informerCacheSyncTimeoutSeconds, metricsRegistry)
	if err != nil {
		return nil, fmt.Errorf("failed to create kube clients: %v", err)
	}
	return clients, nil
}

func ObjectToUnstructured(object interface{}) (*unstructured.Unstructured, error) {
	obj := &unstructured.Unstructured{}

	byteData, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal object, error: %w", err)
	}

	err = obj.UnmarshalJSON(byteData)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal object %w", err)
	}
	return obj, nil
}

// NewKubernetesClient creates a dynamic kubernetes client configured using the given config.
// It causes the app to panic if creation of the client fails.

// ConvertKindToResource uses the kubernetes discovery client to look up the given kind and find the matching resource.
func ConvertKindToResource(discoveryClient discovery.DiscoveryInterface, groupVersionKind schema.GroupVersionKind) (schema.GroupVersionResource, error) {
	groupVersionResources, err := discoveryClient.ServerResourcesForGroupVersion(groupVersionKind.GroupVersion().String())
	if err != nil {
		return schema.GroupVersionResource{}, fmt.Errorf("failed to get kubernetes resources for GroupVersion = %q: %w", groupVersionKind.GroupVersion(), err)
	}
	for _, apiResource := range groupVersionResources.APIResources {
		if apiResource.Kind == groupVersionKind.Kind {
			return groupVersionKind.GroupVersion().WithResource(apiResource.Name), nil
		}
	}

	return schema.GroupVersionResource{}, fmt.Errorf("failed to get kubernetes resource for GroupVersionKind = %q: no matching resource found for given kind", groupVersionKind)
}

// UpdateSameError is used to denote that an update was skipped because the existing object and the given new
// value are the same for all fields that we care about (e.g. name, annotations, labels, spec).
type UpdateSameError struct {
	Obj         *unstructured.Unstructured
	ObjExisting *unstructured.Unstructured
}

func (err *UpdateSameError) Error() string {
	return fmt.Sprintf(
		"update skipped, existing and new object for \"%s.%s\" of type \"apiVersion=%s, kind=%s\" are the same",
		err.Obj.GetNamespace(),
		err.Obj.GetName(),
		err.Obj.GetAPIVersion(),
		err.Obj.GetKind())
}

// GetObject gets the unstructured obj using the given client.

// CreateOrUpdateObject creates or updates the unstructured obj using the given client.
// Returns an error of type UpdateSameSpecError if an update is supposed to be performed and the spec hasn't changed.
func CreateOrUpdateObject(client dynamic.Interface, obj *unstructured.Unstructured, groupVersionResource schema.GroupVersionResource, retainArgoManager RetainFieldManager, logger *zap.SugaredLogger) error {
	objName := obj.GetName()
	objNamespace := obj.GetNamespace()
	resourceInterface := client.Resource(groupVersionResource).Namespace(objNamespace)

	objLog := logger.With(
		"objResource", groupVersionResource,
		"objNamespace", objNamespace,
		"objName", objName)

	objLog.Infow("preparing to upsert object")
	isErrorRetriable := func(err error) bool {
		retriable := false
		if err != nil {
			retriable = k8serrors.IsConflict(err) || k8serrors.IsServiceUnavailable(err) || k8serrors.IsInternalError(err) || k8serrors.IsTimeout(err) || k8serrors.IsServerTimeout(err)
			if retriable {
				objLog.Infow("error is retriable", "retriable", retriable, "Received error is", err, "reason", k8serrors.ReasonForError(err))
				return retriable
			}
			objLog.Debugw("Received error", "retriable", retriable, "Received error is", err, "reason", k8serrors.ReasonForError(err))
		}
		return retriable
	}
	return retry.OnError(retry.DefaultRetry, isErrorRetriable, func() error {
		// Try to get the existing resource with the same namespace/name.
		objExisting, err := resourceInterface.Get(context.TODO(), objName, metav1.GetOptions{})
		switch {
		case err == nil:
			// There is an existing resource, possibly perform an update.

			// First we check for equality in the fields that we care about in the objects (e.g. name, annotations, labels, spec).
			// If they are equal, skip the update.
			objsEqual, err := areObjsEqual(objExisting, obj)
			if err != nil {
				return err
			}
			if objsEqual {
				objLog.Infow("skipping object update, object unchanged")
				return &UpdateSameError{ObjExisting: objExisting, Obj: obj}
			}

			errInRetainingArgoFields := retainArgoManager.RetainUnmanaged(objExisting, obj)
			if errInRetainingArgoFields != nil {
				return errInRetainingArgoFields
			}

			// The spec has changed, proceed with the update.
			return update(resourceInterface, obj, objExisting.GetResourceVersion(), objLog)
		case k8serrors.IsNotFound(err):
			// There is no existing resource, perform a create.
			return create(resourceInterface, obj, groupVersionResource, objLog)
		default:
			objLog.Errorw("failed to get object", "error", err)
			return err
		}
	})
}

func IsErrorResourceNotFound(err error) bool {
	return k8serrors.IsNotFound(err)
}

func GetNamespace(client dynamic.Interface, namespaceName string) (*unstructured.Unstructured, error) {
	return client.Resource(constants.NamespaceResource).Get(context.TODO(), namespaceName, metav1.GetOptions{})
}

func GetTweakListOptionsFunc(selector string) (func(options *v1.ListOptions), error) {
	_, err := k8slabels.Parse(selector)
	if err != nil {
		return nil, err
	}

	return func(options *v1.ListOptions) {
		options.LabelSelector = selector
	}, nil
}

func create(ri dynamic.ResourceInterface, obj *unstructured.Unstructured, gvr schema.GroupVersionResource, log *zap.SugaredLogger) error {
	_, err := ri.Create(context.TODO(), obj, metav1.CreateOptions{})
	switch {
	case err == nil:
		log.Infow("created new object")
		return nil
	case k8serrors.IsAlreadyExists(err):
		log.Infow("race condition encountered while creating object, retrying")
		return k8serrors.NewConflict(gvr.GroupResource(), obj.GetName(), errors.New("create race condition"))
	default:
		log.Errorw("failed to create object", "error", err)
		return err
	}
}

func update(ri dynamic.ResourceInterface, obj *unstructured.Unstructured, version string, log *zap.SugaredLogger) error {
	// Copy the resourceVersion metadata to our new object to support k8s' concurrency control.
	// See https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md#concurrency-control-and-consistency.
	obj.SetResourceVersion(version)
	_, err := ri.Update(context.TODO(), obj, metav1.UpdateOptions{})

	switch {
	case err == nil:
		log.Infow("updated existing object")
		return nil
	case k8serrors.IsConflict(err):
		log.Infow("race condition encountered while updating object, retrying")
		return err
	default:
		log.Errorw("failed to update object", "error", err)
		return err
	}
}

func areObjsEqual(objExisting *unstructured.Unstructured, obj *unstructured.Unstructured) (bool, error) {
	var spec, specExisting interface{}
	var specFound, specExistingFound bool
	var err error

	if specExisting, specExistingFound, err = unstructured.NestedFieldNoCopy(objExisting.UnstructuredContent(), "spec"); err != nil {
		return false, fmt.Errorf("failed to get spec from existing obj %v: %w", objExisting, err)
	}

	if spec, specFound, err = unstructured.NestedFieldNoCopy(obj.UnstructuredContent(), "spec"); err != nil {
		return false, fmt.Errorf("failed to get spec from obj %v: %w", obj, err)
	}

	objsEqual := specFound && specExistingFound &&
		objExisting.GetName() == obj.GetName() &&
		objExisting.GetNamespace() == obj.GetNamespace() &&
		reflect.DeepEqual(specExisting, spec) &&
		reflect.DeepEqual(objExisting.GetAnnotations(), obj.GetAnnotations()) &&
		reflect.DeepEqual(objExisting.GetLabels(), obj.GetLabels()) &&
		reflect.DeepEqual(objExisting.GetOwnerReferences(), obj.GetOwnerReferences())

	return objsEqual, nil
}
