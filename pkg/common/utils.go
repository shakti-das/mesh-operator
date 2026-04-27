package common

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"

	v1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/alias"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/logging"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	resourceSuffixByKind = map[string]string{
		constants.ServiceKind.Kind:                  "",
		constants.ServiceEntryKind.Kind:             "--se",
		constants.KnativeIngressKind.Kind:           "--ing",
		constants.KnativeServerlessServiceKind.Kind: "--sks",
		constants.TrafficShardingPolicyKind.Kind:    "--tsp",
	}
)

func CreateNewLogger(level zapcore.Level) *zap.SugaredLogger {
	zapLogger, err := logging.NewLogger(level)
	if err != nil {
		panic(fmt.Sprintf("Failed to create root logger %v", err))
	}
	logger := zapLogger.Sugar()
	return logger
}

// MergeOwnerReferences adds the new owner references to the existing owner references without repeating owner refs,
// while keeping the order of existing owner refs consistent.
func MergeOwnerReferences(existing []metav1.OwnerReference, new []metav1.OwnerReference) []metav1.OwnerReference {
	if len(existing) == 0 {
		return new
	}

	if len(new) == 0 {
		return existing
	}

	existingOwnerRefsMap := make(map[string]struct{}, len(existing))
	for _, owner := range existing {
		key := strings.Join([]string{owner.APIVersion, owner.Kind, owner.Name}, "_")
		existingOwnerRefsMap[key] = struct{}{}
	}

	for _, newOwnerRef := range new {
		key := strings.Join([]string{newOwnerRef.APIVersion, newOwnerRef.Kind, newOwnerRef.Name}, "_")
		if _, exists := existingOwnerRefsMap[key]; !exists {
			existing = append(existing, newOwnerRef)
		}
	}

	return existing
}

func SliceContains(sl []string, v string) bool {
	for _, s := range sl {
		if s == v {
			return true
		}
	}
	return false
}

// UpdateExtensionSourceAnnotation will append Key (eg. `cluster2/svc` or `cluster1`) if needed to keep it in sync with csv key value on filter object in cluster
func UpdateExtensionSourceAnnotation(obj *unstructured.Unstructured, keyToAppend, csvKeysInCluster string) {
	// append svcKey if don't exist in the extensionSourceAnnotationValue
	if !SliceContains(strings.Split(csvKeysInCluster, ","), keyToAppend) {
		if csvKeysInCluster != "" {
			csvKeysInCluster = csvKeysInCluster + "," + keyToAppend
		} else {
			csvKeysInCluster = keyToAppend
		}
	}
	AddAnnotation(obj, constants.ExtensionSourceAnnotation, csvKeysInCluster)
}

func GetResourceParent(parentNamespace string, parentName string, kind string) string {
	parentNameWithSuffix := GetRecordNameSuffixedByKind(parentName, kind)
	return fmt.Sprintf("%s/%s", parentNamespace, parentNameWithSuffix)
}

func AddAnnotation(obj *unstructured.Unstructured, annotation string, value string) {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string, 1)
	}
	annotations[annotation] = value
	obj.SetAnnotations(annotations)
}

func AddLabel(obj *unstructured.Unstructured, label, value string) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string, 1)
	}
	labels[label] = value
	obj.SetLabels(labels)
}

func GetAnnotation(obj *unstructured.Unstructured, annotation string) string {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return ""
	}
	return annotations[annotation]
}

func CreateDeepCopyListOfUnstructuredObjects(listOfUnstructruredObjects []*unstructured.Unstructured) []*unstructured.Unstructured {
	copyOfListOfUnstructuredObjects := make([]*unstructured.Unstructured, len(listOfUnstructruredObjects))
	for index, object := range listOfUnstructruredObjects {
		copyOfListOfUnstructuredObjects[index] = object.DeepCopy()
	}
	return copyOfListOfUnstructuredObjects
}

func MergeMaps(map1, map2 map[string]string) map[string]string {
	result := make(map[string]string)
	for k, v := range map1 {
		result[k] = v
	}
	for k, v := range map2 {
		result[k] = v
	}

	return result
}

