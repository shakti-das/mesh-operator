//go:build !ignore_test_utils

package templating_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/cache"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
)

type GeneratedConfigMetadata struct {
	ObjectKey         string
	MopKey            string
	TemplatesRendered []string
	Metadata          map[string]string
	Owners            []metav1.OwnerReference
	ApplicatorResult  []*templating.AppliedConfigObject
}

// TestableApplicator - an implementation of the (k8s) applicator that captures the namespace/name of the service
// passed to render, as well as the list of templates rendered for it.
type TestableApplicator struct {
	ApplicatorError    error
	K8sPatchErrorToAdd error
	AppliedResults     []GeneratedConfigMetadata
}

func (p *TestableApplicator) WaitForRender(expectedAppliedConfigCount int) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	for {
		if len(p.AppliedResults) >= expectedAppliedConfigCount || ctx.Err() != nil {
			break
		}
	}
}

func (p *TestableApplicator) ApplyServiceConfig(context *templating.RenderRequestContext,
	config *templating.GeneratedConfig) ([]*unstructured.Unstructured, error) {
	return p.internalApply(context, config)
}

func (p *TestableApplicator) ApplyConfig(context *templating.RenderRequestContext,
	generatedConfig *templating.GeneratedConfig, _ *zap.SugaredLogger) ([]*templating.AppliedConfigObject, error) {
	if generatedConfig == nil {
		return nil, nil
	}
	objects, err := p.internalApply(context, generatedConfig)
	var results []*templating.AppliedConfigObject
	for i := range objects {
		patched := templating.AppliedConfigObject{
			Object: objects[i],
			Error:  p.K8sPatchErrorToAdd,
		}
		results = append(results, &patched)
	}
	return results, err
}

func (p *TestableApplicator) internalApply(context *templating.RenderRequestContext,
	renderedConfig *templating.GeneratedConfig) ([]*unstructured.Unstructured, error) {

	if p.ApplicatorError != nil {
		return nil, p.ApplicatorError
	}

	var serviceKey = ""
	var mopKey = ""
	if context.Object != nil {
		serviceKey, _ = cache.MetaNamespaceKeyFunc(context.Object)
	}
	if context.MeshOperator != nil {
		mopKey, _ = cache.MetaNamespaceKeyFunc(context.MeshOperator)
	}

	var templates []string
	var objects []*unstructured.Unstructured
	result := renderedConfig.Config
	for k := range result {
		templates = append(templates, k)
		objects = append(objects, result[k]...)
	}

	var results []*templating.AppliedConfigObject
	for i := range objects {
		patched := templating.AppliedConfigObject{
			Object: objects[i],
			Error:  p.K8sPatchErrorToAdd,
		}
		results = append(results, &patched)
	}

	var owners []metav1.OwnerReference
	if context.OwnerRef != nil {
		owners = []metav1.OwnerReference{*context.OwnerRef}
	}

	p.AppliedResults = append(
		p.AppliedResults,
		GeneratedConfigMetadata{
			ObjectKey:         serviceKey,
			MopKey:            mopKey,
			TemplatesRendered: templates,
			Metadata:          context.Metadata,
			Owners:            owners,
			ApplicatorResult:  results,
		})

	return objects, nil
}

func (p *TestableApplicator) Reset() {
	p.AppliedResults = []GeneratedConfigMetadata{}
}

func (p *TestableApplicator) AssertResults(t *testing.T, expectedConfigMetadata []GeneratedConfigMetadata) {
	assert.Equal(t, len(expectedConfigMetadata), len(p.AppliedResults))

	// Sort expected and actual, to make sure consistent tests
	sortFunc := func(a, b GeneratedConfigMetadata) int {
		comp := strings.Compare(a.ObjectKey, b.ObjectKey)
		if comp != 0 {
			return comp
		}
		return strings.Compare(a.MopKey, b.MopKey)
	}

	expectedSorted := append([]GeneratedConfigMetadata{}, expectedConfigMetadata...)
	slices.SortStableFunc(expectedSorted, sortFunc)
	actualSorted := append([]GeneratedConfigMetadata{}, p.AppliedResults...)
	slices.SortStableFunc(actualSorted, sortFunc)

	for i := range expectedSorted {
		assert.Equal(t, expectedSorted[i].ObjectKey, actualSorted[i].ObjectKey)
		assert.Equal(t, expectedSorted[i].MopKey, actualSorted[i].MopKey)
		assert.ElementsMatch(t, expectedSorted[i].TemplatesRendered, actualSorted[i].TemplatesRendered)
		assert.Equal(t, expectedSorted[i].Metadata, actualSorted[i].Metadata)
		assert.ElementsMatch(t, expectedSorted[i].Owners, actualSorted[i].Owners)
	}
}
