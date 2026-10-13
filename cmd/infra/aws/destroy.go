package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/ram"
	ramtypes "github.com/aws/aws-sdk-go-v2/service/ram/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type DestroyInfraOptions struct {
	Region              string
	InfraID             string
	AWSCredentialsOpts  *DelegatedAWSCredentialOptions
	Name                string
	BaseDomain          string
	BaseDomainPrefix    string
	RedactBaseDomain    bool
	AwsInfraGracePeriod time.Duration
	Log                 logr.Logger

	CredentialsSecretData *util.CredentialsSecretData

	AWSEbsCsiDriverControllerCredentialsFile    string
	CloudControllerCredentialsFile              string
	CloudNetworkConfigControllerCredentialsFile string
	ControlPlaneOperatorCredentialsFile         string
	NodePoolCredentialsFile                     string
	OpenshiftImageRegistryCredentialsFile       string

	VPCOwnerCredentialsOpts      awsutil.AWSCredentialsOptions
	PrivateZonesInClusterAccount bool
}

type DelegatedAWSCredentialOptions struct {
	AWSCredentialsOpts *awsutil.AWSCredentialsOptions

	AWSEbsCsiDriverControllerCredentialsFile    string
	CloudControllerCredentialsFile              string
	CloudNetworkConfigControllerCredentialsFile string
	ControlPlaneOperatorCredentialsFile         string
	NodePoolCredentialsFile                     string
	OpenshiftImageRegistryCredentialsFile       string
}

func DefaultDelegatedAWSCredentialOptions() *DelegatedAWSCredentialOptions {
	return &DelegatedAWSCredentialOptions{
		AWSCredentialsOpts: &awsutil.AWSCredentialsOptions{},
	}
}

func BindOptions(opts *DelegatedAWSCredentialOptions, flags *pflag.FlagSet) {
	opts.AWSCredentialsOpts.BindFlags(flags)

	flags.StringVar(&opts.AWSEbsCsiDriverControllerCredentialsFile, "aws-creds.aws-ebs-csi-driver-controller", opts.AWSEbsCsiDriverControllerCredentialsFile, "Path to an AWS credentials file for the aws-ebs-csi-driver-controller")
	flags.StringVar(&opts.CloudControllerCredentialsFile, "aws-creds.cloud-controller", opts.CloudControllerCredentialsFile, "Path to an AWS credentials file for the cloud-controller")
	flags.StringVar(&opts.CloudNetworkConfigControllerCredentialsFile, "aws-creds.cloud-network-config-controller", opts.CloudNetworkConfigControllerCredentialsFile, "Path to an AWS credentials file for the cloud-network-config-controller")
	flags.StringVar(&opts.ControlPlaneOperatorCredentialsFile, "aws-creds.control-plane-operator", opts.ControlPlaneOperatorCredentialsFile, "Path to an AWS credentials file for the control-plane-operator")
	flags.StringVar(&opts.NodePoolCredentialsFile, "aws-creds.node-pool", opts.NodePoolCredentialsFile, "Path to an AWS credentials file for the node-pool")
	flags.StringVar(&opts.OpenshiftImageRegistryCredentialsFile, "aws-creds.openshift-image-registry", opts.OpenshiftImageRegistryCredentialsFile, "Path to an AWS credentials file for the openshift-image-registry")
}

func (o *DelegatedAWSCredentialOptions) Validate() error {
	allComponentCredentialsPresent := true
	anyComponentCredentialsPresent := false
	for _, credential := range []string{
		o.AWSEbsCsiDriverControllerCredentialsFile,
		o.CloudControllerCredentialsFile,
		o.CloudNetworkConfigControllerCredentialsFile,
		o.ControlPlaneOperatorCredentialsFile,
		o.NodePoolCredentialsFile,
		o.OpenshiftImageRegistryCredentialsFile,
	} {
		if credential == "" {
			allComponentCredentialsPresent = false
		} else {
			anyComponentCredentialsPresent = true
		}
	}

	// ensure that only one type of credential has been passed
	globalCredentialsPresent := o.AWSCredentialsOpts.AWSCredentialsFile != "" || o.AWSCredentialsOpts.STSCredentialsFile != ""
	if globalCredentialsPresent {
		if !anyComponentCredentialsPresent {
			return o.AWSCredentialsOpts.Validate()
		} else {
			return fmt.Errorf("cannot set any --aws-creds.component flags at the same time as other credentials")
		}
	} else {
		if !allComponentCredentialsPresent {
			return fmt.Errorf("either --aws-creds, --sts-creds, or all --aws-creds.component flags must be set")
		}
	}
	return nil
}

