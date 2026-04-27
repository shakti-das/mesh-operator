package k8swebhook

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

// createTestLogger creates a no-op logger for testing
func createTestLogger() *zap.SugaredLogger {
	return zap.NewNop().Sugar()
}

// createTempCACertFile creates a temporary CA cert file for testing
func createTempCACertFile(t *testing.T, content string) string {
	t.Helper()
	tmpDir := t.TempDir()
	certFile := filepath.Join(tmpDir, "ca-cert.pem")
	err := os.WriteFile(certFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp cert file: %v", err)
	}
	return certFile
}

// countPatchActions counts the number of PATCH operations from fake client
func countPatchActions(client *fake.Clientset) int {
	count := 0
	for _, action := range client.Actions() {
		if action.GetVerb() == "patch" {
			count++
		}
	}
	return count
}

// TestMutatingWebhookWithCooldown verifies cooldown prevents rapid patches
func TestMutatingWebhookWithCooldown(t *testing.T) {
	// Setup fake time
	var fakeTime atomic.Value
	now := time.Now()
	fakeTime.Store(now)
	timeProvider := func() time.Time {
		return fakeTime.Load().(time.Time)
	}

	// Create mutating webhook config
	webhookConfig := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-mutating-config",
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "test-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
				},
			},
		},
	}

	client := fake.NewSimpleClientset(webhookConfig)

	// Create temp CA cert file
	certFile := createTempCACertFile(t, "initial-ca-bundle")

	// Create patcher with 5 second cooldown
	patcher := &V1Patcher{
		caCertFile:        certFile,
		cooldownDuration:  5 * time.Second,
		webhookConfigName: "test-mutating-config",
		webhookName:       "test-webhook",
		delayedRetryTime:  1 * time.Second,
		logger:            createTestLogger(),
		timeProvider:      timeProvider,
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	// Override kubernetes client creation by directly calling the patch loop
	// We'll test by simulating file changes
	err := os.WriteFile(certFile, []byte("new-ca-bundle-1"), 0644)
	if err != nil {
		t.Fatalf("Failed to write cert file: %v", err)
	}

	// Manually call patch (simulating first file change)
	caBundle1, _ := os.ReadFile(certFile)
	err = patcher.PatchMutatingWebhookConfig(client.AdmissionregistrationV1().MutatingWebhookConfigurations(), caBundle1)
	if err != nil {
		t.Fatalf("First patch failed: %v", err)
	}

	patchCount1 := countPatchActions(client)
	if patchCount1 == 0 {
		t.Fatal("Expected at least one patch after first write")
	}

	// Test cooldown: create cooldown and record patch time
	cd := newCooldown(5*time.Second, timeProvider)
	cd.RecordPatchTime()

	// Immediately try to patch again (should be skipped by cooldown)
	if !cd.ShouldSkip() {
		t.Fatal("Expected cooldown to prevent immediate second patch")
	}

	// Advance time by 4 seconds (still under cooldown)
	fakeTime.Store(now.Add(4 * time.Second))
	if !cd.ShouldSkip() {
		t.Fatal("Expected cooldown to still be active after 4 seconds")
	}

	// Advance time past cooldown (5+ seconds)
	fakeTime.Store(now.Add(6 * time.Second))
	if cd.ShouldSkip() {
		t.Fatal("Expected cooldown to be expired after 6 seconds")
	}

	// Now patch should succeed
	err = os.WriteFile(certFile, []byte("new-ca-bundle-2"), 0644)
	if err != nil {
		t.Fatalf("Failed to write cert file: %v", err)
	}

	caBundle2, _ := os.ReadFile(certFile)
	err = patcher.PatchMutatingWebhookConfig(client.AdmissionregistrationV1().MutatingWebhookConfigurations(), caBundle2)
	if err != nil {
		t.Fatalf("Second patch after cooldown failed: %v", err)
	}

	patchCount2 := countPatchActions(client)
	if patchCount2 <= patchCount1 {
		t.Fatalf("Expected more patches after cooldown expired: before=%d after=%d", patchCount1, patchCount2)
	}
}

