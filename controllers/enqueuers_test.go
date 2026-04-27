package controllers

import (
	"testing"

	"github.com/istio-ecosystem/mesh-operator/pkg/controllers_api"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/tools/cache"
)

func TestSingleQueueEnquer_Enqueue(t *testing.T) {
	priorityNamespace := "priority-namespace"
	features.PriorityNamespaces = map[string]struct{}{
		priorityNamespace: {},
	}
	objectOfPriorityNs := testSvc.DeepCopy()
	objectOfPriorityNs.SetNamespace(priorityNamespace)
	testCases := []struct {
		name                    string
		usePriorityQueue        bool
		objectToBeEnqueued      interface{}
		expectedEnqueueCount    int
		expectedEnqueueNowCount int
	}{
		{
			name:                    "Enqueuing element of priority namespace when priority queue is enabled",
			usePriorityQueue:        true,
			objectToBeEnqueued:      objectOfPriorityNs,
			expectedEnqueueCount:    0,
			expectedEnqueueNowCount: 1,
		},
		{
			name:                    "Enqueuing element of priority namespace when priority queue is disabled",
			usePriorityQueue:        false,
			objectToBeEnqueued:      objectOfPriorityNs,
			expectedEnqueueCount:    1,
			expectedEnqueueNowCount: 0,
		},
		{
			name:                    "Enqueuing element of non-priority namespace",
			objectToBeEnqueued:      testSvc,
			expectedEnqueueCount:    1,
			expectedEnqueueNowCount: 0,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.UsePriorityQueue = tc.usePriorityQueue
			defer func() { features.UsePriorityQueue = false }()
			queue := &TestableQueue{}
			enqueuer := NewSingleQueueEnqueuer(queue)
			enqueuer.Enqueue("some-cluster", tc.objectToBeEnqueued, controllers_api.EventAdd)
			expectedKey, _ := cache.MetaNamespaceKeyFunc(tc.objectToBeEnqueued)
			assert.ElementsMatch(t, []string{expectedKey}, getObjectNames(t, queue.recordedItems))
			assert.Equal(t, tc.expectedEnqueueCount, queue.enqueueCount)
			assert.Equal(t, tc.expectedEnqueueNowCount, queue.enqueueNowCount)
		})
	}
}

func TestSingleQueueEnquer_EnqueueNow(t *testing.T) {
	queue := &TestableQueue{}
	enqueuer := NewSingleQueueEnqueuer(queue)

	enqueuer.EnqueueNow("some-cluster", testSvc, controllers_api.EventAdd)

	assert.ElementsMatch(t, []string{"test-namespace/svc1"}, getObjectNames(t, queue.recordedItems))
}
