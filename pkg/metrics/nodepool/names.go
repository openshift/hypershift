package nodepool

const (
	CountByPlatformMetricName                    = "hypershift_nodepools"
	CountByPlatformAndFailureConditionMetricName = "hypershift_nodepools_failure_conditions"
	CountByHClusterMetricName                    = "hypershift_hostedcluster_nodepools"
	VCpusCountByHClusterMetricName               = "hypershift_cluster_vcpus"
	VCpusComputationErrorByHClusterMetricName    = "hypershift_cluster_vcpus_computation_error"
	TransitionDurationMetricName                 = "hypershift_nodepools_transition_seconds"

	InitialRollingOutDurationMetricName = "hypershift_nodepools_initial_rolling_out_duration_seconds"
	SizeMetricName                      = "hypershift_nodepools_size"
	AvailableReplicasMetricName         = "hypershift_nodepools_available_replicas"
	DeletingDurationMetricName          = "hypershift_nodepools_deleting_duration_seconds"
)
