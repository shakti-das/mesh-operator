package controllers

import (
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	coretesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/controller/priorityqueue"

	"github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	metricstesting "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics/testing"
	"github.com/istio-ecosystem/mesh-operator/pkg/common/ocm"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/istio-ecosystem/mesh-operator/pkg/rollout"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating"
)

func TestGetEnabledServicesOnly(t *testing.T) {
	testSvc := kube_test.NewServiceBuilder("test-object", "test-ns").Build()
	disabledSvc := kube_test.NewServiceBuilder("test-object", "test-ns").
		SetAnnotations(map[string]string{
			string(constants.RoutingConfigEnabledAnnotation): "false",
		}).
		Build()

	testCases := []struct {
		name           string
		services       map[string]*corev1.Service
		expectedResult map[string]*corev1.Service
	}{
		{
			name:           "empty",
			services:       map[string]*corev1.Service{},
			expectedResult: map[string]*corev1.Service{},
		},
		{
			name: "one enabled service",
			services: map[string]*corev1.Service{
				"cluster-1": &testSvc,
			},
			expectedResult: map[string]*corev1.Service{
				"cluster-1": &testSvc,
			},
		},
		{
			name: "one disabled service",
			services: map[string]*corev1.Service{
				"cluster-1": &disabledSvc,
			},
			expectedResult: map[string]*corev1.Service{},
		},
		{
			name: "mix",
			services: map[string]*corev1.Service{
				"cluster-1": &testSvc,
				"cluster-2": &disabledSvc,
			},
			expectedResult: map[string]*corev1.Service{
				"cluster-1": &testSvc,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			actualResult := GetEnabledServicesOnly(tc.services)

			assert.Equal(t, tc.expectedResult, actualResult)
		})
	}
}

func TestCreateDeploymentObjectHandler(t *testing.T) {
	deployment := kube_test.NewDeploymentBuilder("test-ns", "test-deployment").
		SetLabels(map[string]string{"deployment-label1": "deployment-value1"}).
		SetTemplateLabels(map[string]string{"label1": "value1"}).
		SetGeneration(1).Build()

	updatedTemplateLabels := deployment.DeepCopy()
	updatedTemplateLabels.Spec.Template.Labels = map[string]string{"label2": "value2"}
	updatedTemplateLabels.Generation = 2

	updatedLabels := deployment.DeepCopy()
	updatedLabels.SetLabels(map[string]string{"updated-deployment-label1": "updated-deployment-value1"})
	// Note, generation doesn't change in this case

	noRelevantUpdatesDeployment := deployment.DeepCopy()
	noRelevantUpdatesDeployment.Generation = 2
	noRelevantUpdatesDeployment.SetAnnotations(map[string]string{"unrelated-annotation": "annotation-value"})

	testCases := []struct {
		name            string
		object          interface{}
		updatedObject   interface{}
		enqueueExpected bool
	}{
		{
			name:            "Template labels changed",
			object:          deployment,
			updatedObject:   updatedTemplateLabels,
			enqueueExpected: true,
		},
		{
			name:            "Labels changed",
			object:          deployment,
			updatedObject:   updatedLabels,
			enqueueExpected: true,
		},
		{
			name:            "No changes",
			object:          deployment,
			updatedObject:   noRelevantUpdatesDeployment,
			enqueueExpected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recordedObjects := []interface{}{}
			recordedEvents := []controllers_api.Event{}
			handler := CreateDeploymentObjectHandler(func(event controllers_api.Event, objs ...interface{}) {
				recordedObjects = append(recordedObjects, objs...)
				recordedEvents = append(recordedEvents, event)
			})

			// Run Add test
			handler.OnAdd(tc.object, false)

			assert.ElementsMatch(t, []interface{}{tc.object}, recordedObjects)
			assert.Equal(t, []controllers_api.Event{controllers_api.EventAdd}, recordedEvents)

			//Run Delete test
			recordedObjects = []interface{}{}
			recordedEvents = []controllers_api.Event{}
			handler.OnDelete(tc.object)

			assert.ElementsMatch(t, []interface{}{tc.object}, recordedObjects)
			assert.Equal(t, []controllers_api.Event{controllers_api.EventDelete}, recordedEvents)

			// Run Update test
			recordedObjects = []interface{}{}
			recordedEvents = []controllers_api.Event{}
			handler.OnUpdate(tc.object, tc.updatedObject)

			if tc.enqueueExpected {
				assert.ElementsMatch(t, []interface{}{tc.object, tc.updatedObject}, recordedObjects)
				assert.ElementsMatch(t, []controllers_api.Event{controllers_api.EventUpdate}, recordedEvents)
			} else {
				assert.Equal(t, 0, len(recordedObjects))
			}
		})
	}
}

