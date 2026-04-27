package controllers

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	commonmetrics "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/common/metrics"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"k8s.io/client-go/util/workqueue"
)

// WorkqueueWatchdog wraps a TypedRateLimitingInterface to monitor for stuck queues
// and trigger recovery mechanisms when detected.
// The watchdog runs a background goroutine to periodically check queue health.
type WorkqueueWatchdog struct {
	// Embedded queue interface - delegates all calls to the underlying queue
	workqueue.TypedRateLimitingInterface[any]

	queueName        string
	logger           *zap.SugaredLogger
	metricsRegistry  *prometheus.Registry
	thresholdMinutes int

	// Tracks the last time an item was successfully dequeued
	lastDequeueTime atomic.Pointer[time.Time]

	// Context for stopping the monitoring goroutine
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup // Wait for monitor goroutine to stop
}

// NewWorkqueueWatchdog creates a new watchdog wrapper around the provided queue.
// It starts a monitoring goroutine that periodically checks if the queue is stuck.
func NewWorkqueueWatchdog(
	ctx context.Context,
	queue workqueue.TypedRateLimitingInterface[any],
	queueName string,
	logger *zap.SugaredLogger,
	metricsRegistry *prometheus.Registry,
) *WorkqueueWatchdog {
	ctx, cancel := context.WithCancel(ctx)

	watchdog := &WorkqueueWatchdog{
		TypedRateLimitingInterface: queue,
		queueName:                  queueName,
		logger:                     logger.With("component", "queue-watchdog", "queue", queueName),
		metricsRegistry:            metricsRegistry,
		thresholdMinutes:           features.QueueWatchdogThresholdMinutes,
		ctx:                        ctx,
		cancel:                     cancel,
	}

	// Initialize last dequeue time to now
	now := time.Now()
	watchdog.lastDequeueTime.Store(&now)

	// Pre-register metrics with value 0 so they appear in /metrics immediately
	watchdog.initializeMetrics()

	// Start monitoring goroutine
	watchdog.wg.Add(1)
	go watchdog.monitor()

	return watchdog
}

// Get wraps the underlying queue's Get method and tracks the dequeue time.
func (w *WorkqueueWatchdog) Get() (any, bool) {
	item, shutdown := w.TypedRateLimitingInterface.Get()
	if !shutdown {
		// Update last dequeue time whenever we successfully get an item
		now := time.Now()
		w.lastDequeueTime.Store(&now)
	}
	return item, shutdown
}

// ShutDown stops the watchdog monitoring and shuts down the underlying queue.
func (w *WorkqueueWatchdog) ShutDown() {
	w.cancel()  // Stop the monitoring goroutine
	w.wg.Wait() // Wait for monitor goroutine to fully stop
	w.TypedRateLimitingInterface.ShutDown()
}

// monitor runs in a separate goroutine and periodically checks if the queue is stuck.
func (w *WorkqueueWatchdog) monitor() {
	defer w.wg.Done()

	// Check every minute
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// Convert warning percentage (e.g., 70) to decimal (0.7)
	warningPercentage := float64(features.QueueWatchdogWarningPercentage) / 100.0
	warningThreshold := time.Duration(float64(w.thresholdMinutes)*warningPercentage) * time.Minute
	panicThreshold := time.Duration(w.thresholdMinutes) * time.Minute

	w.logger.Infof("Queue watchdog started: warning threshold=%v (%.0f%%), panic threshold=%v (100%%)",
		warningThreshold, warningPercentage*100, panicThreshold)

	for {
		select {
		case <-w.ctx.Done():
			w.logger.Info("Queue watchdog stopped")
			return
		case <-ticker.C:
			w.checkQueueHealth(warningThreshold, panicThreshold)
		}
	}
}