func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "aws",
		Short:        "Destroys AWS infrastructure resources for a cluster",
		SilenceUsage: true,
	}

	opts := DestroyInfraOptions{
		Region: "us-east-1",
		Name:   "example",
		Log:    log.Log,

		AWSCredentialsOpts: DefaultDelegatedAWSCredentialOptions(),
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID with which to tag AWS resources (required)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra should be created")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The ingress base domain for the cluster")
	cmd.Flags().StringVar(&opts.BaseDomainPrefix, "base-domain-prefix", opts.BaseDomainPrefix, "The ingress base domain prefix for the cluster, defaults to cluster name. se 'none' for an empty prefix")
	cmd.Flags().DurationVar(&opts.AwsInfraGracePeriod, "aws-infra-grace-period", opts.AwsInfraGracePeriod, "Timeout for destroying infrastructure in minutes")
	cmd.Flags().BoolVar(&opts.PrivateZonesInClusterAccount, "private-zones-in-cluster-account", opts.PrivateZonesInClusterAccount, "In shared VPC infrastructure, destroy private hosted zones in cluster account")

	BindOptions(opts.AWSCredentialsOpts, cmd.Flags())
	opts.VPCOwnerCredentialsOpts.BindVPCOwnerFlags(cmd.Flags())

	_ = cmd.MarkFlagRequired("infra-id")
	_ = cmd.MarkFlagRequired("base-domain")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Validate(); err != nil {
			opts.Log.Error(err, "Incorrect flags passed")
			return err
		}
		if err := opts.Run(cmd.Context()); err != nil {
			opts.Log.Error(err, "Failed to destroy infrastructure")
			return err
		}
		opts.Log.Info("Successfully destroyed infrastructure")
		return nil
	}

	return cmd
}

func (o *DestroyInfraOptions) Validate() error {
	return o.AWSCredentialsOpts.Validate()
}

func (o *DestroyInfraOptions) Run(ctx context.Context) error {
	var infraCtx context.Context
	var destroyInfraCtxCancel context.CancelFunc

	if o.AwsInfraGracePeriod != 0 {
		infraCtx, destroyInfraCtxCancel = context.WithTimeout(ctx, o.AwsInfraGracePeriod)
		defer destroyInfraCtxCancel()

		o.Log.Info(fmt.Sprintf("Infra destruction timeout set to %d s", int(o.AwsInfraGracePeriod.Seconds())))
	} else {
		infraCtx = ctx
	}

	return wait.PollUntilContextCancel(infraCtx, 5*time.Second, true, func(context.Context) (bool, error) {
		err := o.DestroyInfra(infraCtx)
		if err != nil {
			if !awsutil.IsErrorRetryable(err) {
				return false, err
			}
			o.Log.Info("WARNING: error during destroy, will retry", "error", err.Error())
			return false, nil
		}
		return true, nil
	})
}

