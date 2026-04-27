package k8s

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	commonconstants "github.com/istio-ecosystem/mesh-operator/common/pkg/k8s/constants"

	"go.uber.org/zap"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
)

// GenericKubeOperationFunc is the function signature for performing Kubernetes API actions like Get or List.
type GenericKubeOperationFunc func() (bool, error)

// NewKubernetesClient creates a dynamic kubernetes client configured using the given config.
// It causes the app to panic if creation of the client fails.
func NewKubernetesClient(logger *zap.SugaredLogger, config *rest.Config) dynamic.Interface {
	client, err := dynamic.NewForConfig(config)
	if err != nil {
		logger.Fatalw("failed to create dynamic k8s api client", "error", err)
	}

	return client
}

// NewKubernetesDiscoveryClient creates a kubernetes discovery client configured using the given config.
// It causes the app to panic if creation of the client fails.
func NewKubernetesDiscoveryClient(logger *zap.SugaredLogger, config *rest.Config) discovery.DiscoveryInterface {
	client, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		logger.Fatalw("failed to create k8s discovery client", "error", err)
	}

	return client
}

// NewKubernetesCoreV1Client creates a kubernetes core v1 client configured using the given config.
// It causes the app to panic if creation of the client fails.
func NewKubernetesCoreV1Client(logger *zap.SugaredLogger, config *rest.Config) corev1.CoreV1Interface {
	client, err := corev1.NewForConfig(config)
	if err != nil {
		logger.Fatalw("failed to create k8s rest client", "error", err)
	}

	return client
}

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

// GetObjectsByListOptions gets the unstructured object list using the given client for the provided selector options.
func GetObjectsByListOptions(client dynamic.Interface, namespace string, groupVersionResource schema.GroupVersionResource,
	selectorOptions metav1.ListOptions,
) (*unstructured.UnstructuredList, error) {
	objects, err := client.Resource(groupVersionResource).Namespace(namespace).List(context.TODO(), selectorOptions)
	return objects, err
}

// GetObject gets the unstructured obj using the given client.
func GetObject(client dynamic.Interface, objName string, objNamespace string, groupVersionResource schema.GroupVersionResource) (*unstructured.Unstructured, error) {
	resourceInterface := client.Resource(groupVersionResource).Namespace(objNamespace)
	return resourceInterface.Get(context.TODO(), objName, metav1.GetOptions{})
}

// DeleteObject deletes the obj using the given client.
func DeleteObject(client dynamic.Interface, objName string, objNamespace string, groupVersionResource schema.GroupVersionResource) error {
	resourceInterface := client.Resource(groupVersionResource).Namespace(objNamespace)
	return resourceInterface.Delete(context.TODO(), objName, metav1.DeleteOptions{})
}

// CreateOrUpdateObject creates or updates the unstructured obj using the given client.
// Returns an error of type UpdateSameSpecError if an update is supposed to be performed and the spec hasn't changed.
func CreateOrUpdateObject(client dynamic.Interface, obj *unstructured.Unstructured, groupVersionResource schema.GroupVersionResource, logger *zap.SugaredLogger) error {
	objName := obj.GetName()
	objNamespace := obj.GetNamespace()
	resourceInterface := client.Resource(groupVersionResource).Namespace(objNamespace)

	objLog := logger.With(
		"objResource", groupVersionResource,
		"objNamespace", objNamespace,
		"objName", objName)

	objLog.Debugw("preparing to upsert object")
	isErrorRetriable := func(err error) bool {
		retriable := false
		if err != nil {
			retriable = k8serrors.IsConflict(err) || k8serrors.IsServiceUnavailable(err) || k8serrors.IsInternalError(err) || k8serrors.IsTimeout(err) || k8serrors.IsServerTimeout(err) || k8serrors.IsTooManyRequests(err)
			if retriable {
				objLog.Infow("error is retriable", "retriable", retriable, "Received error is", err, "reason", k8serrors.ReasonForError(err))
				return retriable
			}
			objLog.Debugw("Received error", "retriable", retriable, "Received error is", err, "reason", k8serrors.ReasonForError(err))
		}
		return retriable
	}

	return retry.OnError(retry.DefaultBackoff, isErrorRetriable, func() error {
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
	return client.Resource(commonconstants.NamespaceResource).Get(context.TODO(), namespaceName, metav1.GetOptions{})
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

// annotationsExcludedFromEquality lists annotation keys that should be ignored when
// comparing objects for equality. These annotations carry volatile metadata (e.g.
// timestamps) that changes on every render and should not trigger a Kubernetes API
// update by itself.
var annotationsExcludedFromEquality = map[string]bool{
	"mesh.io.example.com/last-updated": true,
}

// annotationsForEquality returns a copy of the provided annotations map with excluded keys removed.
func annotationsForEquality(annotations map[string]string) map[string]string {
	filtered := make(map[string]string, len(annotations))
	for k, v := range annotations {
		if !annotationsExcludedFromEquality[k] {
			filtered[k] = v
		}
	}
	return filtered
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
		reflect.DeepEqual(annotationsForEquality(objExisting.GetAnnotations()), annotationsForEquality(obj.GetAnnotations())) &&
		reflect.DeepEqual(objExisting.GetLabels(), obj.GetLabels()) &&
		reflect.DeepEqual(objExisting.GetOwnerReferences(), obj.GetOwnerReferences())

	return objsEqual, nil
}

func RetryWithBackoffOnTooManyRequests(
	backoff wait.Backoff, // Backoff configuration
	operation GenericKubeOperationFunc, // Function performing the actual Kubernetes API call
	log *zap.SugaredLogger,
) error {
	var retryCount int // Track the number of retries
	return wait.ExponentialBackoff(backoff, func() (bool, error) {
		retryCount++ // Increment the retry count
		done, err := operation()
		if err != nil {
			if k8serrors.IsTooManyRequests(err) {
				// Retry on too many requests
				log.Warnw("Retrying due to 429 Too Many Requests", "retryCount", retryCount, "error", err)
				return false, nil
			}
			// Don't retry on other errors
			log.Warnw("Operation failed with a non-retryable error", retryCount, "error", err)
			return false, err
		}
		if done {
			// Log successful operation
			log.Infow("Operation succeeded", "retryCount", retryCount, "retry", false)
		} else {
			// Log incomplete operation requiring another retry
			log.Warnw("Operation incomplete, retrying", "retryCount", retryCount, "retry", true)
		}
		return done, nil
	})
}
