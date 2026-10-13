package aws

import (
	"context"
	"fmt"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/support/awsapi"

	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/smithy-go/middleware"
)

// NewDelegatingClient creates a new set of AWS service clients that delegate individual calls to the right credentials.
func NewDelegatingClient(
	ctx context.Context,
	awsEbsCsiDriverControllerCredentialsFile string,
	cloudControllerCredentialsFile string,
	cloudNetworkConfigControllerCredentialsFile string,
	controlPlaneOperatorCredentialsFile string,
	nodePoolCredentialsFile string,
	openshiftImageRegistryCredentialsFile string,
) (*DelegatingClient, error) {
	awsConfig := awsutil.NewConfig()
	awsEbsCsiDriverControllerCfg, err := config.LoadDefaultConfig(ctx,
		config.WithSharedConfigFiles([]string{awsEbsCsiDriverControllerCredentialsFile}),
		config.WithAPIOptions([]func(*middleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", "aws-ebs-csi-driver-controller"),
		}))
	if err != nil {
		return nil, fmt.Errorf("error loading AWS config for awsEbsCsiDriverController: %w", err)
	}
	awsEbsCsiDriverController := &awsEbsCsiDriverControllerClientDelegate{
		ec2Client: ec2.NewFromConfig(awsEbsCsiDriverControllerCfg, func(o *ec2.Options) {
			o.Retryer = awsConfig()
		}),
	}
	cloudControllerCfg, err := config.LoadDefaultConfig(ctx,
		config.WithSharedConfigFiles([]string{cloudControllerCredentialsFile}),
		config.WithAPIOptions([]func(*middleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", "cloud-controller"),
		}))
	if err != nil {
		return nil, fmt.Errorf("error loading AWS config for cloudController: %w", err)
	}
	cloudController := &cloudControllerClientDelegate{
		ec2Client: ec2.NewFromConfig(cloudControllerCfg, func(o *ec2.Options) {
			o.Retryer = awsConfig()
		}),
		elasticloadbalancingClient: elasticloadbalancing.NewFromConfig(cloudControllerCfg, func(o *elasticloadbalancing.Options) {
			o.Retryer = awsConfig()
		}),
		elasticloadbalancingv2Client: elasticloadbalancingv2.NewFromConfig(cloudControllerCfg, func(o *elasticloadbalancingv2.Options) {
			o.Retryer = awsConfig()
		}),
	}
	cloudNetworkConfigControllerCfg, err := config.LoadDefaultConfig(ctx,
		config.WithSharedConfigFiles([]string{cloudNetworkConfigControllerCredentialsFile}),
		config.WithAPIOptions([]func(*middleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", "cloud-network-config-controller"),
		}))
	if err != nil {
		return nil, fmt.Errorf("error loading AWS config for cloudNetworkConfigController: %w", err)
	}
	cloudNetworkConfigController := &cloudNetworkConfigControllerClientDelegate{
		ec2Client: ec2.NewFromConfig(cloudNetworkConfigControllerCfg, func(o *ec2.Options) {
			o.Retryer = awsConfig()
		}),
	}
	controlPlaneOperatorCfg, err := config.LoadDefaultConfig(ctx,
		config.WithSharedConfigFiles([]string{controlPlaneOperatorCredentialsFile}),
		config.WithAPIOptions([]func(*middleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", "control-plane-operator"),
		}))
	if err != nil {
		return nil, fmt.Errorf("error loading AWS config for controlPlaneOperator: %w", err)
	}
	controlPlaneOperator := &controlPlaneOperatorClientDelegate{
		ec2Client: ec2.NewFromConfig(controlPlaneOperatorCfg, func(o *ec2.Options) {
			o.Retryer = awsConfig()
		}),
		route53Client: route53.NewFromConfig(controlPlaneOperatorCfg, func(o *route53.Options) {
			o.Retryer = awsConfig()
		}),
	}
	nodePoolCfg, err := config.LoadDefaultConfig(ctx,
		config.WithSharedConfigFiles([]string{nodePoolCredentialsFile}),
		config.WithAPIOptions([]func(*middleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", "node-pool"),
		}))
	if err != nil {
		return nil, fmt.Errorf("error loading AWS config for nodePool: %w", err)
	}
	nodePool := &nodePoolClientDelegate{
		ec2Client: ec2.NewFromConfig(nodePoolCfg, func(o *ec2.Options) {
			o.Retryer = awsConfig()
		}),
		sqsClient: sqs.NewFromConfig(nodePoolCfg, func(o *sqs.Options) {
			o.Retryer = awsConfig()
		}),
	}
	openshiftImageRegistryCfg, err := config.LoadDefaultConfig(ctx,
		config.WithSharedConfigFiles([]string{openshiftImageRegistryCredentialsFile}),
		config.WithAPIOptions([]func(*middleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", "openshift-image-registry"),
		}))
	if err != nil {
		return nil, fmt.Errorf("error loading AWS config for openshiftImageRegistry: %w", err)
	}
	openshiftImageRegistry := &openshiftImageRegistryClientDelegate{
		s3Client: s3.NewFromConfig(openshiftImageRegistryCfg, func(o *s3.Options) {
			o.Retryer = awsConfig()
		}),
	}
	return &DelegatingClient{
		EC2Client: &ec2Client{
			EC2API:                       nil,
			awsEbsCsiDriverController:    awsEbsCsiDriverController,
			cloudController:              cloudController,
			cloudNetworkConfigController: cloudNetworkConfigController,
			controlPlaneOperator:         controlPlaneOperator,
			nodePool:                     nodePool,
		},
		ELBClient: &elasticloadbalancingClient{
			ELBAPI:          nil,
			cloudController: cloudController,
		},
		ELBV2Client: &elasticloadbalancingv2Client{
			ELBV2API:        nil,
			cloudController: cloudController,
		},
		ROUTE53Client: &route53Client{
			ROUTE53API:           nil,
			controlPlaneOperator: controlPlaneOperator,
		},
		S3Client: &s3Client{
			S3API:                  nil,
			openshiftImageRegistry: openshiftImageRegistry,
		},
		SQSClient: &sqsClient{
			SQSAPI:   nil,
			nodePool: nodePool,
		},
	}, nil
}