func (o *DestroyInfraOptions) DestroyInfra(ctx context.Context) error {
	var ec2Client, vpcOwnerEC2Client awsapi.EC2API
	var elbClient awsapi.ELBAPI
	var elbv2Client awsapi.ELBV2API
	var clusterRoute53Client, vpcOwnerRoute53Client, listRoute53Client, recordsRoute53Client awsapi.ROUTE53API
	var s3Client awsapi.S3API
	var ramClient *ram.Client
	if o.AWSCredentialsOpts.AWSCredentialsOpts.AWSCredentialsFile != "" || o.AWSCredentialsOpts.AWSCredentialsOpts.STSCredentialsFile != "" {
		awsSession, err := o.AWSCredentialsOpts.AWSCredentialsOpts.GetSession(ctx, "cli-destroy-infra", o.CredentialsSecretData, o.Region)
		if err != nil {
			return err
		}
		awsConfig := awsutil.NewConfig()
		ec2Client = ec2.NewFromConfig(*awsSession, func(o *ec2.Options) {
			o.Retryer = awsConfig()
		})
		vpcOwnerEC2Client = ec2Client
		elbClient = elb.NewFromConfig(*awsSession, func(o *elb.Options) {
			o.Retryer = awsConfig()
		})
		elbv2Client = elbv2.NewFromConfig(*awsSession, func(o *elbv2.Options) {
			o.Retryer = awsConfig()
		})
		route53Config := awsutil.NewRoute53Config()
		clusterRoute53Client = route53.NewFromConfig(*awsSession, func(o *route53.Options) {
			o.Retryer = route53Config()
		})
		s3Client = s3.NewFromConfig(*awsSession, func(o *s3.Options) {
			o.Retryer = awsConfig()
		})

		if o.VPCOwnerCredentialsOpts.AWSCredentialsFile != "" {
			vpcOwnerSession, err := o.VPCOwnerCredentialsOpts.GetSession(ctx, "cli-destroy-infra", nil, o.Region)
			if err != nil {
				return err
			}
			vpcOwnerEC2Client = ec2.NewFromConfig(*vpcOwnerSession, func(o *ec2.Options) {
				o.Retryer = awsConfig()
			})
			vpcOwnerRoute53Client = route53.NewFromConfig(*vpcOwnerSession, func(o *route53.Options) {
				o.Retryer = route53Config()
			})
			ramClient = ram.NewFromConfig(*vpcOwnerSession, func(o *ram.Options) {
				o.Retryer = awsutil.NewConfig()()
			})
		}

		listRoute53Client = clusterRoute53Client
		recordsRoute53Client = clusterRoute53Client
		if vpcOwnerRoute53Client != nil {
			listRoute53Client = vpcOwnerRoute53Client
			if !o.PrivateZonesInClusterAccount {
				recordsRoute53Client = vpcOwnerRoute53Client
			}
		}

	} else {
		if o.VPCOwnerCredentialsOpts.AWSCredentialsFile != "" {
			return fmt.Errorf("delegating client is not supported for shared vpc infrastructure")
		}
		delegatingClent, err := NewDelegatingClient(
			ctx,
			o.AWSEbsCsiDriverControllerCredentialsFile,
			o.CloudControllerCredentialsFile,
			o.CloudNetworkConfigControllerCredentialsFile,
			o.ControlPlaneOperatorCredentialsFile,
			o.NodePoolCredentialsFile,
			o.OpenshiftImageRegistryCredentialsFile,
		)
		if err != nil {
			return fmt.Errorf("failed to create delegating client: %w", err)
		}
		ec2Client = delegatingClent.EC2Client
		elbClient = delegatingClent.ELBClient
		elbv2Client = delegatingClent.ELBV2Client
		listRoute53Client = delegatingClent.ROUTE53Client
		recordsRoute53Client = delegatingClent.ROUTE53Client
		s3Client = delegatingClent.S3Client
	}

	errs := o.destroyInstances(ctx, ec2Client)
	errs = append(errs, o.DestroyInternetGateways(ctx, vpcOwnerEC2Client)...)
	errs = append(errs, o.DestroyDNS(ctx, recordsRoute53Client)...)
	errs = append(errs, o.DestroyS3Buckets(ctx, s3Client)...)
	errs = append(errs, o.DestroyVPCEndpointServices(ctx, vpcOwnerEC2Client)...)
	errs = append(errs, o.DestroyVPCs(ctx, ec2Client, vpcOwnerEC2Client, elbClient, elbv2Client, listRoute53Client, recordsRoute53Client, ramClient)...)
	if err := utilerrors.NewAggregate(errs); err != nil {
		return err
	}
	errs = append(errs, o.DestroyEIPs(ctx, ec2Client)...)
	errs = append(errs, o.DestroyEIPs(ctx, vpcOwnerEC2Client)...)
	errs = append(errs, o.DestroyDHCPOptions(ctx, vpcOwnerEC2Client)...)

	return utilerrors.NewAggregate(errs)
}

