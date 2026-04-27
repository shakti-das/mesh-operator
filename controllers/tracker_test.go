package controllers

import (
	"testing"

	"go.uber.org/zap/zaptest"

	"github.com/stretchr/testify/assert"
)

func TestTrackServiceKey(t *testing.T) {
	serviceNamespace = "some-namespace"
	serviceName = "some-service"
	serviceKey = serviceNamespace + "/" + serviceName

	testCases := []struct {
		name                            string
		remoteClusterReconcilingService bool
		keyAlreadyTracked               bool
	}{
		{
			name:                            "Key already tracked(Race condition)",
			remoteClusterReconcilingService: true,
			keyAlreadyTracked:               true,
		},
		{
			name:                            "Key not tracked yet",
			remoteClusterReconcilingService: false,
			keyAlreadyTracked:               false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			serviceKeyMapper := make(map[string]struct{})

			if tc.remoteClusterReconcilingService {
				serviceKeyMapper[serviceKey] = struct{}{}
			}

			multiClusterReconcileTracker := multiClusterReconcileManager{
				keyMapper: serviceKeyMapper,
			}

			keyAlreadyTracked := multiClusterReconcileTracker.trackKey(serviceNamespace, serviceName)

			assert.Equal(t, tc.keyAlreadyTracked, keyAlreadyTracked)
		})
	}
}

func TestUnTrackServiceKey(t *testing.T) {
	serviceNamespace = "some-namespace"
	serviceName = "some-service"
	serviceKey = serviceNamespace + "/" + serviceName

	testCases := []struct {
		name             string
		keyExistsAlready bool
	}{
		{
			name:             "Key already exist",
			keyExistsAlready: true,
		},
		{
			name:             "Key does not exist in serviceKey Mapper",
			keyExistsAlready: false,
		},
	}

	logger := zaptest.NewLogger(t).Sugar()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			serviceKeyMapper := make(map[string]struct{})

			if tc.keyExistsAlready {
				serviceKeyMapper[serviceKey] = struct{}{}
			}

			multiClusterReconcileManager := multiClusterReconcileManager{
				logger:    logger,
				keyMapper: serviceKeyMapper,
			}

			multiClusterReconcileManager.unTrackKey(logger, "whatever", serviceNamespace, serviceName)

			_, keyExist := multiClusterReconcileManager.keyMapper[serviceKey]

			// assert key untracked
			assert.False(t, keyExist)

		})
	}
}
