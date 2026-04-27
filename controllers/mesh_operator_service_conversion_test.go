package controllers

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/kube_test"
	"github.com/stretchr/testify/assert"
	"go.uber.org/zap/zaptest"
)

func TestConvertObjectToUnstructured_Service_Success(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	mop := kube_test.NewMopBuilder("test-ns", "test-mop").Build()
	service := kube_test.NewServiceBuilder("test-service", "test-ns").
		Build()

	ok, updatedMop, serviceAsUnstructured := convertObjectToUnstructured(
		logger,
		mop,
		constants.ServiceKind.Kind,
		&service,
	)

	assert.True(t, ok)
	assert.Equal(t, mop, updatedMop) // MOP should be unchanged on success
	assert.NotNil(t, serviceAsUnstructured)

	assert.NotNil(t, serviceAsUnstructured)
	assert.Equal(t, "test-service", serviceAsUnstructured.GetName())
	assert.Equal(t, "test-ns", serviceAsUnstructured.GetNamespace())
	assert.Equal(t, constants.ServiceKind.Kind, serviceAsUnstructured.GetKind())
}

func TestConvertObjectToUnstructured_ServiceEntry_Success(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	mop := kube_test.NewMopBuilder("test-ns", "test-mop").Build()
	serviceEntry := kube_test.CreateServiceEntryWithLabels("test-ns", "test-se", map[string]string{})

	ok, updatedMop, seAsUnstructured := convertObjectToUnstructured(
		logger,
		mop,
		constants.ServiceEntryKind.Kind,
		serviceEntry,
	)

	assert.True(t, ok)
	assert.Equal(t, mop, updatedMop)
	assert.NotNil(t, seAsUnstructured)

	assert.Equal(t, "test-se", seAsUnstructured.GetName())
	assert.Equal(t, "test-ns", seAsUnstructured.GetNamespace())
	assert.Equal(t, constants.ServiceEntryKind.Kind, seAsUnstructured.GetKind())
}

func TestConvertObjectToUnstructured_Service_NilObject(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	mop := kube_test.NewMopBuilder("test-ns", "test-mop").Build()

	// Try to convert nil object - this should fail
	ok, updatedMop, serviceAsUnstructured := convertObjectToUnstructured(
		logger,
		mop,
		constants.ServiceKind.Kind,
		nil,
	)

	// We don't know object details, so just fail the whole mop here
	assert.False(t, ok)
	assert.Equal(t, mop, updatedMop)
	assert.Nil(t, serviceAsUnstructured)
}

func TestConvertObjectToUnstructured_Service_EmptyService(t *testing.T) {
	logger := zaptest.NewLogger(t).Sugar()
	mop := kube_test.NewMopBuilder("test-ns", "test-mop").Build()
	service := corev1.Service{}

	// Try to convert nil object - this should fail
	ok, updatedMop, serviceAsUnstructured := convertObjectToUnstructured(
		logger,
		mop,
		constants.ServiceKind.Kind,
		&service,
	)

	// We don't know object details, so just fail the whole mop here
	assert.False(t, ok)
	assert.Equal(t, mop, updatedMop)
	assert.Nil(t, serviceAsUnstructured)
}
