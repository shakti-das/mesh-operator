package common

import (
	"fmt"
	"testing"
	"time"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/alias"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestUpdateFilterSourceAnnotation(t *testing.T) {

	testCases := []struct {
		name                    string
		obj                     *unstructured.Unstructured
		csvKeysInCluster        string
		keyToAppend             string
		expectedAnnotationValue string
	}{
		{
			name:                    "AppendClusterKey",
			obj:                     createEnvoyFilter("mop-123456789-1", "test"),
			keyToAppend:             "cluster4",
			csvKeysInCluster:        "cluster1,cluster2",
			expectedAnnotationValue: "cluster1,cluster2,cluster4",
		},
		{
			name:                    "ClusterKeyAlreadyExist",
			obj:                     createEnvoyFilter("mop-123456789-1", "test"),
			keyToAppend:             "cluster4",
			csvKeysInCluster:        "cluster1,cluster2,cluster4",
			expectedAnnotationValue: "cluster1,cluster2,cluster4",
		},
		{
			name:                    "AppendSvcKey",
			obj:                     createEnvoyFilter("mop-123456789-1", "test"),
			keyToAppend:             "c1/svc1",
			csvKeysInCluster:        "c2/svc1",
			expectedAnnotationValue: "c2/svc1,c1/svc1",
		},
		{
			name:                    "SvcKeyAlreadyExist",
			obj:                     createEnvoyFilter("mop-123456789-1", "test"),
			keyToAppend:             "c2/svc1",
			csvKeysInCluster:        "c1/svc1,c2/svc1",
			expectedAnnotationValue: "c1/svc1,c2/svc1",
		},
		{
			name:                    "EmptyKeyInCluster",
			obj:                     createEnvoyFilter("mop-123456789-1", "test"),
			keyToAppend:             "c2/svc1",
			csvKeysInCluster:        "",
			expectedAnnotationValue: "c2/svc1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			UpdateExtensionSourceAnnotation(tc.obj, tc.keyToAppend, tc.csvKeysInCluster)
			assert.Equal(t, tc.expectedAnnotationValue, tc.obj.GetAnnotations()[constants.ExtensionSourceAnnotation])
		})
	}
}

func TestSetLabel(t *testing.T) {
	noLabelsObject := unstructured.Unstructured{}
	AddLabel(&noLabelsObject, "some-label", "value")

	assert.Equal(t, noLabelsObject.GetLabels(), map[string]string{"some-label": "value"})

	objectWithLabels := unstructured.Unstructured{}
	objectWithLabels.SetLabels(map[string]string{"existing-label": "existing-value"})
	AddLabel(&objectWithLabels, "some-label", "value")

	assert.Equal(t, objectWithLabels.GetLabels(), map[string]string{
		"existing-label": "existing-value",
		"some-label":     "value"})
}

func TestMergeMaps(t *testing.T) {
	assert.Equal(t, map[string]string{}, MergeMaps(nil, nil))
	assert.Equal(t, map[string]string{"k1": "v1"}, MergeMaps(map[string]string{"k1": "v1"}, nil))
	assert.Equal(t, map[string]string{"k1": "v1"}, MergeMaps(nil, map[string]string{"k1": "v1"}))
	assert.Equal(t, map[string]string{"k1": "v1"}, MergeMaps(map[string]string{"k1": "v1"}, map[string]string{"k1": "v1"}))
	assert.Equal(t, map[string]string{"k1": "v1", "k2": "v2"}, MergeMaps(map[string]string{"k1": "v1"}, map[string]string{"k2": "v2"}))
}

func TestGetFirstNonNil(t *testing.T) {
	err1 := fmt.Errorf("test-error1")
	err2 := fmt.Errorf("test-error2")

	assert.Equal(t, nil, GetFirstNonNil(nil))
	assert.Equal(t, nil, GetFirstNonNil(nil, nil))
	assert.Equal(t, nil, GetFirstNonNil(nil, nil))
	assert.Equal(t, nil, GetFirstNonNil(nil, nil, nil))

	assert.Equal(t, err1, GetFirstNonNil(err1))
	assert.Equal(t, err1, GetFirstNonNil(nil, err1))
	assert.Equal(t, err1, GetFirstNonNil(err1, nil))
	assert.Equal(t, err1, GetFirstNonNil(err1, err2))
	assert.Equal(t, err1, GetFirstNonNil(nil, err1, err2))
	assert.Equal(t, err1, GetFirstNonNil(nil, err1, err2, nil))
}

