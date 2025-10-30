package util

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftaws "github.com/openshift/hypershift/cmd/cluster/aws"
	"github.com/openshift/hypershift/cmd/cluster/azure"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/cluster/kubevirt"
	"github.com/openshift/hypershift/cmd/cluster/none"
	hypershiftopenstack "github.com/openshift/hypershift/cmd/cluster/openstack"
	"github.com/openshift/hypershift/cmd/cluster/powervs"
	awscmdutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	azurenodepool "github.com/openshift/hypershift/cmd/nodepool/azure"
	kubevirtnodepool "github.com/openshift/hypershift/cmd/nodepool/kubevirt"
	openstacknodepool "github.com/openshift/hypershift/cmd/nodepool/openstack"
	"github.com/openshift/hypershift/cmd/util"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/support/supportedversion"

	"k8s.io/apimachinery/pkg/util/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

const (
	DefaultCIBaseDomain = "origin-ci-int-aws.dev.rhcloud.com"
)

// Options are global test options applicable to all scenarios.
type Options struct {
	LatestReleaseImage   string
	PreviousReleaseImage string
	N2MinorReleaseImage  string
	N1MinorReleaseImage  string
	IsRunningInCI        bool
	ArtifactDir          string

	// BeforeApply is a function passed to the CLI create command giving the test
	// code an opportunity to inspect or mutate the resources the CLI will create
	// before they're applied.
	BeforeApply func(client.Object) `json:"-"`

	Platform    hyperv1.PlatformType
	PlatformRaw string

	ConfigurableClusterOptions ConfigurableClusterOptions
	AdditionalTags             stringSliceVar

	IssuerURL                string
	ServiceAccountSigningKey []byte

	// If set, the CreateCluster test will create a cluster with request serving
	// isolation topology.
	RequestServingIsolation bool

	// If testing request serving isolation topology, we need a kubeconfig to the
	// parent of the management cluster, name and namespace of the management cluster
	// so we can create additional nodepools for it.
	ManagementParentKubeconfig string
	ManagementClusterNamespace string
	ManagementClusterName      string
	// If set, the UpgradeControlPlane test will upgrade control plane without
	// reconciling PKI.
	DisablePKIReconciliation bool

	HyperShiftOperatorLatestImage string

	// This is used in tests which include the HyperShift Operator as part of
	// the test such as the UpgradeHyperShiftOperatorTest
	HOInstallationOptions HyperShiftOperatorInstallOptions
	// RunUpgradeTest is set to run HyperShift Operator upgrade test
	RunUpgradeTest bool

	// external oidc for authentication in spec.configurations
	ExternalOIDCProvider        string
	ExternalOIDCCliClientID     string
	ExternalOIDCConsoleClientID string
	ExternalOIDCIssuerURL       string
	ExternalOIDCConsoleSecret   string
	ExternalOIDCCABundleFile    string
	ExternalOIDCTestUsers       string
}

type HyperShiftOperatorInstallOptions struct {
	AWSOidcS3BucketName                    string
	AWSOidcS3Credentials                   string
	AWSOidcS3Region                        string
	AWSPrivateCredentialsFile              string
	AWSPrivateRegion                       string
	EnableCIDebugOutput                    bool
	ExternalDNSCredentials                 string
	ExternalDNSDomain                      string
	ExternalDNSDomainFilter                string
	ExternalDNSProvider                    string
	HyperShiftOperatorLatestImage          string
	PlatformMonitoring                     string
	PrivatePlatform                        string
	EnableSizeTagging                      bool
	EnableDedicatedRequestServingIsolation bool
	EnableCPOOverrides                     bool
	EnableEtcdRecovery                     bool
	DryRun                                 bool
	DryRunDir                              string
}

