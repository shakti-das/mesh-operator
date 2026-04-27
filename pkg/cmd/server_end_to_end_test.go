package cmd

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/secretdiscovery"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"

	"github.com/spf13/cobra"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating_test"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	kubeutil "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
	"k8s.io/apimachinery/pkg/runtime"

	"go.uber.org/zap"
	istiov1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"

	networkingv1alpha3 "istio.io/api/networking/v1alpha3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

const (
	TemplatesLocation                    = "projects/services/servicemesh/mesh-operator/pkg/testdata/end-to-end-templates"
	RenderingConfigFile                  = "projects/services/servicemesh/mesh-operator/pkg/testdata/additional-object/config.yaml"
	MopEnabledNamespaceName              = "mop-namespace"
	MopEnabledNamespaceNameRemoteCluster = "mop-namespace-c2"
	SecretWatchNamespace                 = "mesh-control-plane"
	ServiceName                          = "test-service"
	RemoteServiceName                    = "test-service-c2"
	TestInformerRescanPeriod             = time.Second * 1
)

var (
	initialStsReplicas = int32(2)
	updatedStsReplicas = int32(3)

	dateInPast   = metav1.Time{Time: time.Now().Add(-100 * time.Hour)}
	dateInFuture = metav1.Time{Time: time.Now().Add(100 * time.Hour)}

	mopEnabledNamespace = corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: MopEnabledNamespaceName,
			Labels: map[string]string{
				"operator.mesh.io/enabled": "true",
				"istio-injection":          "enabled",
			},
		},
	}

	mopEnabledNamespaceRemoteCluster = corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: MopEnabledNamespaceNameRemoteCluster,
			Labels: map[string]string{
				"operator.mesh.io/enabled": "true",
				"istio-injection":          "enabled",
			},
		},
	}

	secrets1 = kube_test.CreateSecret("secrets1", SecretWatchNamespace, kube_test.ClusterCredential{ClusterID: "c1", Kubeconfig: []byte("kubeconfig-c1")})

	serviceObject = corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.ServiceKind.Kind,
			APIVersion: constants.ServiceResource.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              ServiceName,
			Namespace:         MopEnabledNamespaceName,
			Annotations:       map[string]string{},
			ResourceVersion:   "1",
			CreationTimestamp: dateInPast,
		},
	}

	bgServiceObject = corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.ServiceKind.Kind,
			APIVersion: constants.ServiceResource.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            ServiceName,
			Namespace:       MopEnabledNamespaceName,
			Annotations:     map[string]string{templateOverrideAnnotationAsStr: "default/bg-stateless"},
			ResourceVersion: "1",
		},
	}

	serviceObjectInRemoteCluster = corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.ServiceKind.Kind,
			APIVersion: constants.ServiceResource.Version,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              RemoteServiceName,
			Namespace:         MopEnabledNamespaceNameRemoteCluster,
			Annotations:       map[string]string{},
			ResourceVersion:   "1",
			CreationTimestamp: dateInFuture,
		},
	}

	serviceEntry = istiov1alpha3.ServiceEntry{
		TypeMeta: metav1.TypeMeta{
			Kind:       constants.ServiceEntryKind.Kind,
			APIVersion: constants.ServiceEntryResource.GroupVersion().String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:            ServiceName,
			Namespace:       MopEnabledNamespaceName,
			Annotations:     map[string]string{},
			ResourceVersion: "1",
		},
		Spec: networkingv1alpha3.ServiceEntry{
			Hosts:    []string{"test-host"},
			Location: networkingv1alpha3.ServiceEntry_MESH_INTERNAL,
		},
	}

	stsObject = appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-sts",
			Namespace:       MopEnabledNamespaceName,
			ResourceVersion: "1",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &initialStsReplicas,
			ServiceName: ServiceName,
		},
	}

	rolloutObject = kube_test.NewRolloutBuilder("test-rollout", MopEnabledNamespaceName).SetAnnotations(map[string]string{constants.BgActiveServiceAnnotation: ServiceName}).SetTemplateLabel(map[string]string{constants.IstioSideCarInjectLabel: "true"}).SetNestedMap(map[string]interface{}{}, "spec", "strategy", "canary").Build()

	templateOverrideAnnotationAsStr = common.GetStringForAttribute(constants.TemplateOverrideAnnotation)
)

