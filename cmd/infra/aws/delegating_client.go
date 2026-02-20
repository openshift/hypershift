package aws

import (
	"context"
	"fmt"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/support/awsapi"

	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	configv2 "github.com/aws/aws-sdk-go-v2/config"
	route53v2 "github.com/aws/aws-sdk-go-v2/service/route53"
	s3v2 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/elb"
	"github.com/aws/aws-sdk-go/service/elb/elbiface"
	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/aws/aws-sdk-go/service/elbv2/elbv2iface"
	"github.com/aws/aws-sdk-go/service/sqs"
	"github.com/aws/aws-sdk-go/service/sqs/sqsiface"
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
	awsConfigv2 := awsutil.NewConfigV2()
	awsEbsCsiDriverControllerSession, err := session.NewSessionWithOptions(session.Options{SharedConfigFiles: []string{awsEbsCsiDriverControllerCredentialsFile}})
	if err != nil {
		return nil, fmt.Errorf("error creating new AWS session for awsEbsCsiDriverController: %w", err)
	}
	awsEbsCsiDriverControllerSession.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/hypershift",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io hypershift", "aws-ebs-csi-driver-controller"),
	})
	awsEbsCsiDriverController := &awsEbsCsiDriverControllerClientDelegate{
		ec2Client: ec2.New(awsEbsCsiDriverControllerSession, awsConfig),
	}
	cloudControllerSession, err := session.NewSessionWithOptions(session.Options{SharedConfigFiles: []string{cloudControllerCredentialsFile}})
	if err != nil {
		return nil, fmt.Errorf("error creating new AWS session for cloudController: %w", err)
	}
	cloudControllerSession.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/hypershift",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io hypershift", "cloud-controller"),
	})
	cloudController := &cloudControllerClientDelegate{
		ec2Client:   ec2.New(cloudControllerSession, awsConfig),
		elbClient:   elb.New(cloudControllerSession, awsConfig),
		elbv2Client: elbv2.New(cloudControllerSession, awsConfig),
	}
	cloudNetworkConfigControllerSession, err := session.NewSessionWithOptions(session.Options{SharedConfigFiles: []string{cloudNetworkConfigControllerCredentialsFile}})
	if err != nil {
		return nil, fmt.Errorf("error creating new AWS session for cloudNetworkConfigController: %w", err)
	}
	cloudNetworkConfigControllerSession.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/hypershift",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io hypershift", "cloud-network-config-controller"),
	})
	cloudNetworkConfigController := &cloudNetworkConfigControllerClientDelegate{
		ec2Client: ec2.New(cloudNetworkConfigControllerSession, awsConfig),
	}
	controlPlaneOperatorSession, err := session.NewSessionWithOptions(session.Options{SharedConfigFiles: []string{controlPlaneOperatorCredentialsFile}})
	if err != nil {
		return nil, fmt.Errorf("error creating new AWS session for controlPlaneOperator: %w", err)
	}
	controlPlaneOperatorSession.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/hypershift",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io hypershift", "control-plane-operator"),
	})
	controlPlaneOperatorCfg, err := configv2.LoadDefaultConfig(ctx,
		configv2.WithSharedConfigFiles([]string{controlPlaneOperatorCredentialsFile}),
		configv2.WithAPIOptions([]func(*middleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", "control-plane-operator"),
		}))
	if err != nil {
		return nil, fmt.Errorf("error loading AWS config for controlPlaneOperator: %w", err)
	}
	controlPlaneOperator := &controlPlaneOperatorClientDelegate{
		ec2Client: ec2.New(controlPlaneOperatorSession, awsConfig),
		route53Client: route53v2.NewFromConfig(controlPlaneOperatorCfg, func(o *route53v2.Options) {
			o.Retryer = awsConfigv2()
		}),
	}
	nodePoolSession, err := session.NewSessionWithOptions(session.Options{SharedConfigFiles: []string{nodePoolCredentialsFile}})
	if err != nil {
		return nil, fmt.Errorf("error creating new AWS session for nodePool: %w", err)
	}
	nodePoolSession.Handlers.Build.PushBackNamed(request.NamedHandler{
		Name: "openshift.io/hypershift",
		Fn:   request.MakeAddToUserAgentHandler("openshift.io hypershift", "node-pool"),
	})
	nodePool := &nodePoolClientDelegate{
		ec2Client: ec2.New(nodePoolSession, awsConfig),
		sqsClient: sqs.New(nodePoolSession, awsConfig),
	}
	openshiftImageRegistryCfg, err := configv2.LoadDefaultConfig(ctx,
		configv2.WithSharedConfigFiles([]string{openshiftImageRegistryCredentialsFile}),
		configv2.WithAPIOptions([]func(*middleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", "openshift-image-registry"),
		}))
	if err != nil {
		return nil, fmt.Errorf("error loading AWS config for openshiftImageRegistry: %w", err)
	}
	openshiftImageRegistry := &openshiftImageRegistryClientDelegate{
		s3Client: s3v2.NewFromConfig(openshiftImageRegistryCfg, func(o *s3v2.Options) {
			o.Retryer = awsConfigv2()
		}),
	}
	return &DelegatingClient{
		EC2API: &ec2Client{
			EC2API:                       nil,
			awsEbsCsiDriverController:    awsEbsCsiDriverController,
			cloudController:              cloudController,
			cloudNetworkConfigController: cloudNetworkConfigController,
			controlPlaneOperator:         controlPlaneOperator,
			nodePool:                     nodePool,
		},
		ELBAPI: &elbClient{
			ELBAPI:          nil,
			cloudController: cloudController,
		},
		ELBV2API: &elbv2Client{
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
		SQSAPI: &sqsClient{
			SQSAPI:   nil,
			nodePool: nodePool,
		},
	}, nil
}

type awsEbsCsiDriverControllerClientDelegate struct {
	ec2Client ec2iface.EC2API
}

type cloudControllerClientDelegate struct {
	ec2Client   ec2iface.EC2API
	elbClient   elbiface.ELBAPI
	elbv2Client elbv2iface.ELBV2API
}

type cloudNetworkConfigControllerClientDelegate struct {
	ec2Client ec2iface.EC2API
}