type ConfigurableClusterOptions struct {
	AWSCredentialsFile                    string
	AWSEndpointAccess                     string
	AWSKmsKeyAlias                        string
	AWSMultiArch                          bool
	AWSOidcS3BucketName                   string
	Annotations                           stringMapVar
	AzureCredentialsFile                  string
	AzureManagedIdentitiesFile            string
	AzureIssuerURL                        string
	AzureMultiArch                        bool
	AzureServiceAccountTokenIssuerKeyPath string
	AzureDataPlaneIdentities              string
	AzureWorkloadIdentitiesFile           string
	AzureEncryptionKeyID                  string
	AzureKMSUserAssignedCredsSecretName   string
	OpenStackCredentialsFile              string
	OpenStackCACertFile                   string
	AzureLocation                         string
	AzureMarketplaceOffer                 string
	AzureMarketplacePublisher             string
	AzureMarketplaceSKU                   string
	AzureMarketplaceVersion               string
	BaseDomain                            string
	ClusterCIDR                           stringSliceVar
	ControlPlaneOperatorImage             string
	EtcdStorageClass                      string
	ExternalDNSDomain                     string
	KubeVirtContainerDiskImage            string
	KubeVirtInfraKubeconfigFile           string
	KubeVirtInfraNamespace                string
	KubeVirtNodeCores                     uint
	KubeVirtNodeMemory                    string
	KubeVirtRootVolumeSize                uint
	KubeVirtRootVolumeVolumeMode          string
	NetworkType                           string
	NodePoolReplicas                      int
	OpenStackExternalNetworkID            string
	OpenStackNodeAvailabilityZone         string
	OpenStackNodeFlavor                   string
	OpenStackNodeImageName                string
	OpenStackDNSNameservers               stringSliceVar
	PowerVSCloudConnection                string
	PowerVSCloudInstanceID                string
	PowerVSMemory                         int
	PowerVSPER                            bool
	PowerVSProcType                       hyperv1.PowerVSNodePoolProcType
	PowerVSProcessors                     string
	PowerVSRegion                         string
	PowerVSResourceGroup                  string
	PowerVSSysType                        string
	PowerVSTransitGateway                 string
	PowerVSTransitGatewayLocation         string
	PowerVSVPC                            string
	PowerVSVpcRegion                      string
	PowerVSZone                           string
	PullSecretFile                        string
	Region                                string
	SSHKeyFile                            string
	ServiceCIDR                           stringSliceVar
	Zone                                  stringSliceVar
}

