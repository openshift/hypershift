package hostedcluster

const (
	CountByIdentityProviderMetricName            = "hypershift_cluster_identity_providers"
	CountByPlatformMetricName                    = "hypershift_hostedclusters"
	CountByPlatformAndFailureConditionMetricName = "hypershift_hostedclusters_failure_conditions"
	TransitionDurationMetricName                 = "hypershift_hosted_cluster_transition_seconds"

	WaitingInitialAvailabilityDurationMetricName  = "hypershift_cluster_waiting_initial_availability_duration_seconds"
	InitialRollingOutDurationMetricName           = "hypershift_cluster_initial_rolling_out_duration_seconds"
	UpgradingDurationMetricName                   = "hypershift_cluster_upgrading_duration_seconds"
	LimitedSupportEnabledMetricName               = "hypershift_cluster_limited_support_enabled"
	SilenceAlertsMetricName                       = "hypershift_cluster_silence_alerts"
	ProxyMetricName                               = "hypershift_cluster_proxy"
	ProxyCAValidMetricName                        = "hypershift_cluster_proxy_ca_valid"
	ProxyCAExpiryTimestampName                    = "hypershift_cluster_proxy_ca_expiry_timestamp"
	InvalidAwsCredsMetricName                     = "hypershift_cluster_invalid_aws_creds"
	InvalidGcpCredsMetricName                     = "hypershift_cluster_invalid_gcp_creds"
	DeletingDurationMetricName                    = "hypershift_cluster_deleting_duration_seconds"
	GuestCloudResourcesDeletingDurationMetricName = "hypershift_cluster_guest_cloud_resources_deleting_duration_seconds"
	EtcdManualInterventionRequiredMetricName      = "hypershift_etcd_manual_intervention_required"
	ClusterSizeOverrideMetricName                 = "hypershift_cluster_size_override_instances"
	HostedClusterManagedAzureInfoMetricName       = "hosted_cluster_managed_azure_info"
	HostedClusterAzureInfoMetricName              = "hosted_cluster_azure_info"
	AcrPullIdentityConfiguredMetricName           = "hypershift_cluster_acr_pull_identity_configured"
)
