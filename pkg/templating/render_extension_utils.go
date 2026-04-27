package templating

import (
	"errors"
	"reflect"
	"strings"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/features"

	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/api/mesh.io/v1alpha1"
	"git.soma.salesforce.com/services/go-sfdc-bazel/projects/services/servicemesh/mesh-operator/pkg/constants"
)

func getConfigNsExtensionToNamespace() map[string]string {
	return map[string]string{
		constants.ConfigNamespaceEnabled:        features.ConfigNamespace,
		constants.IngressConfigNamespaceEnabled: features.IngressConfigNamespace,
	}
}

// GetExtensionType - method that determines the "public" type/name of the extension (eg. faultInjection, oauth etc.)
func GetExtensionType(extensionElement v1alpha1.ExtensionElement) (string, error) {
	extensionType, _, err := getExtensionTypeAndFieldName(extensionElement)
	return extensionType, err
}

// getExtensionType - method scans through the provided extension element, determines 1st non-nil field
// json-name of the field is the type of the extension, if field doesn't have json tag, field name is used.
// Expectation is that no more than one field on the extension element is set, which must be enforced by CRD schema.
// Returns extension "type" (like faultInjection, outh etc.) and extension field name
func getExtensionTypeAndFieldName(extensionElement v1alpha1.ExtensionElement) (string, string, error) {
	typ := reflect.TypeOf(extensionElement)
	v := reflect.ValueOf(extensionElement)
	ind := reflect.Indirect(v)
	for i := 0; i < typ.NumField(); i++ {
		val := ind.Field(i)
		if val.IsNil() {
			continue
		}
		field := typ.Field(i)
		jsonTag, found := field.Tag.Lookup("json")
		if !found {
			continue
		}
		splitTag := strings.Split(jsonTag, ",")
		if len(splitTag) > 0 {
			return splitTag[0], field.Name, nil
		} else {
			return "", field.Name, nil
		}
	}

	return "", "", errors.New("extension type not supported by MeshOperator")
}

func retrieveExtensionIndex(extensionObjectName string) string {
	nameSlice := strings.Split(extensionObjectName, "-")
	return nameSlice[len(nameSlice)-1]
}

// IsConfigNamespaceExtension checks if a given extension type is a config ns extension, and returns corresponding config namespace (if config extension)
func IsConfigNamespaceExtension(extensionFieldName string) (bool, string) {
	typ := reflect.TypeOf(v1alpha1.ExtensionElement{})
	extElementField, found := typ.FieldByName(extensionFieldName)

	if !found {
		return false, "" // That's weird and shouldn't happen
	}

	configNsExtensionToNamespace := getConfigNsExtensionToNamespace()

	fieldTyp := extElementField.Type
	if fieldTyp.Kind() == reflect.Pointer {
		fieldStructType := fieldTyp.Elem()
		if fieldStructType.Kind() == reflect.Struct {
			for configNsExtensionAnnotation, configNamespace := range configNsExtensionToNamespace {
				_, found = fieldStructType.FieldByName(configNsExtensionAnnotation)
				if found {
					return true, configNamespace
				}
			}
		}
	}
	return false, ""
}