func (o *Options) DefaultClusterOptions(t *testing.T) PlatformAgnosticOptions {
	createOption := PlatformAgnosticOptions{
		RawCreateOptions: core.RawCreateOptions{
			ReleaseImage:                     o.LatestReleaseImage,
			NodePoolReplicas:                 2,
			ControlPlaneAvailabilityPolicy:   string(hyperv1.SingleReplica),
			InfrastructureAvailabilityPolicy: string(hyperv1.SingleReplica),
			NetworkType:                      string(o.ConfigurableClusterOptions.NetworkType),
			BaseDomain:                       o.ConfigurableClusterOptions.BaseDomain,
			PullSecretFile:                   o.ConfigurableClusterOptions.PullSecretFile,
			ControlPlaneOperatorImage:        o.ConfigurableClusterOptions.ControlPlaneOperatorImage,
			ExternalDNSDomain:                o.ConfigurableClusterOptions.ExternalDNSDomain,
			NodeUpgradeType:                  hyperv1.UpgradeTypeReplace,
			ServiceCIDR:                      []string{"172.31.0.0/16"},
			ClusterCIDR:                      []string{"10.132.0.0/14"},
			BeforeApply:                      o.BeforeApply,
			Log:                              NewLogr(t),
			Annotations: []string{
				fmt.Sprintf("%s=true", hyperv1.CleanupCloudResourcesAnnotation),
				fmt.Sprintf("%s=true", hyperv1.SkipReleaseImageValidation),
			},
			EtcdStorageClass: o.ConfigurableClusterOptions.EtcdStorageClass,
		},
		NonePlatform:      o.DefaultNoneOptions(),
		AWSPlatform:       o.DefaultAWSOptions(),
		KubevirtPlatform:  o.DefaultKubeVirtOptions(),
		AzurePlatform:     o.DefaultAzureOptions(),
		PowerVSPlatform:   o.DefaultPowerVSOptions(),
		OpenStackPlatform: o.DefaultOpenStackOptions(),
	}

	switch o.Platform {
	case hyperv1.AWSPlatform, hyperv1.AzurePlatform, hyperv1.NonePlatform, hyperv1.KubevirtPlatform, hyperv1.OpenStackPlatform:
		createOption.Arch = hyperv1.ArchitectureAMD64
	case hyperv1.PowerVSPlatform:
		createOption.Arch = hyperv1.ArchitecturePPC64LE
	}

	if o.ConfigurableClusterOptions.SSHKeyFile == "" {
		createOption.GenerateSSH = true
	} else {
		createOption.SSHKeyFile = o.ConfigurableClusterOptions.SSHKeyFile
	}

	if o.ConfigurableClusterOptions.Annotations != nil {
		for k, v := range o.ConfigurableClusterOptions.Annotations {
			createOption.Annotations = append(createOption.Annotations, fmt.Sprintf("%s=%s", k, v))
		}
	}

	if len(o.ConfigurableClusterOptions.ServiceCIDR) != 0 {
		createOption.ServiceCIDR = o.ConfigurableClusterOptions.ServiceCIDR
	}

	if len(o.ConfigurableClusterOptions.ClusterCIDR) != 0 {
		createOption.ClusterCIDR = o.ConfigurableClusterOptions.ClusterCIDR
	}

	// set external OIDC if enabled
	if o.ExternalOIDCProvider != "" {
		createOption.ExtOIDCConfig = GetExtOIDCConfig(o.ExternalOIDCProvider, o.ExternalOIDCCliClientID, o.ExternalOIDCConsoleClientID,
			o.ExternalOIDCIssuerURL, o.ExternalOIDCConsoleSecret, o.ExternalOIDCCABundleFile, o.ExternalOIDCTestUsers)
	}

	return createOption
}

func (o *Options) DefaultNoneOptions() none.RawCreateOptions {
	return none.RawCreateOptions{
		APIServerAddress:          "",
		ExposeThroughLoadBalancer: true,
	}
}

func (p *Options) DefaultOpenStackOptions() hypershiftopenstack.RawCreateOptions {
	opts := hypershiftopenstack.RawCreateOptions{
		OpenStackCredentialsFile:   p.ConfigurableClusterOptions.OpenStackCredentialsFile,
		OpenStackCACertFile:        p.ConfigurableClusterOptions.OpenStackCACertFile,
		OpenStackExternalNetworkID: p.ConfigurableClusterOptions.OpenStackExternalNetworkID,
		NodePoolOpts: &openstacknodepool.RawOpenStackPlatformCreateOptions{
			OpenStackPlatformOptions: &openstacknodepool.OpenStackPlatformOptions{
				Flavor:         p.ConfigurableClusterOptions.OpenStackNodeFlavor,
				AvailabityZone: p.ConfigurableClusterOptions.OpenStackNodeAvailabilityZone,
			},
		},
		OpenStackDNSNameservers: p.ConfigurableClusterOptions.OpenStackDNSNameservers,
	}

	return opts
}

var nextAWSZoneIndex = 0