func TestCreateStsObjectHandler(t *testing.T) {
	sts := kube_test.CreateStatefulSet("test-ns", "test-sts", "test-object", 2)
	sts.SetGeneration(1)
	sts.Spec.Template.Labels = map[string]string{"label1": "value1"}

	stsUpdatedLabels := sts.DeepCopy()
	stsUpdatedLabels.SetGeneration(2)
	stsUpdatedLabels.Spec.Template.Labels = map[string]string{"label2": "value2"}

	var replicas int32 = 10
	stsUpdatedReplicas := sts.DeepCopy()
	stsUpdatedReplicas.SetGeneration(2)
	stsUpdatedReplicas.Spec.Replicas = &replicas

	stsUpdatedOrdinalsStart := sts.DeepCopy()
	stsUpdatedOrdinalsStart.SetGeneration(2)
	var ordinalsStart int32 = 5
	stsUpdatedOrdinalsStart.Spec.Ordinals = &appsv1.StatefulSetOrdinals{
		Start: ordinalsStart,
	}

	stsNoRelevantChanges := sts.DeepCopy()
	stsNoRelevantChanges.SetGeneration(2)

	testCases := []struct {
		name            string
		object          interface{}
		updatedObject   interface{}
		enqueueExpected bool
	}{
		{
			name:            "Template labels changed",
			object:          sts,
			updatedObject:   stsUpdatedLabels,
			enqueueExpected: true,
		},
		{
			name:            "Replicas changed",
			object:          sts,
			updatedObject:   stsUpdatedReplicas,
			enqueueExpected: true,
		},
		{
			name:            "Ordinals start changed",
			object:          sts,
			updatedObject:   stsUpdatedOrdinalsStart,
			enqueueExpected: true,
		},
		{
			name:            "No relevant changes",
			object:          sts,
			updatedObject:   stsNoRelevantChanges,
			enqueueExpected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			recordedObjects := []interface{}{}
			recordedEvents := []controllers_api.Event{}
			handler := CreateStsObjectHandler(func(event controllers_api.Event, objs ...interface{}) {
				recordedObjects = append(recordedObjects, objs...)
				recordedEvents = append(recordedEvents, event)
			})

			// Run Add test
			handler.OnAdd(tc.object, false)

			assert.ElementsMatch(t, []interface{}{tc.object}, recordedObjects)
			assert.Equal(t, []controllers_api.Event{controllers_api.EventAdd}, recordedEvents)

			//Run Delete test
			recordedObjects = []interface{}{}
			recordedEvents = []controllers_api.Event{}
			handler.OnDelete(tc.object)

			assert.ElementsMatch(t, []interface{}{tc.object}, recordedObjects)
			assert.Equal(t, []controllers_api.Event{controllers_api.EventDelete}, recordedEvents)

			// Run Update test
			recordedObjects = []interface{}{}
			recordedEvents = []controllers_api.Event{}
			handler.OnUpdate(tc.object, tc.updatedObject)

			if tc.enqueueExpected {
				assert.ElementsMatch(t, []interface{}{tc.object, tc.updatedObject}, recordedObjects)
				assert.ElementsMatch(t, []controllers_api.Event{controllers_api.EventUpdate}, recordedEvents)
			} else {
				assert.Equal(t, 0, len(recordedObjects))
			}
		})
	}
}

