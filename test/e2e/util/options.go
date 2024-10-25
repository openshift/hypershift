package util

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"

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
	"github.com/openshift/hypershift/cmd/version"
	controlplaneoperatoroverrides "github.com/openshift/hypershift/hypershift-operator/controlplaneoperator-overrides"
	"github.com/openshift/hypershift/test/e2e/util"
)

// options are global test options applicable to all scenarios.
type options struct {
	LatestReleaseImage   string
	PreviousReleaseImage string
	n2MinorReleaseImage  string
	n1MinorReleaseImage  string
	IsRunningInCI        bool
	ArtifactDir          string

	// BeforeApply is a function passed to the CLI create command giving the test
	// code an opportunity to inspect or mutate the resources the CLI will create
	// before they're applied.
	BeforeApply func(crclient.Object) `json:"-"`

	Platform    hyperv1.PlatformType
	platformRaw string

	configurableClusterOptions configurableClusterOptions
	additionalTags             stringSliceVar

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
}

type configurableClusterOptions struct {
	AWSCredentialsFile            string
	AWSMultiArch                  bool
	AzureCredentialsFile          string
	AzureManagedIdentitiesFile    string
	AzureIssuerURL                        string
	AzureServiceAccountTokenIssuerKeyPath string
	AzureDataPlaneIdentities              string
	OpenStackCredentialsFile      string
	OpenStackCACertFile           string
	AzureLocation                 string
	AzureMarketplaceOffer         string
	AzureMarketplacePublisher     string
	AzureMarketplaceSKU           string
	AzureMarketplaceVersion       string
	Region                        string
	Zone                          stringSliceVar
	PullSecretFile                string
	BaseDomain                    string
	ControlPlaneOperatorImage     string
	AWSEndpointAccess             string
	AWSOidcS3BucketName           string
	AWSKmsKeyAlias                string
	ExternalDNSDomain             string
	KubeVirtContainerDiskImage    string
	KubeVirtNodeMemory            string
	KubeVirtRootVolumeSize        uint
	KubeVirtRootVolumeVolumeMode  string
	KubeVirtNodeCores             uint
	KubeVirtInfraKubeconfigFile   string
	KubeVirtInfraNamespace        string
	NodePoolReplicas              int
	SSHKeyFile                    string
	NetworkType                   string
	OpenStackExternalNetworkID    string
	OpenStackNodeFlavor           string
	OpenStackNodeImageName        string
	OpenStackNodeAvailabilityZone string
	PowerVSResourceGroup          string
	PowerVSRegion                 string
	PowerVSZone                   string
	PowerVSVpcRegion              string
	PowerVSSysType                string
	PowerVSProcType               hyperv1.PowerVSNodePoolProcType
	PowerVSProcessors             string
	PowerVSMemory                 int
	PowerVSCloudInstanceID        string
	PowerVSCloudConnection        string
	PowerVSVPC                    string
	PowerVSPER                    bool
	PowerVSTransitGatewayLocation string
	PowerVSTransitGateway         string
	EtcdStorageClass              string
	Annotations                   stringMapVar
	ServiceCIDR                   stringSliceVar
	ClusterCIDR                   stringSliceVar
}

var nextAWSZoneIndex = 0