type awsEbsCsiDriverControllerClientDelegate struct {
	ec2Client awsapi.EC2API
}

type cloudControllerClientDelegate struct {
	ec2Client                    awsapi.EC2API
	elasticloadbalancingClient   awsapi.ELBAPI
	elasticloadbalancingv2Client awsapi.ELBV2API
}

type cloudNetworkConfigControllerClientDelegate struct {
	ec2Client awsapi.EC2API
}

type controlPlaneOperatorClientDelegate struct {
	ec2Client     awsapi.EC2API
	route53Client awsapi.ROUTE53API
}

type nodePoolClientDelegate struct {
	ec2Client awsapi.EC2API
	sqsClient awsapi.SQSAPI
}

type openshiftImageRegistryClientDelegate struct {
	s3Client awsapi.S3API
}

// DelegatingClient embeds clients for AWS services we have privileges to use with guest cluster component roles.
type DelegatingClient struct {
	EC2Client     awsapi.EC2API
	ELBClient     awsapi.ELBAPI
	ELBV2Client   awsapi.ELBV2API
	ROUTE53Client awsapi.ROUTE53API
	S3Client      awsapi.S3API
	SQSClient     awsapi.SQSAPI
}

// ec2Client delegates to individual component clients for API calls we know those components will have privileges to make.
type ec2Client struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	awsapi.EC2API

	awsEbsCsiDriverController    *awsEbsCsiDriverControllerClientDelegate
	cloudController              *cloudControllerClientDelegate
	cloudNetworkConfigController *cloudNetworkConfigControllerClientDelegate
	controlPlaneOperator         *controlPlaneOperatorClientDelegate
	nodePool                     *nodePoolClientDelegate
}

func (c *ec2Client) AttachVolume(ctx context.Context, input *ec2.AttachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.AttachVolumeOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.AttachVolume(ctx, input, optFns...)
}
func (c *ec2Client) CreateSnapshot(ctx context.Context, input *ec2.CreateSnapshotInput, optFns ...func(*ec2.Options)) (*ec2.CreateSnapshotOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.CreateSnapshot(ctx, input, optFns...)
}
func (c *ec2Client) CreateTags(ctx context.Context, input *ec2.CreateTagsInput, optFns ...func(*ec2.Options)) (*ec2.CreateTagsOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.CreateTags(ctx, input, optFns...)
}
func (c *ec2Client) CreateVolume(ctx context.Context, input *ec2.CreateVolumeInput, optFns ...func(*ec2.Options)) (*ec2.CreateVolumeOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.CreateVolume(ctx, input, optFns...)
}
func (c *ec2Client) DeleteSnapshot(ctx context.Context, input *ec2.DeleteSnapshotInput, optFns ...func(*ec2.Options)) (*ec2.DeleteSnapshotOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DeleteSnapshot(ctx, input, optFns...)
}
func (c *ec2Client) DeleteTags(ctx context.Context, input *ec2.DeleteTagsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteTagsOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DeleteTags(ctx, input, optFns...)
}
func (c *ec2Client) DeleteVolume(ctx context.Context, input *ec2.DeleteVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DeleteVolumeOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DeleteVolume(ctx, input, optFns...)
}
func (c *ec2Client) DescribeInstances(ctx context.Context, input *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DescribeInstances(ctx, input, optFns...)
}
func (c *ec2Client) DescribeSnapshots(ctx context.Context, input *ec2.DescribeSnapshotsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSnapshotsOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DescribeSnapshots(ctx, input, optFns...)
}
func (c *ec2Client) DescribeTags(ctx context.Context, input *ec2.DescribeTagsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeTagsOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DescribeTags(ctx, input, optFns...)
}
func (c *ec2Client) DescribeVolumes(ctx context.Context, input *ec2.DescribeVolumesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DescribeVolumes(ctx, input, optFns...)
}
func (c *ec2Client) DescribeVolumesModifications(ctx context.Context, input *ec2.DescribeVolumesModificationsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVolumesModificationsOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DescribeVolumesModifications(ctx, input, optFns...)
}
func (c *ec2Client) DetachVolume(ctx context.Context, input *ec2.DetachVolumeInput, optFns ...func(*ec2.Options)) (*ec2.DetachVolumeOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DetachVolume(ctx, input, optFns...)
}
func (c *ec2Client) ModifyVolume(ctx context.Context, input *ec2.ModifyVolumeInput, optFns ...func(*ec2.Options)) (*ec2.ModifyVolumeOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.ModifyVolume(ctx, input, optFns...)
}