func TestReMutateRollouts(t *testing.T) {
	argoService := kube_test.NewServiceBuilder("test-object", "test-ns").
		SetAnnotations(map[string]string{
			string(constants.TemplateOverrideAnnotation): rollout.OptInServiceTemplateBG,
		}).Build()

	nonArgoService := kube_test.NewServiceBuilder("test-object", "test-ns").Build()

	rol := kube_test.NewRolloutBuilder("test-rollout1", argoService.Namespace).SetAnnotations(map[string]string{
		constants.BgActiveServiceAnnotation: argoService.Name,
	}).Build()
	otherRol := kube_test.NewRolloutBuilder("test-rollout2", "test-ns").SetAnnotations(map[string]string{
		constants.BgActiveServiceAnnotation: argoService.Name,
	}).Build()
	ocmManagedRollout := kube_test.NewRolloutBuilder("test-rollout1", argoService.Namespace).
		SetAnnotations(map[string]string{
			constants.BgActiveServiceAnnotation: argoService.Name,
		}).
		SetLabels(map[string]string{
			ocm.ManagedByLabel: ocm.ManagedByValue,
		}).Build()

	testCases := []struct {
		name                   string
		argoIntegrationEnabled bool
		ocmIntegrationEnabled  bool
		service                *corev1.Service
		rolloutsInCluster      []runtime.Object
		errorOnRender          bool
		errorOnPatch           bool
		emptyPatch             bool
		expectedActions        []coretesting.Action
	}{
		{
			name:                   "argo-integration not enabled",
			argoIntegrationEnabled: false,
			service:                &argoService,
		},
		{
			name:                   "non argo service",
			argoIntegrationEnabled: true,
			service:                &nonArgoService,
		},
		{
			name:                   "no rollouts for service",
			argoIntegrationEnabled: true,
			service:                &argoService,
		},
		{
			name:                   "ocm managed rollout",
			argoIntegrationEnabled: true,
			ocmIntegrationEnabled:  true,
			service:                &argoService,
			rolloutsInCluster:      []runtime.Object{ocmManagedRollout},
		},
		{
			name:                   "error on patch",
			argoIntegrationEnabled: true,
			service:                &argoService,
			rolloutsInCluster:      []runtime.Object{rol},
			errorOnPatch:           true,
			expectedActions: []coretesting.Action{
				coretesting.PatchActionImpl{
					ActionImpl: coretesting.ActionImpl{
						Namespace: "test-ns",
						Verb:      "patch",
						Resource:  constants.RolloutResource,
					},
					Name:      "test-rollout1",
					PatchType: types.JSONPatchType,
					Patch:     []byte("[\n   {\n      \"op\": \"replace\",\n      \"path\": \"/\",\n      \"value\": { }\n   }\n]\n"),
				},
			},
		},
		{
			name:                   "error on render",
			argoIntegrationEnabled: true,
			service:                &argoService,
			rolloutsInCluster:      []runtime.Object{rol},
			errorOnRender:          true,
		},
		{
			name:                   "successful patch",
			argoIntegrationEnabled: true,
			service:                &argoService,
			rolloutsInCluster:      []runtime.Object{rol},
			expectedActions: []coretesting.Action{
				coretesting.PatchActionImpl{
					ActionImpl: coretesting.ActionImpl{
						Namespace: "test-ns",
						Verb:      "patch",
						Resource:  constants.RolloutResource,
					},
					Name:      "test-rollout1",
					PatchType: types.JSONPatchType,
					Patch:     []byte("[\n   {\n      \"op\": \"replace\",\n      \"path\": \"/\",\n      \"value\": { }\n   }\n]\n"),
				},
			},
		},
		{
			name:                   "successful patch, multiple rollouts",
			argoIntegrationEnabled: true,
			service:                &argoService,
			rolloutsInCluster:      []runtime.Object{rol, otherRol},
			expectedActions: []coretesting.Action{
				coretesting.PatchActionImpl{
					ActionImpl: coretesting.ActionImpl{
						Namespace: "test-ns",
						Verb:      "patch",
						Resource:  constants.RolloutResource,
					},
					Name:      "test-rollout1",
					PatchType: types.JSONPatchType,
					Patch:     []byte("[\n   {\n      \"op\": \"replace\",\n      \"path\": \"/\",\n      \"value\": { }\n   }\n]\n"),
				},
				coretesting.PatchActionImpl{
					ActionImpl: coretesting.ActionImpl{
						Namespace: "test-ns",
						Verb:      "patch",
						Resource:  constants.RolloutResource,
					},
					Name:      "test-rollout2",
					PatchType: types.JSONPatchType,
					Patch:     []byte("[\n   {\n      \"op\": \"replace\",\n      \"path\": \"/\",\n      \"value\": { }\n   }\n]\n"),
				},
			},
		},
		{
			name:                   "empty patch",
			argoIntegrationEnabled: true,
			emptyPatch:             true,
			service:                &argoService,
			rolloutsInCluster:      []runtime.Object{rol},
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	clusterName := "testCluster1"
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.EnableArgoIntegration = tc.argoIntegrationEnabled
			features.EnableOcmIntegration = tc.ocmIntegrationEnabled
			defer func() {
				features.EnableArgoIntegration = false
				features.EnableOcmIntegration = false
			}()

			stopCh := make(chan struct{})
			defer close(stopCh)

			registry := prometheus.NewRegistry()
			client := kube_test.NewKubeClientBuilder().
				AddDynamicClientObjects(tc.rolloutsInCluster...).
				Build()
			fakeClient := (client).(*kube_test.FakeClient)

			templateManager := &TestableTemplateManager{templateContent: "function(service, rollout, context) [{op: 'replace', path: '/', value: {}}]"}
			if tc.errorOnRender {
				templateManager = &TestableTemplateManager{templateContent: "function(broken, template, context) {}"}
			}
			if tc.emptyPatch {
				templateManager = &TestableTemplateManager{templateContent: "function(service, rollout, context) if false then []"}
			}
			if tc.errorOnPatch {
				fakeClient.DynamicClient.PrependReactor("patch", "*", func(_ coretesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, fmt.Errorf("test error")
				})
			}

			informer := client.DynamicInformerFactory().ForResource(constants.RolloutResource).Informer()
			_ = rollout.AddServiceNameToRolloutIndexer(informer)

			_ = client.RunAndWait(stopCh, true, nil)

			err := reMutateRollouts(logger, templateManager, templateManager, registry, tc.service, clusterName, client)

			// Check metrics and client actions
			filteredMopClientActions := filterInformerActions(fakeClient.DynamicClient.Actions())
			assert.Equal(t, tc.expectedActions, filteredMopClientActions)

			if tc.errorOnRender || tc.errorOnPatch {
				assert.NotNil(t, err)

				labels := map[string]string{
					"error":   rollout.MutatingErrorPatching,
					"cluster": clusterName,
				}

				assertCounterWithLabels(t, registry, metrics.RolloutReMutateErrors, labels, 1)
			} else {
				assert.Nil(t, err)

				metricstesting.AssertEqualsCounterValue(t, registry, metrics.RolloutReMutateSuccess, float64(len(tc.expectedActions)))
			}
		})
	}
}

