package envtest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func PrepForEnv(t *testing.T) {
	t.Helper()
	// TEST_SRCDIR points to the runfiles dir, see https://bazel.build/reference/test-encyclopedia
	runfilesPath := ensureExists(t, os.Getenv("TEST_SRCDIR"))
	// kubebuilder/kubebuilder/...
	kubebuildRootDir := ensureExists(t, filepath.Join(runfilesPath, "kubebuilder"))
	kubebuildRootDir = ensureExists(t, filepath.Join(kubebuildRootDir, "kubebuilder"))
	// kubebuilder/kubebuilder/[darwin_amd64|linux_amd64]
	assetsPath := ensureExists(t, filepath.Join(kubebuildRootDir, fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)))
	// kubebuilder/kubebuilder/[darwin_amd64|linux_amd64]/kube-apiserver
	kubeApiServerBin := ensureExists(t, filepath.Join(assetsPath, "kube-apiserver"))
	require.NoError(t, os.Setenv("TEST_ASSET_KUBE_APISERVER", kubeApiServerBin))
	// kubebuilder/kubebuilder/[darwin_amd64|linux_amd64]/etcd
	etcdBin := ensureExists(t, filepath.Join(assetsPath, "etcd"))
	require.NoError(t, os.Setenv("TEST_ASSET_ETCD", etcdBin))
	// kubebuilder/kubebuilder/[darwin_amd64|linux_amd64]/kubectl
	kubeCtlBin := ensureExists(t, filepath.Join(assetsPath, "kubectl"))
	require.NoError(t, os.Setenv("TEST_ASSET_KUBECTL", kubeCtlBin))
	require.NoError(t, os.Setenv("KUBEBUILDER_CONTROLPLANE_START_TIMEOUT", "10m"))
	require.NoError(t, os.Setenv("KUBEBUILDER_CONTROLPLANE_STOP_TIMEOUT", "5m"))

	// Set TMPDIR to TEST_TMPDIR for SFCI managed compatibility
	// In Bazel sandbox, /tmp may not exist, so use Bazel's provided temp directory
	if testTmpDir := os.Getenv("TEST_TMPDIR"); testTmpDir != "" {
		require.NoError(t, os.Setenv("TMPDIR", testTmpDir))
	}
}

func ensureExists(t *testing.T, path string) string {
	_, err := os.Stat(path)
	require.NoError(t, err, "%s does not exist", path)
	return path
}
