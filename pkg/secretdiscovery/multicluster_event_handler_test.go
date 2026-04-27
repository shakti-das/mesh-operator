package secretdiscovery

import (
	"testing"

	"github.com/stretchr/testify/assert"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIsMulticlusterSecret(t *testing.T) {
	testCases := []struct {
		name           string
		secret         *v1.Secret
		expectedResult bool
	}{
		{
			name: "Not in secret namespace",
			secret: &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "some-other-namespace",
					Labels: map[string]string{
						"istio/multiCluster": "true",
					},
				},
			},
			expectedResult: false,
		},
		{
			name: "No label",
			secret: &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "mesh-control-plane",
				},
			},
			expectedResult: false,
		},
		{
			name: "Multicluster secret",
			secret: &v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "mesh-control-plane",
					Labels: map[string]string{
						"istio/multiCluster": "true",
					},
				},
			},
			expectedResult: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler := multiClusterSecretEventHandler{secretsNamespace: "mesh-control-plane"}

			assert.Equal(t, tc.expectedResult, handler.isMultiClusterSecret(tc.secret))
		})
	}
}
