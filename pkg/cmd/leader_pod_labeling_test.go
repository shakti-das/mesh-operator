package cmd

import (
	"context"
	"testing"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube_test"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

func TestCreateLeaderPodLabeler_disabled(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()

	result := CreateLeaderPodLabeler(logger, nil)

	assert.IsType(t, &noOpPodLabeler{}, result)
}

func TestCreateLeaderPodLabeler_emptyNamespace(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	features.EnableLeaderPodLabel = true
	features.MyPodName = "some-pod"
	defer func() {
		features.EnableLeaderPodLabel = false
		features.MyPodName = ""
	}()

	result := CreateLeaderPodLabeler(logger, nil)

	assert.IsType(t, &noOpPodLabeler{}, result)
}

func TestCreateLeaderPodLabeler_emptyPodName(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	features.EnableLeaderPodLabel = true
	features.MyPodNamespace = "some-namespace"
	defer func() {
		features.EnableLeaderPodLabel = false
		features.MyPodNamespace = ""
	}()

	result := CreateLeaderPodLabeler(logger, nil)

	assert.IsType(t, &noOpPodLabeler{}, result)
}

func TestCreateLeaderPodLabeler(t *testing.T) {
	myNamespace := "test-namespace"
	myPod := "my-pod"
	logger := zaptest.NewLogger(t).Sugar()
	features.EnableLeaderPodLabel = true
	features.MyPodNamespace = myNamespace
	features.MyPodName = myPod
	defer func() {
		features.EnableLeaderPodLabel = false
		features.MyPodNamespace = ""
		features.MyPodName = ""
	}()

	namespaceObj := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: myNamespace,
		},
	}
	podObj := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      myPod,
			Namespace: myNamespace,
			Labels: map[string]string{
				"some-label": "some-value",
			},
		},
	}
	client := kube_test.NewKubeClientBuilder().
		AddK8sObjects(namespaceObj, podObj).
		Build().Kube()

	result := CreateLeaderPodLabeler(logger, client)

	assert.IsType(t, &k8sPodLabeler{}, result)

	// Make sure that pod got marked as non-leader at start up.
	podInCluster, err := client.CoreV1().Pods(myNamespace).Get(context.Background(), myPod, metav1.GetOptions{})
	assert.NoError(t, err)
	assert.NotNil(t, podInCluster)
	assert.Equal(t, "false", podInCluster.GetLabels()["mesh.io/is-leader"])
}

func TestNoOpPodLabeler_LabelMyPod(t *testing.T) {
	(&noOpPodLabeler{}).LabelMyPod(true)
}

func TestK8sPodLabeler_LabelMyPod(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	myNamespace := "test-namespace"
	myPod := "my-pod"

	namespaceObj := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: myNamespace,
		},
	}
	podObj := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      myPod,
			Namespace: myNamespace,
			Labels: map[string]string{
				"some-label": "some-value",
			},
		},
	}
	client := kube_test.NewKubeClientBuilder().
		AddK8sObjects(namespaceObj, podObj).
		Build().Kube()
	labeler := &k8sPodLabeler{
		logger:      logger,
		myNamespace: myNamespace,
		myPodName:   myPod,
		kubeClient:  client,
	}

	labeler.LabelMyPod(true)

	podInCluster, err := client.CoreV1().Pods(myNamespace).Get(context.Background(), myPod, metav1.GetOptions{})

	assert.NoError(t, err)
	assert.NotNil(t, podInCluster)
	assert.Equal(t, "true", podInCluster.GetLabels()["mesh.io/is-leader"])
}
