package transition

type TransitionMetrics string

const (
	MeshConfigTotal            TransitionMetrics = "mesh_config_total"
	MeshConfigTransitionErrors TransitionMetrics = "mesh_config_transition_errors"

	MopTransitionMissingConfigs              TransitionMetrics = "transition_missing_configs"
	MopTransitionExtraConfigs                TransitionMetrics = "transition_extra_configs_generated"
	MopTransitionDifferentConfigSpec         TransitionMetrics = "transition_different_config_spec"
	MopTransitionUnknownConfigGenerated      TransitionMetrics = "transition_unknown_config_generated"
	MopTransitionSuccess                     TransitionMetrics = "transition_success"
	MopTransitionDeprecatedCopilotTemplate   TransitionMetrics = "transition_deprecated_template"
	MopTransitionComparisonFailure           TransitionMetrics = "transition_comparison_failure"
	MopTransitionFetchCopilotResourceFailure TransitionMetrics = "transition_fetch_copilot_resource_failure"

	CasamInitOverlaysMopSuccess = "transition_casam_init_success"
	CasamInitOverlaysMopError   = "transition_casam_init_error"
)

func (m TransitionMetrics) GetName() string {
	return string(m)
}
