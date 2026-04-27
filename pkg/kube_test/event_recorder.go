package kube_test

import (
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

// TestableEventRecorder
type TestableEventRecorder struct {
	record.EventRecorder

	RecordedObject runtime.Object
	Eventtype      string
	Reason         string
	Message        string
}

func NewTestableEventRecorder(object runtime.Object, eventtype, reason, message string) *TestableEventRecorder {
	return &TestableEventRecorder{RecordedObject: object, Eventtype: eventtype, Reason: reason, Message: message}
}

func (r *TestableEventRecorder) Event(object runtime.Object, eventtype, reason, message string) {
	r.RecordedObject = object
	r.Eventtype = eventtype
	r.Reason = reason
	r.Message = message
}