func (o *Options) DefaultAWSOptions() hypershiftaws.RawCreateOptions {
	opts := hypershiftaws.RawCreateOptions{
		RootVolumeSize: 64,
		RootVolumeType: "gp3",
		Credentials: awscmdutil.AWSCredentialsOptions{
			AWSCredentialsFile: o.ConfigurableClusterOptions.AWSCredentialsFile,
		},
		Region:                 o.ConfigurableClusterOptions.Region,
		EndpointAccess:         o.ConfigurableClusterOptions.AWSEndpointAccess,
		IssuerURL:              o.IssuerURL,
		MultiArch:              o.ConfigurableClusterOptions.AWSMultiArch,
		PublicOnly:             true,
		UseROSAManagedPolicies: true,
	}
	if IsLessThan(semver.MustParse("4.16.0")) {
		opts.PublicOnly = false
	}

	// Set an expiration date tag if it's not already set
	expirationDateTagSet := false
	for _, tag := range o.AdditionalTags {
		key := strings.Split(tag, "=")[0]
		if key == "expirationDate" {
			expirationDateTagSet = true
			break
		}
	}
	if !expirationDateTagSet {
		// Set the expiration date tag to be 4 hours from now
		o.AdditionalTags = append(o.AdditionalTags, fmt.Sprintf("expirationDate=%s", time.Now().Add(4*time.Hour).UTC().Format(time.RFC3339)))
	}

	opts.AdditionalTags = append(opts.AdditionalTags, o.AdditionalTags...)
	if len(o.ConfigurableClusterOptions.Zone) == 0 {
		// align with default for e2e.aws-region flag
		opts.Zones = []string{"us-east-1a"}
	} else {
		// For AWS, select a single zone for InfrastructureAvailabilityPolicy: SingleReplica guest cluster.
		// This option is currently not configurable through flags and not set manually
		// in any test, so we know InfrastructureAvailabilityPolicy is SingleReplica.
		// If any test changes this in the future, we need to add logic here to make the
		// guest cluster multi-zone in that case.
		zones := strings.Split(o.ConfigurableClusterOptions.Zone.String(), ",")
		awsGuestZone := zones[nextAWSZoneIndex]
		nextAWSZoneIndex = (nextAWSZoneIndex + 1) % len(zones)
		opts.Zones = []string{awsGuestZone}
	}

	return opts
}

func (o *Options) DefaultKubeVirtOptions() kubevirt.RawCreateOptions {
	return kubevirt.RawCreateOptions{
		ServicePublishingStrategy: kubevirt.IngressServicePublishingStrategy,
		InfraKubeConfigFile:       o.ConfigurableClusterOptions.KubeVirtInfraKubeconfigFile,
		InfraNamespace:            o.ConfigurableClusterOptions.KubeVirtInfraNamespace,
		NodePoolOpts: &kubevirtnodepool.RawKubevirtPlatformCreateOptions{
			KubevirtPlatformOptions: &kubevirtnodepool.KubevirtPlatformOptions{
				Cores:                uint32(o.ConfigurableClusterOptions.KubeVirtNodeCores),
				Memory:               o.ConfigurableClusterOptions.KubeVirtNodeMemory,
				RootVolumeSize:       uint32(o.ConfigurableClusterOptions.KubeVirtRootVolumeSize),
				RootVolumeVolumeMode: o.ConfigurableClusterOptions.KubeVirtRootVolumeVolumeMode,
			},
		},
	}
}

