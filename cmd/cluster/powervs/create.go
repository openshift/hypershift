package powervs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/cmd/cluster/core"
	powervsinfra "github.com/openshift/hypershift/cmd/infra/powervs"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultCIDRBlock = "10.0.0.0/16"
)

func DefaultOptions() *CreateOptions {
	return &CreateOptions{
		Region:                 "us-south",
		Zone:                   "us-south",
		VPCRegion:              "us-south",
		TransitGatewayLocation: "us-south",
		SysType:                "s922",
		ProcType:               hyperv1.PowerVSNodePoolSharedProcType,
		Processors:             "0.5",
		Memory:                 32,
	}
}

func BindOptions(opts *CreateOptions, flags *pflag.FlagSet) {
	flags.StringVar(&opts.ResourceGroup, "resource-group", opts.ResourceGroup, "IBM Cloud Resource group")
	flags.StringVar(&opts.Region, "region", opts.Region, "IBM Cloud region. Default is us-south")
	flags.StringVar(&opts.Zone, "zone", opts.Zone, "IBM Cloud zone. Default is us-south")
	flags.StringVar(&opts.CloudInstanceID, "cloud-instance-id", opts.CloudInstanceID, "IBM Cloud PowerVS Service Instance ID. Use this flag to reuse an existing PowerVS Service Instance resource for cluster's infra")
	flags.StringVar(&opts.CloudConnection, "cloud-connection", opts.CloudConnection, "Cloud Connection in given zone. Use this flag to reuse an existing Cloud Connection resource for cluster's infra")
	flags.StringVar(&opts.VPCRegion, "vpc-region", opts.VPCRegion, "IBM Cloud VPC Region for VPC resources. Default is us-south")
	flags.StringVar(&opts.VPC, "vpc", opts.VPC, "IBM Cloud VPC Name. Use this flag to reuse an existing VPC resource for cluster's infra")
	flags.StringVar(&opts.SysType, "sys-type", opts.SysType, "System type used to host the instance(e.g: s922, e980, e880). Default is s922")
	flags.Var(&opts.ProcType, "proc-type", "Processor type (dedicated, shared, capped). Default is shared")
	flags.StringVar(&opts.Processors, "processors", opts.Processors, "Number of processors allocated. Default is 0.5")
	flags.Int32Var(&opts.Memory, "memory", opts.Memory, "Amount of memory allocated (in GB). Default is 32")
	flags.BoolVar(&opts.Debug, "debug", opts.Debug, "Enabling this will print PowerVS API Request & Response logs")
	flags.BoolVar(&opts.RecreateSecrets, "recreate-secrets", opts.RecreateSecrets, "Enabling this flag will recreate creds mentioned https://hypershift-docs.netlify.app/reference/api/#hypershift.openshift.io/v1alpha1.PowerVSPlatformSpec here. This is required when rerunning 'hypershift create cluster powervs' or 'hypershift create infra powervs' commands, since API key once created cannot be retrieved again. Please make sure that cluster name used is unique across different management clusters before using this flag")
	flags.BoolVar(&opts.PER, "power-edge-router", opts.PER, "Enabling this flag will utilize Power Edge Router solution via transit gateway instead of cloud connection to create a connection between PowerVS and VPC")
	flags.StringVar(&opts.TransitGatewayLocation, "transit-gateway-location", opts.TransitGatewayLocation, "IBM Cloud Transit Gateway location")
	flags.StringVar(&opts.TransitGateway, "transit-gateway", opts.TransitGateway, "IBM Cloud Transit Gateway. Use this flag to reuse an existing Transit Gateway resource for cluster's infra")
}