// TestInitFlow - an end-to-end test that, depending on the scenario:
// - creates a fake k8s context
// - populates it with a requested initial state (NS, Service, ServiceEntries etc.)
// - starts the mesh-operator server through the CLI command instance
// - waits for the expected rendering to occur
// - verify the expected rendering results
func TestInitFlow(t *testing.T) {
	testCases := []struct {
		name                         string
		initialObjectsInCluster      []runtime.Object
		initialIstioObjectsInCluster []runtime.Object
		unstructuredObjectsInCluster []runtime.Object
		expectedConfigMetadata       []templating_test.GeneratedConfigMetadata
	}{
		{
			name: "ExistingServiceInMopEnabledNamespace",
			initialObjectsInCluster: []runtime.Object{
				&mopEnabledNamespace,
				&serviceObject,
			},
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         strings.Join([]string{MopEnabledNamespaceName, ServiceName}, "/"),
					TemplatesRendered: []string{"default_default_destinationrule", "default_default_virtualservice"},
					Metadata:          map[string]string{},
					Owners:            getMsmOwnerRef(ServiceName, "Service"),
				},
			},
		},
		{
			name: "ExistingServiceEntryInMopEnabledNamespace",
			initialObjectsInCluster: []runtime.Object{
				&mopEnabledNamespace,
			},
			initialIstioObjectsInCluster: []runtime.Object{
				&serviceEntry,
			},
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         strings.Join([]string{MopEnabledNamespaceName, ServiceName}, "/"),
					TemplatesRendered: []string{"default_external-service_destinationrule", "default_external-service_virtualservice"},
					Metadata:          map[string]string{"enableMaxConnectionDurationFilter": "false"},
					Owners:            getMsmOwnerRef(ServiceName, "ServiceEntry"),
				},
			},
		},
		// Even though on init, STS and Service object produce two events, those are de-duped on the workqueue into one.
		{
			name: "ExistingSts",
			initialObjectsInCluster: []runtime.Object{
				&mopEnabledNamespace,
				&serviceObject,
				&stsObject,
			},
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         strings.Join([]string{MopEnabledNamespaceName, ServiceName}, "/"),
					TemplatesRendered: []string{"default_default_destinationrule", "default_default_virtualservice"},
					Metadata: map[string]string{
						"statefulSetReplicaName":   "test-sts",
						"statefulSetReplicas":      "2",
						"statefulSetOrdinalsStart": "0",
						"statefulSets":             "[{\"statefulSetReplicaName\":\"test-sts\",\"statefulSetReplicas\":\"2\"}]",
					},
					Owners: getMsmOwnerRef(ServiceName, "Service"),
				},
			},
		},
		//Even though on init, Rollout and Service object produce two events, those are de-duped on the workqueue into one.
		{
			name: "ExistingRolloutObjInMopEnabledNamespace",
			initialObjectsInCluster: []runtime.Object{
				&mopEnabledNamespace,
				&bgServiceObject,
			},
			unstructuredObjectsInCluster: []runtime.Object{rolloutObject},
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         strings.Join([]string{MopEnabledNamespaceName, ServiceName}, "/"),
					TemplatesRendered: []string{"default_bg-stateless_destinationrule", "default_bg-stateless_virtualservice"},
					Metadata:          map[string]string{},
					Owners:            getMsmOwnerRef(ServiceName, "Service"),
				},
			},
		},
	}

	for _, tc := range testCases {
		test := func(t *testing.T) {
			signalChan := make(chan os.Signal, 1)
			signalChannelFactory := func() chan os.Signal {
				return signalChan
			}

			applicator := &templating_test.TestableApplicator{}

			client := kube_test.NewKubeClientBuilder().
				AddK8sObjects(tc.initialObjectsInCluster...).
				AddIstioObjects(tc.initialIstioObjectsInCluster...).
				AddDynamicClientObjects(tc.unstructuredObjectsInCluster...).
				Build()

			fakeBuildClientFromKubeConfigHandler := func(_ *zap.SugaredLogger, _ string, _ []byte, _ time.Duration, _ string, _ int, _ *prometheus.Registry, _ float32, _ int) (kubeutil.Client, error) {
				return client, nil
			}

			fakeClient := (client).(*kube_test.FakeClient)

			command := getCommand(*fakeClient, applicator, signalChannelFactory, fakeBuildClientFromKubeConfigHandler)

			go func() {
				err := command.Execute()
				if err != nil {
					panic(err)
				}
			}()

			applicator.WaitForRender(len(tc.expectedConfigMetadata))
			signalChan <- os.Interrupt
			applicator.AssertResults(t, tc.expectedConfigMetadata)
		}

		t.Run(tc.name, test)
	}
}

