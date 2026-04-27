package kube_test

import (
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type DeploymentBuilder struct {
	deployment *appsv1.Deployment
}

func NewDeploymentBuilder(namespace, name string) *DeploymentBuilder {
	deploymentBuilder := &DeploymentBuilder{
		deployment: &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
			Spec: appsv1.DeploymentSpec{},
		},
	}
	return deploymentBuilder
}

func (r *DeploymentBuilder) SetLabels(labels map[string]string) *DeploymentBuilder {
	r.deployment.SetLabels(labels)
	return r
}

func (r *DeploymentBuilder) SetTemplateLabels(labels map[string]string) *DeploymentBuilder {
	r.deployment.Spec.Template.Labels = labels
	return r
}

func (r *DeploymentBuilder) Build() *appsv1.Deployment {
	return r.deployment
}

func (r *DeploymentBuilder) SetGeneration(generation int64) *DeploymentBuilder {
	r.deployment.SetGeneration(generation)
	return r
}
