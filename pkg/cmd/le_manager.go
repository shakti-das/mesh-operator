package cmd

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/istio-ecosystem/mesh-operator/common/pkg/metrics"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/istio-ecosystem/mesh-operator/controllers"
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"go.uber.org/zap"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

const meshOpMainLeaderElectionName = "mop-leader-election-config"

type LeaderElectionOptions struct {
	LeaderElect                 bool
	LeaderElectionNamespace     string
	LeaderElectionLeaseDuration time.Duration
	LeaderElectionRenewDeadline time.Duration
	LeaderElectionRetryPeriod   time.Duration
}

type processKiller func(p *os.Process)
type LeaderStartHook func(ctx context.Context) error

func CreateLeConfig(
	logger *zap.SugaredLogger,
	options LeaderElectionOptions,
	kubeClient kubernetes.Interface,
	multiClusterController controllers.Controller,
	registry *prometheus.Registry,
	leaderStartHook LeaderStartHook) (leaderelection.LeaderElectionConfig, error) {

	if options.LeaderElectionNamespace == "" {
		return leaderelection.LeaderElectionConfig{}, fmt.Errorf("leaderElectionNamespace is empty, disabled leader election")
	}

	id, err := os.Hostname()
	if err != nil {
		return leaderelection.LeaderElectionConfig{}, err
	}

	id = id + "_" + string(uuid.NewUUID())
	logger.Infof("Leaderelection get id %s", id)

	leaderLabeler := CreateLeaderPodLabeler(logger, kubeClient)
	leaderHandler := NewLeaderLifecycleHandler(logger, id, multiClusterController, leaderLabeler, registry, leaderStartHook)

	leConfig := leaderelection.LeaderElectionConfig{
		Lock: &resourcelock.LeaseLock{
			LeaseMeta: v1.ObjectMeta{Name: constants.DefaultLeaderElectionLeaseLockName, Namespace: options.LeaderElectionNamespace}, Client: kubeClient.CoordinationV1(),
			LockConfig: resourcelock.ResourceLockConfig{Identity: id},
		},
		ReleaseOnCancel: true,
		LeaseDuration:   options.LeaderElectionLeaseDuration,
		RenewDeadline:   options.LeaderElectionRenewDeadline,
		RetryPeriod:     options.LeaderElectionRetryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: leaderHandler.onStartLeading,
			OnStoppedLeading: leaderHandler.onStoppedLeading,
			OnNewLeader:      leaderHandler.onNewLeader,
		},
		Name: meshOpMainLeaderElectionName,
	}

	return leConfig, nil
}

type LeaderLifecycleHandler struct {
	logger                 *zap.SugaredLogger
	id                     string
	multiClusterController controllers.Controller
	labeler                LeaderPodLabeler
	isLeaderGauge          prometheus.Gauge
	leaderStartHook        LeaderStartHook
}

func NewLeaderLifecycleHandler(
	logger *zap.SugaredLogger,
	id string,
	multiClusterController controllers.Controller,
	labeler LeaderPodLabeler,
	registry *prometheus.Registry,
	leaderStartHook LeaderStartHook) *LeaderLifecycleHandler {

	leaderGauge := metrics.GetOrRegisterGaugeWithLabels(
		"leader_election_pod_status",
		map[string]string{"name": meshOpMainLeaderElectionName},
		registry)

	return &LeaderLifecycleHandler{
		logger:                 logger,
		id:                     id,
		multiClusterController: multiClusterController,
		labeler:                labeler,
		isLeaderGauge:          leaderGauge,
		leaderStartHook:        leaderStartHook,
	}
}

func (h *LeaderLifecycleHandler) onStartLeading(ctx context.Context) {
	h.isLeaderGauge.Set(1.0)
	h.labeler.LabelMyPod(true)
	if h.leaderStartHook != nil {
		if err := h.leaderStartHook(ctx); err != nil {
			h.logger.Fatalf("failed leaderStartHook: %v", err)
		}
	}
	startMultiClusterControllers(h.logger, h.multiClusterController, ctx.Done())
	enableAutoShutdown(h.logger, killProcess)
}

func (h *LeaderLifecycleHandler) onStoppedLeading() {
	h.isLeaderGauge.Set(0.0)
	h.labeler.LabelMyPod(false)
	h.logger.Infof("Stopped leading controller: %s", h.id)
}

func (h *LeaderLifecycleHandler) onNewLeader(identity string) {
	if identity == h.id {
		return
	}
	h.logger.Infof("New leader elected: %s", identity)
}

func enableAutoShutdown(logger *zap.SugaredLogger, kill processKiller) {
	if features.AutomatedShutdownInterval != "" {
		go func() {
			err := doAutoShutdown(logger, kill)
			if err != nil {
				logger.Errorf("failed to configure auto shutdown %v", err)
			}
		}()
	}
}

func doAutoShutdown(logger *zap.SugaredLogger, kill processKiller) error {
	sleepDuration, err := time.ParseDuration(features.AutomatedShutdownInterval)
	if err != nil {
		return fmt.Errorf("error parsing automated shutdown interval: %w", err)
	}

	logger.Infof("Sleeping for %s", sleepDuration.String())
	time.Sleep(sleepDuration)

	logger.Infof("%s of sleep time has passed, sending SIGTERM signal...", sleepDuration.String())
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return fmt.Errorf("error finding process: %w", err)
	}
	kill(p)
	return nil
}

func killProcess(p *os.Process) {
	_ = p.Signal(syscall.SIGTERM)
}
