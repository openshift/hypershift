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
		"Describe(\"[sig-hypershift][Jira:Hypershift][Feature:JUnitContract] Synthetic JUnit contract\")#1/It(\"required WIF mutation\")#1": {
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
		"RegisterAzureBootstrapDiagnosticsTests/It(\"should collect a serial boot log from a worker that never registers a Node\")#1": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
		},
	},
	"test/e2e/v2/tests/azure_machine_diagnostics_test.go": {
		"RegisterAzureMachineDiagnosticsTests/It(\"should collect nonempty serial, journal, kubelet and CRI-O artifacts for Azure workers\")#1": {
			"dump.VerifyAzureMachineDiagnostics",
		},
	},
	"test/e2e/v2/tests/backup_restore_test.go": {
		"Describe(\"[sig-hypershift][Jira:Hypershift][Feature:BackupRestore] BackupRestore\")#1": {
			"Condition",
		},
		"Describe(\"[sig-hypershift][Jira:Hypershift][Feature:CrossClusterRestore] CrossClusterRestore\")#1": {
			"Condition",
		},
		"Describe(\"[sig-hypershift][Jira:Hypershift][Feature:EtcdSnapshot] BackupRestoreEtcdSnapshot\")#1": {
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
		"VerifyPKIOperatorTLSConfigTest/When(\"control plane TLS configuration is modified\")#1/BeforeAll()#1": {
			"GetConfig",
		},
	},
	"test/e2e/v2/tests/control_plane_upgrade_test.go": {
		"ControlPlaneUpgradeTest/It(\"should upgrade the control plane from N-1 to latest\")#1": {
			"UpdateObject",
			"Version422",
		},
	},
	"test/e2e/v2/tests/control_plane_workloads_test.go": {
		"ContainerTerminationMessagePolicyTest/Context(\"Container termination message policy\")#1/BeforeEach()#1": {
			"Version419",
		},
		"CustomLabelsTest/Context(\"Custom labels\")#1/BeforeEach()#1": {
			"Version419",
		},
		"CustomTolerationsTest/Context(\"Custom tolerations\")#1/BeforeEach()#1": {
			"Version419",
		},
		"NoCrashingPodsTest/Context(\"No crashing pods\")#1/Context(<dynamic>)#1/It(\"should have no crashing pods\")#1": {
			"GetConfig",
		},
		"ReadOnlyRootFilesystemTest/Context(\"Read-only root filesystem\")#1/BeforeEach()#1": {
			"Version420",
		},
		"ReadOnlyRootFilesystemTmpDirMountTest/Context(\"Read-only root filesystem tmp dir mount\")#1/BeforeEach()#1": {
			"Version420",
		},
		"SafeToEvictAnnotationsTest/Context(\"Safe-to-evict annotations\")#1/BeforeEach()#1": {
			"Version420",
		},
		"ServiceAccountTokenMountingTest/Context(\"Service account token mounting\")#1/Context(<dynamic>)#1/It(\"should not mount service account token unless necessary for pods\")#1": {
			"Version416",
			"Version418",
		},
	},
	"test/e2e/v2/tests/etcd_chaos_test.go": {
		"EtcdKillAllMembersTest/It(\"should preserve data when all members are killed simultaneously\")#1": {
			"EventuallyObjects",
			"Predicate",
			"WithInterval",
			"WithTimeout",
		},
		"EtcdMissingMemberRecoveryTest/It(\"should recover after a member is removed from the etcd cluster\")#1": {
			"EventuallyObject",
			"Predicate",
			"RunCommandInPod",
			"WithInterval",
			"WithTimeout",
		},
		"EtcdSingleMemberCorruptionTest/It(\"should recover after a single member's data is corrupted\")#1": {
			"EventuallyObject",
			"Predicate",
			"RunCommandInPod",
			"WithInterval",
			"WithTimeout",
		},
		"EtcdSingleMemberRecoveryTest/It(\"should recover after a single member loses its data\")#1": {
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
		"AWSCCMWithCustomizationsTest/Context(\"[Feature:AWSNLB] AWS CCM NLB Security Group\")#1/BeforeEach()#1": {
			"Version423",
		},
		"AWSResourceTagOverridePolicyTest/When(\"[Feature:AWSResourceTagOverrides] HostedCluster tags have mixed override policies\")#1/It(\"should block or allow NodePool tag overrides based on overridePolicy and reflect conflicts in the NodePool condition\")#1": {
			"UpdateObject",
			"WaitForReadyNodesByNodePool",
		},
		"AWSResourceTagOverridePolicyTest/When(\"[Feature:AWSResourceTagOverrides] HostedCluster tags have mixed override policies\")#1/It(\"should block or allow NodePool tag overrides based on overridePolicy and reflect conflicts in the NodePool condition\")#1/DeferCleanup()#1": {
			"UpdateObject",
		},
		"EnsureDefaultSecurityGroupTagsTest/When(\"[Feature:AWSSecurityGroups] a day-2 resource tag is added to the HostedCluster spec\")#1/It(\"should apply the tag to the default worker security group via AWS API\")#1": {
			"GetDefaultSecurityGroup",
			"PutRolePolicy",
			"UpdateObject",
			"Version420",
		},
		"EnsureDefaultSecurityGroupTagsTest/When(\"[Feature:AWSSecurityGroups] a day-2 resource tag is added to the HostedCluster spec\")#1/It(\"should apply the tag to the default worker security group via AWS API\")#1/DeferCleanup()#2": {
			"UpdateObject",
		},
		"EnsureDefaultSecurityGroupTagsWithSpacesTest/When(\"[Feature:AWSSecurityGroups] a day-2 resource tag with spaces is added to the HostedCluster spec\")#1/It(\"should apply the tag with spaces to the default worker security group via AWS API\")#1": {
			"GetDefaultSecurityGroup",
			"PutRolePolicy",
			"UpdateObject",
			"Version420",
		},
		"EnsureDefaultSecurityGroupTagsWithSpacesTest/When(\"[Feature:AWSSecurityGroups] a day-2 resource tag with spaces is added to the HostedCluster spec\")#1/It(\"should apply the tag with spaces to the default worker security group via AWS API\")#1/DeferCleanup()#2": {
			"UpdateObject",
		},
		"NodePoolDay2TagsWithSpacesTest/When(\"[Feature:AWSResourceTags] a day-2 NodePool tag with spaces is added\")#1/It(\"should propagate the tag with spaces to AWSMachine and EC2 instances\")#1": {
			"UpdateObject",
		},
		"NodePoolDay2TagsWithSpacesTest/When(\"[Feature:AWSResourceTags] a day-2 NodePool tag with spaces is added\")#1/It(\"should propagate the tag with spaces to AWSMachine and EC2 instances\")#1/DeferCleanup()#1": {
			"UpdateObject",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_azure_test.go": {
		"AzureEndpointAccessTransitionTest/Context(\"[Feature:AzureEndpointAccess] Azure Endpoint Access Transition\")#1/BeforeAll()#1/DeferCleanup()#1": {
			"UpdateObject",
		},
		"AzureEndpointAccessTransitionTest/Context(\"[Feature:AzureEndpointAccess] Azure Endpoint Access Transition\")#1/It(\"should remain available after restoring Private topology\")#1": {
			"EventuallyObject",
			"Predicate",
			"WithTimeout",
		},
		"AzureEndpointAccessTransitionTest/Context(\"[Feature:AzureEndpointAccess] Azure Endpoint Access Transition\")#1/It(\"should transition from Private to PublicAndPrivate\")#1": {
			"EventuallyObject",
			"EventuallyObjects",
			"Predicate",
			"UpdateObject",
			"WithTimeout",
		},
		"AzureEndpointAccessTransitionTest/Context(\"[Feature:AzureEndpointAccess] Azure Endpoint Access Transition\")#1/It(\"should transition from PublicAndPrivate back to Private\")#1": {
			"EventuallyObject",
			"EventuallyObjects",
			"Predicate",
			"UpdateObject",
			"WithTimeout",
		},
		"AzureOAuthLoadBalancerPrivateTest/Context(\"[Feature:AzureOAuth] Azure OAuth LoadBalancer in Private Topology\")#1/It(\"should complete OAuth token flow through LoadBalancer endpoint\")#1": {
			"SetupGuestKASPortForwardConfig",
			"SetupOAuthPortForwardTransport",
			"ValidateOAuthIdentityProviderFlow",
			"WaitForOAuthLoadBalancerEndpoint",
			"WithGuestConfig",
			"WithTransport",
			"WithTransportFactory",
		},
		"AzureOAuthLoadBalancerPrivateTest/Context(\"[Feature:AzureOAuth] Azure OAuth LoadBalancer in Private Topology\")#1/It(\"should create oauth-openshift Service as LoadBalancer with an allocated endpoint\")#1": {
			"EventuallyObject",
			"WithTimeout",
		},
		"AzureOAuthLoadBalancerPrivateTest/Context(\"[Feature:AzureOAuth] Azure OAuth LoadBalancer in Private Topology\")#1/It(\"should have Azure internal LB annotation on oauth-openshift Service\")#1": {
			"EventuallyObject",
			"Predicate",
			"WithTimeout",
		},
		"AzureOAuthLoadBalancerTest/Context(\"[Feature:AzureOAuth] Azure OAuth LoadBalancer\")#1/It(\"should complete OAuth token flow through LoadBalancer endpoint\")#1": {
			"ValidateOAuthWithIdentityProviderViaLoadBalancer",
		},
		"AzureOAuthLoadBalancerTest/Context(\"[Feature:AzureOAuth] Azure OAuth LoadBalancer\")#1/It(\"should create oauth-openshift Service as LoadBalancer with endpoint\")#1": {
			"EventuallyObject",
			"WithTimeout",
		},
		"AzurePrivateTopologyTest/Context(\"[Feature:AzurePrivateLink] Azure Private Topology\")#1/It(\"should create AzurePrivateLinkService CR with PLS alias\")#1": {
			"EventuallyObjects",
			"Predicate",
			"WithInterval",
			"WithTimeout",
		},
		"AzurePrivateTopologyTest/Context(\"[Feature:AzurePrivateLink] Azure Private Topology\")#1/It(\"should have Azure internal LB annotation on private-router Service\")#1": {
			"EventuallyObject",
			"Predicate",
			"WithTimeout",
		},
		"AzurePrivateTopologyTest/Context(\"[Feature:AzurePrivateLink] Azure Private Topology\")#1/It(\"should populate Private DNS Zone ID in PLS status\")#1": {
			"EventuallyObjects",
			"Predicate",
			"WithInterval",
			"WithTimeout",
		},
		"AzurePrivateTopologyTest/Context(\"[Feature:AzurePrivateLink] Azure Private Topology\")#1/It(\"should populate Private Endpoint IP in PLS status\")#1": {
			"EventuallyObjects",
			"Predicate",
			"WithInterval",
			"WithTimeout",
		},
		"AzurePublicClusterTest/Context(\"[Feature:AzureWorkloadIdentity] Azure Public Cluster\")#1/It(\"should have Ingress Operator configuration applied\")#1": {
			"ValidateIngressOperatorConfiguration",
			"Version421",
			"WaitForGuestKubeConfig",
		},
		"AzurePublicClusterTest/Context(\"[Feature:AzureWorkloadIdentity] Azure Public Cluster\")#1/It(\"should have expected KAS allowed CIDRs\")#1": {
			"ValidateKubeAPIServerAllowedCIDRs",
			"WaitForGuestKubeConfig",
		},
		"AzurePublicClusterTest/Context(\"[Feature:AzureWorkloadIdentity] Azure Public Cluster\")#1/It(\"should mutate pods with workload identity federated credentials\")#1": {
			"ValidateAzureWorkloadIdentityWebhookMutation",
			"Version420",
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
		"GCPCloudControllerManagerTest/Context(\"GCP Cloud Controller Manager\")#1/When(\"nodes are initialized by the CCM\")#1/BeforeEach()#1": {
			"WaitForGuestKubeConfig",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_compliance_test.go": {
		"EnsureKASConnectionCheckerSpecTest/When(\"kas-connection-checker deployment is reconciled\")#1/It(\"should have safe-to-evict annotation, no tolerations, and topology spread constraint\")#1": {
			"Version423",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_cpo_test.go": {
		"VerifyCPOOverrideImageTest/When(\"a CPO override image is configured for the platform and version\")#1/It(\"should run the control-plane-operator pod with the expected override image\")#1": {
			"ExtractVersionFromReleaseImage",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_dns_test.go": {
		"EnsureKubeAPIDNSNameTests/When(\"KubeAPIDNSName and custom certificate are configured\")#1/PIt(\"should keep KAS reachable via the internal SVC URL\")#1": {
			"Version419",
		},
		"EnsureKubeAPIDNSNameTests/When(\"KubeAPIDNSName and custom certificate are configured\")#1/PIt(\"should make KAS reachable via the custom DNS endpoint\")#1": {
			"Version419",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_external_oidc_test.go": {
		"ExternalOIDCKeycloakAuthTest/Context(\"Keycloak Authentication and Claims\")#1": {
			"ExtOIDCConfig",
		},
		"ExternalOIDCKeycloakAuthTest/Context(\"Keycloak Authentication and Claims\")#1/BeforeAll()#1": {
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
		"ExternalOIDCKeycloakAuthTest/Context(\"Keycloak Authentication and Claims\")#1/It(\"should map groups claim with prefix\")#1": {
			"ExtOIDCConfig.GroupPrefix",
		},
		"ExternalOIDCKeycloakAuthTest/Context(\"Keycloak Authentication and Claims\")#1/It(\"should map username claim correctly\")#1": {
			"ExtOIDCConfig.UserPrefix",
		},
		"obtainKeycloakIDToken": {
			"ExtOIDCConfig",
			"ExtOIDCConfig.CliClientID",
			"ExtOIDCConfig.IssuerURL",
			"ExtOIDCConfig.TestUsers",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_gcp_wif_test.go": {
		"GCPWorkloadIdentityTest/Context(\"GCP Workload Identity\")#1/BeforeEach()#1": {
			"Version51",
		},
		"GCPWorkloadIdentityTest/Context(\"GCP Workload Identity\")#1/It(\"[Skipped:Disconnected] should mutate pods with workload identity federated credentials\")#1": {
			"ValidateGCPWorkloadIdentityWebhookMutation",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_health_test.go": {
		"EnsureCAPIFinalizersTest/When(\"CAPI components are deployed\")#1/It(\"should have component finalizers on all CAPI deployments\")#1": {
			"Version422",
		},
		"EnsureFeatureGateStatusTest/When(\"hosted cluster version is completed\")#1/It(\"should have feature gate status matching cluster version\")#1": {
			"Version419",
		},
		"ValidateConfigurationStatusTest/When(\"hosted cluster authentication is configured\")#1/It(\"should propagate configuration status consistently\")#1": {
			"Version421",
		},
		"ValidateHostedClusterConditionsTest/When(\"hosted cluster is operational\")#1/It(\"should have all expected conditions with correct status\")#1": {
			"Version421",
			"Version422",
			"Version423",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_ingress_test.go": {
		"ServiceProviderDefaultIngressServingCertificateLifecycleTest/When(\"a custom default ingress certificate is configured\")#1/AfterAll()#1": {
			"UpdateObject",
		},
		"ServiceProviderDefaultIngressServingCertificateLifecycleTest/When(\"a custom default ingress certificate is configured\")#1/It(\"should create the cert secret and set defaultCertificate on the HostedCluster\")#1": {
			"UpdateObject",
		},
		"ServiceProviderDefaultIngressServingCertificateLifecycleTest/When(\"a custom default ingress certificate is configured\")#1/It(\"should propagate rotated certificate data when the source secret is updated\")#1": {
			"UpdateObject",
		},
		"ServiceProviderDefaultIngressServingCertificateLifecycleTest/When(\"a custom default ingress certificate is configured\")#1/It(\"should report InvalidCertificateSecret and preserve the served certificate when the source secret is missing tls.key\")#1": {
			"UpdateObject",
		},
		"ServiceProviderDefaultIngressServingCertificateLifecycleTest/When(\"a custom default ingress certificate is configured\")#1/It(\"should revert to the generated wildcard certificate when defaultCertificate is cleared\")#1": {
			"UpdateObject",
		},
		"ServiceProviderDefaultIngressServingCertificateLifecycleTest/When(\"the ingress capability is disabled\")#1/It(\"should not copy a configured default ingress certificate to the control plane or hosted cluster\")#1": {
			"UpdateObject",
		},
		"ServiceProviderDefaultIngressServingCertificateLifecycleTest/When(\"the ingress capability is disabled\")#1/It(\"should not copy a configured default ingress certificate to the control plane or hosted cluster\")#1/DeferCleanup()#2": {
			"UpdateObject",
		},
		"ValidateIngressOperatorConfigurationTest/When(\"hosted cluster has IngressOperator EndpointPublishingStrategy configured\")#1/It(\"should reflect the custom strategy in the hosted cluster IngressController\")#1": {
			"Version421",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_managed_dns_test.go": {
		"AWSManagedDNSTest/Context(\"AWS Managed Ingress DNS\")#1/BeforeEach()#1": {
			"Version51",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_metrics_test.go": {
		"EnsureKubeSchedulerMetricsEndpointTest/When(\"kube-scheduler is running\")#1/It(\"should have functional kube-scheduler metrics endpoints\")#1": {
			"Version423",
		},
		"EnsureMetricsForwarderWorkingTest/When(\"metrics forwarding is enabled\")#1/It(\"should deploy the metrics pipeline and scrape kube-apiserver metrics end-to-end\")#1": {
			"Version422",
		},
		"EnsureNodeTuningOperatorMetricsEndpointTest/When(\"cluster has worker nodes\")#1/It(\"should have a functional node-tuning-operator metrics endpoint\")#1": {
			"GetConfig",
			"Version422",
		},
		"ValidateCPOMetricsTest/When(\"KAS health metrics are exposed\")#1/BeforeEach()#1": {
			"Version51",
		},
		"ValidateCPOMetricsTest/When(\"KAS health metrics are exposed\")#1/It(\"should contain availability and latency data from CPO\")#1": {
			"GetConfig",
		},
		"ValidateMetricsTest/When(\"HyperShift operator is running\")#1/It(\"should expose expected metrics at the metrics endpoint\")#1": {
			"GetConfig",
		},
		"fetchMetricsViaPortForward": {
			"GetConfig",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_node_communication_test.go": {
		"EnsureGCPWorkerFirewallTest/When(\"the hosted cluster is on GCP\")#1/It(\"should reconcile the worker firewall rule and allow cross-node worker traffic\")#1": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
		},
		"buildGCPFirewallProbeDeployment": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_pull_secret_test.go": {
		"EnsureGlobalPullSecretTest/When(\"an additional pull secret is created in the hosted cluster\")#1/It(\"should propagate it through the global pull secret pipeline and clean up on deletion\")#1": {
			"Version420",
			"Version422",
			"WaitForReadyNodesByNodePool",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_secret_encryption_test.go": {
		"KMSFunctionalValidationTest/Context(\"KMS Functional Validation\")#1/It(\"should encrypt secrets in etcd using KMSv2\")#1": {
			"RunCommandInPod",
			"Version417",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_security_test.go": {
		"EnsureAdmissionPoliciesTest/When(\"[Feature:AdmissionPolicies] checking admission policies on a public hosted cluster\")#1/BeforeAll()#1": {
			"Version418",
		},
		"EnsureAdmissionPoliciesTest/When(\"[Feature:AdmissionPolicies] checking admission policies on a public hosted cluster\")#1/It(\"should deny unauthorized deletion of kas-bootstrap RBAC bindings via VAPs\")#1": {
			"Version51",
		},
		"EnsureAdmissionPoliciesTest/When(\"[Feature:AdmissionPolicies] checking admission policies on a public hosted cluster\")#1/It(\"should find all required ValidatingAdmissionPolicies\")#1": {
			"Version51",
		},
		"EnsureNetworkPoliciesTest/When(\"[Feature:NetworkPolicies] checking network policies on an AWS hosted cluster\")#1/It(\"should block egress traffic from non-privileged pods to the management KAS\")#1": {
			"GetConfig",
		},
	},
	"test/e2e/v2/tests/hosted_cluster_storage_kms_test.go": {
		"StorageKMSPropagationTest/Context(\"Day-1 KMS key configuration\")#1/It(\"should propagate initialKMSKeyARN to the hosted cluster ClusterCSIDriver and StorageClass, and encrypt provisioned volumes\")#1": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
			"Version51",
		},
	},
	"test/e2e/v2/tests/karpenter_control_plane_upgrade_test.go": {
		"Describe(\"[sig-hypershift][Jira:Hypershift] Karpenter\")#1/BeforeEach()#1": {
			"Version422",
			"Version423",
		},
		"KarpenterUpgradeTest/Context(\"[Feature:AutoNode] Karpenter Upgrade\")#1/It(\"should upgrade the control plane and drift Karpenter nodes to the new version\")#1": {
			"UpdateObject",
		},
		"expectControlPlaneRolloutWithoutDrift": {
			"Condition.Status",
			"Condition.Type",
			"Conditions",
		},
	},
	"test/e2e/v2/tests/karpenter_test.go": {
		"Describe(\"[sig-hypershift][Jira:Hypershift] Karpenter\")#1/BeforeEach()#1": {
			"Version422",
			"Version423",
		},
		"KarpenterARM64ProvisioningTest/Context(\"[Feature:AutoNode] ARM64 instance provisioning\")#1/It(\"should provision and deprovision ARM64 nodes\")#1": {
			"WaitForReadyNodesByLabels",
		},
		"KarpenterArbitrarySubnetTest/Context(\"[Feature:AutoNode] Arbitrary subnet propagation\")#1/It(\"should propagate a custom subnet through the VPC endpoint and provision a node in it\")#1": {
			"CreateTestSubnet",
			"E2ETagsFromEnvironment",
			"WaitForReadyNodesByLabels",
		},
		"KarpenterAutoNodeLifecycleTest/Context(\"[Feature:AutoNode] AutoNode enable/disable lifecycle\")#1/It(\"should disable and re-enable AutoNode with correct condition transitions\")#1": {
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
		"KarpenterBillingConsolidationTest/Context(\"[Feature:AutoNode] Billing vCPUs and consolidation\")#1/It(\"should track vCPU billing metrics and consolidate nodes when workload scales down\")#1": {
			"UpdateObject",
			"WaitForReadyNodesByLabels",
		},
		"KarpenterCPUOptionsNestedVirtualization/Context(\"[Feature:AutoNode] AutoNode with CPUOptions enabled\")#1/It(\"should provision EC2 instance with nested virtualization enabled\")#1": {
			"WaitForReadyNodesByLabels",
		},
		"KarpenterCPUOptionsNestedVirtualization/Context(\"[Feature:AutoNode] AutoNode with CPUOptions enabled\")#1/It(\"should provision EC2 instance with nested virtualization enabled\")#1/DeferCleanup()#3": {
			"WaitForReadyNodesByLabels",
		},
		"KarpenterCapacityReservationTest/Context(\"[Feature:AutoNode] Capacity reservation selector propagation\")#1/It(\"should provision a node into a capacity reservation\")#1": {
			"CreateCapacityReservation",
			"E2ETagsFromEnvironment",
			"EventuallyObject",
			"Predicate",
			"WaitForReadyNodesByLabels",
			"WithInterval",
			"WithTimeout",
		},
		"KarpenterInstanceProfileTest/Context(\"[Feature:AutoNode] Instance profile annotation propagation\")#1/It(\"should propagate instance profile annotation to EC2NodeClass and EC2 instances\")#1": {
			"UpdateObject",
			"WaitForReadyNodesByLabels",
		},
		"KarpenterInstanceProfileTest/Context(\"[Feature:AutoNode] Instance profile annotation propagation\")#1/It(\"should propagate instance profile annotation to EC2NodeClass and EC2 instances\")#1/DeferCleanup()#1": {
			"UpdateObject",
		},
		"KarpenterKubeletPropagationTest/Context(\"[Feature:AutoNode] OpenshiftEC2NodeClass Kubelet propagation\")#1/It(\"should propagate kubelet config to provisioned nodes\")#1": {
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
		"KarpenterNodeClassVersionTest/Context(\"[Feature:AutoNode] OpenshiftEC2NodeClass version field and MetadataOptions\")#1/It(\"should resolve version, propagate MetadataOptions, and provision node with correct kubelet version\")#1": {
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
		"KarpenterPlumbingTests/Context(\"[Feature:AutoNode] Karpenter Plumbing\")#1/It(\"should expose Karpenter metrics\")#1": {
			"GetMetricsFromPod",
			"ValidateMetricPresence",
		},
		"KarpenterPlumbingTests/Context(\"[Feature:AutoNode] Karpenter Plumbing\")#1/It(\"should have AutoNodeEnabled condition set to True\")#1": {
			"Condition",
			"Condition.Reason",
			"Condition.Status",
			"Condition.Type",
			"ConditionPredicate",
			"EventuallyObject",
			"Predicate",
			"WithTimeout",
		},
		"KarpenterPlumbingTests/Context(\"[Feature:AutoNode] Karpenter Plumbing\")#1/It(\"should have Karpenter CRDs installed in the hosted cluster\")#1": {
			"EventuallyObject",
			"WithTimeout",
		},
		"KarpenterPlumbingTests/Context(\"[Feature:AutoNode] Karpenter Plumbing\")#1/It(\"should have default EC2NodeClass with immutable service-owned fields\")#1": {
			"EventuallyObject",
			"Predicate",
			"WithTimeout",
		},
		"KarpenterPlumbingTests/Context(\"[Feature:AutoNode] Karpenter Plumbing\")#1/It(\"should have default OpenshiftEC2NodeClass with correct subnet and security group selectors\")#1": {
			"EventuallyObject",
			"Predicate",
			"WithTimeout",
		},
		"getVCPUsMetric": {
			"GetMetricsFromPod",
		},
	},
	"test/e2e/v2/tests/nodepool_arm64_create_test.go": {
		"NodePoolArm64CreateTest/It(\"When creating an ARM64 NodePool, it should provision a node with ARM64 architecture\")#1": {
			"WaitForReadyNodesByNodePool",
		},
	},
	"test/e2e/v2/tests/nodepool_autoscaling_test.go": {
		"AutoscalingBalancingTest/It(\"should balance pods across multiple autoscaling NodePools\")#1": {
			"EventuallyObject",
			"Predicate",
			"Version420",
			"WaitForNReadyNodesWithOptions",
			"WithClientOptions",
			"WithInterval",
			"WithTimeout",
		},
		"AutoscalingScaleFromZeroTest/It(\"should scale from zero when a workload is created\")#1": {
			"EventuallyObject",
			"Predicate",
			"WaitForNReadyNodesWithOptions",
			"WithClientOptions",
			"WithInterval",
			"WithTimeout",
		},
		"AutoscalingScaleUpDownTest/It(\"should scale up when workload increases and scale down when workload decreases\")#1": {
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
		"NodePoolAutoRepairTest/It(\"should auto-repair a NodePool when a node is terminated\")#1": {
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolDiskEncryptionTest/It(\"should create a NodePool with Azure DiskEncryptionSet and verify it is applied\")#1": {
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolIPForwardingTest/It(\"should create a NodePool with Azure IP forwarding set to AllowAllAddresses and verify it is applied\")#1": {
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolInPlaceUpgradeTest/It(\"should upgrade a NodePool from previous to latest release via InPlace strategy\")#1": {
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
		"NodePoolMachineconfigRolloutTest/It(\"should roll out a MachineConfig change via Replace upgrade strategy\")#1": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
			"WaitForNodePoolConfigUpdateCompleteWithPlatform",
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolMirrorConfigsTest/It(\"should mirror KubeletConfig to the hosted cluster and clean up on removal\")#1": {
			"EventuallyObjects",
			"NameGenerator.GenerateName",
			"Predicate",
			"SimpleNameGenerator",
			"Version418",
			"WaitForReadyNodesByNodePool",
			"WithInterval",
			"WithTimeout",
		},
		"NodePoolNTOInPlaceTest/It(\"should roll out an NTO Tuned config change via InPlace upgrade strategy\")#1": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
			"WaitForNodePoolConfigUpdateCompleteWithPlatform",
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolNTOPerformanceProfileTest/It(\"should create and manage NTO PerformanceProfile via NodePool TuningConfig\")#1": {
			"EventuallyObjects",
			"NameGenerator.GenerateName",
			"Predicate",
			"SimpleNameGenerator",
			"Version417",
			"WaitForReadyNodesByNodePool",
			"WithInterval",
			"WithTimeout",
		},
		"NodePoolNTORolloutTest/It(\"should roll out an NTO Tuned config change via Replace upgrade strategy\")#1": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
			"WaitForNodePoolConfigUpdateCompleteWithPlatform",
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolPrevReleaseN1Test/It(\"should create a NodePool at N-1 release and have ready nodes\")#1": {
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolPrevReleaseN2Test/It(\"should create a NodePool at N-2 release and have ready nodes\")#1": {
			"WaitForReadyNodesByNodePool",
		},
		"NodePoolReplaceUpgradeTest/It(\"should upgrade a NodePool from previous to latest release via Replace strategy\")#1": {
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
		"NodePoolRollingUpgradeTest/It(\"should perform a rolling upgrade when instance type or VM size changes\")#1": {
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
		"NodePoolTrustBundleTest/It(\"should propagate and remove additional trust bundle to/from the hosted cluster\")#1": {
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
		"NodePoolTrustBundleTest/It(\"should propagate and remove additional trust bundle to/from the hosted cluster\")#1/DeferCleanup()#3": {
			"UpdateObject",
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
		"NodePoolOSImageStreamCrossMajorUpgradeTest/It(\"When a NodePool at OCP 4.23 is upgraded to 5.0+, it should switch to rhel-10 by default\")#1": {
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
		"NodePoolOSImageStreamDefaultStatusTest/It(\"When no osImageStream is set, it should report a recognized RHEL stream in status\")#1": {
			"EventuallyObject",
			"Predicate",
			"Version50",
			"WithInterval",
			"WithTimeout",
		},
		"NodePoolOSImageStreamExplicitDefaultNoRolloutTest/It(\"When osImageStream is set to the version-derived default, it should not trigger a rollout\")#1": {
			"EventuallyObject",
			"Predicate",
			"WithInterval",
			"WithTimeout",
		},
		"NodePoolOSImageStreamNodeOSVerificationTest/It(\"When NodePools have different osImageStream values, nodes should run the matching OS version\")#1": {
			"EventuallyObject",
			"Predicate",
			"Version50",
			"WithInterval",
			"WithTimeout",
		},
		"NodePoolOSImageStreamPinnedRHEL9UpgradeTest/It(\"When a NodePool pinned to rhel-9 is upgraded from 4.23 to 5.0+, it should remain on RHEL-9\")#1": {
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
		"NodePoolOSImageStreamRHEL10RuncRejectionTest/It(\"When osImageStream is set to rhel-10 with runc ContainerRuntimeConfig, it should set ValidMachineConfig to False\")#1": {
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
		"NodePoolOSImageStreamUpgradeVerificationTest/It(\"When a NodePool is upgraded, it should report the correct osImageStream in status\")#1": {
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
		"OperatorUpgradeNoRolloutTest/When(\"the HyperShift operator is upgraded to support rollout hash\")#1/It(\"should seed the rollout hash annotation on first reconcile without triggering a rollout\")#1": {
			"WaitForReadyNodesByNodePool",
		},
		"SpecDrivenChangeTriggersRolloutTest/When(\"a spec-driven change is made to a NodePool\")#1/It(\"should trigger a rollout and update the rollout hash annotation when a MachineConfig is added\")#1": {
			"NameGenerator.GenerateName",
			"SimpleNameGenerator",
			"WaitForNodePoolConfigUpdateCompleteWithPlatform",
			"WaitForReadyNodesByNodePool",
		},
	},
}