func (o *Options) DefaultAzureOptions() azure.RawCreateOptions {
	opts := azure.RawCreateOptions{
		CredentialsFile:                  o.ConfigurableClusterOptions.AzureCredentialsFile,
		Location:                         o.ConfigurableClusterOptions.AzureLocation,
		IssuerURL:                        o.ConfigurableClusterOptions.AzureIssuerURL,
		ServiceAccountTokenIssuerKeyPath: o.ConfigurableClusterOptions.AzureServiceAccountTokenIssuerKeyPath,
		DataPlaneIdentitiesFile:          o.ConfigurableClusterOptions.AzureDataPlaneIdentities,
		DNSZoneRGName:                    "os4-common",
		AssignServicePrincipalRoles:      true,
		MultiArch:                        o.ConfigurableClusterOptions.AzureMultiArch,

		NodePoolOpts: azurenodepool.DefaultOptions(),
	}
	if len(o.ConfigurableClusterOptions.Zone) != 0 {
		zones := strings.Split(o.ConfigurableClusterOptions.Zone.String(), ",")
		// Assign all Azure zones to guest cluster
		opts.AvailabilityZones = zones
	}

	if o.ConfigurableClusterOptions.AzureManagedIdentitiesFile != "" {
		opts.ManagedIdentitiesFile = o.ConfigurableClusterOptions.AzureManagedIdentitiesFile
	}

	if o.ConfigurableClusterOptions.AzureWorkloadIdentitiesFile != "" {
		opts.WorkloadIdentitiesFile = o.ConfigurableClusterOptions.AzureWorkloadIdentitiesFile
	}

	if opts.ManagedIdentitiesFile != "" {
		opts.TechPreviewEnabled = true
	}

	if o.ConfigurableClusterOptions.AzureMarketplaceOffer != "" {
		opts.NodePoolOpts.MarketplaceOffer = o.ConfigurableClusterOptions.AzureMarketplaceOffer
	}

	if o.ConfigurableClusterOptions.AzureMarketplacePublisher != "" {
		opts.NodePoolOpts.MarketplacePublisher = o.ConfigurableClusterOptions.AzureMarketplacePublisher
	}

	if o.ConfigurableClusterOptions.AzureMarketplaceSKU != "" {
		opts.NodePoolOpts.MarketplaceSKU = o.ConfigurableClusterOptions.AzureMarketplaceSKU
	}

	if o.ConfigurableClusterOptions.AzureMarketplaceVersion != "" {
		opts.NodePoolOpts.MarketplaceVersion = o.ConfigurableClusterOptions.AzureMarketplaceVersion
	}

	return opts
}

func (o *Options) DefaultPowerVSOptions() powervs.RawCreateOptions {
	return powervs.RawCreateOptions{
		ResourceGroup:          o.ConfigurableClusterOptions.PowerVSResourceGroup,
		Region:                 o.ConfigurableClusterOptions.PowerVSRegion,
		Zone:                   o.ConfigurableClusterOptions.PowerVSZone,
		VPCRegion:              o.ConfigurableClusterOptions.PowerVSVpcRegion,
		SysType:                o.ConfigurableClusterOptions.PowerVSSysType,
		ProcType:               o.ConfigurableClusterOptions.PowerVSProcType,
		Processors:             o.ConfigurableClusterOptions.PowerVSProcessors,
		Memory:                 int32(o.ConfigurableClusterOptions.PowerVSMemory),
		CloudInstanceID:        o.ConfigurableClusterOptions.PowerVSCloudInstanceID,
		VPC:                    o.ConfigurableClusterOptions.PowerVSVPC,
		TransitGatewayLocation: o.ConfigurableClusterOptions.PowerVSTransitGatewayLocation,
		TransitGateway:         o.ConfigurableClusterOptions.PowerVSTransitGateway,
	}
}