func TestUpdateInRuntimeFlow(t *testing.T) {
	rolloutObjectWithUpdatedSpec := rolloutObject.DeepCopy()
	_ = unstructured.SetNestedField(rolloutObjectWithUpdatedSpec.Object, "2", "spec", "replicas")
	rolloutObject.SetGeneration(2)

	rolloutObjectWithUpdatedMetadata := rolloutObject.DeepCopy()
	rolloutObject.SetLabels(map[string]string{"some-label": "value"})
	rolloutObjectWithUpdatedMetadata.SetResourceVersion("2")

	testCases := []struct {
		name                         string
		initialObjectsInCluster      []runtime.Object
		initialIstioObjectsInCluster []runtime.Object
		unstructuredObjectsInCluster []runtime.Object
		initConfigCount              int

		serviceToUpdate      *corev1.Service
		serviceEntryToUpdate *istiov1alpha3.ServiceEntry
		stsToUpdate          *appsv1.StatefulSet
		rolloutToUpdate      *unstructured.Unstructured

		expectedConfigMetadata []templating_test.GeneratedConfigMetadata
	}{
		{
			name: "UpdateService",
			initialObjectsInCluster: []runtime.Object{
				&mopEnabledNamespace,
				&serviceObject,
			},
			initConfigCount: 1,
			serviceToUpdate: &corev1.Service{
				TypeMeta: metav1.TypeMeta{
					Kind:       constants.ServiceKind.Kind,
					APIVersion: constants.ServiceResource.Version,
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:            ServiceName,
					Namespace:       MopEnabledNamespaceName,
					Annotations:     map[string]string{},
					ResourceVersion: "2",
				},
			},
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         "mop-namespace/test-service",
					TemplatesRendered: []string{"default_default_destinationrule", "default_default_virtualservice"},
					Metadata:          map[string]string{},
					Owners:            getMsmOwnerRef("test-service", "Service"),
				},
			},
		},
		{
			name:                         "UpdateServiceEntry",
			initialObjectsInCluster:      []runtime.Object{&mopEnabledNamespace},
			initialIstioObjectsInCluster: []runtime.Object{&serviceEntry},
			initConfigCount:              1,
			serviceEntryToUpdate: &istiov1alpha3.ServiceEntry{
				TypeMeta: metav1.TypeMeta{
					Kind:       constants.ServiceEntryKind.Kind,
					APIVersion: constants.ServiceEntryResource.GroupVersion().String(),
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:        ServiceName,
					Namespace:   MopEnabledNamespaceName,
					Annotations: map[string]string{},
				},
				Spec: networkingv1alpha3.ServiceEntry{
					Hosts:    []string{"test-host-2"},
					Location: networkingv1alpha3.ServiceEntry_MESH_INTERNAL,
				},
			},
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         "mop-namespace/test-service",
					TemplatesRendered: []string{"default_external-service_destinationrule", "default_external-service_virtualservice"},
					Metadata:          map[string]string{"enableMaxConnectionDurationFilter": "false"},
					Owners:            getMsmOwnerRef(ServiceName, "ServiceEntry"),
				},
			},
		},
		{
			name: "UpdateSts",
			initialObjectsInCluster: []runtime.Object{
				&mopEnabledNamespace,
				&serviceObject,
				&stsObject,
			},
			initConfigCount: 1,
			stsToUpdate: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-sts",
					Namespace:  MopEnabledNamespaceName,
					Generation: 10,
				},
				Spec: appsv1.StatefulSetSpec{
					Replicas:    &updatedStsReplicas,
					ServiceName: ServiceName,
				},
			},
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         "mop-namespace/test-service",
					TemplatesRendered: []string{"default_default_destinationrule", "default_default_virtualservice"},
					Metadata: map[string]string{
						"statefulSetReplicaName":   "test-sts",
						"statefulSetReplicas":      "3",
						"statefulSetOrdinalsStart": "0",
						"statefulSets":             "[{\"statefulSetReplicaName\":\"test-sts\",\"statefulSetReplicas\":\"3\"}]",
					},
					Owners: getMsmOwnerRef("test-service", "Service"),
				},
			},
		},
	}
	for _, tc := range testCases {
		test := func(t *testing.T) {
			signalChan := make(chan os.Signal, 1)
			signalChannelFactory := func() chan os.Signal {
				return signalChan
			}
			applicator := &templating_test.TestableApplicator{}

			client := kube_test.NewKubeClientBuilder().
				AddK8sObjects(tc.initialObjectsInCluster...).
				AddIstioObjects(tc.initialIstioObjectsInCluster...).
				AddDynamicClientObjects(tc.unstructuredObjectsInCluster...).
				Build()

			fakeBuildClientFromKubeConfigHandler := func(_ *zap.SugaredLogger, _ string, _ []byte, _ time.Duration, _ string, _ int, _ *prometheus.Registry, _ float32, _ int) (kubeutil.Client, error) {
				return client, nil
			}

			fakeClient := (client).(*kube_test.FakeClient)

			command := getCommand(*fakeClient, applicator, signalChannelFactory, fakeBuildClientFromKubeConfigHandler)

			go func() {
				err := command.Execute()
				if err != nil {
					panic(err)
				}
			}()
			applicator.WaitForRender(tc.initConfigCount)
			applicator.Reset()

			if tc.serviceToUpdate != nil {
				_, _ = fakeClient.KubeClient.CoreV1().Services(MopEnabledNamespaceName).Update(context.TODO(), tc.serviceToUpdate, metav1.UpdateOptions{})
			}
			if tc.serviceEntryToUpdate != nil {
				_, _ = fakeClient.IstioClient.NetworkingV1alpha3().ServiceEntries(MopEnabledNamespaceName).Update(context.TODO(), tc.serviceEntryToUpdate, metav1.UpdateOptions{})
			}
			if tc.stsToUpdate != nil {
				_, _ = fakeClient.KubeClient.AppsV1().StatefulSets(MopEnabledNamespaceName).Update(context.TODO(), tc.stsToUpdate, metav1.UpdateOptions{})
			}
			if tc.rolloutToUpdate != nil {
				_, _ = fakeClient.DynamicClient.Resource(constants.RolloutResource).Namespace(MopEnabledNamespaceName).Update(context.TODO(), tc.rolloutToUpdate, metav1.UpdateOptions{})
			}

			applicator.WaitForRender(len(tc.expectedConfigMetadata))
			applicator.AssertResults(t, tc.expectedConfigMetadata)
		}

		t.Run(tc.name, test)
	}
}