func (o *DestroyInfraOptions) DestroyS3Buckets(ctx context.Context, client awsapi.S3API) []error {
	var errs []error
	result, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		errs = append(errs, err)
		return errs
	}
	for _, bucket := range result.Buckets {
		if strings.HasPrefix(aws.ToString(bucket.Name), fmt.Sprintf("%s-image-registry-", o.InfraID)) {
			if err := emptyBucket(ctx, client, aws.ToString(bucket.Name)); err != nil {
				errs = append(errs, fmt.Errorf("failed to empty bucket %s: %w", aws.ToString(bucket.Name), err))
				continue
			}
			_, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{
				Bucket: bucket.Name,
			})
			if err != nil {
				var nsbErr *s3types.NoSuchBucket
				if errors.As(err, &nsbErr) {
					o.Log.Info("S3 Bucket already deleted", "name", aws.ToString(bucket.Name))
				} else {
					errs = append(errs, err)
				}
			} else {
				o.Log.Info("Deleted S3 Bucket", "name", aws.ToString(bucket.Name))
			}
		}
	}
	return errs
}

// emptyBucket deletes all objects in an S3 bucket.
// Note: AWS SDK Go v2 does not provide a BatchDelete utility like v1's s3manager.BatchDelete.
// This is a known limitation tracked in https://github.com/aws/aws-sdk-go-v2/issues/1463
// We manually implement batch deletion using ListObjectsV2Paginator + DeleteObjects API.
func emptyBucket(ctx context.Context, client awsapi.S3API, name string) error {
	paginator := s3.NewListObjectsV2Paginator(client, &s3.ListObjectsV2Input{
		Bucket: aws.String(name),
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			var nsbErr *s3types.NoSuchBucket
			if errors.As(err, &nsbErr) {
				return nil
			}
			return err
		}
		if len(page.Contents) == 0 {
			continue
		}
		var objects []s3types.ObjectIdentifier
		for _, obj := range page.Contents {
			objects = append(objects, s3types.ObjectIdentifier{Key: obj.Key})
		}
		output, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(name),
			Delete: &s3types.Delete{Objects: objects},
		})
		if err != nil {
			var nsbErr *s3types.NoSuchBucket
			if errors.As(err, &nsbErr) {
				return nil // Bucket doesn't exist, consistent with v1 behavior
			}
			return fmt.Errorf("failed to delete objects from bucket %s: %w", name, err)
		}

		// Check for partial failures (consistent with v1 BatchDelete behavior)
		// v1's BatchDelete returns error on partial failure, v2 should behave the same
		if len(output.Errors) > 0 {
			errMsgs := make([]string, 0, len(output.Errors))
			for _, delErr := range output.Errors {
				errMsgs = append(errMsgs, fmt.Sprintf("%s: %s",
					aws.ToString(delErr.Key),
					aws.ToString(delErr.Message)))
			}
			return fmt.Errorf("failed to delete %d objects from bucket %s: %s",
				len(output.Errors), name, strings.Join(errMsgs, "; "))
		}
	}
	return nil
}