// Complete is intended to be called after flags have been bound and sets
// up additional contextual defaulting.
func (o *Options) Complete() error {

	if shouldTestCPOOverride() {
		o.LatestReleaseImage, o.PreviousReleaseImage = controlplaneoperatoroverrides.LatestOverrideTestReleases(string(o.Platform))
	}

	if len(o.LatestReleaseImage) == 0 {
		client, err := util.GetClient()
		if err != nil {
			return fmt.Errorf("failed to get client: %w", err)
		}
		defaultVersion, err := supportedversion.LookupDefaultOCPVersion(context.TODO(), "", client)
		if err != nil {
			return fmt.Errorf("couldn't look up default OCP version: %w", err)
		}
		o.LatestReleaseImage = defaultVersion.PullSpec
	}
	// TODO: This is actually basically a required field right now. Maybe the input
	// to tests should be a small API spec that describes the tests and their
	// inputs to avoid having to make every test input required. Or extract
	// e2e test suites into subcommands with their own distinct flags to make
	// selectively running them easier?
	if len(o.PreviousReleaseImage) == 0 {
		o.PreviousReleaseImage = o.LatestReleaseImage
	}

	o.IsRunningInCI = os.Getenv("OPENSHIFT_CI") == "true"

	if o.IsRunningInCI {
		if len(o.ArtifactDir) == 0 {
			o.ArtifactDir = os.Getenv("ARTIFACT_DIR")
		}
		if len(o.ConfigurableClusterOptions.BaseDomain) == 0 && o.Platform != hyperv1.KubevirtPlatform {
			// TODO: make this an envvar with change to openshift/release, then change here
			o.ConfigurableClusterOptions.BaseDomain = DefaultCIBaseDomain
		}
	}

	if o.HyperShiftOperatorLatestImage != "" {
		o.HOInstallationOptions.HyperShiftOperatorLatestImage = o.HyperShiftOperatorLatestImage
	}

	if o.ConfigurableClusterOptions.AWSOidcS3BucketName != "" {
		o.HOInstallationOptions.AWSOidcS3BucketName = o.ConfigurableClusterOptions.AWSOidcS3BucketName
	}

	if o.ConfigurableClusterOptions.ExternalDNSDomain != "" {
		o.HOInstallationOptions.ExternalDNSDomain = o.ConfigurableClusterOptions.ExternalDNSDomain
	}

	return nil
}

// Validate is intended to be called after Complete and validates the options
// are usable by tests.
func (o *Options) Validate() error {
	var errs []error

	if len(o.LatestReleaseImage) == 0 {
		errs = append(errs, fmt.Errorf("latest release image is required"))
	}

	if len(o.ConfigurableClusterOptions.BaseDomain) == 0 {
		// The KubeVirt e2e tests don't require a base domain right now.
		//
		// For KubeVirt, the e2e tests generate a base domain within the *.apps domain
		// of the ocp cluster. So, the guest cluster's base domain is a
		// subdomain of the hypershift infra/mgmt cluster's base domain.
		//
		// Example:
		//   Infra/Mgmt cluster's DNS
		//     Base: example.com
		//     Cluster: mgmt-cluster.example.com
		//     Apps:    *.apps.mgmt-cluster.example.com
		//   KubeVirt Guest cluster's DNS
		//     Base: apps.mgmt-cluster.example.com
		//     Cluster: guest.apps.mgmt-cluster.example.com
		//     Apps: *.apps.guest.apps.mgmt-cluster.example.com
		//
		// This is possible using OCP wildcard routes
		if o.Platform != hyperv1.KubevirtPlatform {
			errs = append(errs, fmt.Errorf("base domain is required"))
		}
	}

	if o.RequestServingIsolation {
		if o.ManagementClusterName == "" || o.ManagementClusterNamespace == "" || o.ManagementParentKubeconfig == "" {
			errs = append(errs, fmt.Errorf("management cluster name, namespace, and parent kubeconfig are required to test request serving isolation"))
		}
	}

	return errors.NewAggregate(errs)
}

var _ flag.Value = &stringSliceVar{}

// stringSliceVar mimics github.com/spf13/pflag.StringSliceVar in a stdlib-compatible way
type stringSliceVar []string

func (s *stringSliceVar) String() string     { return strings.Join(*s, ",") }
func (s *stringSliceVar) Set(v string) error { *s = append(*s, strings.Split(v, ",")...); return nil }

type stringMapVar map[string]string

func (s *stringMapVar) String() string {
	if *s == nil {
		return ""
	}
	return fmt.Sprintf("%v", *s)
}

func (s *stringMapVar) Set(value string) error {
	split := strings.SplitN(value, "=", 2)
	if len(split) != 2 {
		return fmt.Errorf("invalid argument: %s", value)
	}
	if *s == nil {
		*s = map[string]string{}
	}
	map[string]string(*s)[split[0]] = split[1]
	return nil
}

func shouldTestCPOOverride() bool {
	return os.Getenv("TEST_CPO_OVERRIDE") == "1"
}