func TestReMutateRolloutsWithTemplateMetadata(t *testing.T) {
	originalValue := features.EnableTemplateMetadata
	features.EnableTemplateMetadata = true
	defer func() { features.EnableTemplateMetadata = originalValue }()

	metadataService := kube_test.NewServiceBuilder("test-object", "test-ns").
		SetAnnotations(map[string]string{
			string(constants.TemplateOverrideAnnotation): "example-coreapp/coreapp-argo-bg",
		}).Build()

	noMetadataService := kube_test.NewServiceBuilder("test-object", "test-ns").
		SetAnnotations(map[string]string{
			string(constants.TemplateOverrideAnnotation): "unknown/template",
		}).Build()

	rol := kube_test.NewRolloutBuilder("test-rollout1", metadataService.Namespace).SetAnnotations(map[string]string{
		constants.BgActiveServiceAnnotation: metadataService.Name,
	}).Build()

	testMetadata := map[string]*templating.TemplateMetadata{
		"example-coreapp_coreapp-argo-bg": {
			Rollout: &templating.RolloutMetadata{
				MutationTemplate:   "coreappBlueGreen",
				ReMutationTemplate: "coreappReMutateBlueGreen",
			},
		},
	}

	testCases := []struct {
		name              string
		service           *corev1.Service
		rolloutsInCluster []runtime.Object
		expectedActions   []coretesting.Action
	}{
		{
			name:              "metadata based re-mutation",
			service:           &metadataService,
			rolloutsInCluster: []runtime.Object{rol},
			expectedActions: []coretesting.Action{
				coretesting.PatchActionImpl{
					ActionImpl: coretesting.ActionImpl{
						Namespace: "test-ns",
						Verb:      "patch",
						Resource:  constants.RolloutResource,
					},
					Name:      "test-rollout1",
					PatchType: types.JSONPatchType,
					Patch:     []byte("[\n   {\n      \"op\": \"replace\",\n      \"path\": \"/\",\n      \"value\": { }\n   }\n]\n"),
				},
			},
		},
		{
			name:              "no metadata - skips re-mutation",
			service:           &noMetadataService,
			rolloutsInCluster: []runtime.Object{rol},
			expectedActions:   nil,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()
	clusterName := "testCluster1"

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.EnableArgoIntegration = true
			defer func() { features.EnableArgoIntegration = false }()

			stopCh := make(chan struct{})
			defer close(stopCh)

			registry := prometheus.NewRegistry()
			client := kube_test.NewKubeClientBuilder().
				AddDynamicClientObjects(tc.rolloutsInCluster...).
				Build()
			fakeClient := (client).(*kube_test.FakeClient)

			templateManager := &TestableTemplateManager{
				templateContent:  "function(service, rollout, context) [{op: 'replace', path: '/', value: {}}]",
				templateMetadata: testMetadata,
			}

			informer := client.DynamicInformerFactory().ForResource(constants.RolloutResource).Informer()
			_ = rollout.AddServiceNameToRolloutIndexer(informer)

			_ = client.RunAndWait(stopCh, true, nil)

			err := reMutateRollouts(logger, templateManager, templateManager, registry, tc.service, clusterName, client)

			filteredMopClientActions := filterInformerActions(fakeClient.DynamicClient.Actions())
			assert.Equal(t, tc.expectedActions, filteredMopClientActions)
			assert.Nil(t, err)
		})
	}
}

