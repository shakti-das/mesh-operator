package templating_test

import (
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/templating"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// FakeMutator either adds a prefix to the config object name, or throws an error.
type FakeMutator struct {
	MutatorErrorToThrow error
	MutatorPrefix       string
}

func (m *FakeMutator) Mutate(_ *templating.RenderRequestContext,
	config *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	if m.MutatorErrorToThrow != nil {
		return nil, m.MutatorErrorToThrow
	}

	previousName := config.GetName()
	config.SetName(m.MutatorPrefix + previousName)
	return config, nil
}