// GetRecordNameSuffixedByKind returns suffixes per kind used by MSM and other parent<->child relationship trackers.
func GetRecordNameSuffixedByKind(resourceName string, kind string) string {
	return resourceName + resourceSuffixByKind[kind]
}

func GetFirstNonNil(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
	return nil
}

func GetLabelOrAlias(attribute constants.Attribute, objectLabels map[string]string) string {
	labelAliases := alias.Manager.GetLabelAliases()
	return findAttributesInMap(attribute, objectLabels, labelAliases)
}

func GetAnnotationOrAlias(attribute constants.Attribute, objectAnnotations map[string]string) string {
	annotationAliases := alias.Manager.GetAnnotationAliases()
	return findAttributesInMap(attribute, objectAnnotations, annotationAliases)
}

func findAttributesInMap(attribute constants.Attribute, attributeMap map[string]string, aliasMap map[string][]string) string {
	attributeAsStr := GetStringForAttribute(attribute)
	attrVal := attributeMap[attributeAsStr]
	if attrVal != "" {
		return attrVal
	}

	targetingAliases := aliasMap[attributeAsStr]
	for _, targetingAlias := range targetingAliases {
		if val, found := attributeMap[targetingAlias]; found {
			return val
		}
	}
	return ""
}

func FindAnnotationSuffixByPrefixOrAlias(attribute constants.Attribute, annotationKey string) (bool, string) {
	attrPrefixAsStr := GetStringForAttribute(attribute)
	if strings.HasPrefix(annotationKey, attrPrefixAsStr) {
		return true, strings.TrimPrefix(annotationKey, attrPrefixAsStr)
	}

	annotationAliases := alias.Manager.GetAnnotationAliases()
	targetingAliases := annotationAliases[attrPrefixAsStr]
	for _, targetingAliasPrefix := range targetingAliases {
		if strings.HasPrefix(annotationKey, targetingAliasPrefix) {
			return true, strings.TrimPrefix(annotationKey, targetingAliasPrefix)
		}
	}
	return false, ""
}

func GetStringForAttribute(attribute constants.Attribute) string {
	return string(attribute)
}

// ControllerConfig contains the configuration used across controllers_api
type ControllerConfig struct {
	InformerCacheSyncTimeoutSeconds int

	// How often we'll be retrying informer sync for unavailable clusters
	ClusterSyncRetryTimeoutSeconds int

	// informer label filter
	Selector string

	// KubeAPIQPS is the maximum queries per second to the Kubernetes API server
	KubeAPIQPS float32

	// KubeAPIBurst is the maximum burst for throttle to the Kubernetes API server
	KubeAPIBurst int
}

// IsOperatorDisabled will take any resource implementing metav1.Object and find whether operator is diabled for it.
func IsOperatorDisabled(obj interface{}) bool {
	object := obj.(metav1.Object)
	value := GetAnnotationOrAlias(constants.RoutingConfigEnabledAnnotation, object.GetAnnotations())
	if strings.EqualFold(value, "false") {
		return true
	}
	value = object.GetAnnotations()[constants.MeshOperatorEnabled]
	if strings.EqualFold(value, "false") {
		return true
	}
	return false
}

func UnmarshallObject(ar *v1.AdmissionReview) (*unstructured.Unstructured, error) {
	m := make(map[string]interface{})
	if err := json.Unmarshal(ar.Request.Object.Raw, &m); err != nil {
		return nil, fmt.Errorf("failed to unmarshal %s from mutation request: %w", ar.Request.Kind, err)
	}
	obj := &unstructured.Unstructured{}
	obj.Object = m

	return obj, nil
}

func GetMetaObject(obj interface{}) metav1.Object {
	accessor, _ := meta.Accessor(obj)
	return accessor
}

type TimeProvider interface {
	Now() *metav1.Time
}

// IsHeadlessService returns true if the given object is a Kubernetes Service with ClusterIP set to "None".
func IsHeadlessService(obj metav1.Object) bool {
	if svc, ok := obj.(*corev1.Service); ok {
		return svc.Spec.ClusterIP == "None"
	}
	return false
}

type RealTimeProvider struct{}

func NewRealTimeProvider() TimeProvider {
	return &RealTimeProvider{}
}

func (g *RealTimeProvider) Now() *metav1.Time {
	return &metav1.Time{time.Now()}
}
