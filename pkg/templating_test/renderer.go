//go:build !ignore_test_utils

package templating_test

import (
	"github.com/istio-ecosystem/mesh-operator/pkg/constants"
	"github.com/istio-ecosystem/mesh-operator/pkg/templating"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	gvk = schema.GroupVersionKind{
		Group:   constants.EnvoyFilterResource.Group,
		Version: constants.EnvoyFilterResource.Version,
		Kind:    constants.EnvoyFilterKind.Kind,
	}
)

// TestRendererForMop creates a filter with the given name in the provided MOP's namespace,
// or throws the provided error in Render.
type TestRendererForMop struct {
	RenderErrorToThrow error
	TemplateType       string
	FilterName         string
}

func (r *TestRendererForMop) Render(ctx *templating.RenderRequestContext) (*templating.GeneratedConfig, error) {
	if r.RenderErrorToThrow != nil {
		return nil, r.RenderErrorToThrow
	}

	rendered := make(map[string][]*unstructured.Unstructured)
	if ctx != nil && ctx.MeshOperator != nil {
		filter := &unstructured.Unstructured{Object: map[string]interface{}{}}
		filter.SetName(r.FilterName)
		filter.SetNamespace(ctx.MeshOperator.Namespace)
		filter.SetGroupVersionKind(gvk)
		rendered[r.TemplateType] = []*unstructured.Unstructured{filter}
	}
	return &templating.GeneratedConfig{Config: rendered}, nil
}

// FixedResultRenderer is a 'transparent' renderer that literally returns the results desired.
type FixedResultRenderer struct {
	ResultToReturn templating.GeneratedConfig
	ErrorToThrow   error
}

func (r *FixedResultRenderer) Render(_ *templating.RenderRequestContext) (*templating.GeneratedConfig, error) {
	return &r.ResultToReturn, r.ErrorToThrow
}