// TestInitMultiClusterFlow - an end-to-end test that, depending on the scenario:
// - creates fake k8s context for primary and remote cluster
// - populates it with a requested initial state (Secrets, NS, Service)
// - starts the mesh-operator server through the CLI command instance
// - waits for the expected rendering to occur for primary and remote cluster
// - verify the expected rendering results
func TestInitMultiClusterFlow(t *testing.T) {
	testCases := []struct {
		name                    string
		objectsInPrimaryCluster []runtime.Object
		objectsInRemoteCluster  []runtime.Object
		expectedConfigMetadata  []templating_test.GeneratedConfigMetadata
	}{
		{
			name: "ExistingServiceInMopEnabledNamespace",
			objectsInPrimaryCluster: []runtime.Object{
				&mopEnabledNamespace,
				secrets1,
				&serviceObject,
			},
			objectsInRemoteCluster: []runtime.Object{
				&mopEnabledNamespaceRemoteCluster,
				&serviceObjectInRemoteCluster,
			},
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         strings.Join([]string{MopEnabledNamespaceName, ServiceName}, "/"),
					TemplatesRendered: []string{"default_default_destinationrule", "default_default_virtualservice"},
					Metadata:          map[string]string{},
					Owners:            getMsmOwnerRef(ServiceName, "Service"),
				},
				{
					ObjectKey:         strings.Join([]string{MopEnabledNamespaceNameRemoteCluster, RemoteServiceName}, "/"),
					TemplatesRendered: []string{"default_default_destinationrule", "default_default_virtualservice"},
					Metadata:          map[string]string{},
					Owners:            getMsmOwnerRef(RemoteServiceName, "Service"),
				},
			},
		},
	}

	for _, tc := range testCases {
		test := func(t *testing.T) {
			signalChan := make(chan os.Signal, 1)
			signalChannelFactory := func() chan os.Signal {
				return signalChan
			}

			applicator := &templating_test.TestableApplicator{}

			primaryClient := kube_test.NewKubeClientBuilder().
				AddK8sObjects(tc.objectsInPrimaryCluster...).
				Build()

			client := kube_test.NewKubeClientBuilder().
				AddK8sObjects(tc.objectsInRemoteCluster...).
				Build()

			fakeBuildClientFromKubeConfigHandler := func(_ *zap.SugaredLogger, _ string, _ []byte, _ time.Duration, _ string, _ int, _ *prometheus.Registry, _ float32, _ int) (kubeutil.Client, error) {
				return client, nil
			}

			primaryFakeClient := (primaryClient).(*kube_test.FakeClient)

			command := getCommand(*primaryFakeClient, applicator, signalChannelFactory, fakeBuildClientFromKubeConfigHandler)

			go func() {
				err := command.Execute()
				if err != nil {
					panic(err)
				}
			}()

			applicator.WaitForRender(len(tc.expectedConfigMetadata))
			signalChan <- os.Interrupt
			applicator.AssertResults(t, tc.expectedConfigMetadata)
		}

		t.Run(tc.name, test)
	}
}

