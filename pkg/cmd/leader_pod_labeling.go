package cmd

import (
	"context"
	"fmt"

	"github.com/istio-ecosystem/mesh-operator/pkg/features"
	"go.uber.org/zap"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const (
	LEADER_POD_LABEL = "mesh.io~1is-leader"
	patchTemplate    = "[{\"op\": \"add\", \"path\": \"/metadata/labels/%s\",\"value\":\"%t\"}]"
)

type LeaderPodLabeler interface {
	LabelMyPod(isLeader bool)
}

func CreateLeaderPodLabeler(logger *zap.SugaredLogger, kubeClient kubernetes.Interface) LeaderPodLabeler {
	var podLabeler LeaderPodLabeler = &noOpPodLabeler{}

	if features.EnableLeaderPodLabel {
		if len(features.MyPodNamespace) > 0 &&
			len(features.MyPodName) > 0 {
			podLabeler = &k8sPodLabeler{
				logger:      logger,
				kubeClient:  kubeClient,
				myNamespace: features.MyPodNamespace,
				myPodName:   features.MyPodName,
			}
		} else {
			logger.Errorf("leader pod labeling misconfigured. Empty namespace(%s) or pod name(%s) provided. Labeling won't be performed.",
				features.MyPodNamespace,
				features.MyPodName)
		}
	} else {
		logger.Infof("leader pod labeling disabled")
	}

	// Mark the pod as non-leader
	// This is needed to account for cases, where container crashed and came back up.
	// At this point pod would lose its leader status, but label would still state that it's leader.
	podLabeler.LabelMyPod(false)

	return podLabeler
}

// k8sPodLabeler is a component that is responsible for patching the current pod with a label reflecting whether this pod is a leader or not.
// Pod namespace and name are determined by two environment variables that are expected to be set MY_POD_NAMESPACE and MY_POD_NAME.
// No errors are bubbled up, only logged.
type k8sPodLabeler struct {
	logger      *zap.SugaredLogger
	myNamespace string
	myPodName   string
	kubeClient  kubernetes.Interface
}

func (l *k8sPodLabeler) LabelMyPod(isLeader bool) {

	// k patch pod istio-ordering-7558cbdfb9-9jx8b --type=json -p='[{"op": "add", "path": "/metadata/labels/is-leader","value":"true"}]'
	patch := fmt.Sprintf(patchTemplate, LEADER_POD_LABEL, isLeader)
	_, err := l.kubeClient.CoreV1().Pods(l.myNamespace).Patch(
		context.Background(),
		l.myPodName,
		types.JSONPatchType,
		[]byte(patch), metav1.PatchOptions{})

	if err != nil {
		l.logger.Errorf("failed to patch pod with leader info: %o", err)
	}
}

// noOpPodLabeler - a pod labeler that does nothing. Used if labeling is disabled or not configured properly.
type noOpPodLabeler struct{}

func (l *noOpPodLabeler) LabelMyPod(_ bool) {
}