func (o *DestroyInfraOptions) DestroyV1ELBs(ctx context.Context, client awsapi.ELBAPI, vpcID string) []error {
	var errs []error
	paginator := elb.NewDescribeLoadBalancersPaginator(client, &elb.DescribeLoadBalancersInput{})
	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, lb := range out.LoadBalancerDescriptions {
			if aws.ToString(lb.VPCId) != vpcID {
				continue
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elb.DeleteLoadBalancerInput{
				LoadBalancerName: lb.LoadBalancerName,
			}); err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted ELB", "name", aws.ToString(lb.LoadBalancerName))
			}
		}
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyV2ELBs(ctx context.Context, client awsapi.ELBV2API, vpcID string) []error {
	var errs []error
	lbPaginator := elbv2.NewDescribeLoadBalancersPaginator(client, &elbv2.DescribeLoadBalancersInput{})
	for lbPaginator.HasMorePages() {
		out, err := lbPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, lb := range out.LoadBalancers {
			if aws.ToString(lb.VpcId) != vpcID {
				continue
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elbv2.DeleteLoadBalancerInput{
				LoadBalancerArn: lb.LoadBalancerArn,
			}); err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted ELBV2 load balancer", "name", aws.ToString(lb.LoadBalancerName))
			}
		}
	}
	tgPaginator := elbv2.NewDescribeTargetGroupsPaginator(client, &elbv2.DescribeTargetGroupsInput{})
	for tgPaginator.HasMorePages() {
		out, err := tgPaginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, tg := range out.TargetGroups {
			if aws.ToString(tg.VpcId) != vpcID {
				continue
			}
			if _, err := client.DeleteTargetGroup(ctx, &elbv2.DeleteTargetGroupInput{
				TargetGroupArn: tg.TargetGroupArn,
			}); err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted TargetGroup", "name", aws.ToString(tg.TargetGroupName))
			}
		}
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyVPCEndpoints(ctx context.Context, client awsapi.EC2API, vpcID string) []error {
	var errs []error
	paginator := ec2.NewDescribeVpcEndpointsPaginator(client, &ec2.DescribeVpcEndpointsInput{Filters: vpcFilter(vpcID)})
	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		ids := make([]string, 0, len(out.VpcEndpoints))
		for _, ep := range out.VpcEndpoints {
			ids = append(ids, aws.ToString(ep.VpcEndpointId))
		}
		if len(ids) > 0 {
			_, err := client.DeleteVpcEndpoints(ctx, &ec2.DeleteVpcEndpointsInput{
				VpcEndpointIds: ids,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted VPC endpoints", "IDs", strings.Join(ids, " "))
			}
		}
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyVPCEndpointServices(ctx context.Context, client awsapi.EC2API) []error {
	var errs []error
	paginator := ec2.NewDescribeVpcEndpointServiceConfigurationsPaginator(client, &ec2.DescribeVpcEndpointServiceConfigurationsInput{Filters: o.ec2Filters()})
outer:
	for paginator.HasMorePages() {
		desc, err := paginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		var ids []string
		for _, cfg := range desc.ServiceConfigurations {
			ids = append(ids, aws.ToString(cfg.ServiceId))
		}
		if len(ids) < 1 {
			continue
		}

		endpointConnections, err := client.DescribeVpcEndpointConnections(ctx, &ec2.DescribeVpcEndpointConnectionsInput{
			Filters: []ec2types.Filter{{Name: aws.String("service-id"), Values: ids}},
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to list endpoint connections: %w", err))
			break
		}
		endpointConnectionsByServiceID := map[string][]string{}
		for _, endpointConnection := range endpointConnections.VpcEndpointConnections {
			svcID := aws.ToString(endpointConnection.ServiceId)
			epID := aws.ToString(endpointConnection.VpcEndpointId)
			endpointConnectionsByServiceID[svcID] = append(endpointConnectionsByServiceID[svcID], epID)
		}
		for service, endpoints := range endpointConnectionsByServiceID {
			if _, err := client.RejectVpcEndpointConnections(ctx, &ec2.RejectVpcEndpointConnectionsInput{
				ServiceId: aws.String(service), VpcEndpointIds: endpoints,
			}); err != nil {
				errs = append(errs, fmt.Errorf("failed to reject endpoint connections for service %s endpoints %v", service, endpoints))
				break outer
			}
			o.Log.Info("Deleted endpoint connections", "serviceID", service, "endpoints", fmt.Sprintf("%v", endpoints))
		}

		if _, err := client.DeleteVpcEndpointServiceConfigurations(ctx, &ec2.DeleteVpcEndpointServiceConfigurationsInput{
			ServiceIds: ids,
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete vpc endpoint services with ids %v: %w", ids, err))
		} else {
			o.Log.Info("Deleted VPC endpoint services", "IDs", ids)
		}
	}

	return errs
}

func (o *DestroyInfraOptions) DestroyRouteTables(ctx context.Context, client awsapi.EC2API, vpcID string) []error {
	var errs []error
	paginator := ec2.NewDescribeRouteTablesPaginator(client, &ec2.DescribeRouteTablesInput{Filters: vpcFilter(vpcID)})
	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, routeTable := range out.RouteTables {
			var routeErrs []error
			for _, route := range routeTable.Routes {
				if string(route.Origin) == "CreateRoute" {
					_, err := client.DeleteRoute(ctx, &ec2.DeleteRouteInput{
						RouteTableId:             routeTable.RouteTableId,
						DestinationCidrBlock:     route.DestinationCidrBlock,
						DestinationIpv6CidrBlock: route.DestinationIpv6CidrBlock,
						DestinationPrefixListId:  route.DestinationPrefixListId,
					})
					if err != nil {
						routeErrs = append(routeErrs, err)
					} else {
						o.Log.Info("Deleted route from route table", "table", aws.ToString(routeTable.RouteTableId), "destination", aws.ToString(route.DestinationCidrBlock))
					}
				}
			}
			if len(routeErrs) > 0 {
				errs = append(errs, routeErrs...)
				continue
			}
			hasMain := false
			var assocErrs []error
			for _, assoc := range routeTable.Associations {
				if aws.ToBool(assoc.Main) {
					hasMain = true
					continue
				}
				_, err := client.DisassociateRouteTable(ctx, &ec2.DisassociateRouteTableInput{
					AssociationId: assoc.RouteTableAssociationId,
				})
				if err != nil {
					assocErrs = append(assocErrs, err)
				} else {
					o.Log.Info("Removed route table association", "table", aws.ToString(routeTable.RouteTableId), "association", aws.ToString(assoc.RouteTableId))
				}
			}
			if len(assocErrs) > 0 {
				errs = append(errs, assocErrs...)
				continue
			}
			if hasMain {
				continue
			}
			_, err := client.DeleteRouteTable(ctx, &ec2.DeleteRouteTableInput{
				RouteTableId: routeTable.RouteTableId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted route table", "table", aws.ToString(routeTable.RouteTableId))
			}
		}
	}
	return errs
}

func (o *DestroyInfraOptions) DestroySecurityGroups(ctx context.Context, client awsapi.EC2API, vpcID string) []error {
	var errs []error
	paginator := ec2.NewDescribeSecurityGroupsPaginator(client, &ec2.DescribeSecurityGroupsInput{Filters: vpcFilter(vpcID)})
	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, sg := range out.SecurityGroups {
			var permissionErrs []error
			if len(sg.IpPermissions) > 0 {
				_, err := client.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
					GroupId:       sg.GroupId,
					IpPermissions: sg.IpPermissions,
				})
				if err != nil {
					permissionErrs = append(permissionErrs, err)
				} else {
					o.Log.Info("Revoked security group ingress permissions", "group", aws.ToString(sg.GroupId))
				}
			}

			if len(sg.IpPermissionsEgress) > 0 {
				_, err := client.RevokeSecurityGroupEgress(ctx, &ec2.RevokeSecurityGroupEgressInput{
					GroupId:       sg.GroupId,
					IpPermissions: sg.IpPermissionsEgress,
				})
				if err != nil {
					permissionErrs = append(permissionErrs, err)
				} else {
					o.Log.Info("Revoked security group egress permissions", "group", aws.ToString(sg.GroupId))
				}
			}
			if len(permissionErrs) > 0 {
				errs = append(errs, permissionErrs...)
				continue
			}
			if aws.ToString(sg.GroupName) == "default" {
				continue
			}
			_, err := client.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
				GroupId: sg.GroupId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted security group", "group", aws.ToString(sg.GroupId))
			}
		}
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyNATGateways(ctx context.Context, client awsapi.EC2API, vpcID string) []error {
	var errs []error
	paginator := ec2.NewDescribeNatGatewaysPaginator(client, &ec2.DescribeNatGatewaysInput{Filter: vpcFilter(vpcID)})
	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, natGateway := range out.NatGateways {
			if natGateway.State == ec2types.NatGatewayStateDeleted {
				continue
			}
			if natGateway.State == ec2types.NatGatewayStateDeleting {
				errs = append(errs, fmt.Errorf("NAT gateway %s still deleting", aws.ToString(natGateway.NatGatewayId)))
				continue
			}
			_, err := client.DeleteNatGateway(ctx, &ec2.DeleteNatGatewayInput{
				NatGatewayId: natGateway.NatGatewayId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				errs = append(errs, fmt.Errorf("deleting NAT gateway %s", aws.ToString(natGateway.NatGatewayId)))
			}
		}
	}
	return errs
}

func (o *DestroyInfraOptions) destroyInstances(ctx context.Context, client awsapi.EC2API) []error {
	var errs []error
	paginator := ec2.NewDescribeInstancesPaginator(client, &ec2.DescribeInstancesInput{Filters: o.ec2Filters()})
	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to describe instances: %w", err))
			break
		}
		var instanceIDs []string
		for _, reservation := range out.Reservations {
			for _, instance := range reservation.Instances {
				instanceIDs = append(instanceIDs, aws.ToString(instance.InstanceId))
			}
		}
		if len(instanceIDs) > 0 {
			if _, err := client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{InstanceIds: instanceIDs}); err != nil {
				errs = append(errs, fmt.Errorf("failed to terminate instances: %w", err))
			}
		}
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyInternetGateways(ctx context.Context, client awsapi.EC2API) []error {
	var errs []error
	paginator := ec2.NewDescribeInternetGatewaysPaginator(client, &ec2.DescribeInternetGatewaysInput{Filters: o.ec2Filters()})
	for paginator.HasMorePages() {
		out, paginateErr := paginator.NextPage(ctx)
		if paginateErr != nil {
			errs = append(errs, paginateErr)
			break
		}
		for _, igw := range out.InternetGateways {
			var detachErrs []error
			for _, attachment := range igw.Attachments {
				_, err := client.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{
					InternetGatewayId: igw.InternetGatewayId,
					VpcId:             attachment.VpcId,
				})
				if err != nil {
					detachErrs = append(detachErrs, err)
				} else {
					o.Log.Info("Detached internet gateway from VPC", "gateway id", aws.ToString(igw.InternetGatewayId), "vpc", aws.ToString(attachment.VpcId))
				}
			}
			if len(detachErrs) > 0 {
				errs = append(errs, detachErrs...)
				continue
			}
			_, err := client.DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{
				InternetGatewayId: igw.InternetGatewayId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted internet gateway", "id", aws.ToString(igw.InternetGatewayId))
			}
		}
	}
	return errs
}