func (c *ec2Client) AuthorizeSecurityGroupIngress(ctx context.Context, input *ec2.AuthorizeSecurityGroupIngressInput, optFns ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	return c.cloudController.ec2Client.AuthorizeSecurityGroupIngress(ctx, input, optFns...)
}
func (c *ec2Client) CreateRoute(ctx context.Context, input *ec2.CreateRouteInput, optFns ...func(*ec2.Options)) (*ec2.CreateRouteOutput, error) {
	return c.cloudController.ec2Client.CreateRoute(ctx, input, optFns...)
}
func (c *ec2Client) CreateSecurityGroup(ctx context.Context, input *ec2.CreateSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.CreateSecurityGroupOutput, error) {
	return c.cloudController.ec2Client.CreateSecurityGroup(ctx, input, optFns...)
}
func (c *ec2Client) DeleteRoute(ctx context.Context, input *ec2.DeleteRouteInput, optFns ...func(*ec2.Options)) (*ec2.DeleteRouteOutput, error) {
	return c.cloudController.ec2Client.DeleteRoute(ctx, input, optFns...)
}
func (c *ec2Client) DeleteSecurityGroup(ctx context.Context, input *ec2.DeleteSecurityGroupInput, optFns ...func(*ec2.Options)) (*ec2.DeleteSecurityGroupOutput, error) {
	return c.cloudController.ec2Client.DeleteSecurityGroup(ctx, input, optFns...)
}
func (c *ec2Client) DescribeAvailabilityZones(ctx context.Context, input *ec2.DescribeAvailabilityZonesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAvailabilityZonesOutput, error) {
	return c.cloudController.ec2Client.DescribeAvailabilityZones(ctx, input, optFns...)
}
func (c *ec2Client) DescribeImages(ctx context.Context, input *ec2.DescribeImagesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeImagesOutput, error) {
	return c.cloudController.ec2Client.DescribeImages(ctx, input, optFns...)
}
func (c *ec2Client) DescribeRegions(ctx context.Context, input *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error) {
	return c.cloudController.ec2Client.DescribeRegions(ctx, input, optFns...)
}
func (c *ec2Client) DescribeRouteTables(ctx context.Context, input *ec2.DescribeRouteTablesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRouteTablesOutput, error) {
	return c.cloudController.ec2Client.DescribeRouteTables(ctx, input, optFns...)
}
func (c *ec2Client) DescribeSecurityGroups(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSecurityGroupsOutput, error) {
	return c.cloudController.ec2Client.DescribeSecurityGroups(ctx, input, optFns...)
}
func (c *ec2Client) DescribeSubnets(ctx context.Context, input *ec2.DescribeSubnetsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeSubnetsOutput, error) {
	return c.cloudController.ec2Client.DescribeSubnets(ctx, input, optFns...)
}
func (c *ec2Client) DescribeVpcs(ctx context.Context, input *ec2.DescribeVpcsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcsOutput, error) {
	return c.cloudController.ec2Client.DescribeVpcs(ctx, input, optFns...)
}
func (c *ec2Client) ModifyInstanceAttribute(ctx context.Context, input *ec2.ModifyInstanceAttributeInput, optFns ...func(*ec2.Options)) (*ec2.ModifyInstanceAttributeOutput, error) {
	return c.cloudController.ec2Client.ModifyInstanceAttribute(ctx, input, optFns...)
}
func (c *ec2Client) RevokeSecurityGroupIngress(ctx context.Context, input *ec2.RevokeSecurityGroupIngressInput, optFns ...func(*ec2.Options)) (*ec2.RevokeSecurityGroupIngressOutput, error) {
	return c.cloudController.ec2Client.RevokeSecurityGroupIngress(ctx, input, optFns...)
}

func (c *ec2Client) AssignIpv6Addresses(ctx context.Context, input *ec2.AssignIpv6AddressesInput, optFns ...func(*ec2.Options)) (*ec2.AssignIpv6AddressesOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.AssignIpv6Addresses(ctx, input, optFns...)
}
func (c *ec2Client) AssignPrivateIpAddresses(ctx context.Context, input *ec2.AssignPrivateIpAddressesInput, optFns ...func(*ec2.Options)) (*ec2.AssignPrivateIpAddressesOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.AssignPrivateIpAddresses(ctx, input, optFns...)
}
func (c *ec2Client) DescribeInstanceStatus(ctx context.Context, input *ec2.DescribeInstanceStatusInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceStatusOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.DescribeInstanceStatus(ctx, input, optFns...)
}
func (c *ec2Client) DescribeInstanceTypes(ctx context.Context, input *ec2.DescribeInstanceTypesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstanceTypesOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.DescribeInstanceTypes(ctx, input, optFns...)
}
func (c *ec2Client) DescribeNetworkInterfaces(ctx context.Context, input *ec2.DescribeNetworkInterfacesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfacesOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.DescribeNetworkInterfaces(ctx, input, optFns...)
}
func (c *ec2Client) UnassignIpv6Addresses(ctx context.Context, input *ec2.UnassignIpv6AddressesInput, optFns ...func(*ec2.Options)) (*ec2.UnassignIpv6AddressesOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.UnassignIpv6Addresses(ctx, input, optFns...)
}
func (c *ec2Client) UnassignPrivateIpAddresses(ctx context.Context, input *ec2.UnassignPrivateIpAddressesInput, optFns ...func(*ec2.Options)) (*ec2.UnassignPrivateIpAddressesOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.UnassignPrivateIpAddresses(ctx, input, optFns...)
}