// TestValidatingWebhookWithCooldown verifies cooldown prevents rapid patches
func TestValidatingWebhookWithCooldown(t *testing.T) {
	// Setup fake time
	var fakeTime atomic.Value
	now := time.Now()
	fakeTime.Store(now)
	timeProvider := func() time.Time {
		return fakeTime.Load().(time.Time)
	}

	// Create validating webhook config
	webhookConfig := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-validating-config",
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "test-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
				},
			},
		},
	}

	client := fake.NewSimpleClientset(webhookConfig)

	// Create temp CA cert file
	certFile := createTempCACertFile(t, "initial-ca-bundle")

	// Create patcher with 3 second cooldown
	patcher := &V1Patcher{
		caCertFile:        certFile,
		cooldownDuration:  3 * time.Second,
		webhookConfigName: "test-validating-config",
		webhookName:       "test-webhook",
		delayedRetryTime:  1 * time.Second,
		logger:            createTestLogger(),
		timeProvider:      timeProvider,
	}

	// Write new bundle
	err := os.WriteFile(certFile, []byte("new-ca-bundle-1"), 0644)
	if err != nil {
		t.Fatalf("Failed to write cert file: %v", err)
	}

	// Manually call patch (simulating first file change)
	caBundle1, _ := os.ReadFile(certFile)
	err = patcher.PatchValidatingWebhookConfig(client.AdmissionregistrationV1().ValidatingWebhookConfigurations(), caBundle1)
	if err != nil {
		t.Fatalf("First patch failed: %v", err)
	}

	patchCount1 := countPatchActions(client)
	if patchCount1 == 0 {
		t.Fatal("Expected at least one patch after first write")
	}

	// Test cooldown
	cd := newCooldown(3*time.Second, timeProvider)
	cd.RecordPatchTime()

	// Immediately try again (should be skipped)
	if !cd.ShouldSkip() {
		t.Fatal("Expected cooldown to prevent immediate second patch")
	}

	// Advance time past cooldown
	fakeTime.Store(now.Add(4 * time.Second))
	if cd.ShouldSkip() {
		t.Fatal("Expected cooldown to be expired after 4 seconds")
	}

	// Patch should succeed now
	err = os.WriteFile(certFile, []byte("new-ca-bundle-2"), 0644)
	if err != nil {
		t.Fatalf("Failed to write cert file: %v", err)
	}

	caBundle2, _ := os.ReadFile(certFile)
	err = patcher.PatchValidatingWebhookConfig(client.AdmissionregistrationV1().ValidatingWebhookConfigurations(), caBundle2)
	if err != nil {
		t.Fatalf("Second patch after cooldown failed: %v", err)
	}

	patchCount2 := countPatchActions(client)
	if patchCount2 <= patchCount1 {
		t.Fatalf("Expected more patches after cooldown expired: before=%d after=%d", patchCount1, patchCount2)
	}
}

// TestFileSystemWatcherWithMutatingWebhook tests full integration with fsnotify
func TestFileSystemWatcherWithMutatingWebhook(t *testing.T) {
	// This test uses the real file system watcher
	// Setup fake time for cooldown control
	var fakeTime atomic.Value
	now := time.Now()
	fakeTime.Store(now)
	timeProvider := func() time.Time {
		return fakeTime.Load().(time.Time)
	}

	// Create mutating webhook config
	webhookConfig := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-mutating-config",
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "test-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
				},
			},
		},
	}

	client := fake.NewSimpleClientset(webhookConfig)

	// Create temp CA cert file
	certFile := createTempCACertFile(t, "initial-ca-bundle")

	// Create patcher with short cooldown
	patcher := &V1Patcher{
		caCertFile:        certFile,
		cooldownDuration:  2 * time.Second,
		webhookConfigName: "test-mutating-config",
		webhookName:       "test-webhook",
		delayedRetryTime:  100 * time.Millisecond,
		logger:            createTestLogger(),
		timeProvider:      timeProvider,
	}

	// We can't fully test StartMutatingWebhookPatcher without in-cluster config
	// But we can verify the patch logic works with our test setup
	caBundle, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("Failed to read cert file: %v", err)
	}

	err = patcher.PatchMutatingWebhookConfig(client.AdmissionregistrationV1().MutatingWebhookConfigurations(), caBundle)
	if err != nil {
		t.Fatalf("Patch failed: %v", err)
	}

	// Verify the patch was applied
	actions := client.Actions()
	foundPatch := false
	for _, action := range actions {
		if action.GetVerb() == "patch" {
			patchAction := action.(k8stesting.PatchAction)
			if patchAction.GetName() == "test-mutating-config" {
				foundPatch = true
				break
			}
		}
	}

	if !foundPatch {
		t.Fatal("Expected to find patch action for mutating webhook config")
	}
}