func (o *DestroyInfraOptions) DestroySubnets(ctx context.Context, client awsapi.EC2API, vpcID string) []error {
	var errs []error
	paginator := ec2.NewDescribeSubnetsPaginator(client, &ec2.DescribeSubnetsInput{Filters: vpcFilter(vpcID)})
	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, subnet := range out.Subnets {
			_, err := client.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{
				SubnetId: subnet.SubnetId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted subnet", "id", aws.ToString(subnet.SubnetId))
			}
		}
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyVPCs(ctx context.Context,
	ec2client awsapi.EC2API,
	vpcOwnerEC2Client awsapi.EC2API,
	elbclient awsapi.ELBAPI,
	elbv2client awsapi.ELBV2API,
	route53listClient awsapi.ROUTE53API,
	route53client awsapi.ROUTE53API,
	ramClient *ram.Client) []error {
	var errs []error
	paginator := ec2.NewDescribeVpcsPaginator(vpcOwnerEC2Client, &ec2.DescribeVpcsInput{Filters: o.ec2Filters()})
	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, vpc := range out.Vpcs {
			var childErrs []error

			// First, destroy resources that exist in cluster account (in the case vpc is shared)
			childErrs = append(childErrs, o.DestroyV1ELBs(ctx, elbclient, aws.ToString(vpc.VpcId))...)
			childErrs = append(childErrs, o.DestroyV2ELBs(ctx, elbv2client, aws.ToString(vpc.VpcId))...)
			childErrs = append(childErrs, o.DestroySecurityGroups(ctx, ec2client, aws.ToString(vpc.VpcId))...)

			if ramClient != nil {
				// Delete the VPC share
				childErrs = append(childErrs, o.DestroyVPCShare(ctx, ramClient)...)
			}

			childErrs = append(childErrs, o.DestroyVPCEndpoints(ctx, vpcOwnerEC2Client, aws.ToString(vpc.VpcId))...)
			childErrs = append(childErrs, o.DestroyPrivateZones(ctx, route53listClient, route53client, aws.ToString(vpc.VpcId))...)
			childErrs = append(childErrs, o.DestroyRouteTables(ctx, vpcOwnerEC2Client, aws.ToString(vpc.VpcId))...)
			childErrs = append(childErrs, o.DestroyNATGateways(ctx, vpcOwnerEC2Client, aws.ToString(vpc.VpcId))...)
			if len(childErrs) > 0 {
				errs = append(errs, childErrs...)
				continue
			}
			childErrs = append(childErrs, o.DestroySecurityGroups(ctx, vpcOwnerEC2Client, aws.ToString(vpc.VpcId))...)
			childErrs = append(childErrs, o.DestroySubnets(ctx, vpcOwnerEC2Client, aws.ToString(vpc.VpcId))...)
			if len(childErrs) > 0 {
				errs = append(errs, childErrs...)
				continue
			}
			_, err := vpcOwnerEC2Client.DeleteVpc(ctx, &ec2.DeleteVpcInput{
				VpcId: vpc.VpcId,
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to delete vpc with id %s: %w", aws.ToString(vpc.VpcId), err))
			} else {
				o.Log.Info("Deleted VPC", "id", aws.ToString(vpc.VpcId))
			}
		}
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyVPCShare(ctx context.Context, client *ram.Client) []error {
	result, err := client.GetResourceShares(ctx, &ram.GetResourceSharesInput{
		ResourceOwner: ramtypes.ResourceOwnerSelf,
		TagFilters: []ramtypes.TagFilter{
			{
				TagKey:    aws.String(clusterTag(o.InfraID)),
				TagValues: []string{clusterTagValue},
			},
		},
	})
	if err != nil {
		return []error{err}
	}

	var errs []error
	for _, share := range result.ResourceShares {
		if _, err := client.DeleteResourceShare(ctx, &ram.DeleteResourceShareInput{
			ResourceShareArn: share.ResourceShareArn,
		}); err != nil {
			errs = append(errs, err)
		}
		o.Log.Info("Deleted VPC resource share", "arn", aws.ToString(share.ResourceShareArn))
	}

	return errs
}

func (o *DestroyInfraOptions) DestroyDHCPOptions(ctx context.Context, client awsapi.EC2API) []error {
	var errs []error
	paginator := ec2.NewDescribeDhcpOptionsPaginator(client, &ec2.DescribeDhcpOptionsInput{Filters: o.ec2Filters()})
	for paginator.HasMorePages() {
		out, err := paginator.NextPage(ctx)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, dhcpOpt := range out.DhcpOptions {
			_, err := client.DeleteDhcpOptions(ctx, &ec2.DeleteDhcpOptionsInput{
				DhcpOptionsId: dhcpOpt.DhcpOptionsId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted DHCP options", "id", aws.ToString(dhcpOpt.DhcpOptionsId))
			}
		}
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyEIPs(ctx context.Context, client awsapi.EC2API) []error {
	var errs []error
	out, err := client.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
		Filters: o.ec2Filters(),
	})
	if err != nil {
		errs = append(errs, err)
		return errs
	}

	for _, addr := range out.Addresses {
		_, err := client.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{
			AllocationId: addr.AllocationId,
		})
		if err != nil {
			errs = append(errs, err)
		} else {
			o.Log.Info("Deleted EIP", "id", aws.ToString(addr.AllocationId))
		}
	}
	return errs
}

func (o *DestroyInfraOptions) ec2Filters() []ec2types.Filter {
	return []ec2types.Filter{
		{
			Name:   aws.String(fmt.Sprintf("tag:%s", clusterTag(o.InfraID))),
			Values: []string{clusterTagValue},
		},
	}
}

func vpcFilter(vpcID string) []ec2types.Filter {
	return []ec2types.Filter{
		{
			Name:   aws.String("vpc-id"),
			Values: []string{vpcID},
		},
	}
}