type CreateOptions struct {
	// ResourceGroup to use in IBM Cloud
	ResourceGroup string
	// Region to use in PowerVS service in IBM Cloud
	Region string
	// Zone to use in PowerVS service in IBM Cloud
	Zone string
	// CloudInstanceID of the existing PowerVS service instance
	// Set this field when reusing existing resources from IBM Cloud
	CloudInstanceID string
	// CloudConnection is name of the existing cloud connection
	// Set this field when reusing existing resources from IBM Cloud
	CloudConnection string
	// VPCRegion to use in IBM Cloud
	// Set this field when reusing existing resources from IBM Cloud
	VPCRegion string
	// VPC is name of the existing VPC instance
	VPC string
	// Debug flag is to enable debug logs in powervs client
	Debug bool
	// RecreateSecrets flag is to delete the existing secrets created in IBM Cloud and recreate new secrets
	// This is required since cannot recover the secret once its created
	// Can be used during rerun
	RecreateSecrets bool
	// PER flag is to choose Power Edge Router via Transit Gateway instead of using cloud connections to connect VPC
	PER bool
	// TransitGatewayLocation to use in Transit gateway service in IBM Cloud
	TransitGatewayLocation string
	// TransitGateway is name of the existing Transit gateway instance
	// Set this field when reusing existing resources from IBM Cloud
	TransitGateway string

	// nodepool related options
	// SysType of the worker node in PowerVS service
	SysType string
	// ProcType of the worker node in PowerVS service
	ProcType hyperv1.PowerVSNodePoolProcType
	// Processors count of the worker node in PowerVS service
	Processors string
	// Memory of the worker node in PowerVS service
	Memory int32

	externalDNSDomain string

	infra *powervsinfra.Infra
}

func (o *CreateOptions) Validate(ctx context.Context, opts *core.CreateOptions) error {
	if opts.BaseDomain == "" && opts.InfrastructureJSON == "" {
		return fmt.Errorf("base-domain flag is required if infra-json is not provided")
	}

	if o.ResourceGroup == "" && opts.InfrastructureJSON == "" {
		return fmt.Errorf("resource-group flag is required if infra-json is not provided")
	}

	if o.PER && o.TransitGatewayLocation == "" {
		return fmt.Errorf("transit gateway location is required if use-power-edge-router flag is enabled")
	}
	return nil
}

func (o *CreateOptions) Complete(ctx context.Context, opts *core.CreateOptions) error {
	opts.Arch = hyperv1.ArchitecturePPC64LE
	o.externalDNSDomain = opts.ExternalDNSDomain

	// Load or create infrastructure for the cluster
	if len(opts.InfrastructureJSON) > 0 {
		rawInfra, err := os.ReadFile(opts.InfrastructureJSON)
		if err != nil {
			return fmt.Errorf("failed to read infra json file: %w", err)
		}
		if err = json.Unmarshal(rawInfra, o.infra); err != nil {
			return fmt.Errorf("failed to load infra json: %w", err)
		}
	} else {
		var opt *powervsinfra.CreateInfraOptions
		opt, o.infra = CreateInfraOptions(o, opts)
		if err := o.infra.SetupInfra(ctx, opt); err != nil {
			return fmt.Errorf("failed to create infra: %w", err)
		}
	}

	return nil
}

func (o *CreateOptions) ApplyPlatformSpecifics(cluster *hyperv1.HostedCluster) error {
	cluster.Spec.Platform = hyperv1.PlatformSpec{
		Type: hyperv1.PowerVSPlatform,
		PowerVS: &hyperv1.PowerVSPlatformSpec{
			AccountID:         o.infra.AccountID,
			ResourceGroup:     o.ResourceGroup,
			Region:            o.Region,
			Zone:              o.Zone,
			CISInstanceCRN:    o.infra.CISCRN,
			ServiceInstanceID: o.CloudInstanceID,
			Subnet: &hyperv1.PowerVSResourceReference{
				Name: &o.infra.DHCPSubnet,
				ID:   &o.infra.DHCPSubnetID,
			},
			VPC: &hyperv1.PowerVSVPC{
				Name:   o.VPC,
				Region: o.VPCRegion,
				Subnet: o.infra.VPCSubnetName,
			},
			KubeCloudControllerCreds:        corev1.LocalObjectReference{Name: o.infra.Secrets.KubeCloudControllerManager.Name},
			NodePoolManagementCreds:         corev1.LocalObjectReference{Name: o.infra.Secrets.NodePoolManagement.Name},
			IngressOperatorCloudCreds:       corev1.LocalObjectReference{Name: o.infra.Secrets.IngressOperator.Name},
			StorageOperatorCloudCreds:       corev1.LocalObjectReference{Name: o.infra.Secrets.StorageOperator.Name},
			ImageRegistryOperatorCloudCreds: corev1.LocalObjectReference{Name: o.infra.Secrets.ImageRegistryOperator.Name},
		},
	}
	cluster.Spec.DNS = hyperv1.DNSSpec{
		BaseDomain:    o.infra.BaseDomain,
		PublicZoneID:  o.infra.CISDomainID,
		PrivateZoneID: o.infra.CISDomainID,
	}
	cluster.Spec.InfraID = o.infra.ID
	cluster.Spec.Networking.MachineNetwork = []hyperv1.MachineNetworkEntry{{CIDR: *ipnet.MustParseCIDR(defaultCIDRBlock)}}
	cluster.Spec.Services = core.GetIngressServicePublishingStrategyMapping(cluster.Spec.Networking.NetworkType, o.externalDNSDomain != "")
	return nil
}