func (c *ec2Client) AuthorizeSecurityGroupEgress(ctx context.Context, input *ec2.AuthorizeSecurityGroupEgressInput, optFns ...func(*ec2.Options)) (*ec2.AuthorizeSecurityGroupEgressOutput, error) {
	return c.controlPlaneOperator.ec2Client.AuthorizeSecurityGroupEgress(ctx, input, optFns...)
}
func (c *ec2Client) CreateVpcEndpoint(ctx context.Context, input *ec2.CreateVpcEndpointInput, optFns ...func(*ec2.Options)) (*ec2.CreateVpcEndpointOutput, error) {
	return c.controlPlaneOperator.ec2Client.CreateVpcEndpoint(ctx, input, optFns...)
}
func (c *ec2Client) DeleteVpcEndpoints(ctx context.Context, input *ec2.DeleteVpcEndpointsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteVpcEndpointsOutput, error) {
	return c.controlPlaneOperator.ec2Client.DeleteVpcEndpoints(ctx, input, optFns...)
}
func (c *ec2Client) DescribeVpcEndpoints(ctx context.Context, input *ec2.DescribeVpcEndpointsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcEndpointsOutput, error) {
	return c.controlPlaneOperator.ec2Client.DescribeVpcEndpoints(ctx, input, optFns...)
}
func (c *ec2Client) ModifyVpcEndpoint(ctx context.Context, input *ec2.ModifyVpcEndpointInput, optFns ...func(*ec2.Options)) (*ec2.ModifyVpcEndpointOutput, error) {
	return c.controlPlaneOperator.ec2Client.ModifyVpcEndpoint(ctx, input, optFns...)
}
func (c *ec2Client) RevokeSecurityGroupEgress(ctx context.Context, input *ec2.RevokeSecurityGroupEgressInput, optFns ...func(*ec2.Options)) (*ec2.RevokeSecurityGroupEgressOutput, error) {
	return c.controlPlaneOperator.ec2Client.RevokeSecurityGroupEgress(ctx, input, optFns...)
}

