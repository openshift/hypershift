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

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancing"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	route53v2 "github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/ram"
	"github.com/aws/aws-sdk-go/service/ram/ramiface"

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
	var ec2Client, vpcOwnerEC2Client ec2iface.EC2API
	var elbClient awsapi.ELBAPI
	var elbv2Client awsapi.ELBV2API
	var clusterRoute53Client, vpcOwnerRoute53Client, listRoute53Client, recordsRoute53Client awsapi.ROUTE53API
	var s3Client awsapi.S3API
	var ramClient ramiface.RAMAPI
	if o.AWSCredentialsOpts.AWSCredentialsOpts.AWSCredentialsFile != "" || o.AWSCredentialsOpts.AWSCredentialsOpts.STSCredentialsFile != "" {
		awsSession, err := o.AWSCredentialsOpts.AWSCredentialsOpts.GetSession("cli-destroy-infra", o.CredentialsSecretData, o.Region)
		if err != nil {
			return err
		}
		awsConfig := awsutil.NewConfig()
		awsSessionv2, err := o.AWSCredentialsOpts.AWSCredentialsOpts.GetSessionV2(ctx, "cli-destroy-infra", o.CredentialsSecretData, o.Region)
		if err != nil {
			return err
		}
		awsConfigv2 := awsutil.NewConfigV2()
		ec2Client = ec2.New(awsSession, awsConfig)
		vpcOwnerEC2Client = ec2Client
		elbClient = elb.NewFromConfig(*awsSessionv2, func(o *elb.Options) {
			o.Retryer = awsConfigv2()
		})
		elbv2Client = elbv2.NewFromConfig(*awsSessionv2, func(o *elbv2.Options) {
			o.Retryer = awsConfigv2()
		})
		route53Configv2 := awsutil.NewRoute53ConfigV2()
		clusterRoute53Client = route53v2.NewFromConfig(*awsSessionv2, func(o *route53v2.Options) {
			o.Retryer = route53Configv2()
		})
		s3Client = s3.NewFromConfig(*awsSessionv2, func(o *s3.Options) {
			o.Retryer = awsConfigv2()
		})

		if o.VPCOwnerCredentialsOpts.AWSCredentialsFile != "" {
			vpcOwnerAWSSession, err := o.VPCOwnerCredentialsOpts.GetSession("cli-destroy-infra", nil, o.Region)
			if err != nil {
				return err
			}
			vpcOwnerSessionv2, err := o.VPCOwnerCredentialsOpts.GetSessionV2(ctx, "cli-destroy-infra", nil, o.Region)
			if err != nil {
				return err
			}
			vpcOwnerEC2Client = ec2.New(vpcOwnerAWSSession, awsConfig)
			vpcOwnerRoute53Client = route53v2.NewFromConfig(*vpcOwnerSessionv2, func(o *route53v2.Options) {
				o.Retryer = route53Configv2()
			})

			ramClient = ram.New(vpcOwnerAWSSession, awsConfig)
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
		ec2Client = delegatingClent.EC2API
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
		if strings.HasPrefix(awsv2.ToString(bucket.Name), fmt.Sprintf("%s-image-registry-", o.InfraID)) {
			if err := emptyBucket(ctx, client, awsv2.ToString(bucket.Name)); err != nil {
				errs = append(errs, fmt.Errorf("failed to empty bucket %s: %w", awsv2.ToString(bucket.Name), err))
				continue
			}
			_, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{
				Bucket: bucket.Name,
			})
			if err != nil {
				var nsbErr *s3types.NoSuchBucket
				if errors.As(err, &nsbErr) {
					o.Log.Info("S3 Bucket already deleted", "name", awsv2.ToString(bucket.Name))
				} else {
					errs = append(errs, err)
				}
			} else {
				o.Log.Info("Deleted S3 Bucket", "name", awsv2.ToString(bucket.Name))
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
		Bucket: awsv2.String(name),
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
			Bucket: awsv2.String(name),
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
					awsv2.ToString(delErr.Key),
					awsv2.ToString(delErr.Message)))
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
			if awsv2.ToString(lb.VPCId) != vpcID {
				continue
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elb.DeleteLoadBalancerInput{
				LoadBalancerName: lb.LoadBalancerName,
			}); err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted ELB", "name", awsv2.ToString(lb.LoadBalancerName))
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
			if awsv2.ToString(lb.VpcId) != vpcID {
				continue
			}
			if _, err := client.DeleteLoadBalancer(ctx, &elbv2.DeleteLoadBalancerInput{
				LoadBalancerArn: lb.LoadBalancerArn,
			}); err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted ELBV2 load balancer", "name", awsv2.ToString(lb.LoadBalancerName))
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
			if awsv2.ToString(tg.VpcId) != vpcID {
				continue
			}
			if _, err := client.DeleteTargetGroup(ctx, &elbv2.DeleteTargetGroupInput{
				TargetGroupArn: tg.TargetGroupArn,
			}); err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted TargetGroup", "name", awsv2.ToString(tg.TargetGroupName))
			}
		}
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyVPCEndpoints(ctx context.Context, client ec2iface.EC2API, vpcID *string) []error {
	var errs []error
	deleteVPCEndpoints := func(out *ec2.DescribeVpcEndpointsOutput, _ bool) bool {
		ids := make([]*string, 0, len(out.VpcEndpoints))
		for _, ep := range out.VpcEndpoints {
			ids = append(ids, ep.VpcEndpointId)
		}
		if len(ids) > 0 {
			_, err := client.DeleteVpcEndpointsWithContext(ctx, &ec2.DeleteVpcEndpointsInput{
				VpcEndpointIds: ids,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				epIDs := make([]string, 0, len(ids))
				for _, id := range ids {
					epIDs = append(epIDs, aws.StringValue(id))
				}
				o.Log.Info("Deleted VPC endpoints", "IDs", strings.Join(epIDs, " "))
			}
		}
		return true
	}
	err := client.DescribeVpcEndpointsPagesWithContext(ctx,
		&ec2.DescribeVpcEndpointsInput{Filters: vpcFilter(vpcID)},
		deleteVPCEndpoints)
	if err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyVPCEndpointServices(ctx context.Context, client ec2iface.EC2API) []error {
	var errs []error
	deleteVPCEndpointServices := func(desc *ec2.DescribeVpcEndpointServiceConfigurationsOutput, _ bool) bool {
		var ids []string
		for _, cfg := range desc.ServiceConfigurations {
			ids = append(ids, *cfg.ServiceId)
		}
		if len(ids) < 1 {
			return true
		}

		endpointConnections, err := client.DescribeVpcEndpointConnections(&ec2.DescribeVpcEndpointConnectionsInput{Filters: []*ec2.Filter{{Name: aws.String("service-id"), Values: aws.StringSlice(ids)}}})
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to list endpoint connections: %w", err))
			return false
		}
		endpointConnectionsByServiceID := map[*string][]*string{}
		for _, endpointConnection := range endpointConnections.VpcEndpointConnections {
			endpointConnectionsByServiceID[endpointConnection.ServiceId] = append(endpointConnectionsByServiceID[endpointConnection.ServiceId], endpointConnection.VpcEndpointId)
		}
		for service, endpoints := range endpointConnectionsByServiceID {
			if _, err := client.RejectVpcEndpointConnectionsWithContext(ctx, &ec2.RejectVpcEndpointConnectionsInput{ServiceId: service, VpcEndpointIds: endpoints}); err != nil {
				errs = append(errs, fmt.Errorf("failed to reject endpoint connections for service %s endpoints %v", aws.StringValue(service), aws.StringValueSlice(endpoints)))
				return false
			}
			o.Log.Info("Deleted endpoint connections", "serviceID", aws.StringValue(service), "endpoints", fmt.Sprintf("%v", aws.StringValueSlice(endpoints)))
		}

		if _, err := client.DeleteVpcEndpointServiceConfigurationsWithContext(ctx, &ec2.DeleteVpcEndpointServiceConfigurationsInput{
			ServiceIds: aws.StringSlice(ids),
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete vpc endpoint services with ids %v: %w", ids, err))
		} else {
			o.Log.Info("Deleted VPC endpoint services", "IDs", ids)
		}

		return true
	}

	if err := client.DescribeVpcEndpointServiceConfigurationsPagesWithContext(ctx,
		&ec2.DescribeVpcEndpointServiceConfigurationsInput{Filters: o.ec2Filters()},
		deleteVPCEndpointServices,
	); err != nil {
		errs = append(errs, err)
	}

	return errs
}

func (o *DestroyInfraOptions) DestroyRouteTables(ctx context.Context, client ec2iface.EC2API, vpcID *string) []error {
	var errs []error
	deleteRouteTables := func(out *ec2.DescribeRouteTablesOutput, _ bool) bool {
		for _, routeTable := range out.RouteTables {
			var routeErrs []error
			for _, route := range routeTable.Routes {
				if aws.StringValue(route.Origin) == "CreateRoute" {
					_, err := client.DeleteRouteWithContext(ctx, &ec2.DeleteRouteInput{
						RouteTableId:             routeTable.RouteTableId,
						DestinationCidrBlock:     route.DestinationCidrBlock,
						DestinationIpv6CidrBlock: route.DestinationIpv6CidrBlock,
						DestinationPrefixListId:  route.DestinationPrefixListId,
					})
					if err != nil {
						routeErrs = append(routeErrs, err)
					} else {
						o.Log.Info("Deleted route from route table", "table", aws.StringValue(routeTable.RouteTableId), "destination", aws.StringValue(route.DestinationCidrBlock))
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
				if aws.BoolValue(assoc.Main) {
					hasMain = true
					continue
				}
				_, err := client.DisassociateRouteTableWithContext(ctx, &ec2.DisassociateRouteTableInput{
					AssociationId: assoc.RouteTableAssociationId,
				})
				if err != nil {
					assocErrs = append(assocErrs, err)
				} else {
					o.Log.Info("Removed route table association", "table", aws.StringValue(routeTable.RouteTableId), "association", aws.StringValue(assoc.RouteTableId))
				}
			}
			if len(assocErrs) > 0 {
				errs = append(errs, assocErrs...)
				continue
			}
			if hasMain {
				continue
			}
			_, err := client.DeleteRouteTableWithContext(ctx, &ec2.DeleteRouteTableInput{
				RouteTableId: routeTable.RouteTableId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted route table", "table", aws.StringValue(routeTable.RouteTableId))
			}
		}
		return false
	}

	err := client.DescribeRouteTablesPagesWithContext(ctx,
		&ec2.DescribeRouteTablesInput{Filters: vpcFilter(vpcID)},
		deleteRouteTables)
	if err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (o *DestroyInfraOptions) DestroySecurityGroups(ctx context.Context, client ec2iface.EC2API, vpcID *string) []error {
	var errs []error
	deleteSecurityGroups := func(out *ec2.DescribeSecurityGroupsOutput, _ bool) bool {
		for _, sg := range out.SecurityGroups {
			var permissionErrs []error
			if len(sg.IpPermissions) > 0 {
				_, err := client.RevokeSecurityGroupIngressWithContext(ctx, &ec2.RevokeSecurityGroupIngressInput{
					GroupId:       sg.GroupId,
					IpPermissions: sg.IpPermissions,
				})
				if err != nil {
					permissionErrs = append(permissionErrs, err)
				} else {
					o.Log.Info("Revoked security group ingress permissions", "group", aws.StringValue(sg.GroupId))
				}
			}

			if len(sg.IpPermissionsEgress) > 0 {
				_, err := client.RevokeSecurityGroupEgressWithContext(ctx, &ec2.RevokeSecurityGroupEgressInput{
					GroupId:       sg.GroupId,
					IpPermissions: sg.IpPermissionsEgress,
				})
				if err != nil {
					permissionErrs = append(permissionErrs, err)
				} else {
					o.Log.Info("Revoked security group egress permissions", "group", aws.StringValue(sg.GroupId))
				}
			}
			if len(permissionErrs) > 0 {
				errs = append(errs, permissionErrs...)
				continue
			}
			if aws.StringValue(sg.GroupName) == "default" {
				continue
			}
			_, err := client.DeleteSecurityGroupWithContext(ctx, &ec2.DeleteSecurityGroupInput{
				GroupId: sg.GroupId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted security group", "group", aws.StringValue(sg.GroupId))
			}
		}

		return true
	}

	err := client.DescribeSecurityGroupsPagesWithContext(ctx,
		&ec2.DescribeSecurityGroupsInput{Filters: vpcFilter(vpcID)},
		deleteSecurityGroups)
	if err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyNATGateways(ctx context.Context, client ec2iface.EC2API, vpcID *string) []error {
	var errs []error
	deleteNATGateways := func(out *ec2.DescribeNatGatewaysOutput, _ bool) bool {
		for _, natGateway := range out.NatGateways {
			if natGateway.State != nil && *natGateway.State == "deleted" {
				continue
			}
			if natGateway.State != nil && *natGateway.State == "deleting" {
				errs = append(errs, fmt.Errorf("NAT gateway %s still deleting", aws.StringValue(natGateway.NatGatewayId)))
				continue
			}
			_, err := client.DeleteNatGatewayWithContext(ctx, &ec2.DeleteNatGatewayInput{
				NatGatewayId: natGateway.NatGatewayId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				errs = append(errs, fmt.Errorf("deleting NAT gateway %s", aws.StringValue(natGateway.NatGatewayId)))
			}
		}
		return true
	}
	err := client.DescribeNatGatewaysPagesWithContext(ctx,
		&ec2.DescribeNatGatewaysInput{Filter: vpcFilter(vpcID)},
		deleteNATGateways)
	if err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (o *DestroyInfraOptions) destroyInstances(ctx context.Context, client ec2iface.EC2API) []error {
	var errs []error
	deleteInstances := func(out *ec2.DescribeInstancesOutput, _ bool) bool {
		var instanceIDs []*string
		for _, reservation := range out.Reservations {
			for _, instance := range reservation.Instances {
				instanceIDs = append(instanceIDs, aws.String(*instance.InstanceId))
			}
		}
		if len(instanceIDs) > 0 {
			if _, err := client.TerminateInstances(&ec2.TerminateInstancesInput{InstanceIds: instanceIDs}); err != nil {
				errs = append(errs, fmt.Errorf("failed to terminate instances: %w", err))
			}
		}

		return true
	}

	if err := client.DescribeInstancesPagesWithContext(ctx, &ec2.DescribeInstancesInput{Filters: o.ec2Filters()}, deleteInstances); err != nil {
		errs = append(errs, fmt.Errorf("failed to describe instances: %w", err))
	}

	return errs
}

func (o *DestroyInfraOptions) DestroyInternetGateways(ctx context.Context, client ec2iface.EC2API) []error {
	var errs []error
	deleteInternetGateways := func(out *ec2.DescribeInternetGatewaysOutput, _ bool) bool {
		for _, igw := range out.InternetGateways {
			var detachErrs []error
			for _, attachment := range igw.Attachments {
				_, err := client.DetachInternetGatewayWithContext(ctx, &ec2.DetachInternetGatewayInput{
					InternetGatewayId: igw.InternetGatewayId,
					VpcId:             attachment.VpcId,
				})
				if err != nil {
					detachErrs = append(detachErrs, err)
				} else {
					o.Log.Info("Detached internet gateway from VPC", "gateway id", aws.StringValue(igw.InternetGatewayId), "vpc", aws.StringValue(attachment.VpcId))
				}
			}
			if len(detachErrs) > 0 {
				errs = append(errs, detachErrs...)
				continue
			}
			_, err := client.DeleteInternetGatewayWithContext(ctx, &ec2.DeleteInternetGatewayInput{
				InternetGatewayId: igw.InternetGatewayId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted internet gateway", "id", aws.StringValue(igw.InternetGatewayId))
			}
		}
		return true
	}

	err := client.DescribeInternetGatewaysPagesWithContext(ctx,
		&ec2.DescribeInternetGatewaysInput{Filters: o.ec2Filters()},
		deleteInternetGateways)
	if err != nil {
		errs = append(errs, err)
	}
	return nil
}

func (o *DestroyInfraOptions) DestroySubnets(ctx context.Context, client ec2iface.EC2API, vpcID *string) []error {
	var errs []error
	deleteSubnets := func(out *ec2.DescribeSubnetsOutput, _ bool) bool {
		for _, subnet := range out.Subnets {
			_, err := client.DeleteSubnetWithContext(ctx, &ec2.DeleteSubnetInput{
				SubnetId: subnet.SubnetId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted subnet", "id", aws.StringValue(subnet.SubnetId))
			}
		}
		return true
	}
	err := client.DescribeSubnetsPagesWithContext(ctx,
		&ec2.DescribeSubnetsInput{Filters: vpcFilter(vpcID)},
		deleteSubnets)
	if err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyVPCs(ctx context.Context,
	ec2client ec2iface.EC2API,
	vpcOwnerEC2Client ec2iface.EC2API,
	elbclient awsapi.ELBAPI,
	elbv2client awsapi.ELBV2API,
	route53listClient awsapi.ROUTE53API,
	route53client awsapi.ROUTE53API,
	ramClient ramiface.RAMAPI) []error {
	var errs []error
	deleteVPC := func(out *ec2.DescribeVpcsOutput, _ bool) bool {
		for _, vpc := range out.Vpcs {
			var childErrs []error

			// First, destroy resources that exist in cluster account (in the case vpc is shared)
			childErrs = append(childErrs, o.DestroyV1ELBs(ctx, elbclient, aws.StringValue(vpc.VpcId))...)
			childErrs = append(childErrs, o.DestroyV2ELBs(ctx, elbv2client, aws.StringValue(vpc.VpcId))...)
			childErrs = append(childErrs, o.DestroySecurityGroups(ctx, ec2client, vpc.VpcId)...)

			if ramClient != nil {
				// Delete the VPC share
				childErrs = append(childErrs, o.DestroyVPCShare(ctx, ramClient)...)
			}

			childErrs = append(childErrs, o.DestroyVPCEndpoints(ctx, vpcOwnerEC2Client, vpc.VpcId)...)
			childErrs = append(childErrs, o.DestroyPrivateZones(ctx, route53listClient, route53client, vpc.VpcId)...)
			childErrs = append(childErrs, o.DestroyRouteTables(ctx, vpcOwnerEC2Client, vpc.VpcId)...)
			childErrs = append(childErrs, o.DestroyNATGateways(ctx, vpcOwnerEC2Client, vpc.VpcId)...)
			if len(childErrs) > 0 {
				errs = append(errs, childErrs...)
				continue
			}
			childErrs = append(childErrs, o.DestroySecurityGroups(ctx, vpcOwnerEC2Client, vpc.VpcId)...)
			childErrs = append(childErrs, o.DestroySubnets(ctx, vpcOwnerEC2Client, vpc.VpcId)...)
			if len(childErrs) > 0 {
				errs = append(errs, childErrs...)
				continue
			}
			_, err := vpcOwnerEC2Client.DeleteVpcWithContext(ctx, &ec2.DeleteVpcInput{
				VpcId: vpc.VpcId,
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to delete vpc with id %s: %w", *vpc.VpcId, err))
			} else {
				o.Log.Info("Deleted VPC", "id", aws.StringValue(vpc.VpcId))
			}
		}
		return true
	}
	err := vpcOwnerEC2Client.DescribeVpcsPagesWithContext(ctx,
		&ec2.DescribeVpcsInput{Filters: o.ec2Filters()},
		deleteVPC)

	if err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyVPCShare(ctx context.Context, client ramiface.RAMAPI) []error {
	result, err := client.GetResourceSharesWithContext(ctx, &ram.GetResourceSharesInput{
		ResourceOwner: aws.String("SELF"),
		TagFilters: []*ram.TagFilter{
			{
				TagKey:    aws.String(clusterTag(o.InfraID)),
				TagValues: []*string{aws.String(clusterTagValue)},
			},
		},
	})
	if err != nil {
		return []error{err}
	}

	var errs []error
	for _, share := range result.ResourceShares {
		if _, err := client.DeleteResourceShareWithContext(ctx, &ram.DeleteResourceShareInput{
			ResourceShareArn: share.ResourceShareArn,
		}); err != nil {
			errs = append(errs, err)
		}
		o.Log.Info("Deleted VPC resource share", "arn", aws.StringValue(share.ResourceShareArn))
	}

	return errs
}

func (o *DestroyInfraOptions) DestroyDHCPOptions(ctx context.Context, client ec2iface.EC2API) []error {
	var errs []error
	deleteDHCPOptions := func(out *ec2.DescribeDhcpOptionsOutput, _ bool) bool {
		for _, dhcpOpt := range out.DhcpOptions {
			_, err := client.DeleteDhcpOptionsWithContext(ctx, &ec2.DeleteDhcpOptionsInput{
				DhcpOptionsId: dhcpOpt.DhcpOptionsId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				o.Log.Info("Deleted DHCP options", "id", aws.StringValue(dhcpOpt.DhcpOptionsId))
			}
		}
		return true
	}
	err := client.DescribeDhcpOptionsPagesWithContext(ctx,
		&ec2.DescribeDhcpOptionsInput{Filters: o.ec2Filters()},
		deleteDHCPOptions)
	if err != nil {
		errs = append(errs, err)
	}
	return errs
}

func (o *DestroyInfraOptions) DestroyEIPs(ctx context.Context, client ec2iface.EC2API) []error {
	var errs []error
	out, err := client.DescribeAddressesWithContext(ctx, &ec2.DescribeAddressesInput{
		Filters: o.ec2Filters(),
	})
	if err != nil {
		errs = append(errs, err)
		return errs
	}

	for _, addr := range out.Addresses {
		_, err := client.ReleaseAddressWithContext(ctx, &ec2.ReleaseAddressInput{
			AllocationId: addr.AllocationId,
		})
		if err != nil {
			errs = append(errs, err)
		} else {
			o.Log.Info("Deleted EIP", "id", aws.StringValue(addr.AllocationId))
		}
	}
	return errs
}

func (o *DestroyInfraOptions) ec2Filters() []*ec2.Filter {
	return []*ec2.Filter{
		{
			Name:   aws.String(fmt.Sprintf("tag:%s", clusterTag(o.InfraID))),
			Values: []*string{aws.String(clusterTagValue)},
		},
	}
}

func vpcFilter(vpcID *string) []*ec2.Filter {
	return []*ec2.Filter{
		{
			Name:   aws.String("vpc-id"),
			Values: []*string{vpcID},
		},
	}
}
