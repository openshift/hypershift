package aws

import (
	"context"
	"fmt"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/support/awsapi"

	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	configv2 "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	"github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
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
	cloudControllerCfg, err := configv2.LoadDefaultConfig(ctx,
		configv2.WithSharedConfigFiles([]string{cloudControllerCredentialsFile}),
		configv2.WithAPIOptions([]func(*middleware.Stack) error{
			awsmiddleware.AddUserAgentKeyValue("openshift.io hypershift", "cloud-controller"),
		}))
	if err != nil {
		return nil, fmt.Errorf("error loading AWS config for cloudController: %w", err)
	}
	cloudController := &cloudControllerClientDelegate{
		ec2Client: ec2.New(cloudControllerSession, awsConfig),
		elasticloadbalancingClient: elasticloadbalancing.NewFromConfig(cloudControllerCfg, func(o *elasticloadbalancing.Options) {
			o.Retryer = awsConfigv2()
		}),
		elasticloadbalancingv2Client: elasticloadbalancingv2.NewFromConfig(cloudControllerCfg, func(o *elasticloadbalancingv2.Options) {
			o.Retryer = awsConfigv2()
		}),
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
		route53Client: route53.NewFromConfig(controlPlaneOperatorCfg, func(o *route53.Options) {
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
		s3Client: s3.NewFromConfig(openshiftImageRegistryCfg, func(o *s3.Options) {
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
	ec2Client                    ec2iface.EC2API
	elasticloadbalancingClient   awsapi.ELBAPI
	elasticloadbalancingv2Client awsapi.ELBV2API
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
	ELBClient     awsapi.ELBAPI
	ELBV2Client   awsapi.ELBV2API
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
	sqsiface.SQSAPI

	nodePool *nodePoolClientDelegate
}

func (c *sqsClient) DeleteMessageWithContext(ctx aws.Context, input *sqs.DeleteMessageInput, opts ...request.Option) (*sqs.DeleteMessageOutput, error) {
	return c.nodePool.sqsClient.DeleteMessageWithContext(ctx, input, opts...)
}
func (c *sqsClient) ReceiveMessageWithContext(ctx aws.Context, input *sqs.ReceiveMessageInput, opts ...request.Option) (*sqs.ReceiveMessageOutput, error) {
	return c.nodePool.sqsClient.ReceiveMessageWithContext(ctx, input, opts...)
}