func TestCreateRelatedRolloutObjectHandler_Add(t *testing.T) {
	var enqueueCalled = false
	var eventType = controllers_api.EventUpdate
	var testRollout = kube_test.NewRolloutBuilder("test-rollout1", "test-ns").Build()

	handler := CreateRelatedRolloutObjectHandler(func(event controllers_api.Event, objs ...interface{}) {
		enqueueCalled = true
		eventType = event
	})

	handler.OnAdd(testRollout, false)

	assert.True(t, enqueueCalled)
	assert.Equal(t, controllers_api.EventAdd, eventType)
}

func TestCreateRelatedRolloutObjectHandler_Delete(t *testing.T) {
	var enqueueCalled = false
	var eventType = controllers_api.EventUpdate
	var testRollout = kube_test.NewRolloutBuilder("test-rollout1", "test-ns").Build()

	handler := CreateRelatedRolloutObjectHandler(func(event controllers_api.Event, objs ...interface{}) {
		enqueueCalled = true
		eventType = event
	})

	handler.OnDelete(testRollout)

	assert.True(t, enqueueCalled)
	assert.Equal(t, controllers_api.EventDelete, eventType)
}

func TestCreateRelatedRolloutObjectHandler_Update(t *testing.T) {
	var testRollout = kube_test.NewRolloutBuilder("test-rollout1", "test-ns").
		SetTemplateLabel(map[string]string{"label1": "value1"}).
		SetGeneration(1).
		Build()
	var testRolloutSameGeneration = kube_test.NewRolloutBuilder("test-rollout1", "test-ns").
		SetTemplateLabel(map[string]string{"label2": "value2"}).
		SetGeneration(1).
		Build()
	var testRolloutSameTemplateLabels = kube_test.NewRolloutBuilder("test-rollout1", "test-ns").
		SetTemplateLabel(map[string]string{"label1": "value1"}).
		SetGeneration(1).
		Build()
	var updatedRollout = kube_test.NewRolloutBuilder("test-rollout1", "test-ns").
		SetTemplateLabel(map[string]string{"label2": "value2"}).
		SetGeneration(2).
		Build()

	testCases := []struct {
		name            string
		oldRollout      *unstructured.Unstructured
		newRollout      *unstructured.Unstructured
		enqueueExpected bool
	}{
		{
			name:            "Same generation",
			oldRollout:      testRollout,
			newRollout:      testRolloutSameGeneration,
			enqueueExpected: false,
		},
		{
			name:            "Same deployment template labels",
			oldRollout:      testRollout,
			newRollout:      testRolloutSameTemplateLabels,
			enqueueExpected: false,
		},
		{
			name:            "Different resourceVersion, different template labels",
			oldRollout:      testRollout,
			newRollout:      updatedRollout,
			enqueueExpected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var enqueueCalled = false
			var eventType = controllers_api.EventUpdate
			handler := CreateRelatedRolloutObjectHandler(func(event controllers_api.Event, objs ...interface{}) {
				enqueueCalled = true
				eventType = event
			})

			handler.OnUpdate(tc.oldRollout, tc.newRollout)

			assert.Equal(t, tc.enqueueExpected, enqueueCalled)
			assert.Equal(t, controllers_api.EventUpdate, eventType)
		})
	}
}

