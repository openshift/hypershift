package aws

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/elb"
	"github.com/aws/aws-sdk-go/service/elb/elbiface"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/spf13/cobra"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/cmd/log"
)

type DestroyInfraOptions struct {
	Region             string
	InfraID            string
	AWSCredentialsFile string
	AWSKey             string
	AWSSecretKey       string
	Name               string
	BaseDomain         string
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
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID with which to tag AWS resources (required)")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra should be created")
	cmd.Flags().StringVar(&opts.Name, "name", opts.Name, "A name for the cluster")
	cmd.Flags().StringVar(&opts.BaseDomain, "base-domain", opts.BaseDomain, "The ingress base domain for the cluster")

	cmd.MarkFlagRequired("infra-id")
	cmd.MarkFlagRequired("aws-creds")
	cmd.MarkFlagRequired("base-domain")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if err := opts.Run(cmd.Context()); err != nil {
			log.Log.Error(err, "Failed to destroy infrastructure")
			return err
		}
		log.Log.Info("Successfully destroyed infrastructure")
		return nil
	}

	return cmd
}

func (o *DestroyInfraOptions) Run(ctx context.Context) error {
	return wait.PollImmediateUntil(5*time.Second, func() (bool, error) {
		err := o.DestroyInfra(ctx)
		if err != nil {
			if !awsutil.IsErrorRetryable(err) {
				return false, err
			}
			log.Log.Info("WARNING: error during destroy, will retry", "error", err.Error(), "type", fmt.Sprintf("%T,%+v", err, err))
			return false, nil
		}
		return true, nil
	}, ctx.Done())
}

func (o *DestroyInfraOptions) DestroyInfra(ctx context.Context) error {
	awsSession := awsutil.NewSession("cli-destroy-infra", o.AWSCredentialsFile, o.AWSKey, o.AWSSecretKey, o.Region)
	awsConfig := awsutil.NewConfig()
	ec2Client := ec2.New(awsSession, awsConfig)
	elbClient := elb.New(awsSession, awsConfig)
	route53Client := route53.New(awsSession, awsutil.NewAWSRoute53Config())
	s3Client := s3.New(awsSession, awsConfig)

	var errs []error
	errs = append(errs, o.DestroyInternetGateways(ctx, ec2Client)...)
	errs = append(errs, o.DestroyDHCPOptions(ctx, ec2Client)...)
	errs = append(errs, o.DestroyEIPs(ctx, ec2Client)...)
	errs = append(errs, o.DestroyDNS(ctx, route53Client)...)
	errs = append(errs, o.DestroyS3Buckets(ctx, s3Client)...)
	errs = append(errs, o.DestroyVPCEndpointServices(ctx, ec2Client)...)
	errs = append(errs, o.DestroyVPCs(ctx, ec2Client, elbClient, route53Client)...)

	return utilerrors.NewAggregate(errs)
}

