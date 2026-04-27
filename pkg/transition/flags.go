package transition

import (
	"os"
	"strconv"
	"strings"
)

const (
	MopTransitionAllowCopilotRendering = true
)

var (
	EnableCopilotToMopTransition      = getBooleanEnvValue("ENABLE_COPILOT_TO_MOP_TRANSITION", false)
	TransitionTemplatesOwnedByCopilot = getTransitionTemplateTypesOwnedByCopilot("TRANSITION_TEMPLATE_TYPES_OWNED_BY_COPILOT")

	EnableInitCasamMops = getBooleanEnvValue("ENABLE_INIT_CASAM_MOPS", false)
)

func getStringEnvValue(name string) string {
	val, _ := os.LookupEnv(name)
	return val
}

func getTransitionTemplateTypesOwnedByCopilot(name string) []string {
	templateTypesOwnedByCopilot := make([]string, 0)
	listOfTemplateTypes := strings.Split(getStringEnvValue(name), ",")
	for _, val := range listOfTemplateTypes {
		parts := strings.Split(val, "/")
		if len(parts) == 2 {
			templateTypesOwnedByCopilot = append(templateTypesOwnedByCopilot, val)
		}
	}
	return templateTypesOwnedByCopilot
}

func getBooleanEnvValue(name string, defaultValue bool) bool {
	if val, ok := os.LookupEnv(name); ok {
		booleanVal, err := strconv.ParseBool(val)
		if err != nil {
			return defaultValue
		}
		return booleanVal
	}
	return defaultValue
}