type controlPlaneOperatorClientDelegate struct {
	ec2Client     ec2iface.EC2API
	route53Client awsapi.ROUTE53API
}

type nodePoolClientDelegate struct {
	ec2Client ec2iface.EC2API
	sqsClient sqsiface.SQSAPI
}

type openshiftImageRegistryClientDelegate struct {
	s3Client awsapi.S3API
}

// DelegatingClient embeds clients for AWS services we have privileges to use with guest cluster component roles.
type DelegatingClient struct {
	ec2iface.EC2API
	elbiface.ELBAPI
	elbv2iface.ELBV2API
	ROUTE53Client awsapi.ROUTE53API
	S3Client      awsapi.S3API
	sqsiface.SQSAPI
}

// ec2Client delegates to individual component clients for API calls we know those components will have privileges to make.
type ec2Client struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	ec2iface.EC2API

	awsEbsCsiDriverController    *awsEbsCsiDriverControllerClientDelegate
	cloudController              *cloudControllerClientDelegate
	cloudNetworkConfigController *cloudNetworkConfigControllerClientDelegate
	controlPlaneOperator         *controlPlaneOperatorClientDelegate
	nodePool                     *nodePoolClientDelegate
}

func (c *ec2Client) AttachVolumeWithContext(ctx aws.Context, input *ec2.AttachVolumeInput, opts ...request.Option) (*ec2.VolumeAttachment, error) {
	return c.awsEbsCsiDriverController.ec2Client.AttachVolumeWithContext(ctx, input, opts...)
}
func (c *ec2Client) CreateSnapshotWithContext(ctx aws.Context, input *ec2.CreateSnapshotInput, opts ...request.Option) (*ec2.Snapshot, error) {
	return c.awsEbsCsiDriverController.ec2Client.CreateSnapshotWithContext(ctx, input, opts...)
}
func (c *ec2Client) CreateTagsWithContext(ctx aws.Context, input *ec2.CreateTagsInput, opts ...request.Option) (*ec2.CreateTagsOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.CreateTagsWithContext(ctx, input, opts...)
}
func (c *ec2Client) CreateVolumeWithContext(ctx aws.Context, input *ec2.CreateVolumeInput, opts ...request.Option) (*ec2.Volume, error) {
	return c.awsEbsCsiDriverController.ec2Client.CreateVolumeWithContext(ctx, input, opts...)
}
func (c *ec2Client) DeleteSnapshotWithContext(ctx aws.Context, input *ec2.DeleteSnapshotInput, opts ...request.Option) (*ec2.DeleteSnapshotOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DeleteSnapshotWithContext(ctx, input, opts...)
}
func (c *ec2Client) DeleteTagsWithContext(ctx aws.Context, input *ec2.DeleteTagsInput, opts ...request.Option) (*ec2.DeleteTagsOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DeleteTagsWithContext(ctx, input, opts...)
}
func (c *ec2Client) DeleteVolumeWithContext(ctx aws.Context, input *ec2.DeleteVolumeInput, opts ...request.Option) (*ec2.DeleteVolumeOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DeleteVolumeWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeInstancesWithContext(ctx aws.Context, input *ec2.DescribeInstancesInput, opts ...request.Option) (*ec2.DescribeInstancesOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DescribeInstancesWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeSnapshotsWithContext(ctx aws.Context, input *ec2.DescribeSnapshotsInput, opts ...request.Option) (*ec2.DescribeSnapshotsOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DescribeSnapshotsWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeTagsWithContext(ctx aws.Context, input *ec2.DescribeTagsInput, opts ...request.Option) (*ec2.DescribeTagsOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DescribeTagsWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeVolumesWithContext(ctx aws.Context, input *ec2.DescribeVolumesInput, opts ...request.Option) (*ec2.DescribeVolumesOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DescribeVolumesWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeVolumesModificationsWithContext(ctx aws.Context, input *ec2.DescribeVolumesModificationsInput, opts ...request.Option) (*ec2.DescribeVolumesModificationsOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.DescribeVolumesModificationsWithContext(ctx, input, opts...)
}
func (c *ec2Client) DetachVolumeWithContext(ctx aws.Context, input *ec2.DetachVolumeInput, opts ...request.Option) (*ec2.VolumeAttachment, error) {
	return c.awsEbsCsiDriverController.ec2Client.DetachVolumeWithContext(ctx, input, opts...)
}
func (c *ec2Client) ModifyVolumeWithContext(ctx aws.Context, input *ec2.ModifyVolumeInput, opts ...request.Option) (*ec2.ModifyVolumeOutput, error) {
	return c.awsEbsCsiDriverController.ec2Client.ModifyVolumeWithContext(ctx, input, opts...)
}

func (c *ec2Client) AuthorizeSecurityGroupIngressWithContext(ctx aws.Context, input *ec2.AuthorizeSecurityGroupIngressInput, opts ...request.Option) (*ec2.AuthorizeSecurityGroupIngressOutput, error) {
	return c.cloudController.ec2Client.AuthorizeSecurityGroupIngressWithContext(ctx, input, opts...)
}
func (c *ec2Client) CreateRouteWithContext(ctx aws.Context, input *ec2.CreateRouteInput, opts ...request.Option) (*ec2.CreateRouteOutput, error) {
	return c.cloudController.ec2Client.CreateRouteWithContext(ctx, input, opts...)
}
func (c *ec2Client) CreateSecurityGroupWithContext(ctx aws.Context, input *ec2.CreateSecurityGroupInput, opts ...request.Option) (*ec2.CreateSecurityGroupOutput, error) {
	return c.cloudController.ec2Client.CreateSecurityGroupWithContext(ctx, input, opts...)
}
func (c *ec2Client) DeleteRouteWithContext(ctx aws.Context, input *ec2.DeleteRouteInput, opts ...request.Option) (*ec2.DeleteRouteOutput, error) {
	return c.cloudController.ec2Client.DeleteRouteWithContext(ctx, input, opts...)
}
func (c *ec2Client) DeleteSecurityGroupWithContext(ctx aws.Context, input *ec2.DeleteSecurityGroupInput, opts ...request.Option) (*ec2.DeleteSecurityGroupOutput, error) {
	return c.cloudController.ec2Client.DeleteSecurityGroupWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeAvailabilityZonesWithContext(ctx aws.Context, input *ec2.DescribeAvailabilityZonesInput, opts ...request.Option) (*ec2.DescribeAvailabilityZonesOutput, error) {
	return c.cloudController.ec2Client.DescribeAvailabilityZonesWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeImagesWithContext(ctx aws.Context, input *ec2.DescribeImagesInput, opts ...request.Option) (*ec2.DescribeImagesOutput, error) {
	return c.cloudController.ec2Client.DescribeImagesWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeRegionsWithContext(ctx aws.Context, input *ec2.DescribeRegionsInput, opts ...request.Option) (*ec2.DescribeRegionsOutput, error) {
	return c.cloudController.ec2Client.DescribeRegionsWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeRouteTablesWithContext(ctx aws.Context, input *ec2.DescribeRouteTablesInput, opts ...request.Option) (*ec2.DescribeRouteTablesOutput, error) {
	return c.cloudController.ec2Client.DescribeRouteTablesWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeSecurityGroupsWithContext(ctx aws.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
	return c.cloudController.ec2Client.DescribeSecurityGroupsWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeSubnetsWithContext(ctx aws.Context, input *ec2.DescribeSubnetsInput, opts ...request.Option) (*ec2.DescribeSubnetsOutput, error) {
	return c.cloudController.ec2Client.DescribeSubnetsWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeVpcsWithContext(ctx aws.Context, input *ec2.DescribeVpcsInput, opts ...request.Option) (*ec2.DescribeVpcsOutput, error) {
	return c.cloudController.ec2Client.DescribeVpcsWithContext(ctx, input, opts...)
}
func (c *ec2Client) ModifyInstanceAttributeWithContext(ctx aws.Context, input *ec2.ModifyInstanceAttributeInput, opts ...request.Option) (*ec2.ModifyInstanceAttributeOutput, error) {
	return c.cloudController.ec2Client.ModifyInstanceAttributeWithContext(ctx, input, opts...)
}
func (c *ec2Client) RevokeSecurityGroupIngressWithContext(ctx aws.Context, input *ec2.RevokeSecurityGroupIngressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error) {
	return c.cloudController.ec2Client.RevokeSecurityGroupIngressWithContext(ctx, input, opts...)
}

func (c *ec2Client) AssignIpv6AddressesWithContext(ctx aws.Context, input *ec2.AssignIpv6AddressesInput, opts ...request.Option) (*ec2.AssignIpv6AddressesOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.AssignIpv6AddressesWithContext(ctx, input, opts...)
}
func (c *ec2Client) AssignPrivateIpAddressesWithContext(ctx aws.Context, input *ec2.AssignPrivateIpAddressesInput, opts ...request.Option) (*ec2.AssignPrivateIpAddressesOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.AssignPrivateIpAddressesWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeInstanceStatusWithContext(ctx aws.Context, input *ec2.DescribeInstanceStatusInput, opts ...request.Option) (*ec2.DescribeInstanceStatusOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.DescribeInstanceStatusWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeInstanceTypesWithContext(ctx aws.Context, input *ec2.DescribeInstanceTypesInput, opts ...request.Option) (*ec2.DescribeInstanceTypesOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.DescribeInstanceTypesWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeNetworkInterfacesWithContext(ctx aws.Context, input *ec2.DescribeNetworkInterfacesInput, opts ...request.Option) (*ec2.DescribeNetworkInterfacesOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.DescribeNetworkInterfacesWithContext(ctx, input, opts...)
}
func (c *ec2Client) UnassignIpv6AddressesWithContext(ctx aws.Context, input *ec2.UnassignIpv6AddressesInput, opts ...request.Option) (*ec2.UnassignIpv6AddressesOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.UnassignIpv6AddressesWithContext(ctx, input, opts...)
}
func (c *ec2Client) UnassignPrivateIpAddressesWithContext(ctx aws.Context, input *ec2.UnassignPrivateIpAddressesInput, opts ...request.Option) (*ec2.UnassignPrivateIpAddressesOutput, error) {
	return c.cloudNetworkConfigController.ec2Client.UnassignPrivateIpAddressesWithContext(ctx, input, opts...)
}

func (c *ec2Client) AuthorizeSecurityGroupEgressWithContext(ctx aws.Context, input *ec2.AuthorizeSecurityGroupEgressInput, opts ...request.Option) (*ec2.AuthorizeSecurityGroupEgressOutput, error) {
	return c.controlPlaneOperator.ec2Client.AuthorizeSecurityGroupEgressWithContext(ctx, input, opts...)
}
func (c *ec2Client) CreateVpcEndpointWithContext(ctx aws.Context, input *ec2.CreateVpcEndpointInput, opts ...request.Option) (*ec2.CreateVpcEndpointOutput, error) {
	return c.controlPlaneOperator.ec2Client.CreateVpcEndpointWithContext(ctx, input, opts...)
}
func (c *ec2Client) DeleteVpcEndpointsWithContext(ctx aws.Context, input *ec2.DeleteVpcEndpointsInput, opts ...request.Option) (*ec2.DeleteVpcEndpointsOutput, error) {
	return c.controlPlaneOperator.ec2Client.DeleteVpcEndpointsWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeVpcEndpointsWithContext(ctx aws.Context, input *ec2.DescribeVpcEndpointsInput, opts ...request.Option) (*ec2.DescribeVpcEndpointsOutput, error) {
	return c.controlPlaneOperator.ec2Client.DescribeVpcEndpointsWithContext(ctx, input, opts...)
}
func (c *ec2Client) ModifyVpcEndpointWithContext(ctx aws.Context, input *ec2.ModifyVpcEndpointInput, opts ...request.Option) (*ec2.ModifyVpcEndpointOutput, error) {
	return c.controlPlaneOperator.ec2Client.ModifyVpcEndpointWithContext(ctx, input, opts...)
}
func (c *ec2Client) RevokeSecurityGroupEgressWithContext(ctx aws.Context, input *ec2.RevokeSecurityGroupEgressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupEgressOutput, error) {
	return c.controlPlaneOperator.ec2Client.RevokeSecurityGroupEgressWithContext(ctx, input, opts...)
}

func (c *ec2Client) AssociateRouteTableWithContext(ctx aws.Context, input *ec2.AssociateRouteTableInput, opts ...request.Option) (*ec2.AssociateRouteTableOutput, error) {
	return c.nodePool.ec2Client.AssociateRouteTableWithContext(ctx, input, opts...)
}
func (c *ec2Client) AttachInternetGatewayWithContext(ctx aws.Context, input *ec2.AttachInternetGatewayInput, opts ...request.Option) (*ec2.AttachInternetGatewayOutput, error) {
	return c.nodePool.ec2Client.AttachInternetGatewayWithContext(ctx, input, opts...)
}
func (c *ec2Client) CreateInternetGatewayWithContext(ctx aws.Context, input *ec2.CreateInternetGatewayInput, opts ...request.Option) (*ec2.CreateInternetGatewayOutput, error) {
	return c.nodePool.ec2Client.CreateInternetGatewayWithContext(ctx, input, opts...)
}
func (c *ec2Client) CreateLaunchTemplateWithContext(ctx aws.Context, input *ec2.CreateLaunchTemplateInput, opts ...request.Option) (*ec2.CreateLaunchTemplateOutput, error) {
	return c.nodePool.ec2Client.CreateLaunchTemplateWithContext(ctx, input, opts...)
}
func (c *ec2Client) CreateLaunchTemplateVersionWithContext(ctx aws.Context, input *ec2.CreateLaunchTemplateVersionInput, opts ...request.Option) (*ec2.CreateLaunchTemplateVersionOutput, error) {
	return c.nodePool.ec2Client.CreateLaunchTemplateVersionWithContext(ctx, input, opts...)
}
func (c *ec2Client) CreateNatGatewayWithContext(ctx aws.Context, input *ec2.CreateNatGatewayInput, opts ...request.Option) (*ec2.CreateNatGatewayOutput, error) {
	return c.nodePool.ec2Client.CreateNatGatewayWithContext(ctx, input, opts...)
}
func (c *ec2Client) CreateRouteTableWithContext(ctx aws.Context, input *ec2.CreateRouteTableInput, opts ...request.Option) (*ec2.CreateRouteTableOutput, error) {
	return c.nodePool.ec2Client.CreateRouteTableWithContext(ctx, input, opts...)
}
func (c *ec2Client) CreateSubnetWithContext(ctx aws.Context, input *ec2.CreateSubnetInput, opts ...request.Option) (*ec2.CreateSubnetOutput, error) {
	return c.nodePool.ec2Client.CreateSubnetWithContext(ctx, input, opts...)
}
func (c *ec2Client) DeleteInternetGatewayWithContext(ctx aws.Context, input *ec2.DeleteInternetGatewayInput, opts ...request.Option) (*ec2.DeleteInternetGatewayOutput, error) {
	return c.nodePool.ec2Client.DeleteInternetGatewayWithContext(ctx, input, opts...)
}
func (c *ec2Client) DeleteLaunchTemplateWithContext(ctx aws.Context, input *ec2.DeleteLaunchTemplateInput, opts ...request.Option) (*ec2.DeleteLaunchTemplateOutput, error) {
	return c.nodePool.ec2Client.DeleteLaunchTemplateWithContext(ctx, input, opts...)
}
func (c *ec2Client) DeleteLaunchTemplateVersionsWithContext(ctx aws.Context, input *ec2.DeleteLaunchTemplateVersionsInput, opts ...request.Option) (*ec2.DeleteLaunchTemplateVersionsOutput, error) {
	return c.nodePool.ec2Client.DeleteLaunchTemplateVersionsWithContext(ctx, input, opts...)
}
func (c *ec2Client) DeleteNatGatewayWithContext(ctx aws.Context, input *ec2.DeleteNatGatewayInput, opts ...request.Option) (*ec2.DeleteNatGatewayOutput, error) {
	return c.nodePool.ec2Client.DeleteNatGatewayWithContext(ctx, input, opts...)
}
func (c *ec2Client) DeleteRouteTableWithContext(ctx aws.Context, input *ec2.DeleteRouteTableInput, opts ...request.Option) (*ec2.DeleteRouteTableOutput, error) {
	return c.nodePool.ec2Client.DeleteRouteTableWithContext(ctx, input, opts...)
}
func (c *ec2Client) DeleteSubnetWithContext(ctx aws.Context, input *ec2.DeleteSubnetInput, opts ...request.Option) (*ec2.DeleteSubnetOutput, error) {
	return c.nodePool.ec2Client.DeleteSubnetWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeAccountAttributesWithContext(ctx aws.Context, input *ec2.DescribeAccountAttributesInput, opts ...request.Option) (*ec2.DescribeAccountAttributesOutput, error) {
	return c.nodePool.ec2Client.DescribeAccountAttributesWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeAddressesWithContext(ctx aws.Context, input *ec2.DescribeAddressesInput, opts ...request.Option) (*ec2.DescribeAddressesOutput, error) {
	return c.nodePool.ec2Client.DescribeAddressesWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeDhcpOptionsWithContext(ctx aws.Context, input *ec2.DescribeDhcpOptionsInput, opts ...request.Option) (*ec2.DescribeDhcpOptionsOutput, error) {
	return c.nodePool.ec2Client.DescribeDhcpOptionsWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeInternetGatewaysWithContext(ctx aws.Context, input *ec2.DescribeInternetGatewaysInput, opts ...request.Option) (*ec2.DescribeInternetGatewaysOutput, error) {
	return c.nodePool.ec2Client.DescribeInternetGatewaysWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeLaunchTemplateVersionsWithContext(ctx aws.Context, input *ec2.DescribeLaunchTemplateVersionsInput, opts ...request.Option) (*ec2.DescribeLaunchTemplateVersionsOutput, error) {
	return c.nodePool.ec2Client.DescribeLaunchTemplateVersionsWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeLaunchTemplatesWithContext(ctx aws.Context, input *ec2.DescribeLaunchTemplatesInput, opts ...request.Option) (*ec2.DescribeLaunchTemplatesOutput, error) {
	return c.nodePool.ec2Client.DescribeLaunchTemplatesWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeNatGatewaysWithContext(ctx aws.Context, input *ec2.DescribeNatGatewaysInput, opts ...request.Option) (*ec2.DescribeNatGatewaysOutput, error) {
	return c.nodePool.ec2Client.DescribeNatGatewaysWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeNetworkInterfaceAttributeWithContext(ctx aws.Context, input *ec2.DescribeNetworkInterfaceAttributeInput, opts ...request.Option) (*ec2.DescribeNetworkInterfaceAttributeOutput, error) {
	return c.nodePool.ec2Client.DescribeNetworkInterfaceAttributeWithContext(ctx, input, opts...)
}
func (c *ec2Client) DescribeVpcAttributeWithContext(ctx aws.Context, input *ec2.DescribeVpcAttributeInput, opts ...request.Option) (*ec2.DescribeVpcAttributeOutput, error) {
	return c.nodePool.ec2Client.DescribeVpcAttributeWithContext(ctx, input, opts...)
}
func (c *ec2Client) DetachInternetGatewayWithContext(ctx aws.Context, input *ec2.DetachInternetGatewayInput, opts ...request.Option) (*ec2.DetachInternetGatewayOutput, error) {
	return c.nodePool.ec2Client.DetachInternetGatewayWithContext(ctx, input, opts...)
}
func (c *ec2Client) DisassociateAddressWithContext(ctx aws.Context, input *ec2.DisassociateAddressInput, opts ...request.Option) (*ec2.DisassociateAddressOutput, error) {
	return c.nodePool.ec2Client.DisassociateAddressWithContext(ctx, input, opts...)
}
func (c *ec2Client) DisassociateRouteTableWithContext(ctx aws.Context, input *ec2.DisassociateRouteTableInput, opts ...request.Option) (*ec2.DisassociateRouteTableOutput, error) {
	return c.nodePool.ec2Client.DisassociateRouteTableWithContext(ctx, input, opts...)
}
func (c *ec2Client) ModifyNetworkInterfaceAttributeWithContext(ctx aws.Context, input *ec2.ModifyNetworkInterfaceAttributeInput, opts ...request.Option) (*ec2.ModifyNetworkInterfaceAttributeOutput, error) {
	return c.nodePool.ec2Client.ModifyNetworkInterfaceAttributeWithContext(ctx, input, opts...)
}
func (c *ec2Client) ModifySubnetAttributeWithContext(ctx aws.Context, input *ec2.ModifySubnetAttributeInput, opts ...request.Option) (*ec2.ModifySubnetAttributeOutput, error) {
	return c.nodePool.ec2Client.ModifySubnetAttributeWithContext(ctx, input, opts...)
}
func (c *ec2Client) RunInstancesWithContext(ctx aws.Context, input *ec2.RunInstancesInput, opts ...request.Option) (*ec2.Reservation, error) {
	return c.nodePool.ec2Client.RunInstancesWithContext(ctx, input, opts...)
}
func (c *ec2Client) TerminateInstancesWithContext(ctx aws.Context, input *ec2.TerminateInstancesInput, opts ...request.Option) (*ec2.TerminateInstancesOutput, error) {
	return c.nodePool.ec2Client.TerminateInstancesWithContext(ctx, input, opts...)
}

// elbClient delegates to individual component clients for API calls we know those components will have privileges to make.
type elbClient struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	elbiface.ELBAPI

	cloudController *cloudControllerClientDelegate
}

func (c *elbClient) AddTagsWithContext(ctx aws.Context, input *elb.AddTagsInput, opts ...request.Option) (*elb.AddTagsOutput, error) {
	return c.cloudController.elbClient.AddTagsWithContext(ctx, input, opts...)
}
func (c *elbClient) ApplySecurityGroupsToLoadBalancerWithContext(ctx aws.Context, input *elb.ApplySecurityGroupsToLoadBalancerInput, opts ...request.Option) (*elb.ApplySecurityGroupsToLoadBalancerOutput, error) {
	return c.cloudController.elbClient.ApplySecurityGroupsToLoadBalancerWithContext(ctx, input, opts...)
}
func (c *elbClient) AttachLoadBalancerToSubnetsWithContext(ctx aws.Context, input *elb.AttachLoadBalancerToSubnetsInput, opts ...request.Option) (*elb.AttachLoadBalancerToSubnetsOutput, error) {
	return c.cloudController.elbClient.AttachLoadBalancerToSubnetsWithContext(ctx, input, opts...)
}
func (c *elbClient) ConfigureHealthCheckWithContext(ctx aws.Context, input *elb.ConfigureHealthCheckInput, opts ...request.Option) (*elb.ConfigureHealthCheckOutput, error) {
	return c.cloudController.elbClient.ConfigureHealthCheckWithContext(ctx, input, opts...)
}
func (c *elbClient) CreateLoadBalancerWithContext(ctx aws.Context, input *elb.CreateLoadBalancerInput, opts ...request.Option) (*elb.CreateLoadBalancerOutput, error) {
	return c.cloudController.elbClient.CreateLoadBalancerWithContext(ctx, input, opts...)
}
func (c *elbClient) CreateLoadBalancerListenersWithContext(ctx aws.Context, input *elb.CreateLoadBalancerListenersInput, opts ...request.Option) (*elb.CreateLoadBalancerListenersOutput, error) {
	return c.cloudController.elbClient.CreateLoadBalancerListenersWithContext(ctx, input, opts...)
}
func (c *elbClient) CreateLoadBalancerPolicyWithContext(ctx aws.Context, input *elb.CreateLoadBalancerPolicyInput, opts ...request.Option) (*elb.CreateLoadBalancerPolicyOutput, error) {
	return c.cloudController.elbClient.CreateLoadBalancerPolicyWithContext(ctx, input, opts...)
}
func (c *elbClient) DeleteLoadBalancerWithContext(ctx aws.Context, input *elb.DeleteLoadBalancerInput, opts ...request.Option) (*elb.DeleteLoadBalancerOutput, error) {
	return c.cloudController.elbClient.DeleteLoadBalancerWithContext(ctx, input, opts...)
}
func (c *elbClient) DeleteLoadBalancerListenersWithContext(ctx aws.Context, input *elb.DeleteLoadBalancerListenersInput, opts ...request.Option) (*elb.DeleteLoadBalancerListenersOutput, error) {
	return c.cloudController.elbClient.DeleteLoadBalancerListenersWithContext(ctx, input, opts...)
}
func (c *elbClient) DeregisterInstancesFromLoadBalancerWithContext(ctx aws.Context, input *elb.DeregisterInstancesFromLoadBalancerInput, opts ...request.Option) (*elb.DeregisterInstancesFromLoadBalancerOutput, error) {
	return c.cloudController.elbClient.DeregisterInstancesFromLoadBalancerWithContext(ctx, input, opts...)
}
func (c *elbClient) DescribeLoadBalancerAttributesWithContext(ctx aws.Context, input *elb.DescribeLoadBalancerAttributesInput, opts ...request.Option) (*elb.DescribeLoadBalancerAttributesOutput, error) {
	return c.cloudController.elbClient.DescribeLoadBalancerAttributesWithContext(ctx, input, opts...)
}
func (c *elbClient) DescribeLoadBalancerPoliciesWithContext(ctx aws.Context, input *elb.DescribeLoadBalancerPoliciesInput, opts ...request.Option) (*elb.DescribeLoadBalancerPoliciesOutput, error) {
	return c.cloudController.elbClient.DescribeLoadBalancerPoliciesWithContext(ctx, input, opts...)
}
func (c *elbClient) DescribeLoadBalancersWithContext(ctx aws.Context, input *elb.DescribeLoadBalancersInput, opts ...request.Option) (*elb.DescribeLoadBalancersOutput, error) {
	return c.cloudController.elbClient.DescribeLoadBalancersWithContext(ctx, input, opts...)
}
func (c *elbClient) DetachLoadBalancerFromSubnetsWithContext(ctx aws.Context, input *elb.DetachLoadBalancerFromSubnetsInput, opts ...request.Option) (*elb.DetachLoadBalancerFromSubnetsOutput, error) {
	return c.cloudController.elbClient.DetachLoadBalancerFromSubnetsWithContext(ctx, input, opts...)
}
func (c *elbClient) ModifyLoadBalancerAttributesWithContext(ctx aws.Context, input *elb.ModifyLoadBalancerAttributesInput, opts ...request.Option) (*elb.ModifyLoadBalancerAttributesOutput, error) {
	return c.cloudController.elbClient.ModifyLoadBalancerAttributesWithContext(ctx, input, opts...)
}
func (c *elbClient) RegisterInstancesWithLoadBalancerWithContext(ctx aws.Context, input *elb.RegisterInstancesWithLoadBalancerInput, opts ...request.Option) (*elb.RegisterInstancesWithLoadBalancerOutput, error) {
	return c.cloudController.elbClient.RegisterInstancesWithLoadBalancerWithContext(ctx, input, opts...)
}
func (c *elbClient) SetLoadBalancerPoliciesForBackendServerWithContext(ctx aws.Context, input *elb.SetLoadBalancerPoliciesForBackendServerInput, opts ...request.Option) (*elb.SetLoadBalancerPoliciesForBackendServerOutput, error) {
	return c.cloudController.elbClient.SetLoadBalancerPoliciesForBackendServerWithContext(ctx, input, opts...)
}
func (c *elbClient) SetLoadBalancerPoliciesOfListenerWithContext(ctx aws.Context, input *elb.SetLoadBalancerPoliciesOfListenerInput, opts ...request.Option) (*elb.SetLoadBalancerPoliciesOfListenerOutput, error) {
	return c.cloudController.elbClient.SetLoadBalancerPoliciesOfListenerWithContext(ctx, input, opts...)
}

// elbv2Client delegates to individual component clients for API calls we know those components will have privileges to make.
type elbv2Client struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	elbv2iface.ELBV2API

	cloudController *cloudControllerClientDelegate
}

func (c *elbv2Client) AddTagsWithContext(ctx aws.Context, input *elbv2.AddTagsInput, opts ...request.Option) (*elbv2.AddTagsOutput, error) {
	return c.cloudController.elbv2Client.AddTagsWithContext(ctx, input, opts...)
}
func (c *elbv2Client) CreateListenerWithContext(ctx aws.Context, input *elbv2.CreateListenerInput, opts ...request.Option) (*elbv2.CreateListenerOutput, error) {
	return c.cloudController.elbv2Client.CreateListenerWithContext(ctx, input, opts...)
}
func (c *elbv2Client) CreateLoadBalancerWithContext(ctx aws.Context, input *elbv2.CreateLoadBalancerInput, opts ...request.Option) (*elbv2.CreateLoadBalancerOutput, error) {
	return c.cloudController.elbv2Client.CreateLoadBalancerWithContext(ctx, input, opts...)
}
func (c *elbv2Client) CreateTargetGroupWithContext(ctx aws.Context, input *elbv2.CreateTargetGroupInput, opts ...request.Option) (*elbv2.CreateTargetGroupOutput, error) {
	return c.cloudController.elbv2Client.CreateTargetGroupWithContext(ctx, input, opts...)
}
func (c *elbv2Client) DeleteListenerWithContext(ctx aws.Context, input *elbv2.DeleteListenerInput, opts ...request.Option) (*elbv2.DeleteListenerOutput, error) {
	return c.cloudController.elbv2Client.DeleteListenerWithContext(ctx, input, opts...)
}
func (c *elbv2Client) DeleteLoadBalancerWithContext(ctx aws.Context, input *elbv2.DeleteLoadBalancerInput, opts ...request.Option) (*elbv2.DeleteLoadBalancerOutput, error) {
	return c.cloudController.elbv2Client.DeleteLoadBalancerWithContext(ctx, input, opts...)
}
func (c *elbv2Client) DeleteTargetGroupWithContext(ctx aws.Context, input *elbv2.DeleteTargetGroupInput, opts ...request.Option) (*elbv2.DeleteTargetGroupOutput, error) {
	return c.cloudController.elbv2Client.DeleteTargetGroupWithContext(ctx, input, opts...)
}
func (c *elbv2Client) DeregisterTargetsWithContext(ctx aws.Context, input *elbv2.DeregisterTargetsInput, opts ...request.Option) (*elbv2.DeregisterTargetsOutput, error) {
	return c.cloudController.elbv2Client.DeregisterTargetsWithContext(ctx, input, opts...)
}
func (c *elbv2Client) DescribeListenersWithContext(ctx aws.Context, input *elbv2.DescribeListenersInput, opts ...request.Option) (*elbv2.DescribeListenersOutput, error) {
	return c.cloudController.elbv2Client.DescribeListenersWithContext(ctx, input, opts...)
}
func (c *elbv2Client) DescribeLoadBalancerAttributesWithContext(ctx aws.Context, input *elbv2.DescribeLoadBalancerAttributesInput, opts ...request.Option) (*elbv2.DescribeLoadBalancerAttributesOutput, error) {
	return c.cloudController.elbv2Client.DescribeLoadBalancerAttributesWithContext(ctx, input, opts...)
}
func (c *elbv2Client) DescribeLoadBalancersWithContext(ctx aws.Context, input *elbv2.DescribeLoadBalancersInput, opts ...request.Option) (*elbv2.DescribeLoadBalancersOutput, error) {
	return c.cloudController.elbv2Client.DescribeLoadBalancersWithContext(ctx, input, opts...)
}
func (c *elbv2Client) DescribeTargetGroupAttributesWithContext(ctx aws.Context, input *elbv2.DescribeTargetGroupAttributesInput, opts ...request.Option) (*elbv2.DescribeTargetGroupAttributesOutput, error) {
	return c.cloudController.elbv2Client.DescribeTargetGroupAttributesWithContext(ctx, input, opts...)
}
func (c *elbv2Client) DescribeTargetGroupsWithContext(ctx aws.Context, input *elbv2.DescribeTargetGroupsInput, opts ...request.Option) (*elbv2.DescribeTargetGroupsOutput, error) {
	return c.cloudController.elbv2Client.DescribeTargetGroupsWithContext(ctx, input, opts...)
}
func (c *elbv2Client) DescribeTargetHealthWithContext(ctx aws.Context, input *elbv2.DescribeTargetHealthInput, opts ...request.Option) (*elbv2.DescribeTargetHealthOutput, error) {
	return c.cloudController.elbv2Client.DescribeTargetHealthWithContext(ctx, input, opts...)
}
func (c *elbv2Client) ModifyListenerWithContext(ctx aws.Context, input *elbv2.ModifyListenerInput, opts ...request.Option) (*elbv2.ModifyListenerOutput, error) {
	return c.cloudController.elbv2Client.ModifyListenerWithContext(ctx, input, opts...)
}
func (c *elbv2Client) ModifyLoadBalancerAttributesWithContext(ctx aws.Context, input *elbv2.ModifyLoadBalancerAttributesInput, opts ...request.Option) (*elbv2.ModifyLoadBalancerAttributesOutput, error) {
	return c.cloudController.elbv2Client.ModifyLoadBalancerAttributesWithContext(ctx, input, opts...)
}
func (c *elbv2Client) ModifyTargetGroupWithContext(ctx aws.Context, input *elbv2.ModifyTargetGroupInput, opts ...request.Option) (*elbv2.ModifyTargetGroupOutput, error) {
	return c.cloudController.elbv2Client.ModifyTargetGroupWithContext(ctx, input, opts...)
}
func (c *elbv2Client) ModifyTargetGroupAttributesWithContext(ctx aws.Context, input *elbv2.ModifyTargetGroupAttributesInput, opts ...request.Option) (*elbv2.ModifyTargetGroupAttributesOutput, error) {
	return c.cloudController.elbv2Client.ModifyTargetGroupAttributesWithContext(ctx, input, opts...)
}
func (c *elbv2Client) RegisterTargetsWithContext(ctx aws.Context, input *elbv2.RegisterTargetsInput, opts ...request.Option) (*elbv2.RegisterTargetsOutput, error) {
	return c.cloudController.elbv2Client.RegisterTargetsWithContext(ctx, input, opts...)
}

// route53Client delegates to individual component clients for API calls we know those components will have privileges to make.
type route53Client struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	awsapi.ROUTE53API

	controlPlaneOperator *controlPlaneOperatorClientDelegate
}

func (c *route53Client) ChangeResourceRecordSets(ctx context.Context, input *route53v2.ChangeResourceRecordSetsInput, optFns ...func(*route53v2.Options)) (*route53v2.ChangeResourceRecordSetsOutput, error) {
	return c.controlPlaneOperator.route53Client.ChangeResourceRecordSets(ctx, input, optFns...)
}
func (c *route53Client) ListHostedZones(ctx context.Context, input *route53v2.ListHostedZonesInput, optFns ...func(*route53v2.Options)) (*route53v2.ListHostedZonesOutput, error) {
	return c.controlPlaneOperator.route53Client.ListHostedZones(ctx, input, optFns...)
}
func (c *route53Client) ListResourceRecordSets(ctx context.Context, input *route53v2.ListResourceRecordSetsInput, optFns ...func(*route53v2.Options)) (*route53v2.ListResourceRecordSetsOutput, error) {
	return c.controlPlaneOperator.route53Client.ListResourceRecordSets(ctx, input, optFns...)
}

// s3Client delegates to individual component clients for API calls we know those components will have privileges to make.
type s3Client struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	awsapi.S3API

	openshiftImageRegistry *openshiftImageRegistryClientDelegate
}

func (c *s3Client) AbortMultipartUpload(ctx context.Context, input *s3v2.AbortMultipartUploadInput, optFns ...func(*s3v2.Options)) (*s3v2.AbortMultipartUploadOutput, error) {
	return c.openshiftImageRegistry.s3Client.AbortMultipartUpload(ctx, input, optFns...)
}
func (c *s3Client) CreateBucket(ctx context.Context, input *s3v2.CreateBucketInput, optFns ...func(*s3v2.Options)) (*s3v2.CreateBucketOutput, error) {
	return c.openshiftImageRegistry.s3Client.CreateBucket(ctx, input, optFns...)
}
func (c *s3Client) DeleteBucket(ctx context.Context, input *s3v2.DeleteBucketInput, optFns ...func(*s3v2.Options)) (*s3v2.DeleteBucketOutput, error) {
	return c.openshiftImageRegistry.s3Client.DeleteBucket(ctx, input, optFns...)
}
func (c *s3Client) DeleteObject(ctx context.Context, input *s3v2.DeleteObjectInput, optFns ...func(*s3v2.Options)) (*s3v2.DeleteObjectOutput, error) {
	return c.openshiftImageRegistry.s3Client.DeleteObject(ctx, input, optFns...)
}
func (c *s3Client) DeleteObjects(ctx context.Context, input *s3v2.DeleteObjectsInput, optFns ...func(*s3v2.Options)) (*s3v2.DeleteObjectsOutput, error) {
	return c.openshiftImageRegistry.s3Client.DeleteObjects(ctx, input, optFns...)
}
func (c *s3Client) GetBucketEncryption(ctx context.Context, input *s3v2.GetBucketEncryptionInput, optFns ...func(*s3v2.Options)) (*s3v2.GetBucketEncryptionOutput, error) {
	return c.openshiftImageRegistry.s3Client.GetBucketEncryption(ctx, input, optFns...)
}
func (c *s3Client) GetBucketLifecycleConfiguration(ctx context.Context, input *s3v2.GetBucketLifecycleConfigurationInput, optFns ...func(*s3v2.Options)) (*s3v2.GetBucketLifecycleConfigurationOutput, error) {
	return c.openshiftImageRegistry.s3Client.GetBucketLifecycleConfiguration(ctx, input, optFns...)
}
func (c *s3Client) GetBucketLocation(ctx context.Context, input *s3v2.GetBucketLocationInput, optFns ...func(*s3v2.Options)) (*s3v2.GetBucketLocationOutput, error) {
	return c.openshiftImageRegistry.s3Client.GetBucketLocation(ctx, input, optFns...)
}
func (c *s3Client) GetBucketTagging(ctx context.Context, input *s3v2.GetBucketTaggingInput, optFns ...func(*s3v2.Options)) (*s3v2.GetBucketTaggingOutput, error) {
	return c.openshiftImageRegistry.s3Client.GetBucketTagging(ctx, input, optFns...)
}
func (c *s3Client) GetObject(ctx context.Context, input *s3v2.GetObjectInput, optFns ...func(*s3v2.Options)) (*s3v2.GetObjectOutput, error) {
	return c.openshiftImageRegistry.s3Client.GetObject(ctx, input, optFns...)
}
func (c *s3Client) GetPublicAccessBlock(ctx context.Context, input *s3v2.GetPublicAccessBlockInput, optFns ...func(*s3v2.Options)) (*s3v2.GetPublicAccessBlockOutput, error) {
	return c.openshiftImageRegistry.s3Client.GetPublicAccessBlock(ctx, input, optFns...)
}
func (c *s3Client) ListBuckets(ctx context.Context, input *s3v2.ListBucketsInput, optFns ...func(*s3v2.Options)) (*s3v2.ListBucketsOutput, error) {
	return c.openshiftImageRegistry.s3Client.ListBuckets(ctx, input, optFns...)
}
func (c *s3Client) ListMultipartUploads(ctx context.Context, input *s3v2.ListMultipartUploadsInput, optFns ...func(*s3v2.Options)) (*s3v2.ListMultipartUploadsOutput, error) {
	return c.openshiftImageRegistry.s3Client.ListMultipartUploads(ctx, input, optFns...)
}
func (c *s3Client) ListObjectsV2(ctx context.Context, input *s3v2.ListObjectsV2Input, optFns ...func(*s3v2.Options)) (*s3v2.ListObjectsV2Output, error) {
	return c.openshiftImageRegistry.s3Client.ListObjectsV2(ctx, input, optFns...)
}
func (c *s3Client) PutBucketEncryption(ctx context.Context, input *s3v2.PutBucketEncryptionInput, optFns ...func(*s3v2.Options)) (*s3v2.PutBucketEncryptionOutput, error) {
	return c.openshiftImageRegistry.s3Client.PutBucketEncryption(ctx, input, optFns...)
}
func (c *s3Client) PutBucketLifecycleConfiguration(ctx context.Context, input *s3v2.PutBucketLifecycleConfigurationInput, optFns ...func(*s3v2.Options)) (*s3v2.PutBucketLifecycleConfigurationOutput, error) {
	return c.openshiftImageRegistry.s3Client.PutBucketLifecycleConfiguration(ctx, input, optFns...)
}
func (c *s3Client) PutBucketTagging(ctx context.Context, input *s3v2.PutBucketTaggingInput, optFns ...func(*s3v2.Options)) (*s3v2.PutBucketTaggingOutput, error) {
	return c.openshiftImageRegistry.s3Client.PutBucketTagging(ctx, input, optFns...)
}
func (c *s3Client) PutObject(ctx context.Context, input *s3v2.PutObjectInput, optFns ...func(*s3v2.Options)) (*s3v2.PutObjectOutput, error) {
	return c.openshiftImageRegistry.s3Client.PutObject(ctx, input, optFns...)
}
func (c *s3Client) PutPublicAccessBlock(ctx context.Context, input *s3v2.PutPublicAccessBlockInput, optFns ...func(*s3v2.Options)) (*s3v2.PutPublicAccessBlockOutput, error) {
	return c.openshiftImageRegistry.s3Client.PutPublicAccessBlock(ctx, input, optFns...)
}

// sqsClient delegates to individual component clients for API calls we know those components will have privileges to make.
type sqsClient struct {
	// embedding this fulfills the interface and falls back to a panic for APIs we don't have privileges for
	sqsiface.SQSAPI

	nodePool *nodePoolClientDelegate
}

func (c *sqsClient) DeleteMessageWithContext(ctx aws.Context, input *sqs.DeleteMessageInput, opts ...request.Option) (*sqs.DeleteMessageOutput, error) {
	return c.nodePool.sqsClient.DeleteMessageWithContext(ctx, input, opts...)
}
func (c *sqsClient) ReceiveMessageWithContext(ctx aws.Context, input *sqs.ReceiveMessageInput, opts ...request.Option) (*sqs.ReceiveMessageOutput, error) {
	return c.nodePool.sqsClient.ReceiveMessageWithContext(ctx, input, opts...)
}
