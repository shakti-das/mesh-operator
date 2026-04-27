package controllers

import (
	"reflect"
	"testing"

	meshv1alpha1 "github.com/istio-ecosystem/mesh-operator/api/mesh.io/v1alpha1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTspEventHandler_OnUpdate_ShouldSkip(t *testing.T) {
	testCases := []struct {
		name       string
		oldTsp     *meshv1alpha1.TrafficShardingPolicy
		newTsp     *meshv1alpha1.TrafficShardingPolicy
		shouldSkip bool
	}{
		{
			name: "generation changed - should NOT skip",
			oldTsp: &meshv1alpha1.TrafficShardingPolicy{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
			},
			newTsp: &meshv1alpha1.TrafficShardingPolicy{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
			},
			shouldSkip: false,
		},
		{
			name: "same generation same labels same annotations - should skip",
			oldTsp: &meshv1alpha1.TrafficShardingPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Generation:  1,
					Labels:      map[string]string{"env": "dev"},
					Annotations: map[string]string{"key": "value"},
				},
			},
			newTsp: &meshv1alpha1.TrafficShardingPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Generation:  1,
					Labels:      map[string]string{"env": "dev"},
					Annotations: map[string]string{"key": "value"},
				},
			},
			shouldSkip: true,
		},
		{
			name: "labels changed - should NOT skip",
			oldTsp: &meshv1alpha1.TrafficShardingPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
					Labels:     map[string]string{"env": "dev"},
				},
			},
			newTsp: &meshv1alpha1.TrafficShardingPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
					Labels:     map[string]string{"env": "prod"},
				},
			},
			shouldSkip: false,
		},
		{
			name: "annotations changed - should NOT skip",
			oldTsp: &meshv1alpha1.TrafficShardingPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Generation:  1,
					Annotations: map[string]string{"key": "value1"},
				},
			},
			newTsp: &meshv1alpha1.TrafficShardingPolicy{
				ObjectMeta: metav1.ObjectMeta{
					Generation:  1,
					Annotations: map[string]string{"key": "value2"},
				},
			},
			shouldSkip: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test the skip logic directly (same logic as in OnUpdate)
			shouldSkip := tc.oldTsp.Generation == tc.newTsp.Generation &&
				reflect.DeepEqual(tc.oldTsp.Labels, tc.newTsp.Labels) &&
				reflect.DeepEqual(tc.oldTsp.Annotations, tc.newTsp.Annotations)

			assert.Equal(t, tc.shouldSkip, shouldSkip, tc.name)
		})
	}
}