func (o *DestroyInfraOptions) DestroyS3Buckets(ctx context.Context, client s3iface.S3API) []error {
	var errs []error
	result, err := client.ListBucketsWithContext(ctx, &s3.ListBucketsInput{})
	if err != nil {
		errs = append(errs, err)
		return errs
	}
	for _, bucket := range result.Buckets {
		if strings.HasPrefix(*bucket.Name, fmt.Sprintf("%s-image-registry-", o.InfraID)) {
			if err := emptyBucket(ctx, client, *bucket.Name); err != nil {
				errs = append(errs, fmt.Errorf("failed to empty bucket %s: %w", *bucket.Name, err))
				continue
			}
			_, err := client.DeleteBucketWithContext(ctx, &s3.DeleteBucketInput{
				Bucket: bucket.Name,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				log.Log.Info("Deleted S3 Bucket", "name", *bucket.Name)
			}
		}
	}
	return errs
}

func emptyBucket(ctx context.Context, client s3iface.S3API, name string) error {
	iter := s3manager.NewDeleteListIterator(client, &s3.ListObjectsInput{
		Bucket: aws.String(name),
	})
	return s3manager.NewBatchDeleteWithClient(client).Delete(ctx, iter)
}

func (o *DestroyInfraOptions) DestroyELBs(ctx context.Context, client elbiface.ELBAPI, vpcID *string) []error {
	var errs []error
	deleteLBs := func(out *elb.DescribeLoadBalancersOutput, _ bool) bool {
		for _, lb := range out.LoadBalancerDescriptions {
			if *lb.VPCId != *vpcID {
				continue
			}
			_, err := client.DeleteLoadBalancerWithContext(ctx, &elb.DeleteLoadBalancerInput{
				LoadBalancerName: lb.LoadBalancerName,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				log.Log.Info("Deleted ELB", "name", lb.LoadBalancerName)
			}
		}
		return true
	}
	err := client.DescribeLoadBalancersPagesWithContext(ctx,
		&elb.DescribeLoadBalancersInput{},
		deleteLBs)
	if err != nil {
		errs = append(errs, err)
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
				log.Log.Info("Deleted VPC endpoints", "IDs", strings.Join(epIDs, " "))
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
		if _, err := client.DeleteVpcEndpointServiceConfigurationsWithContext(ctx, &ec2.DeleteVpcEndpointServiceConfigurationsInput{
			ServiceIds: aws.StringSlice(ids),
		}); err != nil {
			errs = append(errs, fmt.Errorf("failed to delete vpc endpoint services with ids %v: %w", ids, err))
		} else {
			log.Log.Info("Deleted VPC endpoint services", "IDs", ids)
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
						log.Log.Info("Deleted route from route table", "table", aws.StringValue(routeTable.RouteTableId), "destination", aws.StringValue(route.DestinationCidrBlock))
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
					log.Log.Info("Removed route table association", "table", aws.StringValue(routeTable.RouteTableId), "association", aws.StringValue(assoc.RouteTableId))
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
				log.Log.Info("Deleted route table", "table", aws.StringValue(routeTable.RouteTableId))
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
					log.Log.Info("Revoked security group ingress permissions", "group", aws.StringValue(sg.GroupId))
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
					log.Log.Info("Revoked security group egress permissions", "group", aws.StringValue(sg.GroupId))
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
				log.Log.Info("Deleted security group", "group", aws.StringValue(sg.GroupId))
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
			_, err := client.DeleteNatGatewayWithContext(ctx, &ec2.DeleteNatGatewayInput{
				NatGatewayId: natGateway.NatGatewayId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				log.Log.Info("Deleted NAT gateway", "id", aws.StringValue(natGateway.NatGatewayId))
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
					log.Log.Info("Detached internet gateway from VPC", "gateway id", aws.StringValue(igw.InternetGatewayId), "vpc", aws.StringValue(attachment.VpcId))
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
				log.Log.Info("Deleted internet gateway", "id", aws.StringValue(igw.InternetGatewayId))
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
				log.Log.Info("Deleted subnet", "id", aws.StringValue(subnet.SubnetId))
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

func (o *DestroyInfraOptions) DestroyVPCs(ctx context.Context, ec2client ec2iface.EC2API, elbclient elbiface.ELBAPI, route53client route53iface.Route53API) []error {
	var errs []error
	deleteVPC := func(out *ec2.DescribeVpcsOutput, _ bool) bool {
		for _, vpc := range out.Vpcs {
			var childErrs []error
			childErrs = append(childErrs, o.DestroyELBs(ctx, elbclient, vpc.VpcId)...)
			childErrs = append(childErrs, o.DestroyVPCEndpoints(ctx, ec2client, vpc.VpcId)...)
			childErrs = append(childErrs, o.DestroyRouteTables(ctx, ec2client, vpc.VpcId)...)
			childErrs = append(childErrs, o.DestroySecurityGroups(ctx, ec2client, vpc.VpcId)...)
			childErrs = append(childErrs, o.DestroyNATGateways(ctx, ec2client, vpc.VpcId)...)
			childErrs = append(childErrs, o.DestroySubnets(ctx, ec2client, vpc.VpcId)...)
			childErrs = append(childErrs, o.DestroyPrivateZones(ctx, route53client, vpc.VpcId)...)
			if len(childErrs) > 0 {
				errs = append(errs, childErrs...)
				continue
			}
			_, err := ec2client.DeleteVpcWithContext(ctx, &ec2.DeleteVpcInput{
				VpcId: vpc.VpcId,
			})
			if err != nil {
				errs = append(errs, fmt.Errorf("failed to delete vpc with id %s: %w", *vpc.VpcId, err))
			} else {
				log.Log.Info("Deleted VPC", "id", aws.StringValue(vpc.VpcId))
			}
		}
		return true
	}
	err := ec2client.DescribeVpcsPagesWithContext(ctx,
		&ec2.DescribeVpcsInput{Filters: o.ec2Filters()},
		deleteVPC)

	if err != nil {
		errs = append(errs, err)
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
				log.Log.Info("Deleted DHCP options", "id", aws.StringValue(dhcpOpt.DhcpOptionsId))
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
			log.Log.Info("Deleted EIP", "id", aws.StringValue(addr.AllocationId))
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