func (o *CreateOptions) GenerateNodePools(constructor core.DefaultNodePoolConstructor) []*hyperv1.NodePool {
	nodePool := constructor(hyperv1.PowerVSPlatform, "")
	if nodePool.Spec.Management.UpgradeType == "" {
		nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace
	}
	nodePool.Spec.Platform.PowerVS = &hyperv1.PowerVSNodePoolPlatform{
		SystemType:    o.SysType,
		ProcessorType: o.ProcType,
		Processors:    intstr.FromString(o.Processors),
		MemoryGiB:     o.Memory,
	}
	return []*hyperv1.NodePool{nodePool}
}

func (o *CreateOptions) GenerateResources() ([]client.Object, error) {
	var objects []client.Object
	for _, secret := range []*corev1.Secret{
		o.infra.Secrets.KubeCloudControllerManager,
		o.infra.Secrets.NodePoolManagement,
		o.infra.Secrets.IngressOperator,
		o.infra.Secrets.StorageOperator,
		o.infra.Secrets.ImageRegistryOperator,
	} {
		if secret != nil {
			objects = append(objects, secret)
		}
	}

	return objects, nil
}

var _ core.Platform = (*CreateOptions)(nil)

func NewCreateCommand(opts *core.CreateOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:          "powervs",
		Short:        "Creates basic functional HostedCluster resources on PowerVS PowerVS",
		SilenceUsage: true,
	}

	powerVsOpts := DefaultOptions()
	BindOptions(powerVsOpts, cmd.Flags())
	cmd.MarkFlagRequired("resource-group")
	cmd.MarkPersistentFlagRequired("pull-secret")

	// these options are only for development and testing purpose,
	// can use these to reuse the existing resources, so hiding it.
	cmd.Flags().MarkHidden("cloud-instance-id")
	cmd.Flags().MarkHidden("cloud-connection")
	cmd.Flags().MarkHidden("vpc")
	cmd.Flags().MarkHidden("transit-gateway")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		ctx, cancel := context.WithCancel(context.Background())
		sigs := make(chan os.Signal, 1)
		signal.Notify(sigs, syscall.SIGINT)
		go func() {
			<-sigs
			cancel()
		}()

		if err := CreateCluster(ctx, opts, powerVsOpts); err != nil {
			opts.Log.Error(err, "Failed to create cluster")
			os.Exit(1)
		}
	}

	return cmd
}

func CreateCluster(ctx context.Context, opts *core.CreateOptions, powerVsOpts *CreateOptions) error {
	var err error
	if err = core.Validate(ctx, opts); err != nil {
		return err
	}
	return core.CreateCluster(ctx, opts, powerVsOpts)
}

func CreateInfraOptions(powerVSOpts *CreateOptions, opts *core.CreateOptions) (*powervsinfra.CreateInfraOptions, *powervsinfra.Infra) {
	return &powervsinfra.CreateInfraOptions{
			Name:                   opts.Name,
			Namespace:              opts.Namespace,
			BaseDomain:             opts.BaseDomain,
			ResourceGroup:          powerVSOpts.ResourceGroup,
			InfraID:                opts.InfraID,
			OutputFile:             opts.InfrastructureJSON,
			Region:                 powerVSOpts.Region,
			Zone:                   powerVSOpts.Zone,
			CloudInstanceID:        powerVSOpts.CloudInstanceID,
			CloudConnection:        powerVSOpts.CloudConnection,
			VPCRegion:              powerVSOpts.VPCRegion,
			VPC:                    powerVSOpts.VPC,
			Debug:                  powerVSOpts.Debug,
			RecreateSecrets:        powerVSOpts.RecreateSecrets,
			PER:                    powerVSOpts.PER,
			TransitGatewayLocation: powerVSOpts.TransitGatewayLocation,
			TransitGateway:         powerVSOpts.TransitGateway,
		}, &powervsinfra.Infra{
			ID:            opts.InfraID,
			BaseDomain:    opts.BaseDomain,
			ResourceGroup: powerVSOpts.ResourceGroup,
			Region:        powerVSOpts.Region,
			Zone:          powerVSOpts.Zone,
			VPCRegion:     powerVSOpts.VPCRegion,
		}
}