// TestUpdateSecretsMultiClusterFlow - an end-to-end test that, depending on the scenario:
// - creates fake k8s context for primary and remote clusters
// - populates primary cluster with secret, ns and service resource, populate remote cluster with ns, svc resource
// - starts the mesh-operator server through the CLI command instance
// - waits for the expected rendering to occur for primary and remote cluster, verify the expected rendering results
// - trigger a secret object update, remote cluster gets updated and svc gets re-rendered after updated remote cluster starts
func TestUpdateSecretsMultiClusterFlow(t *testing.T) {
	testCases := []struct {
		name                                    string
		objectsInPrimaryCluster                 []runtime.Object
		objectsInRemoteCluster                  []runtime.Object
		serviceToUpdate                         *corev1.Service
		secretToUpdate                          *corev1.Secret
		expectedConfigMetadata                  []templating_test.GeneratedConfigMetadata
		expectedConfigMetadataAfterSecretUpdate []templating_test.GeneratedConfigMetadata
	}{
		{
			name: "ExistingServiceInMopEnabledNamespace",
			objectsInPrimaryCluster: []runtime.Object{
				&mopEnabledNamespace,
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "secrets1",
						Namespace: SecretWatchNamespace,
						Labels: map[string]string{
							constants.MultiClusterSecretLabel: "true",
						},
						ResourceVersion: "1",
					},
					Data: map[string][]byte{
						"c1": []byte("kubeconfig-c1"),
					},
				},
				&serviceObject,
			},
			objectsInRemoteCluster: []runtime.Object{
				&mopEnabledNamespaceRemoteCluster,
				&serviceObjectInRemoteCluster,
			},
			secretToUpdate: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "secrets1",
					Namespace: SecretWatchNamespace,
					Labels: map[string]string{
						constants.MultiClusterSecretLabel: "true",
					},
					ResourceVersion: "2",
				},
				Data: map[string][]byte{
					"c1": []byte("kubeconfig-c1-updated"),
				},
			},
			expectedConfigMetadata: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         strings.Join([]string{MopEnabledNamespaceName, ServiceName}, "/"),
					TemplatesRendered: []string{"default_default_destinationrule", "default_default_virtualservice"},
					Metadata:          map[string]string{},
					Owners:            getMsmOwnerRef(ServiceName, "Service"),
				},
				{
					ObjectKey:         strings.Join([]string{MopEnabledNamespaceNameRemoteCluster, RemoteServiceName}, "/"),
					TemplatesRendered: []string{"default_default_destinationrule", "default_default_virtualservice"},
					Metadata:          map[string]string{},
					Owners:            getMsmOwnerRef(RemoteServiceName, "Service"),
				},
			},
			expectedConfigMetadataAfterSecretUpdate: []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         strings.Join([]string{MopEnabledNamespaceNameRemoteCluster, RemoteServiceName}, "/"),
					TemplatesRendered: []string{"default_default_destinationrule", "default_default_virtualservice"},
					Metadata:          map[string]string{},
					Owners:            getMsmOwnerRef(RemoteServiceName, "Service"),
				},
			},
		},
	}

	for _, tc := range testCases {
		test := func(t *testing.T) {
			signalChan := make(chan os.Signal, 1)
			signalChannelFactory := func() chan os.Signal {
				return signalChan
			}

			applicator := &templating_test.TestableApplicator{}

			primaryClient := kube_test.NewKubeClientBuilder().
				AddK8sObjects(tc.objectsInPrimaryCluster...).
				Build()

			fakeBuildClientFromKubeConfigHandler := func(_ *zap.SugaredLogger, _ string, _ []byte, _ time.Duration, _ string, _ int, _ *prometheus.Registry, _ float32, _ int) (kubeutil.Client, error) {
				client := kube_test.NewKubeClientBuilder().
					AddK8sObjects(tc.objectsInRemoteCluster...).
					Build()
				return client, nil
			}

			primaryFakeClient := (primaryClient).(*kube_test.FakeClient)

			command := getCommand(*primaryFakeClient, applicator, signalChannelFactory, fakeBuildClientFromKubeConfigHandler)

			go func() {
				err := command.Execute()
				if err != nil {
					panic(err)
				}
			}()

			applicator.WaitForRender(len(tc.expectedConfigMetadata))
			applicator.AssertResults(t, tc.expectedConfigMetadata)
			applicator.Reset()

			if tc.secretToUpdate != nil {
				_, _ = primaryFakeClient.KubeClient.CoreV1().
					Secrets(SecretWatchNamespace).Update(context.TODO(), tc.secretToUpdate, metav1.UpdateOptions{})
			}

			applicator.WaitForRender(len(tc.expectedConfigMetadataAfterSecretUpdate))
			signalChan <- os.Interrupt
			applicator.AssertResults(t, tc.expectedConfigMetadataAfterSecretUpdate)
		}

		t.Run(tc.name, test)
	}
}