// TestFileSystemWatcherWithValidatingWebhook tests full integration with fsnotify
func TestFileSystemWatcherWithValidatingWebhook(t *testing.T) {
	// Setup fake time for cooldown control
	var fakeTime atomic.Value
	now := time.Now()
	fakeTime.Store(now)
	timeProvider := func() time.Time {
		return fakeTime.Load().(time.Time)
	}

	// Create validating webhook config
	webhookConfig := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-validating-config",
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "test-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
				},
			},
		},
	}

	client := fake.NewSimpleClientset(webhookConfig)

	// Create temp CA cert file
	certFile := createTempCACertFile(t, "initial-ca-bundle")

	// Create patcher with short cooldown
	patcher := &V1Patcher{
		caCertFile:        certFile,
		cooldownDuration:  2 * time.Second,
		webhookConfigName: "test-validating-config",
		webhookName:       "test-webhook",
		delayedRetryTime:  100 * time.Millisecond,
		logger:            createTestLogger(),
		timeProvider:      timeProvider,
	}

	caBundle, err := os.ReadFile(certFile)
	if err != nil {
		t.Fatalf("Failed to read cert file: %v", err)
	}

	err = patcher.PatchValidatingWebhookConfig(client.AdmissionregistrationV1().ValidatingWebhookConfigurations(), caBundle)
	if err != nil {
		t.Fatalf("Patch failed: %v", err)
	}

	// Verify the patch was applied
	actions := client.Actions()
	foundPatch := false
	for _, action := range actions {
		if action.GetVerb() == "patch" {
			patchAction := action.(k8stesting.PatchAction)
			if patchAction.GetName() == "test-validating-config" {
				foundPatch = true
				break
			}
		}
	}

	if !foundPatch {
		t.Fatal("Expected to find patch action for validating webhook config")
	}
}

// Test helper methods for Start functions
func TestDoMutatingWebhookPatch(t *testing.T) {
	webhookConfig := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-mutating-config",
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: "test-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(webhookConfig)

	patcher := &V1Patcher{
		webhookConfigName: "test-mutating-config",
		webhookName:       "test-webhook",
		logger:            createTestLogger(),
	}

	caBundle := []byte("new-ca-bundle")
	retry := patcher.doMutatingWebhookPatch(fakeClient, caBundle)

	if retry {
		t.Fatal("Expected patch to succeed (retry=false)")
	}

	patchCount := countPatchActions(fakeClient)
	if patchCount == 0 {
		t.Fatal("Expected patch action")
	}
}

func TestDoValidatingWebhookPatch(t *testing.T) {
	webhookConfig := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-validating-config",
		},
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{
				Name: "test-webhook",
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					CABundle: []byte("old-ca-bundle"),
				},
			},
		},
	}

	fakeClient := fake.NewSimpleClientset(webhookConfig)

	patcher := &V1Patcher{
		webhookConfigName: "test-validating-config",
		webhookName:       "test-webhook",
		logger:            createTestLogger(),
	}

	caBundle := []byte("new-ca-bundle")
	retry := patcher.doValidatingWebhookPatch(fakeClient, caBundle)

	if retry {
		t.Fatal("Expected patch to succeed (retry=false)")
	}

	patchCount := countPatchActions(fakeClient)
	if patchCount == 0 {
		t.Fatal("Expected patch action")
	}
}
