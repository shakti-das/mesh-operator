//go:build !ignore_test_utils

package kube_test

import (
	"fmt"
	"reflect"

	constants2 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/common/pkg/k8s/constants"

	meshv1alpha1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/generated/clientset/versioned/fake"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	"k8s.io/apimachinery/pkg/runtime/schema"

	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	istiofake "istio.io/client-go/pkg/clientset/versioned/fake"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	discoveryfake "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

type KubeClientBuilder struct {
	k8sClientClusterObjects     []runtime.Object
	istioClientClusterObjects   []runtime.Object
	dynamicClientClusterObjects []runtime.Object
	discoveryClientResources    []*metav1.APIResourceList
	mopClientClusterObjects     []runtime.Object
	clusterName                 string
}

func NewKubeClientBuilder() *KubeClientBuilder {
	istioNetworkingResources := metav1.APIResourceList{
		GroupVersion: schema.GroupVersion{Group: "networking.istio.io", Version: "v1alpha3"}.String(),
		APIResources: []metav1.APIResource{
			{
				Name: "virtualservices",
				Kind: constants.VirtualServiceKind.Kind,
			},
			{
				Name: "destinationrules",
				Kind: constants.DestinationRuleKind.Kind,
			},
			{
				Name: "envoyfilters",
				Kind: constants.EnvoyFilterKind.Kind,
			},
		},
	}

	argoResources := metav1.APIResourceList{
		GroupVersion: schema.GroupVersion{Group: "argoproj.io", Version: "v1alpha1"}.String(),
		APIResources: []metav1.APIResource{
			{
				Name: "rollouts",
				Kind: constants.RolloutKind.Kind,
			},
		},
	}

	meshOperatorResources := metav1.APIResourceList{
		GroupVersion: schema.GroupVersion{Group: "mesh.io", Version: "v1alpha1"}.String(),
		APIResources: []metav1.APIResource{
			{
				Name: "clustertrafficpolicies",
				Kind: constants2.ClusterTrafficPolicyKind.Kind,
			},
			{
				Name: "trafficshardingpolicies",
				Kind: constants2.TrafficShardingPolicyKind.Kind,
			},
			{
				Name: "meshoperators",
				Kind: constants2.MeshOperatorKind.Kind,
			},
			{
				Name: "sidecarconfigs",
				Kind: constants.SidecarConfigKind.Kind,
			},
		},
	}

	knativeIngressResources := metav1.APIResourceList{
		GroupVersion: constants.KnativeIngressResource.GroupVersion().String(),
		APIResources: []metav1.APIResource{
			{
				Name: "ingresses",
				Kind: constants.KnativeIngressKind.Kind,
			},
		},
	}

	knativeServerlessServiceResources := metav1.APIResourceList{
		GroupVersion: constants.KnativeServerlessServiceResource.GroupVersion().String(),
		APIResources: []metav1.APIResource{
			{
				Name: "serverlessservices",
				Kind: constants.KnativeServerlessServiceKind.Kind,
			},
		},
	}
	return &KubeClientBuilder{
		discoveryClientResources:    []*metav1.APIResourceList{&istioNetworkingResources, &argoResources, &meshOperatorResources, &knativeIngressResources, &knativeServerlessServiceResources},
		k8sClientClusterObjects:     []runtime.Object{},
		istioClientClusterObjects:   []runtime.Object{},
		dynamicClientClusterObjects: []runtime.Object{},
		mopClientClusterObjects:     []runtime.Object{},
	}
}

func (b *KubeClientBuilder) SetClusterName(clusterName string) *KubeClientBuilder {
	b.clusterName = clusterName
	return b
}

func (b *KubeClientBuilder) AddK8sObjects(obj ...runtime.Object) *KubeClientBuilder {
	b.k8sClientClusterObjects = appendNonNils(b.k8sClientClusterObjects, obj...)
	return b
}

func (b *KubeClientBuilder) AddIstioObjects(obj ...runtime.Object) *KubeClientBuilder {
	b.istioClientClusterObjects = appendNonNils(b.istioClientClusterObjects, obj...)
	return b
}

func (b *KubeClientBuilder) AddDynamicClientObjects(obj ...runtime.Object) *KubeClientBuilder {
	b.dynamicClientClusterObjects = appendNonNils(b.dynamicClientClusterObjects, obj...)
	return b
}

func (b *KubeClientBuilder) AddMopClientObjects(obj ...runtime.Object) *KubeClientBuilder {
	b.mopClientClusterObjects = appendNonNils(b.mopClientClusterObjects, obj...)
	return b
}

func appendNonNils(list []runtime.Object, obj ...runtime.Object) []runtime.Object {
	result := list
	for _, object := range obj {
		reflect.ValueOf(object).IsNil()
		if object != nil && !reflect.ValueOf(object).IsNil() {
			result = append(result, object)
		}
	}
	return result
}

func (b *KubeClientBuilder) Build() kube.Client {
	k8sClient := k8sfake.NewSimpleClientset(b.k8sClientClusterObjects...)
	istioClient := istiofake.NewSimpleClientset(b.istioClientClusterObjects...)

	scheme := getScheme()
	listMapping := map[schema.GroupVersionResource]string{
		constants.RolloutResource:                  "rolloutsList",
		constants.KnativeIngressResource:           "ingressesList",
		constants.KnativeServerlessServiceResource: "serverlessservicesList",
		constants2.ClusterTrafficPolicyResource:    "clustertrafficpoliciesList",
		constants2.TrafficShardingPolicyResource:   "trafficshardingpoliciesList",
		constants.SidecarConfigResource:            "sidecarconfigsList",
	}
	allDynamicObjects := append(b.dynamicClientClusterObjects, b.mopClientClusterObjects...)
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listMapping, allDynamicObjects...)

	discoveryClient := &discoveryfake.FakeDiscovery{Fake: &k8stesting.Fake{}}
	for _, res := range b.discoveryClientResources {
		discoveryClient.Resources = append(discoveryClient.Resources, res)
	}
	mopApiClient := fake.NewSimpleClientset(b.mopClientClusterObjects...)

	kubeClient := FakeClient{
		KubeClient:      k8sClient,
		IstioClient:     istioClient,
		DynamicClient:   dynamicClient,
		DiscoveryClient: discoveryClient,
		MopClient:       mopApiClient,
		clusterName:     b.clusterName}

	kubeClient.InitFactory()

	// Mention the necessary informers to avoid doing it in tests.
	// This is important, as it tells corresponding kube-informer factories to initialize the informers.
	kubeClient.mopInformerFactory.Mesh().V1alpha1().MeshOperators().Lister()
	kubeClient.kubeInformerFactory.Core().V1().Services().Lister()

	return &kubeClient
}

func getScheme() *runtime.Scheme {
	scheme := runtime.NewScheme()
	err := istiov1alpha3.AddToScheme(scheme)
	if err != nil {
		panic(fmt.Sprintf("Could not add istio objects to schema. Error: %q", err))
	}
	err = v1.AddToScheme(scheme)
	if err != nil {
		panic(fmt.Sprintf("Could not add k8s v1 objects to schema. Error: %q", err))
	}
	err = meshv1alpha1.AddToScheme(scheme)
	if err != nil {
		panic(fmt.Sprintf("Could not add mesh.io objects to schema. Error: %q", err))
	}
	return scheme
}
