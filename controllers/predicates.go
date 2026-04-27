package controllers

import (
	"reflect"

	v1 "k8s.io/api/apps/v1"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"

	meshv1alpha1 "git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// WasChangedByUser - check, whether an object was changed by user.
// This includes spec, annotation or label changes
func WasChangedByUser(old, new metav1.Object) bool {
	return generationChanged(old, new) ||
		labelsChanged(old, new) ||
		annotationsChanged(old, new)
}

// WasChanged - check, whether an object has changed at all.
func WasChanged(old, new metav1.Object) bool {
	return resourceVersionChanged(old, new)
}

func IsBrandNewMop(mop *meshv1alpha1.MeshOperator) bool {
	return mop.Status.Phase == ""
}

func PhaseChangedToPending(old, new *meshv1alpha1.MeshOperator) bool {
	return old.Status.Phase != PhasePending && new.Status.Phase == PhasePending
}

func PhaseChanged(old, new *meshv1alpha1.MeshOperator) bool {
	return old.Status.Phase != new.Status.Phase
}

func IsFinalStatusUpdate(old, new *meshv1alpha1.MeshOperator) bool {
	return old.Status.Phase == PhasePending &&
		(new.Status.Phase == PhaseSucceeded || new.Status.Phase == PhaseFailed)
}

func generationChanged(old, new metav1.Object) bool {
	return old.GetGeneration() != new.GetGeneration()
}

func labelsChanged(old, new metav1.Object) bool {
	return !reflect.DeepEqual(old.GetLabels(), new.GetLabels())
}

func annotationsChanged(old, new metav1.Object) bool {
	return !reflect.DeepEqual(old.GetAnnotations(), new.GetAnnotations())
}

func resourceVersionChanged(old, new metav1.Object) bool {
	return old.GetResourceVersion() != new.GetResourceVersion()
}

func stsReplicasChanged(old, new *v1.StatefulSet) bool {
	oldReplicas := old.Spec.Replicas
	newReplicas := new.Spec.Replicas

	oldReplicasFound := oldReplicas != nil
	newReplicasFound := newReplicas != nil

	return oldReplicasFound != newReplicasFound ||
		(oldReplicasFound && newReplicasFound && *oldReplicas != *newReplicas)
}

func stsOrdinalsStartChanged(old, new *v1.StatefulSet) bool {
	var oldStart int32 = 0
	if old.Spec.Ordinals != nil {
		oldStart = old.Spec.Ordinals.Start
	}

	var newStart int32 = 0
	if new.Spec.Ordinals != nil {
		newStart = new.Spec.Ordinals.Start
	}

	return oldStart != newStart
}

func deploymentTemplateLabelsChanged(old, new *v1.Deployment) bool {
	oldLabels := old.Spec.Template.Labels
	newLabels := new.Spec.Template.Labels

	return templateLabelsChanged(oldLabels, newLabels)
}

func stsTemplateLabelsChanged(old, new *v1.StatefulSet) bool {
	oldLabels := old.Spec.Template.Labels
	newLabels := new.Spec.Template.Labels

	return templateLabelsChanged(oldLabels, newLabels)
}

// deploymentTemplateLabelsChanged - only applicable to Deployment style resources that have a spec.template
func rolloutTemplateLabelsChanged(old, new *unstructured.Unstructured) bool {
	oldOTemplateLabels, _, _ := unstructured.NestedFieldNoCopy(old.Object, "spec", "template", "metadata", "labels")
	newTemplateLabels, _, _ := unstructured.NestedFieldNoCopy(new.Object, "spec", "template", "metadata", "labels")

	return templateLabelsChanged(oldOTemplateLabels, newTemplateLabels)
}

func templateLabelsChanged(oldLabels, newLabels interface{}) bool {
	oldFound := oldLabels != nil
	newFound := newLabels != nil

	return oldFound != newFound ||
		(oldFound && newFound && !reflect.DeepEqual(oldLabels, newLabels))
}

// isPendingResourceTracking - determines, whether given MOP update belongs to a pending resource tracking
// which we don't want to process.
// Skip if: feature is on, newMop in pending, oldMop in success/failed, newMop has a resource in pending
func isPendingResourceTracking(new *meshv1alpha1.MeshOperator) bool {
	if features.EnableMopPendingTracking &&
		new.Status.Phase == PhasePending {
		for _, rr := range new.Status.RelatedResources {
			if rr.Phase == PhasePending {
				return true
			}
		}

		if isRelatedResourceStatusSetToPending(new.Status.Services) || isRelatedResourceStatusSetToPending(new.Status.ServiceEntries) {
			return true
		}

	}
	return false
}

// isRelatedResourceStatusSetToPending checks if any of the service/SE related resource status is set to PENDING
func isRelatedResourceStatusSetToPending(servicesStatusMap map[string]*meshv1alpha1.ServiceStatus) bool {
	for _, svcStatus := range servicesStatusMap {
		for _, rr := range svcStatus.RelatedResources {
			if rr.Phase == PhasePending {
				return true
			}
		}
	}
	return false
}