// checkQueueHealth examines the queue state and takes action if stuck.
func (w *WorkqueueWatchdog) checkQueueHealth(warningThreshold, panicThreshold time.Duration) {
	queueLen := w.Len()

	if queueLen == 0 {
		return
	}

	// Get last dequeue time
	lastDequeue := w.lastDequeueTime.Load()
	if lastDequeue == nil {
		// This should never happen as we initialize it in NewWorkqueueWatchdog,
		// but handle it gracefully just in case
		w.logger.Warn("Last dequeue time is nil, skipping health check")
		return
	}

	timeSinceLastDequeue := time.Since(*lastDequeue)

	w.logger.Debugf("Queue health check: len=%d, timeSinceLastDequeue=%v",
		queueLen, timeSinceLastDequeue)

	// Check if we've crossed the warning threshold (70%)
	if timeSinceLastDequeue >= warningThreshold && timeSinceLastDequeue < panicThreshold {
		w.logger.Warnf("Queue appears stuck! Queue has %d items but no dequeue in %v",
			queueLen, timeSinceLastDequeue)
		w.emitStuckWarningMetric()
	}

	// Check if we've crossed the panic threshold (100%)
	if timeSinceLastDequeue >= panicThreshold {
		w.logger.Errorf("Queue critically stuck! Queue has %d items but no dequeue in %v. Triggering recovery...",
			queueLen, timeSinceLastDequeue)
		w.emitStuckPanicMetric()
		w.dumpThreadsAndPanic()
	}
}

func (w *WorkqueueWatchdog) initializeMetrics() {
	if w.metricsRegistry != nil {
		labels := map[string]string{
			commonmetrics.QueueNameTag: w.queueName,
		}
		commonmetrics.GetOrRegisterCounterWithLabels(commonmetrics.QueueStuckWarning, labels, w.metricsRegistry)
		commonmetrics.GetOrRegisterCounterWithLabels(commonmetrics.QueueStuckPanic, labels, w.metricsRegistry)
	}
}

// emitStuckWarningMetric increments the warning metric when queue appears stuck.
func (w *WorkqueueWatchdog) emitStuckWarningMetric() {
	if w.metricsRegistry != nil {
		labels := map[string]string{
			commonmetrics.QueueNameTag: w.queueName,
		}
		counter := commonmetrics.GetOrRegisterCounterWithLabels(commonmetrics.QueueStuckWarning, labels, w.metricsRegistry)
		counter.Inc()
	}
}

// emitStuckPanicMetric increments the panic metric before initiating recovery.
func (w *WorkqueueWatchdog) emitStuckPanicMetric() {
	if w.metricsRegistry != nil {
		labels := map[string]string{
			commonmetrics.QueueNameTag: w.queueName,
		}
		counter := commonmetrics.GetOrRegisterCounterWithLabels(commonmetrics.QueueStuckPanic, labels, w.metricsRegistry)
		counter.Inc()
	}
}

func captureGoroutineStacks(logger *zap.SugaredLogger) (dump string, numGoroutines int) {
	numGoroutines = runtime.NumGoroutine()

	const maxBufferSize = 16 * 1024 * 1024 // 16MB max
	buf := make([]byte, 1024*1024)

	for {
		n := runtime.Stack(buf, true) // true = capture all goroutines
		if n < len(buf) {
			buf = buf[:n]
			break
		}

		newSize := len(buf) * 2
		if newSize > maxBufferSize {
			if logger != nil {
				logger.Warnf("Thread dump truncated at %d bytes (would need %d bytes). Captured partial dump.", len(buf), n)
			}
			break
		}

		buf = make([]byte, newSize)
	}

	return string(buf), numGoroutines
}

// dumpThreadsAndPanic captures a thread dump and panics to force pod restart.
func (w *WorkqueueWatchdog) dumpThreadsAndPanic() {
	dumpID := time.Now().UnixMilli()

	dump, numGoroutines := captureGoroutineStacks(w.logger)
	w.logger.Errorf("[DUMP-%d] Total goroutines: %d", dumpID, numGoroutines)
	w.logger.Errorf("[DUMP-%d] Full goroutine dump size: %d bytes", dumpID, len(dump))

	const chunkSize = 15 * 1024
	totalChunks := (len(dump) + chunkSize - 1) / chunkSize
	w.logger.Warnf("[DUMP-%d] Will output %d log chunks", dumpID, totalChunks)

	for i := 0; i < len(dump); i += chunkSize {
		end := i + chunkSize
		if end > len(dump) {
			end = len(dump)
		}
		chunkNum := i/chunkSize + 1
		w.logger.Errorf("[DUMP-%d][CHUNK-%d/%d]\n%s", dumpID, chunkNum, totalChunks, dump[i:end])
	}
	// Panic to trigger pod restart
	panic(fmt.Sprintf("Queue watchdog triggered: queue '%s' stuck for more than %d minutes",
		w.queueName, w.thresholdMinutes))
}