func TestCreateWorkQueue(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics.NewMetricsProvider(registry)
	testCases := []struct {
		name                 string
		priorityQueueEnabled bool
	}{
		{
			name:                 "Priority Queue Enabled",
			priorityQueueEnabled: true,
		},
		{
			name:                 "Priority Queue Not Enabled",
			priorityQueueEnabled: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.UsePriorityQueue = tc.priorityQueueEnabled
			defer func() {
				features.UsePriorityQueue = false
			}()

			queue := createWorkQueue("test-queue")

			// Test the queue type based on priority queue setting
			_, usePriorityQueue := queue.(interface {
				AddWithOpts(priorityqueue.AddOpts, ...any)
			})
			if tc.priorityQueueEnabled {
				assert.True(t, usePriorityQueue, "Queue should be a priority queue when enabled")
			} else {
				assert.False(t, usePriorityQueue, "Queue should not be a priority queue when disabled")
			}
		})
	}
}

type TestableTemplateManager struct {
	templateContent  string
	templateMetadata map[string]*templating.TemplateMetadata
}

func (m *TestableTemplateManager) Exists(_ string) bool {
	return true
}

func (m *TestableTemplateManager) GetTemplatePaths() []string {
	return []string{}
}

func (m *TestableTemplateManager) GetTemplatesByPrefix(_ string) map[string]string {
	var foundTemplates = make(map[string]string)
	foundTemplates["dummy"] = m.templateContent
	return foundTemplates
}

func (m *TestableTemplateManager) GetTemplateMetadata(templateKey string) *templating.TemplateMetadata {
	internalKey := strings.ReplaceAll(templateKey, constants.TemplateNameSeparator, templating.TemplateKeyDelimiter)
	if m.templateMetadata == nil {
		return nil
	}
	return m.templateMetadata[internalKey]
}
