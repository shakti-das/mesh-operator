package dynamicrouting

import (
	"strings"
)

// GetUniqueLabelSubsets returns a json string where key is a unique combination of all subset label values and value is the
// corresponding key-value pair {labelKey: labelValue} that is used to represent the key.
func GetUniqueLabelSubsets(subsetLabelKeys []string, deploymentLabels []map[string]string) map[string]map[string]string {
	// key is unique subset label value concatenated with '-'. Eg : v1-u1
	// value is a map that is used to represent above key {version: v1, user: u1}, where version,user
	// are the keys that are defined in subsetLabelKeys
	uniqueSubsets := make(map[string]map[string]string)
	for _, deploymentLabel := range deploymentLabels {
		partialMatch := false
		uniqueKey := ""
		uniqueValue := map[string]string{}
		//skipSeparator := true
		for _, subsetLabelKey := range subsetLabelKeys {

			if val, ok := deploymentLabel[subsetLabelKey]; ok {
				uniqueKey += val + "-"
				uniqueValue[subsetLabelKey] = val
			} else {
				partialMatch = true
				break
			}
		}
		// add unique subset only if deployment labels match with subsetLabel definition
		if !partialMatch {
			// trim any trailing dashes
			uniqueKey = strings.TrimSuffix(uniqueKey, "-")
			uniqueSubsets[uniqueKey] = uniqueValue
		}

	}
	return uniqueSubsets
}
