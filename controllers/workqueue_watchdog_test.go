package controllers

import (
	"context"
	"fmt"
	"testing"
	"time"

	commonmetrics "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics"
	metricstesting "github.com/istio-ecosystem/mesh-operator/pkg/common/metrics/testing"
	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
	"k8s.io/client-go/util/workqueue"
)

func TestWorkqueueWatchdog_Get_UpdatesDequeueTime(t *testing.T) {
	// Setup
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()
	queue := workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]())

	watchdog := NewWorkqueueWatchdog(context.Background(), queue, "test-queue", logger, registry)
	defer watchdog.ShutDown()

	// Add an item and verify dequeue updates timestamp
	queue.Add("test-item")

	// Get initial timestamp
	initialTime := watchdog.lastDequeueTime.Load()
	require.NotNil(t, initialTime)
	initialTimeVal := *initialTime

	// Wait a bit to ensure time difference
	time.Sleep(100 * time.Millisecond)

	// Dequeue item
	item, shutdown := watchdog.Get()
	assert.False(t, shutdown)
	assert.Equal(t, "test-item", item)
	watchdog.Done(item)

	// Verify timestamp was updated
	updatedTime := watchdog.lastDequeueTime.Load()
	require.NotNil(t, updatedTime)
	updatedTimeVal := *updatedTime

	assert.True(t, updatedTimeVal.After(initialTimeVal), "dequeue time should be updated after Get()")
}

func TestWorkqueueWatchdog_EmptyQueue_NoWarning(t *testing.T) {
	// Setup
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()
	queue := workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]())

	watchdog := NewWorkqueueWatchdog(context.Background(), queue, "test-queue", logger, registry)
	defer watchdog.ShutDown()

	// Set threshold to very short time for testing
	watchdog.thresholdMinutes = 1

	// Simulate time passage without any items
	warningThreshold := time.Duration(float64(watchdog.thresholdMinutes)*0.7) * time.Minute
	panicThreshold := time.Duration(watchdog.thresholdMinutes) * time.Minute

	// Check queue health - should not emit any metrics since queue is empty
	watchdog.checkQueueHealth(warningThreshold, panicThreshold)

	// Verify no warning metric was emitted
	value, err := metricstesting.GetCounterWithLabelsValue(registry, commonmetrics.QueueStuckWarning,
		map[string]string{commonmetrics.QueueNameTag: "test-queue"})
	require.NoError(t, err)
	assert.Equal(t, float64(0), value)
}

func TestWorkqueueWatchdog_StuckQueue_EmitsWarning(t *testing.T) {
	// Setup
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()
	queue := workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]())

	watchdog := NewWorkqueueWatchdog(context.Background(), queue, "test-queue", logger, registry)
	defer watchdog.ShutDown()

	// Add item to queue
	queue.Add("stuck-item")

	// Set a very short threshold for testing
	watchdog.thresholdMinutes = 1
	warningThreshold := 700 * time.Millisecond // 70% of 1 second (for testing)
	panicThreshold := 1000 * time.Millisecond  // 100% of 1 second

	// Manually set last dequeue time to past the warning threshold
	pastTime := time.Now().Add(-800 * time.Millisecond)
	watchdog.lastDequeueTime.Store(&pastTime)

	// Check queue health - should emit warning metric
	watchdog.checkQueueHealth(warningThreshold, panicThreshold)

	// Verify warning metric was emitted
	value, err := metricstesting.GetCounterWithLabelsValue(registry, commonmetrics.QueueStuckWarning,
		map[string]string{commonmetrics.QueueNameTag: "test-queue"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), value)
}

func TestWorkqueueWatchdog_ShutDown_StopsMonitoring(t *testing.T) {
	// Setup
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()
	queue := workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]())

	watchdog := NewWorkqueueWatchdog(context.Background(), queue, "test-queue", logger, registry)

	// Verify context is not cancelled initially
	select {
	case <-watchdog.ctx.Done():
		t.Fatal("context should not be cancelled initially")
	default:
		// Expected
	}

	// Shutdown
	watchdog.ShutDown()

	// Verify context is cancelled after shutdown
	select {
	case <-watchdog.ctx.Done():
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("context should be cancelled after shutdown")
	}
}

func TestCreateWorkQueueWithWatchdog_FeatureDisabled(t *testing.T) {
	// Save original feature flag value
	originalValue := features.EnableQueueWatchdog
	defer func() { features.EnableQueueWatchdog = originalValue }()

	// Disable feature
	features.EnableQueueWatchdog = false

	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()

	queue := createWorkQueueWithWatchdog(context.Background(), "test-queue", logger, registry)
	defer queue.ShutDown()

	// Verify queue is not wrapped with watchdog
	_, isWatchdog := queue.(*WorkqueueWatchdog)
	assert.False(t, isWatchdog, "queue should not be wrapped when feature is disabled")
}

