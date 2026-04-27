//go:build !ignore_test_utils

package controller_test

type FakeNamespacesNamespaceFilter struct {
}

func (f *FakeNamespacesNamespaceFilter) IsNamespaceMopEnabledForObject(_ interface{}) bool {
	return true
}

func (f *FakeNamespacesNamespaceFilter) Run(_ <-chan struct{}) bool {
	// Nothing to do here.
	return true
}