func (o *options) DefaultClusterOptions(t *testing.T) e2eutil.PlatformAgnosticOptions {
	createOption := e2eutil.PlatformAgnosticOptions{
		RawCreateOptions: core.RawCreateOptions{
			ReleaseImage:                     o.LatestReleaseImage,
			NodePoolReplicas:                 2,
			ControlPlaneAvailabilityPolicy:   string(hyperv1.SingleReplica),
			InfrastructureAvailabilityPolicy: string(hyperv1.SingleReplica),
			NetworkType:                      string(o.configurableClusterOptions.NetworkType),
			BaseDomain:                       o.configurableClusterOptions.BaseDomain,
			PullSecretFile:                   o.configurableClusterOptions.PullSecretFile,
			ControlPlaneOperatorImage:        o.configurableClusterOptions.ControlPlaneOperatorImage,
			ExternalDNSDomain:                o.configurableClusterOptions.ExternalDNSDomain,
			NodeUpgradeType:                  hyperv1.UpgradeTypeReplace,
			ServiceCIDR:                      []string{"172.31.0.0/16"},
			ClusterCIDR:                      []string{"10.132.0.0/14"},
			BeforeApply:                      o.BeforeApply,
			Log:                              util.NewLogr(t),
			Annotations: []string{
				fmt.Sprintf("%s=true", hyperv1.CleanupCloudResourcesAnnotation),
				fmt.Sprintf("%s=true", hyperv1.SkipReleaseImageValidation),
			},
			EtcdStorageClass: o.configurableClusterOptions.EtcdStorageClass,
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

	if o.configurableClusterOptions.SSHKeyFile == "" {
		createOption.GenerateSSH = true
	} else {
		createOption.SSHKeyFile = o.configurableClusterOptions.SSHKeyFile
	}

	if o.configurableClusterOptions.Annotations != nil {
		for k, v := range o.configurableClusterOptions.Annotations {
			createOption.Annotations = append(createOption.Annotations, fmt.Sprintf("%s=%s", k, v))
		}
	}

	if len(o.configurableClusterOptions.ServiceCIDR) != 0 {
		createOption.ServiceCIDR = o.configurableClusterOptions.ServiceCIDR
	}

	if len(o.configurableClusterOptions.ClusterCIDR) != 0 {
		createOption.ClusterCIDR = o.configurableClusterOptions.ClusterCIDR
	}

	return createOption
}

func (o *options) DefaultNoneOptions() none.RawCreateOptions {
	return none.RawCreateOptions{
		APIServerAddress:          "",
		ExposeThroughLoadBalancer: true,
	}
}

func (p *options) DefaultOpenStackOptions() hypershiftopenstack.RawCreateOptions {
	opts := hypershiftopenstack.RawCreateOptions{
		OpenStackCredentialsFile:   p.configurableClusterOptions.OpenStackCredentialsFile,
		OpenStackCACertFile:        p.configurableClusterOptions.OpenStackCACertFile,
		OpenStackExternalNetworkID: p.configurableClusterOptions.OpenStackExternalNetworkID,
		NodePoolOpts: &openstacknodepool.RawOpenStackPlatformCreateOptions{
			OpenStackPlatformOptions: &openstacknodepool.OpenStackPlatformOptions{
				Flavor:         p.configurableClusterOptions.OpenStackNodeFlavor,
				ImageName:      p.configurableClusterOptions.OpenStackNodeImageName,
				AvailabityZone: p.configurableClusterOptions.OpenStackNodeAvailabilityZone,
			},
		},
	}

	return opts
}

func (o *options) DefaultAWSOptions() hypershiftaws.RawCreateOptions {
	opts := hypershiftaws.RawCreateOptions{
		RootVolumeSize: 64,
		RootVolumeType: "gp3",
		Credentials: awscmdutil.AWSCredentialsOptions{
			AWSCredentialsFile: o.configurableClusterOptions.AWSCredentialsFile,
		},
		Region:         o.configurableClusterOptions.Region,
		EndpointAccess: o.configurableClusterOptions.AWSEndpointAccess,
		IssuerURL:      o.IssuerURL,
		MultiArch:      o.configurableClusterOptions.AWSMultiArch,
		PublicOnly:     true,
	}
	if e2eutil.IsLessThan(semver.MustParse("4.16.0")) {
		opts.PublicOnly = false
	}

	opts.AdditionalTags = append(opts.AdditionalTags, o.additionalTags...)
	if len(o.configurableClusterOptions.Zone) == 0 {
		// align with default for e2e.aws-region flag
		opts.Zones = []string{"us-east-1a"}
	} else {
		// For AWS, select a single zone for InfrastructureAvailabilityPolicy: SingleReplica guest cluster.
		// This option is currently not configurable through flags and not set manually
		// in any test, so we know InfrastructureAvailabilityPolicy is SingleReplica.
		// If any test changes this in the future, we need to add logic here to make the
		// guest cluster multi-zone in that case.
		zones := strings.Split(o.configurableClusterOptions.Zone.String(), ",")
		awsGuestZone := zones[nextAWSZoneIndex]
		nextAWSZoneIndex = (nextAWSZoneIndex + 1) % len(zones)
		opts.Zones = []string{awsGuestZone}
	}

	return opts
}

func (o *options) DefaultKubeVirtOptions() kubevirt.RawCreateOptions {
	return kubevirt.RawCreateOptions{
		ServicePublishingStrategy: kubevirt.IngressServicePublishingStrategy,
		InfraKubeConfigFile:       o.configurableClusterOptions.KubeVirtInfraKubeconfigFile,
		InfraNamespace:            o.configurableClusterOptions.KubeVirtInfraNamespace,
		NodePoolOpts: &kubevirtnodepool.RawKubevirtPlatformCreateOptions{
			KubevirtPlatformOptions: &kubevirtnodepool.KubevirtPlatformOptions{
				Cores:                uint32(o.configurableClusterOptions.KubeVirtNodeCores),
				Memory:               o.configurableClusterOptions.KubeVirtNodeMemory,
				RootVolumeSize:       uint32(o.configurableClusterOptions.KubeVirtRootVolumeSize),
				RootVolumeVolumeMode: o.configurableClusterOptions.KubeVirtRootVolumeVolumeMode,
			},
		},
	}
}

func (o *options) DefaultAzureOptions() azure.RawCreateOptions {
	opts := azure.RawCreateOptions{
		CredentialsFile:                  o.configurableClusterOptions.AzureCredentialsFile,
		Location:                         o.configurableClusterOptions.AzureLocation,
		IssuerURL:                        o.configurableClusterOptions.AzureIssuerURL,
		ServiceAccountTokenIssuerKeyPath: o.configurableClusterOptions.AzureServiceAccountTokenIssuerKeyPath,
		DataPlaneIdentitiesFile:          o.configurableClusterOptions.AzureDataPlaneIdentities,
		DNSZoneRGName:                    "os4-common",
		AssignServicePrincipalRoles:      true,

		NodePoolOpts: azurenodepool.DefaultOptions(),
	}
	if len(o.configurableClusterOptions.Zone) != 0 {
		zones := strings.Split(o.configurableClusterOptions.Zone.String(), ",")
		// Assign all Azure zones to guest cluster
		opts.AvailabilityZones = zones
	}

	if o.configurableClusterOptions.AzureManagedIdentitiesFile != "" {
		opts.ManagedIdentitiesFile = o.configurableClusterOptions.AzureManagedIdentitiesFile
	}

	if opts.ManagedIdentitiesFile != "" {
		opts.TechPreviewEnabled = true
	}

	if o.configurableClusterOptions.AzureMarketplaceOffer != "" {
		opts.NodePoolOpts.MarketplaceOffer = o.configurableClusterOptions.AzureMarketplaceOffer
	}

	if o.configurableClusterOptions.AzureMarketplacePublisher != "" {
		opts.NodePoolOpts.MarketplacePublisher = o.configurableClusterOptions.AzureMarketplacePublisher
	}

	if o.configurableClusterOptions.AzureMarketplaceSKU != "" {
		opts.NodePoolOpts.MarketplaceSKU = o.configurableClusterOptions.AzureMarketplaceSKU
	}

	if o.configurableClusterOptions.AzureMarketplaceVersion != "" {
		opts.NodePoolOpts.MarketplaceVersion = o.configurableClusterOptions.AzureMarketplaceVersion
	}

	return opts
}

func (o *options) DefaultPowerVSOptions() powervs.RawCreateOptions {
	return powervs.RawCreateOptions{
		ResourceGroup:          o.configurableClusterOptions.PowerVSResourceGroup,
		Region:                 o.configurableClusterOptions.PowerVSRegion,
		Zone:                   o.configurableClusterOptions.PowerVSZone,
		VPCRegion:              o.configurableClusterOptions.PowerVSVpcRegion,
		SysType:                o.configurableClusterOptions.PowerVSSysType,
		ProcType:               o.configurableClusterOptions.PowerVSProcType,
		Processors:             o.configurableClusterOptions.PowerVSProcessors,
		Memory:                 int32(o.configurableClusterOptions.PowerVSMemory),
		CloudInstanceID:        o.configurableClusterOptions.PowerVSCloudInstanceID,
		CloudConnection:        o.configurableClusterOptions.PowerVSCloudConnection,
		VPC:                    o.configurableClusterOptions.PowerVSVPC,
		PER:                    o.configurableClusterOptions.PowerVSPER,
		TransitGatewayLocation: o.configurableClusterOptions.PowerVSTransitGatewayLocation,
		TransitGateway:         o.configurableClusterOptions.PowerVSTransitGateway,
	}
}

// Complete is intended to be called after flags have been bound and sets
// up additional contextual defaulting.
func (o *options) Complete() error {

	if shouldTestCPOOverride() {
		o.LatestReleaseImage, o.PreviousReleaseImage = controlplaneoperatoroverrides.LatestOverrideTestReleases()
	}

	if len(o.LatestReleaseImage) == 0 {
		defaultVersion, err := version.LookupDefaultOCPVersion("")
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
		if len(o.configurableClusterOptions.BaseDomain) == 0 && o.Platform != hyperv1.KubevirtPlatform {
			// TODO: make this an envvar with change to openshift/release, then change here
			o.configurableClusterOptions.BaseDomain = "origin-ci-int-aws.dev.rhcloud.com"
		}
	}

	return nil
}

// Validate is intended to be called after Complete and validates the options
// are usable by tests.
func (o *options) Validate() error {
	var errs []error

	if len(o.LatestReleaseImage) == 0 {
		errs = append(errs, fmt.Errorf("latest release image is required"))
	}

	if len(o.configurableClusterOptions.BaseDomain) == 0 {
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

	return apierr.NewAggregate(errs)
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
	split := strings.Split(value, "=")
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