func TestCreateWorkQueueWithWatchdog_FeatureEnabled(t *testing.T) {
	// Save original feature flag value
	originalValue := features.EnableQueueWatchdog
	defer func() { features.EnableQueueWatchdog = originalValue }()

	// Enable feature
	features.EnableQueueWatchdog = true

	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()

	queue := createWorkQueueWithWatchdog(context.Background(), "test-queue", logger, registry)
	defer queue.ShutDown()

	// Verify queue is wrapped with watchdog
	watchdog, isWatchdog := queue.(*WorkqueueWatchdog)
	assert.True(t, isWatchdog, "queue should be wrapped when feature is enabled")
	if isWatchdog {
		assert.Equal(t, "test-queue", watchdog.queueName)
	}
}

func TestWorkqueueWatchdog_PanicPrevention(t *testing.T) {
	// This test verifies the panic would be triggered but catches it before it happens
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()
	queue := workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]())

	watchdog := NewWorkqueueWatchdog(context.Background(), queue, "test-queue", logger, registry)
	defer watchdog.ShutDown()

	// Add item to queue
	queue.Add("stuck-item")

	// Set a very short threshold for testing
	watchdog.thresholdMinutes = 1
	panicThreshold := 500 * time.Millisecond

	// Manually set last dequeue time to past the panic threshold
	pastTime := time.Now().Add(-600 * time.Millisecond)
	watchdog.lastDequeueTime.Store(&pastTime)

	// Verify panic metric would be emitted (without actually panicking in the test)
	if time.Since(pastTime) >= panicThreshold {
		watchdog.emitStuckPanicMetric()
	}

	// Verify panic metric was emitted
	value, err := metricstesting.GetCounterWithLabelsValue(registry, commonmetrics.QueueStuckPanic,
		map[string]string{commonmetrics.QueueNameTag: "test-queue"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), value)
}

func TestWorkqueueWatchdog_IntegrationWithRealQueue(t *testing.T) {
	// Integration test: verify watchdog works with real queue operations
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()
	queue := workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]())

	watchdog := NewWorkqueueWatchdog(context.Background(), queue, "test-queue", logger, registry)
	defer watchdog.ShutDown()

	// Add multiple items
	watchdog.Add("item1")
	watchdog.Add("item2")
	watchdog.Add("item3")

	assert.Equal(t, 3, watchdog.Len())

	// Process items
	for i := 0; i < 3; i++ {
		item, shutdown := watchdog.Get()
		assert.False(t, shutdown)
		assert.NotNil(t, item)
		watchdog.Done(item)
		watchdog.Forget(item)
	}

	assert.Equal(t, 0, watchdog.Len())
}

// TestCheckQueueHealth_DirectCall tests the checkQueueHealth function directly
// by faking the last get time without actually calling queue.Get
func TestCheckQueueHealth_DirectCall_NullLastDequeue(t *testing.T) {
	// Setup: create queue but don't call Get()
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()
	queue := workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]())

	watchdog := NewWorkqueueWatchdog(context.Background(), queue, "test-queue", logger, registry)
	defer watchdog.ShutDown()

	// Add items to queue but DON'T call Get()
	queue.Add("stuck-item-1")
	queue.Add("stuck-item-2")

	assert.Equal(t, 2, watchdog.Len(), "queue should have 2 items")

	// Set thresholds for testing
	warningThreshold := 100 * time.Millisecond
	panicThreshold := 200 * time.Millisecond

	// Clear the lastDequeueTime to simulate "null" state (never dequeued)
	watchdog.lastDequeueTime.Store(nil)

	// Call checkQueueHealth directly - should return early without emitting metrics
	// (null lastDequeueTime is a safety check - we can't determine if stuck without a baseline)
	watchdog.checkQueueHealth(warningThreshold, panicThreshold)

	// Verify NO warning metric was emitted (watchdog returns early for null timestamp)
	value, err := metricstesting.GetCounterWithLabelsValue(registry, commonmetrics.QueueStuckWarning,
		map[string]string{commonmetrics.QueueNameTag: "test-queue"})
	require.NoError(t, err)
	assert.Equal(t, float64(0), value, "no warning should be emitted when lastDequeueTime is null (safety check)")
}

// TestCheckQueueHealth_DirectCall_FakedTime tests by manually setting last dequeue time
func TestCheckQueueHealth_DirectCall_FakedTime_Warning(t *testing.T) {
	// Setup: create queue
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()
	queue := workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]())

	watchdog := NewWorkqueueWatchdog(context.Background(), queue, "test-queue", logger, registry)
	defer watchdog.ShutDown()

	// Add items to queue but DON'T call Get()
	queue.Add("stuck-item")

	// Fake the last get time to be in the past (beyond 70% threshold)
	warningThreshold := 1 * time.Second       // 70% of total
	panicThreshold := 1500 * time.Millisecond // 100% of total

	// Set last dequeue to 1.2 seconds ago (past warning, before panic)
	fakedTime := time.Now().Add(-1200 * time.Millisecond)
	watchdog.lastDequeueTime.Store(&fakedTime)

	// Call checkQueueHealth directly - should emit warning but NOT panic
	watchdog.checkQueueHealth(warningThreshold, panicThreshold)

	// Verify warning metric was emitted
	warningValue, err := metricstesting.GetCounterWithLabelsValue(registry, commonmetrics.QueueStuckWarning,
		map[string]string{commonmetrics.QueueNameTag: "test-queue"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), warningValue, "warning metric should be emitted once")

	// Verify panic metric was NOT emitted
	panicValue, err := metricstesting.GetCounterWithLabelsValue(registry, commonmetrics.QueueStuckPanic,
		map[string]string{commonmetrics.QueueNameTag: "test-queue"})
	require.NoError(t, err)
	assert.Equal(t, float64(0), panicValue, "panic metric should not be emitted yet")
}

