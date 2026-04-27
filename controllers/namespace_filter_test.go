package controllers

import (
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"

	kubeinformers "k8s.io/client-go/informers"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestIsMopEnabledNamespace(t *testing.T) {
	testCases := []struct {
		name                string
		namespaceExists     bool
		allNamespaces       bool
		passNamespaceObject bool
		labels              map[string]string
		expectedResult      bool
	}{
		{
			name:            "NonExistentNamespace",
			namespaceExists: false,
			allNamespaces:   false,
			expectedResult:  false,
		},
		{
			name:            "MopAndIstioDisabled",
			namespaceExists: true,
			allNamespaces:   false,
			labels:          map[string]string{constants.IstioInjectionLabel: "disabled"},
			expectedResult:  false,
		},
		{
			name:            "MopDisabledIstioEnabled",
			namespaceExists: true,
			allNamespaces:   false,
			labels:          map[string]string{},
			expectedResult:  false,
		},
		{
			name:            "MopDisabledIstioExplicitlyEnabled",
			namespaceExists: true,
			allNamespaces:   false,
			labels:          map[string]string{constants.IstioInjectionLabel: "enabled"},
			expectedResult:  false,
		},
		{
			name:            "MopEnabledIstioDisabled",
			namespaceExists: true,
			allNamespaces:   false,
			labels:          map[string]string{constants.MeshOperatorEnabled: "true", constants.IstioInjectionLabel: "disabled"},
			expectedResult:  false,
		},
		{
			name:            "MopEnabledIstioImplicitlyEnabled",
			namespaceExists: true,
			allNamespaces:   false,
			labels:          map[string]string{constants.MeshOperatorEnabled: "true"},
			expectedResult:  false,
		},
		{
			name:            "MopAndIstioExplicitlyEnabled",
			namespaceExists: true,
			allNamespaces:   false,
			labels:          map[string]string{constants.MeshOperatorEnabled: "true", constants.IstioInjectionLabel: "enabled"},
			expectedResult:  true,
		},
		{
			name:            "AllNsIstioDisabled",
			allNamespaces:   true,
			namespaceExists: true,
			labels:          map[string]string{constants.IstioInjectionLabel: "disabled"},
			expectedResult:  false,
		},
		{
			name:            "AllNsIstioImplicitlyEnabled",
			allNamespaces:   true,
			namespaceExists: true,
			labels:          map[string]string{},
			expectedResult:  true,
		},
		{
			name:            "AllNsIstioExplicitlyEnabled",
			allNamespaces:   true,
			namespaceExists: true,
			labels:          map[string]string{constants.IstioInjectionLabel: "enabled"},
			expectedResult:  true,
		},
		{
			name:            "AllNsMopExplicitlyDisabled",
			allNamespaces:   true,
			namespaceExists: true,
			labels:          map[string]string{constants.MeshOperatorEnabled: "false"},
			expectedResult:  false,
		},
		{
			name:                "NamespaceObjectMopAndIstioEnabled",
			namespaceExists:     true,
			allNamespaces:       false,
			passNamespaceObject: true,
			labels:              map[string]string{constants.MeshOperatorEnabled: "true", constants.IstioInjectionLabel: "enabled"},
			expectedResult:      true,
		},
		{
			name:                "NamespaceObjectMopDisabled",
			namespaceExists:     true,
			allNamespaces:       false,
			passNamespaceObject: true,
			labels:              map[string]string{constants.IstioInjectionLabel: "enabled"},
			expectedResult:      false,
		},
		{
			name:                "NamespaceObjectAllNsImplicitlyEnabled",
			namespaceExists:     true,
			allNamespaces:       true,
			passNamespaceObject: true,
			labels:              map[string]string{},
			expectedResult:      true,
		},
	}
	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			namespace := corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test-namespace",
					Labels: tc.labels,
				},
			}
			client := k8sfake.NewSimpleClientset(&namespace)
			kubeInformerFactory := kubeinformers.NewSharedInformerFactory(client, 1)
			nsInformer := kubeInformerFactory.Core().V1().Namespaces()
			filter := NewNamespaceFilter(zaptest.NewLogger(t).Sugar(), tc.allNamespaces, nsInformer)

			filter.Run(stopCh)

			var obj interface{}
			if tc.passNamespaceObject {
				obj = &namespace
			} else {
				obj = &serviceObject
			}
			isNamespaceMopEnabled := filter.IsNamespaceMopEnabledForObject(obj)

			assert.Equal(t, tc.expectedResult, isNamespaceMopEnabled)
		})
	}
}
