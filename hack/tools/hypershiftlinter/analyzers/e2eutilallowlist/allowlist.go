package e2eutilallowlist

// allowlist records existing legacy utility references by exact source file and
// enclosing function (or top-level Describe registration). Each entry permits only
// the listed symbols, including references in nested closures. Remove entries as
// callers migrate to v2 utilities; do not broaden this migration baseline.
// Unqualified symbols belong to test/e2e/util; subpackages use e.g. dump.Symbol.
var allowlist = map[string]map[string][]string{
	"test/e2e/v2/backuprestore/etcd_snapshot.go": {
		"VerifyEtcdClusterHealth": {
			"RunCommandInPod",
		},
	},
	"test/e2e/v2/cmd/run-tests/testdata/junitfixture/suite_test.go": {
		"Describe(\"[sig-hypershift][Jira:Hypershift][Feature:JUnitContract] Synthetic JUnit contract\")": {
			"Version51",
		},
		"validateWIFMutation": {
			"ValidateGCPWorkloadIdentityWebhookMutation",
		},
	},
	"test/e2e/v2/internal/test_context.go": {
		"SetupTestContextFromEnv": {
			"GetConfig",
		},
		"setupTestContext": {
			"GetClientForConfig",
		},
	},
	"test/e2e/v2/internal/validation.go": {
		"ValidateConditions": {
			"Condition",
			"Condition.Matches",
			"Condition.String",
			"Condition.Type",
			"Conditions",
		},
	},
	"test/e2e/v2/lifecycle/external_oidc.go": {
		"externalOIDCSetup.postVersionRollout": {
			"ExtOIDCConfig",
			"ExtOIDCConfig.CliClientID",
			"ExtOIDCConfig.ConsoleClientID",
			"ExtOIDCConfig.ConsoleClientSecretName",
			"ExtOIDCConfig.ConsoleClientSecretValue",
			"ExtOIDCConfig.ExternalOIDCProvider",
			"ExtOIDCConfig.GetAuthenticationConfig",
			"ExtOIDCConfig.GroupPrefix",
			"ExtOIDCConfig.IssuerCAConfigmapName",
			"ExtOIDCConfig.IssuerURL",
			"ExtOIDCConfig.OIDCProviderName",
			"ExtOIDCConfig.TestUsers",
			"ExtOIDCConfig.UserPrefix",
			"ProviderKeycloak",
		},
	},
	"test/e2e/v2/tests/azure_diagnostics_bootstrap_test.go": {
		"RegisterAzureBootstrapDiagnosticsTests": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
		},
	},
	"test/e2e/v2/tests/azure_machine_diagnostics_test.go": {
		"RegisterAzureMachineDiagnosticsTests": {
			"dump.VerifyAzureMachineDiagnostics",
		},
	},
	"test/e2e/v2/tests/backup_restore_test.go": {
		"Describe(\"[sig-hypershift][Jira:Hypershift][Feature:BackupRestore] BackupRestore\")": {
			"Condition",
		},
		"Describe(\"[sig-hypershift][Jira:Hypershift][Feature:CrossClusterRestore] CrossClusterRestore\")": {
			"Condition",
		},
		"Describe(\"[sig-hypershift][Jira:Hypershift][Feature:EtcdSnapshot] BackupRestoreEtcdSnapshot\")": {
			"Condition",
		},
		"validatePostRestoreControlPlane": {
			"Condition",
		},
		"validatePreBackupControlPlane": {
			"Condition",
			"Condition.Status",
			"Condition.Type",
			"Version421",
		},
	},
	"test/e2e/v2/tests/control_plane_tls_operator_test.go": {
		"VerifyPKIOperatorTLSConfigTest": {
			"GetConfig",
		},
	},
	"test/e2e/v2/tests/control_plane_upgrade_test.go": {
		"ControlPlaneUpgradeTest": {
			"UpdateObject",
			"Version422",
		},
	},
	"test/e2e/v2/tests/control_plane_workloads_test.go": {
		"ContainerTerminationMessagePolicyTest": {
			"Version419",
		},
		"CustomLabelsTest": {
			"Version419",
		},
		"CustomTolerationsTest": {
			"Version419",
		},
		"NoCrashingPodsTest": {
			"GetConfig",
		},
		"ReadOnlyRootFilesystemTest": {
			"Version420",
		},
		"ReadOnlyRootFilesystemTmpDirMountTest": {
			"Version420",
		},
		"SafeToEvictAnnotationsTest": {
			"Version420",
		},
		"ServiceAccountTokenMountingTest": {
			"Version416",
			"Version418",
		},
	},
	"test/e2e/v2/tests/etcd_chaos_test.go": {
		"EtcdKillAllMembersTest": {
			"EventuallyObjects",
			"Predicate",
			"WithInterval",
			"WithTimeout",
		},
		"EtcdMissingMemberRecoveryTest": {
			"EventuallyObject",
			"Predicate",
			"RunCommandInPod",
			"WithInterval",
			"WithTimeout",
		},
		"EtcdSingleMemberCorruptionTest": {
			"EventuallyObject",
			"Predicate",
			"RunCommandInPod",
			"WithInterval",
			"WithTimeout",
		},
		"EtcdSingleMemberRecoveryTest": {
			"EventuallyObject",
			"Predicate",
			"WithInterval",
			"WithTimeout",
		},
		"createMarkerConfigMap": {
			"EventuallyObject",
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
		},
		"verifyMarkerSurvived": {
			"EventuallyObject",
			"Predicate",
			"WithInterval",
			"WithTimeout",
		},
		"waitForEtcdConvergence": {
			"EventuallyObject",
			"Predicate",
			"WithInterval",
			"WithTimeout",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_aws_test.go": {
		"AWSCCMWithCustomizationsTest": {
			"Version423",
		},
		"AWSResourceTagOverridePolicyTest": {
			"UpdateObject",
			"WaitForReadyNodesByNodePool",
		},
		"EnsureDefaultSecurityGroupTagsTest": {
			"GetDefaultSecurityGroup",
			"PutRolePolicy",
			"UpdateObject",
			"Version420",
		},
		"EnsureDefaultSecurityGroupTagsWithSpacesTest": {
			"GetDefaultSecurityGroup",
			"PutRolePolicy",
			"UpdateObject",
			"Version420",
		},
		"NodePoolDay2TagsWithSpacesTest": {
			"UpdateObject",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_azure_test.go": {
		"AzureEndpointAccessTransitionTest": {
			"EventuallyObject",
			"EventuallyObjects",
			"Predicate",
			"UpdateObject",
			"WithTimeout",
		},
		"AzureOAuthLoadBalancerPrivateTest": {
			"EventuallyObject",
			"Predicate",
			"SetupGuestKASPortForwardConfig",
			"SetupOAuthPortForwardTransport",
			"ValidateOAuthIdentityProviderFlow",
			"WaitForOAuthLoadBalancerEndpoint",
			"WithGuestConfig",
			"WithTimeout",
			"WithTransport",
			"WithTransportFactory",
		},
		"AzureOAuthLoadBalancerTest": {
			"EventuallyObject",
			"ValidateOAuthWithIdentityProviderViaLoadBalancer",
			"WithTimeout",
		},
		"AzurePrivateTopologyTest": {
			"EventuallyObject",
			"EventuallyObjects",
			"Predicate",
			"WithInterval",
			"WithTimeout",
		},
		"AzurePublicClusterTest": {
			"ValidateAzureWorkloadIdentityWebhookMutation",
			"ValidateIngressOperatorConfiguration",
			"ValidateKubeAPIServerAllowedCIDRs",
			"Version420",
			"Version421",
			"WaitForGuestKubeConfig",
		},
		"oauthServiceLBPredicates": {
			"Predicate",
		},
		"plsExistsPredicate": {
			"Predicate",
		},
		"routeHasHostPredicate": {
			"Predicate",
		},
		"serviceTypePredicate": {
			"Predicate",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_ccm_test.go": {
		"GCPCloudControllerManagerTest": {
			"WaitForGuestKubeConfig",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_compliance_test.go": {
		"EnsureKASConnectionCheckerSpecTest": {
			"Version423",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_cpo_test.go": {
		"VerifyCPOOverrideImageTest": {
			"ExtractVersionFromReleaseImage",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_dns_test.go": {
		"EnsureKubeAPIDNSNameTests": {
			"Version419",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_external_oidc_test.go": {
		"ExternalOIDCKeycloakAuthTest": {
			"ExtOIDCConfig",
			"ExtOIDCConfig.CliClientID",
			"ExtOIDCConfig.ConsoleClientID",
			"ExtOIDCConfig.ExternalOIDCProvider",
			"ExtOIDCConfig.GroupPrefix",
			"ExtOIDCConfig.IssuerURL",
			"ExtOIDCConfig.TestUsers",
			"ExtOIDCConfig.UserPrefix",
			"ProviderKeycloak",
		},
		"obtainKeycloakIDToken": {
			"ExtOIDCConfig",
			"ExtOIDCConfig.CliClientID",
			"ExtOIDCConfig.IssuerURL",
			"ExtOIDCConfig.TestUsers",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_gcp_wif_test.go": {
		"GCPWorkloadIdentityTest": {
			"ValidateGCPWorkloadIdentityWebhookMutation",
			"Version51",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_health_test.go": {
		"EnsureCAPIFinalizersTest": {
			"Version422",
		},
		"EnsureFeatureGateStatusTest": {
			"Version419",
		},
		"ValidateConfigurationStatusTest": {
			"Version421",
		},
		"ValidateHostedClusterConditionsTest": {
			"Version421",
			"Version422",
			"Version423",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_ingress_test.go": {
		"ServiceProviderDefaultIngressServingCertificateLifecycleTest": {
			"UpdateObject",
		},
		"ValidateIngressOperatorConfigurationTest": {
			"Version421",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_managed_dns_test.go": {
		"AWSManagedDNSTest": {
			"Version51",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_metrics_test.go": {
		"EnsureKubeSchedulerMetricsEndpointTest": {
			"Version423",
		},
		"EnsureMetricsForwarderWorkingTest": {
			"Version422",
		},
		"EnsureNodeTuningOperatorMetricsEndpointTest": {
			"GetConfig",
			"Version422",
		},
		"ValidateCPOMetricsTest": {
			"GetConfig",
			"Version51",
		},
		"ValidateMetricsTest": {
			"GetConfig",
		},
		"fetchMetricsViaPortForward": {
			"GetConfig",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_node_communication_test.go": {
		"EnsureGCPWorkerFirewallTest": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
		},
		"buildGCPFirewallProbeDeployment": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_pull_secret_test.go": {
		"EnsureGlobalPullSecretTest": {
			"Version420",
			"Version422",
			"WaitForReadyNodesByNodePool",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_secret_encryption_test.go": {
		"KMSFunctionalValidationTest": {
			"RunCommandInPod",
			"Version417",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_security_test.go": {
		"EnsureAdmissionPoliciesTest": {
			"Version418",
			"Version51",
		},
		"EnsureNetworkPoliciesTest": {
			"GetConfig",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_storage_kms_test.go": {
		"StorageKMSPropagationTest": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
			"Version51",
		},
	},
	"test/e2e/v2/tests/karpenter_control_plane_upgrade_test.go": {
		"Describe(\"[sig-hypershift][Jira:Hypershift] Karpenter\")": {
			"Version422",
			"Version423",
		},
		"KarpenterUpgradeTest": {
			"UpdateObject",
		},
		"expectControlPlaneRolloutWithoutDrift": {
			"Condition.Status",
			"Condition.Type",
			"Conditions",
		},
	},
	"test/e2e/v2/tests/karpenter_test.go": {
		"Describe(\"[sig-hypershift][Jira:Hypershift] Karpenter\")": {
			"Version422",
			"Version423",
		},
		"KarpenterARM64ProvisioningTest": {
			"WaitForReadyNodesByLabels",
		},
		"KarpenterArbitrarySubnetTest": {
			"CreateTestSubnet",
			"E2ETagsFromEnvironment",
			"WaitForReadyNodesByLabels",
		},
		"KarpenterAutoNodeLifecycleTest": {
			"Condition",
			"Condition.Reason",
			"Condition.Status",
			"Condition.String",
			"Condition.Type",
			"ConditionPredicate",
			"Conditions",
			"EventuallyObject",
			"Predicate",
			"UpdateObject",
			"WithTimeout",
		},
		"KarpenterBillingConsolidationTest": {
			"UpdateObject",
			"WaitForReadyNodesByLabels",
		},
		"KarpenterCPUOptionsNestedVirtualization": {
			"WaitForReadyNodesByLabels",
		},
		"KarpenterCapacityReservationTest": {
			"CreateCapacityReservation",
			"E2ETagsFromEnvironment",
			"EventuallyObject",
			"Predicate",
			"WaitForReadyNodesByLabels",
			"WithInterval",
			"WithTimeout",
		},
		"KarpenterInstanceProfileTest": {
			"UpdateObject",
			"WaitForReadyNodesByLabels",
		},
		"KarpenterKubeletPropagationTest": {
			"Condition",
			"Condition.Status",
			"Condition.Type",
			"ConditionPredicate",
			"EventuallyObject",
			"Predicate",
			"WaitForGuestRestConfig",
			"WaitForReadyNodesByLabels",
			"WithInterval",
			"WithTimeout",
		},
		"KarpenterNodeClassVersionTest": {
			"Condition",
			"Condition.Reason",
			"Condition.Status",
			"Condition.Type",
			"ConditionPredicate",
			"EventuallyObject",
			"Predicate",
			"WaitForNReadyNodesWithOptions",
			"WaitForReadyNodesByLabels",
			"WithClientOptions",
			"WithPredicates",
			"WithTimeout",
		},
		"KarpenterPlumbingTests": {
			"Condition",
			"Condition.Reason",
			"Condition.Status",
			"Condition.Type",
			"ConditionPredicate",
			"EventuallyObject",
			"GetMetricsFromPod",
			"Predicate",
			"ValidateMetricPresence",
			"WithTimeout",
		},
		"getVCPUsMetric": {
			"GetMetricsFromPod",
		},
	},
	"test/e2e/v2/tests/nodepool_arm64_create_test.go": {
		"NodePoolArm64CreateTest": {
			"WaitForReadyNodesByNodePool",
		},
	},
	"test/e2e/v2/tests/nodepool_autoscaling_test.go": {
		"AutoscalingBalancingTest": {
			"EventuallyObject",
			"Predicate",
			"Version420",
			"WaitForNReadyNodesWithOptions",
			"WithClientOptions",
			"WithInterval",
			"WithTimeout",
		},
		"AutoscalingScaleFromZeroTest": {
			"EventuallyObject",
			"Predicate",
			"WaitForNReadyNodesWithOptions",
			"WithClientOptions",
			"WithInterval",
			"WithTimeout",
		},
		"AutoscalingScaleUpDownTest": {
			"WaitForNReadyNodesWithOptions",
			"WithClientOptions",
		},
		"buildAutoscalingNodePool": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
		},
		"cleanupNodePool": {
			"EventuallyNotFound",
			"WithInterval",
			"WithTimeout",
		},
		"newAutoscalingWorkload": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
		},
	},
	"test/e2e/v2/tests/nodepool_lifecycle_test.go": {
		"NodePoolAutoRepairTest": {
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolDiskEncryptionTest": {
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolIPForwardingTest": {
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolInPlaceUpgradeTest": {
			"Condition",
			"Condition.Status",
			"Condition.Type",
			"ConditionPredicate",
			"EventuallyObject",
			"Predicate",
			"UpdateObject",
			"WaitForReadyNodesByNodePool",
			"WithTimeout",
		},
		"NodePoolMachineconfigRolloutTest": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
			"WaitForNodePoolConfigUpdateCompleteWithPlatform",
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolMirrorConfigsTest": {
			"EventuallyObjects",
			"NameGenerator.GenerateName",
			"Predicate",
			"SimpleNameGenerator",
			"Version418",
			"WaitForReadyNodesByNodePool",
			"WithInterval",
			"WithTimeout",
		},
		"NodePoolNTOInPlaceTest": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
			"WaitForNodePoolConfigUpdateCompleteWithPlatform",
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolNTOPerformanceProfileTest": {
			"EventuallyObjects",
			"NameGenerator.GenerateName",
			"Predicate",
			"SimpleNameGenerator",
			"Version417",
			"WaitForReadyNodesByNodePool",
			"WithInterval",
			"WithTimeout",
		},
		"NodePoolNTORolloutTest": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
			"WaitForNodePoolConfigUpdateCompleteWithPlatform",
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolPrevReleaseN1Test": {
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolPrevReleaseN2Test": {
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolReplaceUpgradeTest": {
			"Condition",
			"Condition.Status",
			"Condition.Type",
			"ConditionPredicate",
			"EventuallyObject",
			"Predicate",
			"UpdateObject",
			"WaitForReadyNodesByNodePool",
			"WithTimeout",
		},
		"NodePoolRollingUpgradeTest": {
			"Condition",
			"Condition.Status",
			"Condition.Type",
			"ConditionPredicate",
			"EventuallyObject",
			"Predicate",
			"UpdateObject",
			"WaitForReadyNodesByNodePool",
			"WithTimeout",
		},
		"NodePoolTrustBundleTest": {
			"Condition",
			"Condition.Status",
			"Condition.Type",
			"ConditionPredicate",
			"EventuallyNotFound",
			"EventuallyObject",
			"NameGenerator.GenerateName",
			"Predicate",
			"SimpleNameGenerator",
			"UpdateObject",
			"Version418",
			"Version422",
			"WaitForReadyNodesByNodePool",
			"WithInterval",
			"WithTimeout",
		},
		"buildMachineConfigVerificationDaemonSet": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
		},
		"buildNTOVerificationDaemonSet": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
		},
		"buildTestNodePool": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
		},
		"waitForDaemonSetRollout": {
			"EventuallyObjects",
			"Predicate",
			"WithInterval",
			"WithTimeout",
		},
	},
	"test/e2e/v2/tests/nodepool_osimagestream_test.go": {
		"NodePoolOSImageStreamCrossMajorUpgradeTest": {
			"Condition",
			"Condition.Status",
			"Condition.Type",
			"ConditionPredicate",
			"EventuallyObject",
			"OSImageStreamPredicate",
			"Predicate",
			"UpdateObject",
			"WaitForReadyNodesByNodePool",
			"WithInterval",
			"WithTimeout",
		},
		"NodePoolOSImageStreamDefaultStatusTest": {
			"EventuallyObject",
			"Predicate",
			"Version50",
			"WithInterval",
			"WithTimeout",
		},
		"NodePoolOSImageStreamExplicitDefaultNoRolloutTest": {
			"EventuallyObject",
			"Predicate",
			"WithInterval",
			"WithTimeout",
		},
		"NodePoolOSImageStreamNodeOSVerificationTest": {
			"EventuallyObject",
			"Predicate",
			"Version50",
			"WithInterval",
			"WithTimeout",
		},
		"NodePoolOSImageStreamPinnedRHEL9UpgradeTest": {
			"Condition",
			"Condition.Status",
			"Condition.Type",
			"ConditionPredicate",
			"EventuallyObject",
			"OSImageStreamPredicate",
			"Predicate",
			"UpdateObject",
			"WaitForReadyNodesByNodePool",
			"WithInterval",
			"WithTimeout",
		},
		"NodePoolOSImageStreamRHEL10RuncRejectionTest": {
			"Condition",
			"Condition.Reason",
			"Condition.Status",
			"Condition.Type",
			"ConditionPredicate",
			"EventuallyObject",
			"NameGenerator.GenerateName",
			"Predicate",
			"SimpleNameGenerator",
			"Version50",
			"WithInterval",
			"WithTimeout",
		},
		"NodePoolOSImageStreamUpgradeVerificationTest": {
			"Condition",
			"Condition.Status",
			"Condition.Type",
			"ConditionPredicate",
			"EventuallyObject",
			"OSImageStreamPredicate",
			"Predicate",
			"UpdateObject",
			"WaitForReadyNodesByNodePool",
			"WithInterval",
			"WithTimeout",
		},
		"allNodesReadyPredicate": {
			"Predicate",
		},
		"conditionMessageContains": {
			"Predicate",
		},
		"nodesInfoPopulatedPredicate": {
			"Predicate",
		},
		"osImageStreamSetPredicate": {
			"Predicate",
		},
		"verifyNodeOSMatchesStream": {
			"EventuallyObject",
			"Predicate",
			"WithInterval",
			"WithTimeout",
		},
	},
	"test/e2e/v2/tests/nodepool_rollout_control_test.go": {
		"OperatorUpgradeNoRolloutTest": {
			"WaitForReadyNodesByNodePool",
		},
		"SpecDrivenChangeTriggersRolloutTest": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
			"WaitForNodePoolConfigUpdateCompleteWithPlatform",
			"WaitForReadyNodesByNodePool",
		},
	},
}