// TestInitFlow - an end-to-end test that, depending on the scenario:
// - creates a fake k8s context
// - populates a k8s service object
// - starts the mesh-operator server through the CLI command instance
// - waits for the expected rendering to occur
// - create SE with same name as k8s
// - verify the expected output
func TestServiceAndServiceEntryWithSameNameInSameNamespace(t *testing.T) {
	test := func(t *testing.T) {
		var (
			initialObjectsInCluster = []runtime.Object{&mopEnabledNamespace, &serviceObject}
			expectedConfigMetadata  = []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         strings.Join([]string{MopEnabledNamespaceName, ServiceName}, "/"),
					TemplatesRendered: []string{"default_default_destinationrule", "default_default_virtualservice"},
					Metadata:          map[string]string{},
					Owners:            getMsmOwnerRef(ServiceName, "Service"),
				},
			}
			expectedConfigMetadataAfterSeCreation = []templating_test.GeneratedConfigMetadata{
				{
					ObjectKey:         strings.Join([]string{MopEnabledNamespaceName, ServiceName}, "/"),
					TemplatesRendered: []string{"default_external-service_destinationrule", "default_external-service_virtualservice"},
					Metadata:          map[string]string{"enableMaxConnectionDurationFilter": "false"},
					Owners:            getMsmOwnerRef(ServiceName, "ServiceEntry"),
				},
			}
		)

		signalChan := make(chan os.Signal, 1)
		signalChannelFactory := func() chan os.Signal {
			return signalChan
		}

		applicator := &templating_test.TestableApplicator{}
		client := kube_test.NewKubeClientBuilder().AddK8sObjects(initialObjectsInCluster...).Build()

		fakeBuildClientFromKubeConfigHandler := func(_ *zap.SugaredLogger, _ string, _ []byte, _ time.Duration, _ string, _ int, _ *prometheus.Registry, _ float32, _ int) (kubeutil.Client, error) {
			return client, nil
		}

		fakeClient := (client).(*kube_test.FakeClient)

		command := getCommand(*fakeClient, applicator, signalChannelFactory, fakeBuildClientFromKubeConfigHandler)

		go func() {
			err := command.Execute()
			if err != nil {
				panic(err)
			}
		}()

		applicator.WaitForRender(len(expectedConfigMetadata))
		applicator.AssertResults(t, expectedConfigMetadata)
		applicator.Reset()

		_, _ = fakeClient.IstioClient.NetworkingV1alpha3().ServiceEntries(MopEnabledNamespaceName).Create(context.TODO(), &serviceEntry, metav1.CreateOptions{})

		applicator.WaitForRender(len(expectedConfigMetadataAfterSeCreation))
		signalChan <- os.Interrupt
		applicator.AssertResults(t, expectedConfigMetadataAfterSeCreation)
	}

	t.Run("TestServiceAndServiceEntryWithSameNameInSameNamespace", test)
}

