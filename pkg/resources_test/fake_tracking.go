//go:build !ignore_test_utils

package resources_test

import (
	"fmt"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/kube"

	"go.uber.org/zap"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func NewFakeResourceManager() *FakeResourceManager {
	return &FakeResourceManager{
		RecordedTrackRequests: []TrackRequest{},
	}
}

type TrackRequest struct {
	ClusterName string
	Namespace   string
	Kind        string
	Name        string
	UID         types.UID
}

type OnChangeRequest struct {
	ClusterName string
	ServiceKey  string
	Resources   []*unstructured.Unstructured
}

type FakeResourceManager struct {
	MsmOnTrack     v1alpha1.MeshServiceMetadata
	ErrorOnTrack   error
	ErrorOnChange  error
	ErrorOnUntrack error

	RecordedTrackRequests    []TrackRequest
	RecordedOnChangeRequests []OnChangeRequest
	RecordedUnTrackRequests  []TrackRequest
}

func (m *FakeResourceManager) TrackOwner(clusterName string, namespace string, owner *v1alpha1.Owner, _ *zap.SugaredLogger) (
	*v1alpha1.MeshServiceMetadata, error) {
	if m.ErrorOnTrack != nil {
		return nil, m.ErrorOnTrack
	}
	m.RecordedTrackRequests = append(m.RecordedTrackRequests,
		TrackRequest{
			ClusterName: clusterName,
			Namespace:   namespace,
			Kind:        owner.Kind,
			Name:        owner.Name,
			UID:         owner.UID})
	return &m.MsmOnTrack, nil
}

func (m *FakeResourceManager) OnOwnerChange(_ string, _ kube.Client, msm *v1alpha1.MeshServiceMetadata, _ interface{}, owner *v1alpha1.Owner, resources []*unstructured.Unstructured, _ *zap.SugaredLogger) error {

	if m.ErrorOnChange != nil {
		return m.ErrorOnChange
	}
	m.RecordedOnChangeRequests = append(m.RecordedOnChangeRequests, OnChangeRequest{
		ClusterName: owner.Cluster,
		ServiceKey:  fmt.Sprintf("%s/%s", msm.Namespace, owner.Name),
		Resources:   resources,
	})

	return nil
}

func (m *FakeResourceManager) UnTrackOwner(clusterName, namespace, kind, ownerServiceName string, uid types.UID, _ *zap.SugaredLogger) error {
	if m.ErrorOnUntrack != nil {
		return m.ErrorOnUntrack
	}
	m.RecordedUnTrackRequests = append(m.RecordedUnTrackRequests, TrackRequest{
		ClusterName: clusterName,
		Namespace:   namespace,
		Kind:        kind,
		Name:        ownerServiceName,
		UID:         uid,
	})
	return nil
}