func (c *ec2Client) AssociateRouteTable(ctx context.Context, input *ec2.AssociateRouteTableInput, optFns ...func(*ec2.Options)) (*ec2.AssociateRouteTableOutput, error) {
	return c.nodePool.ec2Client.AssociateRouteTable(ctx, input, optFns...)
}
func (c *ec2Client) AttachInternetGateway(ctx context.Context, input *ec2.AttachInternetGatewayInput, optFns ...func(*ec2.Options)) (*ec2.AttachInternetGatewayOutput, error) {
	return c.nodePool.ec2Client.AttachInternetGateway(ctx, input, optFns...)
}
func (c *ec2Client) CreateInternetGateway(ctx context.Context, input *ec2.CreateInternetGatewayInput, optFns ...func(*ec2.Options)) (*ec2.CreateInternetGatewayOutput, error) {
	return c.nodePool.ec2Client.CreateInternetGateway(ctx, input, optFns...)
}
func (c *ec2Client) CreateLaunchTemplate(ctx context.Context, input *ec2.CreateLaunchTemplateInput, optFns ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateOutput, error) {
	return c.nodePool.ec2Client.CreateLaunchTemplate(ctx, input, optFns...)
}
func (c *ec2Client) CreateLaunchTemplateVersion(ctx context.Context, input *ec2.CreateLaunchTemplateVersionInput, optFns ...func(*ec2.Options)) (*ec2.CreateLaunchTemplateVersionOutput, error) {
	return c.nodePool.ec2Client.CreateLaunchTemplateVersion(ctx, input, optFns...)
}
func (c *ec2Client) CreateNatGateway(ctx context.Context, input *ec2.CreateNatGatewayInput, optFns ...func(*ec2.Options)) (*ec2.CreateNatGatewayOutput, error) {
	return c.nodePool.ec2Client.CreateNatGateway(ctx, input, optFns...)
}
func (c *ec2Client) CreateRouteTable(ctx context.Context, input *ec2.CreateRouteTableInput, optFns ...func(*ec2.Options)) (*ec2.CreateRouteTableOutput, error) {
	return c.nodePool.ec2Client.CreateRouteTable(ctx, input, optFns...)
}
func (c *ec2Client) CreateSubnet(ctx context.Context, input *ec2.CreateSubnetInput, optFns ...func(*ec2.Options)) (*ec2.CreateSubnetOutput, error) {
	return c.nodePool.ec2Client.CreateSubnet(ctx, input, optFns...)
}
func (c *ec2Client) DeleteInternetGateway(ctx context.Context, input *ec2.DeleteInternetGatewayInput, optFns ...func(*ec2.Options)) (*ec2.DeleteInternetGatewayOutput, error) {
	return c.nodePool.ec2Client.DeleteInternetGateway(ctx, input, optFns...)
}
func (c *ec2Client) DeleteLaunchTemplate(ctx context.Context, input *ec2.DeleteLaunchTemplateInput, optFns ...func(*ec2.Options)) (*ec2.DeleteLaunchTemplateOutput, error) {
	return c.nodePool.ec2Client.DeleteLaunchTemplate(ctx, input, optFns...)
}
func (c *ec2Client) DeleteLaunchTemplateVersions(ctx context.Context, input *ec2.DeleteLaunchTemplateVersionsInput, optFns ...func(*ec2.Options)) (*ec2.DeleteLaunchTemplateVersionsOutput, error) {
	return c.nodePool.ec2Client.DeleteLaunchTemplateVersions(ctx, input, optFns...)
}
func (c *ec2Client) DeleteNatGateway(ctx context.Context, input *ec2.DeleteNatGatewayInput, optFns ...func(*ec2.Options)) (*ec2.DeleteNatGatewayOutput, error) {
	return c.nodePool.ec2Client.DeleteNatGateway(ctx, input, optFns...)
}
func (c *ec2Client) DeleteRouteTable(ctx context.Context, input *ec2.DeleteRouteTableInput, optFns ...func(*ec2.Options)) (*ec2.DeleteRouteTableOutput, error) {
	return c.nodePool.ec2Client.DeleteRouteTable(ctx, input, optFns...)
}
func (c *ec2Client) DeleteSubnet(ctx context.Context, input *ec2.DeleteSubnetInput, optFns ...func(*ec2.Options)) (*ec2.DeleteSubnetOutput, error) {
	return c.nodePool.ec2Client.DeleteSubnet(ctx, input, optFns...)
}
func (c *ec2Client) DescribeAccountAttributes(ctx context.Context, input *ec2.DescribeAccountAttributesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAccountAttributesOutput, error) {
	return c.nodePool.ec2Client.DescribeAccountAttributes(ctx, input, optFns...)
}
func (c *ec2Client) DescribeAddresses(ctx context.Context, input *ec2.DescribeAddressesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeAddressesOutput, error) {
	return c.nodePool.ec2Client.DescribeAddresses(ctx, input, optFns...)
}
func (c *ec2Client) DescribeDhcpOptions(ctx context.Context, input *ec2.DescribeDhcpOptionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeDhcpOptionsOutput, error) {
	return c.nodePool.ec2Client.DescribeDhcpOptions(ctx, input, optFns...)
}
func (c *ec2Client) DescribeInternetGateways(ctx context.Context, input *ec2.DescribeInternetGatewaysInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInternetGatewaysOutput, error) {
	return c.nodePool.ec2Client.DescribeInternetGateways(ctx, input, optFns...)
}
func (c *ec2Client) DescribeLaunchTemplateVersions(ctx context.Context, input *ec2.DescribeLaunchTemplateVersionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
	return c.nodePool.ec2Client.DescribeLaunchTemplateVersions(ctx, input, optFns...)
}
func (c *ec2Client) DescribeLaunchTemplates(ctx context.Context, input *ec2.DescribeLaunchTemplatesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeLaunchTemplatesOutput, error) {
	return c.nodePool.ec2Client.DescribeLaunchTemplates(ctx, input, optFns...)
}
func (c *ec2Client) DescribeNatGateways(ctx context.Context, input *ec2.DescribeNatGatewaysInput, optFns ...func(*ec2.Options)) (*ec2.DescribeNatGatewaysOutput, error) {
	return c.nodePool.ec2Client.DescribeNatGateways(ctx, input, optFns...)
}
func (c *ec2Client) DescribeNetworkInterfaceAttribute(ctx context.Context, input *ec2.DescribeNetworkInterfaceAttributeInput, optFns ...func(*ec2.Options)) (*ec2.DescribeNetworkInterfaceAttributeOutput, error) {
	return c.nodePool.ec2Client.DescribeNetworkInterfaceAttribute(ctx, input, optFns...)
}
func (c *ec2Client) DescribeVpcAttribute(ctx context.Context, input *ec2.DescribeVpcAttributeInput, optFns ...func(*ec2.Options)) (*ec2.DescribeVpcAttributeOutput, error) {
	return c.nodePool.ec2Client.DescribeVpcAttribute(ctx, input, optFns...)
}
func (c *ec2Client) DetachInternetGateway(ctx context.Context, input *ec2.DetachInternetGatewayInput, optFns ...func(*ec2.Options)) (*ec2.DetachInternetGatewayOutput, error) {
	return c.nodePool.ec2Client.DetachInternetGateway(ctx, input, optFns...)
}
func (c *ec2Client) DisassociateAddress(ctx context.Context, input *ec2.DisassociateAddressInput, optFns ...func(*ec2.Options)) (*ec2.DisassociateAddressOutput, error) {
	return c.nodePool.ec2Client.DisassociateAddress(ctx, input, optFns...)
}
func (c *ec2Client) DisassociateRouteTable(ctx context.Context, input *ec2.DisassociateRouteTableInput, optFns ...func(*ec2.Options)) (*ec2.DisassociateRouteTableOutput, error) {
	return c.nodePool.ec2Client.DisassociateRouteTable(ctx, input, optFns...)
}
func (c *ec2Client) ModifyNetworkInterfaceAttribute(ctx context.Context, input *ec2.ModifyNetworkInterfaceAttributeInput, optFns ...func(*ec2.Options)) (*ec2.ModifyNetworkInterfaceAttributeOutput, error) {
	return c.nodePool.ec2Client.ModifyNetworkInterfaceAttribute(ctx, input, optFns...)
}
func (c *ec2Client) ModifySubnetAttribute(ctx context.Context, input *ec2.ModifySubnetAttributeInput, optFns ...func(*ec2.Options)) (*ec2.ModifySubnetAttributeOutput, error) {
	return c.nodePool.ec2Client.ModifySubnetAttribute(ctx, input, optFns...)
}
func (c *ec2Client) RunInstances(ctx context.Context, input *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error) {
	return c.nodePool.ec2Client.RunInstances(ctx, input, optFns...)
}
func (c *ec2Client) TerminateInstances(ctx context.Context, input *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error) {
	return c.nodePool.ec2Client.TerminateInstances(ctx, input, optFns...)
}

// elasticloadbalancingClient delegates to individual component clients for API calls we know those components will have privileges to make.
type elasticloadbalancingClient struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	awsapi.ELBAPI

	cloudController *cloudControllerClientDelegate
}

