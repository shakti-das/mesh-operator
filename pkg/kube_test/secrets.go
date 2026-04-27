//go:build !ignore_test_utils

package kube_test

import (
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ClusterCredential struct {
	ClusterID  string
	Kubeconfig []byte
}

func CreateSecret(secretName string, namespace string, clusterConfigs ...ClusterCredential) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				constants.MultiClusterSecretLabel: "true",
			},
		},
		Data: map[string][]byte{},
	}

	for _, config := range clusterConfigs {
		s.Data[config.ClusterID] = config.Kubeconfig
	}
	return s
}
