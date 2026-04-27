package kube_test

import (
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type StatefulSetBuilder struct {
	sts *appsv1.StatefulSet
}

func NewStatefulSetBuilder(namespace, name, servicename string,
	replicas int32) *StatefulSetBuilder {

	StatefulSetBuilder := &StatefulSetBuilder{
		sts: &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      name,
			},
			Spec: appsv1.StatefulSetSpec{
				ServiceName: servicename,
				Replicas:    &replicas,
			},
		},
	}
	return StatefulSetBuilder
}

func (r *StatefulSetBuilder) SetLabels(labels map[string]string) *StatefulSetBuilder {
	r.sts.SetLabels(labels)
	return r
}

func (r *StatefulSetBuilder) SetTemplateLabels(labels map[string]string) *StatefulSetBuilder {
	r.sts.Spec.Template.Labels = labels
	return r
}

func (r *StatefulSetBuilder) Build() *appsv1.StatefulSet {
	return r.sts
}

func (r *StatefulSetBuilder) SetGeneration(generation int64) *StatefulSetBuilder {
	r.sts.SetGeneration(generation)
	return r
}

func (r *StatefulSetBuilder) SetOrdinalsStart(start int32) *StatefulSetBuilder {
	if r.sts.Spec.Ordinals == nil {
		r.sts.Spec.Ordinals = &appsv1.StatefulSetOrdinals{}
	}
	r.sts.Spec.Ordinals.Start = start
	return r
}