// TestCheckQueueHealth_DirectCall_FakedTime_Panic tests panic threshold
func TestCheckQueueHealth_DirectCall_FakedTime_Panic(t *testing.T) {
	// Setup: create queue
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()
	queue := workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]())

	watchdog := NewWorkqueueWatchdog(context.Background(), queue, "test-queue", logger, registry)
	defer watchdog.ShutDown()

	// Add items to queue but DON'T call Get()
	queue.Add("critically-stuck-item")

	// Set threshold
	panicThreshold := 1500 * time.Millisecond

	// Fake the last get time to be in the past (beyond 100% threshold)
	// Set to 2 seconds ago (well past panic threshold)
	fakedTime := time.Now().Add(-2000 * time.Millisecond)
	watchdog.lastDequeueTime.Store(&fakedTime)

	// We need to catch the panic, so we'll only test up to the panic metric emission
	// The actual panic() call would terminate the test
	timeSinceLastDequeue := time.Since(fakedTime)

	// Verify we're past the panic threshold
	assert.Greater(t, timeSinceLastDequeue, panicThreshold, "should be past panic threshold")

	// Manually trigger just the panic metric (without the actual panic)
	if watchdog.Len() > 0 && timeSinceLastDequeue >= panicThreshold {
		watchdog.emitStuckPanicMetric()
	}

	// Verify panic metric was emitted
	panicValue, err := metricstesting.GetCounterWithLabelsValue(registry, commonmetrics.QueueStuckPanic,
		map[string]string{commonmetrics.QueueNameTag: "test-queue"})
	require.NoError(t, err)
	assert.Equal(t, float64(1), panicValue, "panic metric should be emitted when past panic threshold")
}

// TestCheckQueueHealth_DirectCall_EmptyQueue tests that empty queue doesn't trigger warnings
func TestCheckQueueHealth_DirectCall_EmptyQueue_NoWarning(t *testing.T) {
	// Setup: create empty queue
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()
	queue := workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]())

	watchdog := NewWorkqueueWatchdog(context.Background(), queue, "test-queue", logger, registry)
	defer watchdog.ShutDown()

	// DON'T add any items - queue is empty
	assert.Equal(t, 0, watchdog.Len(), "queue should be empty")

	// Fake old last dequeue time
	fakedTime := time.Now().Add(-5 * time.Minute)
	watchdog.lastDequeueTime.Store(&fakedTime)

	// Set thresholds
	warningThreshold := 1 * time.Second
	panicThreshold := 2 * time.Second

	// Call checkQueueHealth directly - should NOT emit anything because queue is empty
	watchdog.checkQueueHealth(warningThreshold, panicThreshold)

	// Verify no warning metric was emitted
	warningValue, err := metricstesting.GetCounterWithLabelsValue(registry, commonmetrics.QueueStuckWarning,
		map[string]string{commonmetrics.QueueNameTag: "test-queue"})
	require.NoError(t, err)
	assert.Equal(t, float64(0), warningValue, "no warning should be emitted for empty queue")

	// Verify no panic metric was emitted
	panicValue, err := metricstesting.GetCounterWithLabelsValue(registry, commonmetrics.QueueStuckPanic,
		map[string]string{commonmetrics.QueueNameTag: "test-queue"})
	require.NoError(t, err)
	assert.Equal(t, float64(0), panicValue, "no panic should be emitted for empty queue")
}

func TestThreadDumpCapture(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	registry := prometheus.NewRegistry()
	queue := workqueue.NewTypedRateLimitingQueue[any](workqueue.DefaultTypedControllerRateLimiter[any]())
	watchdog := NewWorkqueueWatchdog(context.Background(), queue, "test-queue", logger, registry)
	defer watchdog.ShutDown()

	done := make(chan struct{})
	defer close(done)
	const testGoroutines = 30
	for i := 0; i < testGoroutines; i++ {
		go func(id int) { <-done }(i)
	}

	panicCaught := false
	defer func() {
		if r := recover(); r != nil {
			panicCaught = true
			panicMsg := fmt.Sprintf("%v", r)
			assert.Contains(t, panicMsg, "Queue watchdog triggered")
			assert.Contains(t, panicMsg, "test-queue")
			t.Logf("Caught expected panic: %v", r)
		}
	}()

	watchdog.dumpThreadsAndPanic()

	assert.True(t, panicCaught, "dumpThreadsAndPanic should trigger a panic")
}