func TestGetAttributeOrAlias(t *testing.T) {
	metaObj := metav1.ObjectMeta{
		Name: "test-meta-object",
		Annotations: map[string]string{
			"routing.mesh.io.example.com/enabled":     "true",
			"routing.mesh.io.example.com/template":    "test-template",
			"routing.mesh.io.example.com/testing":     "test-svc",
			"routing.mesh.io/dynamic-routing-service": "dynamic-svc",
		},
		Labels: map[string]string{
			"routing.mesh.io.example.com/enabled":     "true",
			"routing.mesh.io.example.com/template":    "test-template",
			"routing.mesh.io.example.com/testing":     "test-svc",
			"routing.mesh.io/dynamic-routing-service": "dynamic-svc",
		},
	}

	aliasesMap := map[string]string{
		"routing.mesh.io/enabled":                 "routing.mesh.io.example.com/enabled",
		"routing.mesh.io/template":                "routing.mesh.io.example.com/template:routing.mesh.io.example.com/testing",
		"routing.mesh.io/dynamic-routing-service": "routing.mesh.io.example.com/dynamic-routing-enabled",
	}

	testCases := []struct {
		name          string
		obj           metav1.ObjectMeta
		attribute     constants.Attribute
		aliasMap      map[string]string
		expectedValue string
		expectedError error
	}{
		{
			name:          "alias found",
			obj:           metaObj,
			attribute:     constants.Attribute("routing.mesh.io/enabled"),
			aliasMap:      aliasesMap,
			expectedValue: "true",
		},
		{
			name:          "multiple aliases present - find alias in specified order",
			obj:           metaObj,
			attribute:     constants.Attribute("routing.mesh.io/template"),
			aliasMap:      aliasesMap,
			expectedValue: "test-template",
		},
		{
			name:          "alias not found",
			obj:           metaObj,
			attribute:     constants.Attribute("no-match"),
			aliasMap:      aliasesMap,
			expectedValue: "",
		},
		{
			name:          "no alias specified - original attribute found",
			obj:           metaObj,
			attribute:     constants.Attribute("routing.mesh.io.example.com/enabled"),
			aliasMap:      make(map[string]string),
			expectedValue: "true",
		},
		{
			name:          "alias specified - original attribute found",
			obj:           metaObj,
			attribute:     constants.Attribute("routing.mesh.io/dynamic-routing-service"),
			aliasMap:      aliasesMap,
			expectedValue: "dynamic-svc",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			alias.Manager = alias.NewAliasManager(tc.aliasMap, tc.aliasMap)

			labelValue := GetLabelOrAlias(tc.attribute, tc.obj.GetAnnotations())
			assert.Equal(t, tc.expectedValue, labelValue)

			annotationValue := GetAnnotationOrAlias(tc.attribute, tc.obj.GetAnnotations())
			assert.Equal(t, tc.expectedValue, annotationValue)
		})
	}
}

func TestAnnotationHasPrefixOrAlias(t *testing.T) {
	metaObj1 := metav1.ObjectMeta{
		Name: "test-meta-object",
		Annotations: map[string]string{
			"routing.mesh.io.example.com/template": "true",
		},
	}

	metaObj2 := metav1.ObjectMeta{
		Name: "test-meta-object2",
		Annotations: map[string]string{
			"template.mesh.io.example.com/testing": "testing",
		},
	}

	annotationAliasesMap := map[string]string{
		"routing.mesh.io/": "routing.mesh.io.example.com/",
		"testing.mesh.io/": "template.mesh.io.example.com/:routing.mesh.io.example.com/",
	}

	testCases := []struct {
		name                string
		obj                 metav1.ObjectMeta
		attribute           constants.Attribute
		aliasMap            map[string]string
		expectedPrefixFound bool
		expectedSuffix      string
	}{
		{
			name:                "prefix alias found",
			obj:                 metaObj1,
			attribute:           constants.Attribute("routing.mesh.io/"),
			aliasMap:            annotationAliasesMap,
			expectedPrefixFound: true,
			expectedSuffix:      "template",
		},
		{
			name:                "alias specified - original prefix found",
			obj:                 metaObj1,
			attribute:           constants.Attribute("routing.mesh.io.example.com/"),
			aliasMap:            annotationAliasesMap,
			expectedPrefixFound: true,
			expectedSuffix:      "template",
		},
		{
			name:                "no alias specified - original prefix found",
			obj:                 metaObj2,
			attribute:           constants.Attribute("template.mesh.io.example.com/"),
			aliasMap:            nil,
			expectedPrefixFound: true,
			expectedSuffix:      "testing",
		},
		{
			name:      "prefix not found",
			obj:       metaObj1,
			attribute: constants.Attribute("something.io/"),
			aliasMap:  annotationAliasesMap,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			for key := range tc.obj.Annotations {
				alias.Manager = alias.NewAliasManager(nil, tc.aliasMap)

				hasPrefix, suffix := FindAnnotationSuffixByPrefixOrAlias(tc.attribute, key)
				assert.Equal(t, tc.expectedPrefixFound, hasPrefix)
				assert.Equal(t, tc.expectedSuffix, suffix)
			}
		})
	}
}

func TestNow(t *testing.T) {
	provider := NewRealTimeProvider()
	timeByProvider := provider.Now()

	assert.NotNil(t, timeByProvider)

	currentTime := time.Now()

	assert.True(t, currentTime.Unix() >= timeByProvider.UTC().Unix(), "Expected time to be >= current time in Unix timestamp")

}

func TestIsHeadlessService(t *testing.T) {
	testCases := []struct {
		name     string
		obj      metav1.Object
		expected bool
	}{
		{
			name: "headless service (ClusterIP=None)",
			obj: &corev1.Service{
				Spec: corev1.ServiceSpec{ClusterIP: "None"},
			},
			expected: true,
		},
		{
			name: "ClusterIP service",
			obj: &corev1.Service{
				Spec: corev1.ServiceSpec{ClusterIP: "10.0.0.1"},
			},
			expected: false,
		},
		{
			name: "service with empty ClusterIP",
			obj: &corev1.Service{
				Spec: corev1.ServiceSpec{},
			},
			expected: false,
		},
		{
			name:     "non-service object",
			obj:      &metav1.ObjectMeta{Name: "not-a-service"},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, IsHeadlessService(tc.obj))
		})
	}
}

func createEnvoyFilter(namespace string, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": constants.EnvoyFilterResource.GroupVersion().String(),
			"kind":       constants.EnvoyFilterKind.Kind,
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": namespace,
			},
		},
	}
}
