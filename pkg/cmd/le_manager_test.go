package cmd

import (
	"context"
	"os"
	"testing"
	"time"

	testing2 "github.com/istio-ecosystem/mesh-operator/common/pkg/metrics/testing"

	"k8s.io/client-go/tools/cache"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

func TestCreateLeaderElectionConfig_EmptyNamespace(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	options := LeaderElectionOptions{
		LeaderElectionNamespace: "",
	}

	client := kube_test.NewKubeClientBuilder().Build()

	_, err := CreateLeConfig(logger, options, client.Kube(), nil, nil, nil)

	assert.ErrorContains(t, err, "leaderElectionNamespace is empty, disabled leader election")
}

func TestCreateLeaderElectionConfig_ConfigValues(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	options := LeaderElectionOptions{
		LeaderElect:                 true,
		LeaderElectionNamespace:     "test-namespace",
		LeaderElectionRetryPeriod:   1 * time.Minute,
		LeaderElectionLeaseDuration: 2 * time.Minute,
		LeaderElectionRenewDeadline: 3 * time.Minute,
	}

	client := kube_test.NewKubeClientBuilder().Build()
	registry := prometheus.NewRegistry()

	config, err := CreateLeConfig(logger, options, client.Kube(), nil, registry, nil)

	assert.NoError(t, err)
	assert.Equal(t, options.LeaderElectionRetryPeriod, config.RetryPeriod)
	assert.Equal(t, options.LeaderElectionLeaseDuration, config.LeaseDuration)
	assert.Equal(t, options.LeaderElectionRenewDeadline, config.RenewDeadline)
	assert.Equal(t, "mop-leader-election-config", config.Name)
}

func TestOnNewLeader(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	handler := LeaderLifecycleHandler{
		logger: logger,
		id:     "my-id",
	}
	handler.onNewLeader("other-id")
	handler.onNewLeader("my-id")
}

func TestOnStopLeading(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	podLabeler := &fakePodLabeler{}
	registry := prometheus.NewRegistry()

	handler := NewLeaderLifecycleHandler(logger, "my-id", nil, podLabeler, registry, nil)
	handler.onStoppedLeading()

	podLabeler.AssertPodRelabeled(t, false)
	testing2.AssertEqualsGaugeValueWithLabel(
		t,
		registry,
		"leader_election_pod_status",
		map[string]string{"name": "mop-leader-election-config"},
		0.0)
}

func TestOnStartLeading(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	ctrl := fakeController{}
	podLabeler := &fakePodLabeler{}
	registry := prometheus.NewRegistry()

	handler := NewLeaderLifecycleHandler(logger, "my-id", &ctrl, podLabeler, registry, nil)

	handler.onStartLeading(context.TODO())

	ctx, cancel := context.WithTimeout(context.TODO(), 2*time.Second)
	defer cancel()
	cache.WaitForCacheSync(ctx.Done(), func() bool {
		return ctrl.started == true
	})

	assert.True(t, ctrl.started)
	podLabeler.AssertPodRelabeled(t, true)
	testing2.AssertEqualsGaugeValueWithLabel(
		t,
		registry,
		"leader_election_pod_status",
		map[string]string{"name": "mop-leader-election-config"},
		1.0)
}

func TestInvalidAutoShutdownInterval(t *testing.T) {
	features.AutomatedShutdownInterval = "invalid-value"
	defer func() {
		features.AutomatedShutdownInterval = ""
	}()

	err := doAutoShutdown(zaptest.NewLogger(t).Sugar(), nil)

	assert.ErrorContains(t, err, "error parsing automated shutdown interval")
}

func TestAutoShutdown(t *testing.T) {
	testCases := []struct {
		name             string
		shutdownInterval string
		killExpected     bool
	}{
		{
			name:             "Valid interval",
			shutdownInterval: "1s",
			killExpected:     true,
		},
		{
			name:             "Invalid interval",
			shutdownInterval: "invalid-value",
			killExpected:     false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			features.AutomatedShutdownInterval = tc.shutdownInterval
			defer func() {
				features.AutomatedShutdownInterval = ""
			}()

			processKilled := false

			enableAutoShutdown(zaptest.NewLogger(t).Sugar(), func(p *os.Process) {
				processKilled = true
			})

			ctx, cancel := context.WithTimeout(context.TODO(), 2*time.Second)
			defer cancel()
			cache.WaitForCacheSync(ctx.Done(), func() bool {
				return processKilled
			})

			assert.Equal(t, tc.killExpected, processKilled)
		})
	}
}

type fakeController struct {
	started bool
}

func (c *fakeController) Run(_ int, _ <-chan struct{}) error {
	c.started = true
	return nil
}

type fakePodLabeler struct {
	podReLabelCalled bool
	podLabelValue    bool
}

func (l *fakePodLabeler) LabelMyPod(isLeader bool) {
	l.podReLabelCalled = true
	l.podLabelValue = isLeader
}

func (l *fakePodLabeler) AssertPodRelabeled(t *testing.T, expectedValue bool) {
	assert.True(t, l.podReLabelCalled)
	assert.Equal(t, expectedValue, l.podLabelValue)
}