func (c *elasticloadbalancingClient) AddTags(ctx context.Context, input *elasticloadbalancing.AddTagsInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.AddTagsOutput, error) {
	return c.cloudController.elasticloadbalancingClient.AddTags(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) ApplySecurityGroupsToLoadBalancer(ctx context.Context, input *elasticloadbalancing.ApplySecurityGroupsToLoadBalancerInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.ApplySecurityGroupsToLoadBalancerOutput, error) {
	return c.cloudController.elasticloadbalancingClient.ApplySecurityGroupsToLoadBalancer(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) AttachLoadBalancerToSubnets(ctx context.Context, input *elasticloadbalancing.AttachLoadBalancerToSubnetsInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.AttachLoadBalancerToSubnetsOutput, error) {
	return c.cloudController.elasticloadbalancingClient.AttachLoadBalancerToSubnets(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) ConfigureHealthCheck(ctx context.Context, input *elasticloadbalancing.ConfigureHealthCheckInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.ConfigureHealthCheckOutput, error) {
	return c.cloudController.elasticloadbalancingClient.ConfigureHealthCheck(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) CreateLoadBalancer(ctx context.Context, input *elasticloadbalancing.CreateLoadBalancerInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.CreateLoadBalancerOutput, error) {
	return c.cloudController.elasticloadbalancingClient.CreateLoadBalancer(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) CreateLoadBalancerListeners(ctx context.Context, input *elasticloadbalancing.CreateLoadBalancerListenersInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.CreateLoadBalancerListenersOutput, error) {
	return c.cloudController.elasticloadbalancingClient.CreateLoadBalancerListeners(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) CreateLoadBalancerPolicy(ctx context.Context, input *elasticloadbalancing.CreateLoadBalancerPolicyInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.CreateLoadBalancerPolicyOutput, error) {
	return c.cloudController.elasticloadbalancingClient.CreateLoadBalancerPolicy(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) DeleteLoadBalancer(ctx context.Context, input *elasticloadbalancing.DeleteLoadBalancerInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.DeleteLoadBalancerOutput, error) {
	return c.cloudController.elasticloadbalancingClient.DeleteLoadBalancer(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) DeleteLoadBalancerListeners(ctx context.Context, input *elasticloadbalancing.DeleteLoadBalancerListenersInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.DeleteLoadBalancerListenersOutput, error) {
	return c.cloudController.elasticloadbalancingClient.DeleteLoadBalancerListeners(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) DeregisterInstancesFromLoadBalancer(ctx context.Context, input *elasticloadbalancing.DeregisterInstancesFromLoadBalancerInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.DeregisterInstancesFromLoadBalancerOutput, error) {
	return c.cloudController.elasticloadbalancingClient.DeregisterInstancesFromLoadBalancer(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) DescribeLoadBalancerAttributes(ctx context.Context, input *elasticloadbalancing.DescribeLoadBalancerAttributesInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.DescribeLoadBalancerAttributesOutput, error) {
	return c.cloudController.elasticloadbalancingClient.DescribeLoadBalancerAttributes(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) DescribeLoadBalancerPolicies(ctx context.Context, input *elasticloadbalancing.DescribeLoadBalancerPoliciesInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.DescribeLoadBalancerPoliciesOutput, error) {
	return c.cloudController.elasticloadbalancingClient.DescribeLoadBalancerPolicies(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) DescribeLoadBalancers(ctx context.Context, input *elasticloadbalancing.DescribeLoadBalancersInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.DescribeLoadBalancersOutput, error) {
	return c.cloudController.elasticloadbalancingClient.DescribeLoadBalancers(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) DetachLoadBalancerFromSubnets(ctx context.Context, input *elasticloadbalancing.DetachLoadBalancerFromSubnetsInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.DetachLoadBalancerFromSubnetsOutput, error) {
	return c.cloudController.elasticloadbalancingClient.DetachLoadBalancerFromSubnets(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) ModifyLoadBalancerAttributes(ctx context.Context, input *elasticloadbalancing.ModifyLoadBalancerAttributesInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.ModifyLoadBalancerAttributesOutput, error) {
	return c.cloudController.elasticloadbalancingClient.ModifyLoadBalancerAttributes(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) RegisterInstancesWithLoadBalancer(ctx context.Context, input *elasticloadbalancing.RegisterInstancesWithLoadBalancerInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.RegisterInstancesWithLoadBalancerOutput, error) {
	return c.cloudController.elasticloadbalancingClient.RegisterInstancesWithLoadBalancer(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) SetLoadBalancerPoliciesForBackendServer(ctx context.Context, input *elasticloadbalancing.SetLoadBalancerPoliciesForBackendServerInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.SetLoadBalancerPoliciesForBackendServerOutput, error) {
	return c.cloudController.elasticloadbalancingClient.SetLoadBalancerPoliciesForBackendServer(ctx, input, optFns...)
}
func (c *elasticloadbalancingClient) SetLoadBalancerPoliciesOfListener(ctx context.Context, input *elasticloadbalancing.SetLoadBalancerPoliciesOfListenerInput, optFns ...func(*elasticloadbalancing.Options)) (*elasticloadbalancing.SetLoadBalancerPoliciesOfListenerOutput, error) {
	return c.cloudController.elasticloadbalancingClient.SetLoadBalancerPoliciesOfListener(ctx, input, optFns...)
}

// elasticloadbalancingv2Client delegates to individual component clients for API calls we know those components will have privileges to make.
type elasticloadbalancingv2Client struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	awsapi.ELBV2API

	cloudController *cloudControllerClientDelegate
}

func (c *elasticloadbalancingv2Client) AddTags(ctx context.Context, input *elasticloadbalancingv2.AddTagsInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.AddTagsOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.AddTags(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) CreateListener(ctx context.Context, input *elasticloadbalancingv2.CreateListenerInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.CreateListenerOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.CreateListener(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) CreateLoadBalancer(ctx context.Context, input *elasticloadbalancingv2.CreateLoadBalancerInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.CreateLoadBalancerOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.CreateLoadBalancer(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) CreateTargetGroup(ctx context.Context, input *elasticloadbalancingv2.CreateTargetGroupInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.CreateTargetGroupOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.CreateTargetGroup(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) DeleteListener(ctx context.Context, input *elasticloadbalancingv2.DeleteListenerInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DeleteListenerOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.DeleteListener(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) DeleteLoadBalancer(ctx context.Context, input *elasticloadbalancingv2.DeleteLoadBalancerInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DeleteLoadBalancerOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.DeleteLoadBalancer(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) DeleteTargetGroup(ctx context.Context, input *elasticloadbalancingv2.DeleteTargetGroupInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DeleteTargetGroupOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.DeleteTargetGroup(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) DeregisterTargets(ctx context.Context, input *elasticloadbalancingv2.DeregisterTargetsInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DeregisterTargetsOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.DeregisterTargets(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) DescribeListeners(ctx context.Context, input *elasticloadbalancingv2.DescribeListenersInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeListenersOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.DescribeListeners(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) DescribeLoadBalancerAttributes(ctx context.Context, input *elasticloadbalancingv2.DescribeLoadBalancerAttributesInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeLoadBalancerAttributesOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.DescribeLoadBalancerAttributes(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) DescribeLoadBalancers(ctx context.Context, input *elasticloadbalancingv2.DescribeLoadBalancersInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeLoadBalancersOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.DescribeLoadBalancers(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) DescribeTargetGroupAttributes(ctx context.Context, input *elasticloadbalancingv2.DescribeTargetGroupAttributesInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetGroupAttributesOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.DescribeTargetGroupAttributes(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) DescribeTargetGroups(ctx context.Context, input *elasticloadbalancingv2.DescribeTargetGroupsInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetGroupsOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.DescribeTargetGroups(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) DescribeTargetHealth(ctx context.Context, input *elasticloadbalancingv2.DescribeTargetHealthInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.DescribeTargetHealthOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.DescribeTargetHealth(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) ModifyListener(ctx context.Context, input *elasticloadbalancingv2.ModifyListenerInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.ModifyListenerOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.ModifyListener(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) ModifyLoadBalancerAttributes(ctx context.Context, input *elasticloadbalancingv2.ModifyLoadBalancerAttributesInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.ModifyLoadBalancerAttributesOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.ModifyLoadBalancerAttributes(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) ModifyTargetGroup(ctx context.Context, input *elasticloadbalancingv2.ModifyTargetGroupInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.ModifyTargetGroupOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.ModifyTargetGroup(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) ModifyTargetGroupAttributes(ctx context.Context, input *elasticloadbalancingv2.ModifyTargetGroupAttributesInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.ModifyTargetGroupAttributesOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.ModifyTargetGroupAttributes(ctx, input, optFns...)
}
func (c *elasticloadbalancingv2Client) RegisterTargets(ctx context.Context, input *elasticloadbalancingv2.RegisterTargetsInput, optFns ...func(*elasticloadbalancingv2.Options)) (*elasticloadbalancingv2.RegisterTargetsOutput, error) {
	return c.cloudController.elasticloadbalancingv2Client.RegisterTargets(ctx, input, optFns...)
}

// route53Client delegates to individual component clients for API calls we know those components will have privileges to make.
type route53Client struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	awsapi.ROUTE53API

	controlPlaneOperator *controlPlaneOperatorClientDelegate
}

func (c *route53Client) ChangeResourceRecordSets(ctx context.Context, input *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error) {
	return c.controlPlaneOperator.route53Client.ChangeResourceRecordSets(ctx, input, optFns...)
}
func (c *route53Client) ListHostedZones(ctx context.Context, input *route53.ListHostedZonesInput, optFns ...func(*route53.Options)) (*route53.ListHostedZonesOutput, error) {
	return c.controlPlaneOperator.route53Client.ListHostedZones(ctx, input, optFns...)
}
func (c *route53Client) ListResourceRecordSets(ctx context.Context, input *route53.ListResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ListResourceRecordSetsOutput, error) {
	return c.controlPlaneOperator.route53Client.ListResourceRecordSets(ctx, input, optFns...)
}

// s3Client delegates to individual component clients for API calls we know those components will have privileges to make.
type s3Client struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	awsapi.S3API

	openshiftImageRegistry *openshiftImageRegistryClientDelegate
}

func (c *s3Client) AbortMultipartUpload(ctx context.Context, input *s3.AbortMultipartUploadInput, optFns ...func(*s3.Options)) (*s3.AbortMultipartUploadOutput, error) {
	return c.openshiftImageRegistry.s3Client.AbortMultipartUpload(ctx, input, optFns...)
}
func (c *s3Client) CreateBucket(ctx context.Context, input *s3.CreateBucketInput, optFns ...func(*s3.Options)) (*s3.CreateBucketOutput, error) {
	return c.openshiftImageRegistry.s3Client.CreateBucket(ctx, input, optFns...)
}
func (c *s3Client) DeleteBucket(ctx context.Context, input *s3.DeleteBucketInput, optFns ...func(*s3.Options)) (*s3.DeleteBucketOutput, error) {
	return c.openshiftImageRegistry.s3Client.DeleteBucket(ctx, input, optFns...)
}
func (c *s3Client) DeleteObject(ctx context.Context, input *s3.DeleteObjectInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error) {
	return c.openshiftImageRegistry.s3Client.DeleteObject(ctx, input, optFns...)
}
func (c *s3Client) DeleteObjects(ctx context.Context, input *s3.DeleteObjectsInput, optFns ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
	return c.openshiftImageRegistry.s3Client.DeleteObjects(ctx, input, optFns...)
}
func (c *s3Client) GetBucketEncryption(ctx context.Context, input *s3.GetBucketEncryptionInput, optFns ...func(*s3.Options)) (*s3.GetBucketEncryptionOutput, error) {
	return c.openshiftImageRegistry.s3Client.GetBucketEncryption(ctx, input, optFns...)
}
func (c *s3Client) GetBucketLifecycleConfiguration(ctx context.Context, input *s3.GetBucketLifecycleConfigurationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLifecycleConfigurationOutput, error) {
	return c.openshiftImageRegistry.s3Client.GetBucketLifecycleConfiguration(ctx, input, optFns...)
}
func (c *s3Client) GetBucketLocation(ctx context.Context, input *s3.GetBucketLocationInput, optFns ...func(*s3.Options)) (*s3.GetBucketLocationOutput, error) {
	return c.openshiftImageRegistry.s3Client.GetBucketLocation(ctx, input, optFns...)
}
func (c *s3Client) GetBucketTagging(ctx context.Context, input *s3.GetBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.GetBucketTaggingOutput, error) {
	return c.openshiftImageRegistry.s3Client.GetBucketTagging(ctx, input, optFns...)
}
func (c *s3Client) GetObject(ctx context.Context, input *s3.GetObjectInput, optFns ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
	return c.openshiftImageRegistry.s3Client.GetObject(ctx, input, optFns...)
}
func (c *s3Client) GetPublicAccessBlock(ctx context.Context, input *s3.GetPublicAccessBlockInput, optFns ...func(*s3.Options)) (*s3.GetPublicAccessBlockOutput, error) {
	return c.openshiftImageRegistry.s3Client.GetPublicAccessBlock(ctx, input, optFns...)
}
func (c *s3Client) ListBuckets(ctx context.Context, input *s3.ListBucketsInput, optFns ...func(*s3.Options)) (*s3.ListBucketsOutput, error) {
	return c.openshiftImageRegistry.s3Client.ListBuckets(ctx, input, optFns...)
}
func (c *s3Client) ListMultipartUploads(ctx context.Context, input *s3.ListMultipartUploadsInput, optFns ...func(*s3.Options)) (*s3.ListMultipartUploadsOutput, error) {
	return c.openshiftImageRegistry.s3Client.ListMultipartUploads(ctx, input, optFns...)
}
func (c *s3Client) ListObjectsV2(ctx context.Context, input *s3.ListObjectsV2Input, optFns ...func(*s3.Options)) (*s3.ListObjectsV2Output, error) {
	return c.openshiftImageRegistry.s3Client.ListObjectsV2(ctx, input, optFns...)
}
func (c *s3Client) PutBucketEncryption(ctx context.Context, input *s3.PutBucketEncryptionInput, optFns ...func(*s3.Options)) (*s3.PutBucketEncryptionOutput, error) {
	return c.openshiftImageRegistry.s3Client.PutBucketEncryption(ctx, input, optFns...)
}
func (c *s3Client) PutBucketLifecycleConfiguration(ctx context.Context, input *s3.PutBucketLifecycleConfigurationInput, optFns ...func(*s3.Options)) (*s3.PutBucketLifecycleConfigurationOutput, error) {
	return c.openshiftImageRegistry.s3Client.PutBucketLifecycleConfiguration(ctx, input, optFns...)
}
func (c *s3Client) PutBucketTagging(ctx context.Context, input *s3.PutBucketTaggingInput, optFns ...func(*s3.Options)) (*s3.PutBucketTaggingOutput, error) {
	return c.openshiftImageRegistry.s3Client.PutBucketTagging(ctx, input, optFns...)
}
func (c *s3Client) PutObject(ctx context.Context, input *s3.PutObjectInput, optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error) {
	return c.openshiftImageRegistry.s3Client.PutObject(ctx, input, optFns...)
}
func (c *s3Client) PutPublicAccessBlock(ctx context.Context, input *s3.PutPublicAccessBlockInput, optFns ...func(*s3.Options)) (*s3.PutPublicAccessBlockOutput, error) {
	return c.openshiftImageRegistry.s3Client.PutPublicAccessBlock(ctx, input, optFns...)
}

// sqsClient delegates to individual component clients for API calls we know those components will have privileges to make.
type sqsClient struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	awsapi.SQSAPI

	nodePool *nodePoolClientDelegate
}

func (c *sqsClient) DeleteMessage(ctx context.Context, input *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	return c.nodePool.sqsClient.DeleteMessage(ctx, input, optFns...)
}
func (c *sqsClient) ReceiveMessage(ctx context.Context, input *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	return c.nodePool.sqsClient.ReceiveMessage(ctx, input, optFns...)
}