func getCommand(fakeClient kube_test.FakeClient, applicator *templating_test.TestableApplicator,
	signalChannelFactory func() chan os.Signal,
	buildClientFunc func(logger *zap.SugaredLogger, clusterName string, kubeConfig []byte, rescanPeriod time.Duration, selector string, informerCacheSyncTimeoutSeconds int, metricsRegistry *prometheus.Registry, qps float32, burst int) (kubeutil.Client, error)) *cobra.Command {

	primaryFakeClient := &secretdiscovery.FakeClient{KubeClient: fakeClient.KubeClient}
	primaryCluster := secretdiscovery.NewFakeCluster(primaryClusterName, true, primaryFakeClient)
	remoteClusters := []secretdiscovery.DynamicCluster{}
	discovery := secretdiscovery.NewFakeDynamicDiscovery(primaryCluster, remoteClusters)

	command := NewServerCommand(
		func(logger *zap.SugaredLogger, clusterName string, restConfig *rest.Config, rescanPeriod time.Duration, selector string, _ int, _ *prometheus.Registry) kubeutil.Client {
			return &fakeClient
		},
		func(logger *zap.SugaredLogger, config *rest.Config, stopCh <-chan struct{}) (secretdiscovery.Discovery, error) {
			return discovery, nil
		},
		NewTestableApplicatorFactory(applicator),
		signalChannelFactory,
		TestInformerRescanPeriod,
		buildClientFunc)

	command.SetArgs([]string{
		"--template-paths=" + TemplatesLocation,
		"--leader-elect=false",
		"--log-level=debug",
		"--management-port=-1",
		"--rendering-config=" + RenderingConfigFile,
		"--default-template-type=Service=default/default,ServiceEntry=default/external-service",
		"--selector=!serving.knative.dev/service"})

	return command
}

func NewTestableApplicatorFactory(applicator *templating_test.TestableApplicator) templating.ApplicatorFactory {
	applicator.AppliedResults = []templating_test.GeneratedConfigMetadata{}
	return func(logger *zap.SugaredLogger, primaryClient kubeutil.Client, dryRun bool) templating.Applicator {
		return applicator
	}
}

func getMsmOwnerRef(resourceName string, kind string) []metav1.OwnerReference {
	return []metav1.OwnerReference{
		{
			APIVersion: v1alpha1.ApiVersion,
			Kind:       v1alpha1.MsmKind.Kind,
			Name:       common.GetRecordNameSuffixedByKind(resourceName, kind),
		},
	}
}
