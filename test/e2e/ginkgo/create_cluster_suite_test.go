//go:build e2e
// +build e2e

package ginkgo

import (
	"context"
	"flag"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

var (
	// Global test context and options initialized in TestMain
	testContext context.Context
	globalOpts  = &e2eutil.Options{}
)

// TestMain deals with global options and flag parsing for the Ginkgo suite.
// This is a surgical duplication of e2e_test.go TestMain, keeping only
// flag definitions and basic initialization.
func TestMain(m *testing.M) {
	// Platform-agnostic flags - duplicated from e2e_test.go
	flag.BoolVar(&globalOpts.DisablePKIReconciliation, "e2e.disable-pki-reconciliation", false, "If set, TestUpgradeControlPlane will upgrade the control plane without reconciling the pki components")
	flag.BoolVar(&globalOpts.RequestServingIsolation, "e2e.test-request-serving-isolation", false, "If set, TestCreate creates a cluster with request serving isolation topology")
	flag.IntVar(&globalOpts.ConfigurableClusterOptions.NodePoolReplicas, "e2e.node-pool-replicas", 2, "the number of replicas for each node pool in the cluster")
	flag.StringVar(&globalOpts.ArtifactDir, "e2e.artifact-dir", "", "The directory where cluster resources and logs should be dumped. If empty, nothing is dumped")
	flag.StringVar(&globalOpts.ManagementClusterName, "e2e.management-cluster-name", "", "Name of the management cluster's HostedCluster (required to test request serving isolation)")
	flag.StringVar(&globalOpts.ManagementClusterNamespace, "e2e.management-cluster-namespace", "", "Namespace of the management cluster's HostedCluster (required to test request serving isolation)")
	flag.StringVar(&globalOpts.ManagementParentKubeconfig, "e2e.management-parent-kubeconfig", "", "Kubeconfig of the management cluster's parent cluster (required to test request serving isolation)")
	flag.StringVar(&globalOpts.LatestReleaseImage, "e2e.latest-release-image", "", "The latest OCP release image for use by tests")
	flag.StringVar(&globalOpts.PreviousReleaseImage, "e2e.previous-release-image", "", "The previous OCP release image relative to the latest")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.BaseDomain, "e2e.base-domain", "", "The ingress base domain for the cluster")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.ControlPlaneOperatorImage, "e2e.control-plane-operator-image", "", "The image to use for the control plane operator. If none specified, the default is used.")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.EtcdStorageClass, "e2e.etcd-storage-class", "", "The persistent volume storage class for etcd data volumes")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.ExternalDNSDomain, "e2e.external-dns-domain", "", "domain that external-dns will use to create DNS records for HCP endpoints")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.NetworkType, "network-type", string(hyperv1.OVNKubernetes), "The network type to use. If unset, will default based on the OCP version.")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PullSecretFile, "e2e.pull-secret-file", "", "path to pull secret")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.SSHKeyFile, "e2e.ssh-key-file", "", "Path to a ssh public key")
	flag.StringVar(&globalOpts.N1MinorReleaseImage, "e2e.n1-minor-release-image", "", "The n-1 minor OCP release image relative to the latest")
	flag.StringVar(&globalOpts.N2MinorReleaseImage, "e2e.n2-minor-release-image", "", "The n-2 minor OCP release image relative to the latest")
	flag.StringVar(&globalOpts.PlatformRaw, "e2e.platform", string(hyperv1.AWSPlatform), "The platform to use for the tests")
	flag.Var(&globalOpts.ConfigurableClusterOptions.Annotations, "e2e.annotations", "Annotations to apply to the HostedCluster (key=value). Can be specified multiple times")
	flag.Var(&globalOpts.ConfigurableClusterOptions.ClusterCIDR, "e2e.cluster-cidr", "The CIDR of the cluster network. Can be specified multiple times.")
	flag.Var(&globalOpts.ConfigurableClusterOptions.ServiceCIDR, "e2e.service-cidr", "The CIDR of the service network. Can be specified multiple times.")
	flag.Var(&globalOpts.ConfigurableClusterOptions.Zone, "e2e.availability-zones", "Availability zones for clusters")
	flag.StringVar(&globalOpts.HyperShiftOperatorLatestImage, "e2e.hypershift-operator-latest-image", "quay.io/hypershift/hypershift-operator:latest", "The latest HyperShift Operator image to deploy. If e2e.hypershift-operator-initial-image is set (e.g. to run an upgrade test), this image will be considered the latest HyperShift Operator image to upgrade to.")
	flag.StringVar(&globalOpts.HOInstallationOptions.PrivatePlatform, "e2e.private-platform", "None", "Platform on which private clusters are supported by the HyperShift Operator (supports \"AWS\" or \"None\"). This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.AWSPrivateCredentialsFile, "e2e.aws-private-credentials-file", "/etc/hypershift-pool-aws-credentials/credentials", "path to AWS private credentials. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.AWSPrivateRegion, "e2e.aws-private-region", "us-east-1", "AWS region where private clusters are supported by the HyperShift Operator. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.AWSOidcS3Credentials, "e2e.aws-oidc-s3-credentials", "/etc/hypershift-pool-aws-credentials/credentials", "AWS S3 credentials for the setup of the OIDC provider. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.AWSOidcS3Region, "e2e.aws-oidc-s3-region", "us-east-1", "AWS S3 region for the setup of the OIDC provider. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.ExternalDNSProvider, "e2e.external-dns-provider", "aws", "Provider to use for managing DNS records using external-dns. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.ExternalDNSDomainFilter, "e2e.external-dns-domain-filter", "service.ci.hypershift.devcluster.openshift.com", "restrict external-dns to changes within the specified domain. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.ExternalDNSCredentials, "e2e.external-dns-credentials", "/etc/hypershift-pool-aws-credentials/credentials", "path to credentials file to use for managing DNS records using external-dns. This is a HyperShift Operator installation option")
	flag.BoolVar(&globalOpts.HOInstallationOptions.EnableCIDebugOutput, "e2e.ho-enable-ci-debug-output", false, "Install the HyperShift Operator with extra CI debug output enabled. This is a HyperShift Operator installation option")
	flag.StringVar(&globalOpts.HOInstallationOptions.PlatformMonitoring, "e2e.platform-monitoring", "All", "The option for enabling platform cluster monitoring when installing the HyperShift Operator. Valid values are: None, OperatorOnly, All. This is a HyperShift Operator installation option")
	flag.BoolVar(&globalOpts.RunUpgradeTest, "upgrade.run-tests", false, "Run HyperShift Operator upgrade test")

	// external OIDC configuration
	flag.StringVar(&globalOpts.ExternalOIDCProvider, "e2e.external-oidc-provider", "", "if not null, enable external OIDC config with provider. supported value: keycloak, azure")
	flag.StringVar(&globalOpts.ExternalOIDCCliClientID, "e2e.external-oidc-cli-client-id", "", "cli client ID for external OIDC. This id is needed if you set external oidc in spec.configuration")
	flag.StringVar(&globalOpts.ExternalOIDCConsoleClientID, "e2e.external-oidc-console-client-id", "", "console client ID for external OIDC. This id is needed if you set external oidc in spec.configuration")
	flag.StringVar(&globalOpts.ExternalOIDCIssuerURL, "e2e.external-oidc-issuer-url", "", "external OIDC issuer URL. This id is needed if you set external oidc in spec.configuration")
	flag.StringVar(&globalOpts.ExternalOIDCConsoleSecret, "e2e.external-oidc-console-secret", "", "external OIDC console secret. This is needed if you set external oidc in spec.configuration for the console")
	flag.StringVar(&globalOpts.ExternalOIDCCABundleFile, "e2e.external-oidc-ca-bundle-file", "", "external OIDC issuer issuerCertificateAuthority")
	flag.StringVar(&globalOpts.ExternalOIDCTestUsers, "e2e.external-oidc-test-users", "", "external OIDC test users to login the cluster by the external oidc")

	// AWS specific flags
	flag.BoolVar(&globalOpts.ConfigurableClusterOptions.AWSMultiArch, "e2e.aws-multi-arch", false, "Enable multi arch for aws clusters")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AWSCredentialsFile, "e2e.aws-credentials-file", "", "path to AWS credentials")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AWSEndpointAccess, "e2e.aws-endpoint-access", "", "endpoint access profile for the cluster")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AWSKmsKeyAlias, "e2e.aws-kms-key-alias", "", "AWS KMS Key Alias to use when creating encrypted nodepools, when empty the default EBS KMS Key will be used")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AWSOidcS3BucketName, "e2e.aws-oidc-s3-bucket-name", "", "AWS S3 Bucket Name to setup the OIDC provider in")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.Region, "e2e.aws-region", "us-east-1", "AWS region for clusters")
	flag.Var(&globalOpts.AdditionalTags, "e2e.additional-tags", "Additional tags to set on AWS resources")
	flag.Var(&globalOpts.ConfigurableClusterOptions.Zone, "e2e.aws-zones", "Deprecated, use -e2e.availability-zones instead")

	// Azure specific flags
	flag.BoolVar(&globalOpts.ConfigurableClusterOptions.AzureMultiArch, "e2e.azure-multi-arch", false, "Enable multi arch for Azure clusters")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureCredentialsFile, "e2e.azure-credentials-file", "", "Path to an Azure credentials file")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureLocation, "e2e.azure-location", "eastus", "The location to use for Azure")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureManagedIdentitiesFile, "e2e.azure-managed-identities-file", "", "Path to an Azure managed identities file")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureMarketplaceOffer, "e2e.azure-marketplace-offer", "", "The location to use for Azure")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureMarketplacePublisher, "e2e.azure-marketplace-publisher", "", "The marketplace publisher to use for Azure")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureMarketplaceSKU, "e2e.azure-marketplace-sku", "", "The marketplace SKU to use for Azure")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureMarketplaceVersion, "e2e.azure-marketplace-version", "", "The marketplace version to use for Azure")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureIssuerURL, "e2e.oidc-issuer-url", "", "The OIDC provider issuer URL")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureServiceAccountTokenIssuerKeyPath, "e2e.sa-token-issuer-private-key-path", "", "The file to the private key for the service account token issuer")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureDataPlaneIdentities, "e2e.azure-data-plane-identities-file", "", "Path to a file containing the client IDs of the managed identities associated with the data plane")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureEncryptionKeyID, "e2e.azure-encryption-key-id", "", "etcd encryption key identifier in the form of https://<vaultName>.vault.azure.net/keys/<keyName>/<keyVersion>")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.AzureKMSUserAssignedCredsSecretName, "e2e.azure-kms-credentials-secret-name", "", "The name of a secret, in Azure KeyVault, containing the JSON UserAssignedIdentityCredentials used in KMS to authenticate to Azure.")

	// Kubevirt specific flags
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.KubeVirtContainerDiskImage, "e2e.kubevirt-container-disk-image", "", "DEPRECATED (ignored will be removed soon)")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.KubeVirtInfraKubeconfigFile, "e2e.kubevirt-infra-kubeconfig", "", "path to the kubeconfig file of the external infra cluster")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.KubeVirtInfraNamespace, "e2e.kubevirt-infra-namespace", "", "the namespace on the infra cluster the workers will be created on")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.KubeVirtNodeMemory, "e2e.kubevirt-node-memory", "8Gi", "the amount of memory to provide to each workload node")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.KubeVirtRootVolumeVolumeMode, "e2e.kubevirt-root-volume-volume-mode", "Filesystem", "The root pvc volume mode")
	flag.UintVar(&globalOpts.ConfigurableClusterOptions.KubeVirtNodeCores, "e2e.kubevirt-node-cores", 2, "The number of cores provided to each workload node")
	flag.UintVar(&globalOpts.ConfigurableClusterOptions.KubeVirtRootVolumeSize, "e2e.kubevirt-root-volume-size", 32, "The root volume size in Gi")

	// OpenStack specific flags
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.OpenStackCACertFile, "e2e.openstack-ca-cert-file", "", "Path to the OpenStack CA certificate file")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.OpenStackCredentialsFile, "e2e.openstack-credentials-file", "", "Path to the OpenStack credentials file")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.OpenStackExternalNetworkID, "e2e.openstack-external-network-id", "", "ID of the OpenStack external network")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.OpenStackNodeAvailabilityZone, "e2e.openstack-node-availability-zone", "", "The availability zone to use for OpenStack nodes")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.OpenStackNodeFlavor, "e2e.openstack-node-flavor", "", "The flavor to use for OpenStack nodes")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.OpenStackNodeImageName, "e2e.openstack-node-image-name", "", "The image name to use for OpenStack nodes")
	flag.Var(&globalOpts.ConfigurableClusterOptions.OpenStackDNSNameservers, "e2e.openstack-dns-nameservers", "List of DNS nameservers to use for the cluster")

	// PowerVS specific flags
	flag.BoolVar(&globalOpts.ConfigurableClusterOptions.PowerVSPER, "e2e-powervs-power-edge-router", false, "Enabling this flag will utilize Power Edge Router solution via transit gateway instead of cloud connection to create a connection between PowerVS and VPC")
	flag.IntVar(&globalOpts.ConfigurableClusterOptions.PowerVSMemory, "e2e.powervs-memory", 32, "Amount of memory allocated (in GB). Default is 32")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSCloudConnection, "e2e-powervs-cloud-connection", "", "Cloud Connection in given zone. Use this flag to reuse an existing Cloud Connection resource for cluster's infra")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSCloudInstanceID, "e2e-powervs-cloud-instance-id", "", "IBM Cloud PowerVS Service Instance ID. Use this flag to reuse an existing PowerVS Service Instance resource for cluster's infra")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSProcessors, "e2e.powervs-processors", "0.5", "Number of processors allocated. Default is 0.5")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSRegion, "e2e.powervs-region", "us-south", "IBM Cloud region. Default is us-south")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSResourceGroup, "e2e.powervs-resource-group", "", "IBM Cloud Resource group")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSSysType, "e2e.powervs-sys-type", "s922", "System type used to host the instance(e.g: s922, e980, e880). Default is s922")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSTransitGateway, "e2e-powervs-transit-gateway", "", "Transit gateway name. Use this flag to reuse an existing transit gateway resource for cluster's infra")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSTransitGatewayLocation, "e2e-powervs-transit-gateway-location", "", "IBM Cloud Transit Gateway location")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSVPC, "e2e-powervs-vpc", "", "IBM Cloud VPC Name. Use this flag to reuse an existing VPC resource for cluster's infra")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSVpcRegion, "e2e.powervs-vpc-region", "us-south", "IBM Cloud VPC Region for VPC resources. Default is us-south")
	flag.StringVar(&globalOpts.ConfigurableClusterOptions.PowerVSZone, "e2e.powervs-zone", "us-south", "IBM Cloud zone. Default is us-sout")
	flag.Var(&globalOpts.ConfigurableClusterOptions.PowerVSProcType, "e2e.powervs-proc-type", "Processor type (dedicated, shared, capped). Default is shared")

	flag.Parse()

	globalOpts.Platform = hyperv1.PlatformType(globalOpts.PlatformRaw)

	// Set defaults and validate options
	if err := globalOpts.Complete(); err != nil {
		GinkgoWriter.Printf("ERROR: failed to complete global test options: %v\n", err)
		os.Exit(1)
	}

	if err := globalOpts.Validate(); err != nil {
		GinkgoWriter.Printf("ERROR: invalid global test options: %v\n", err)
		os.Exit(1)
	}

	// Set up a root context for all tests
	testContext = context.Background()

	os.Exit(m.Run())
}

func TestCreateClusterGinkgo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CreateCluster Ginkgo Suite")
}

var _ = BeforeSuite(func() {
	// Options are already initialized by TestMain
	// Just verify they're set up correctly
	Expect(globalOpts).NotTo(BeNil())
	Expect(testContext).NotTo(BeNil())
})
