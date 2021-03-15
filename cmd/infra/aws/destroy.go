package aws

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/spf13/cobra"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
)

type DestroyInfraOptions struct {
	Region             string
	InfraID            string
	AWSCredentialsFile string
}

func NewDestroyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aws",
		Short: "Destroys AWS infrastructure resources for a cluster",
	}

	opts := DestroyInfraOptions{
		Region: "us-east-1",
	}

	cmd.Flags().StringVar(&opts.InfraID, "infra-id", opts.InfraID, "Cluster ID with which to tag AWS resources (required)")
	cmd.Flags().StringVar(&opts.AWSCredentialsFile, "aws-creds", opts.AWSCredentialsFile, "Path to an AWS credentials file (required)")
	cmd.Flags().StringVar(&opts.Region, "region", opts.Region, "Region where cluster infra should be created")

	cmd.MarkFlagRequired("infra-id")
	cmd.MarkFlagRequired("aws-creds")

	cmd.Run = func(cmd *cobra.Command, args []string) {
		opts.Run(context.Background())
		log.Info("Successfully destroyed AWS infra")
	}

	return cmd
}

func (o *DestroyInfraOptions) Run(ctx context.Context) {
	wait.PollUntil(5*time.Second, func() (bool, error) {
		err := o.DestroyInfra(ctx)
		if err != nil {
			return false, nil
		}
		return true, nil
	}, ctx.Done())
}

func (o *DestroyInfraOptions) DestroyInfra(ctx context.Context) error {
	var errs []error
	client, err := AWSClient(o.AWSCredentialsFile, o.Region)
	if err != nil {
		return err
	}
	errs = append(errs, o.DestroyInternetGateways(ctx, client)...)
	errs = append(errs, o.DestroyVPCs(ctx, client)...)
	errs = append(errs, o.DestroyDHCPOptions(ctx, client)...)
	errs = append(errs, o.DestroyEIPs(ctx, client)...)
	return utilerrors.NewAggregate(errs)
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
				log.Info("Deleted VPC endpoints", "IDs", strings.Join(epIDs, " "))
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
						log.Info("Deleted route from route table", "table", aws.StringValue(routeTable.RouteTableId), "destination", aws.StringValue(route.DestinationCidrBlock))
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
					log.Info("Removed route table association", "table", aws.StringValue(routeTable.RouteTableId), "association", aws.StringValue(assoc.RouteTableId))
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
				log.Info("Deleted route table", "table", aws.StringValue(routeTable.RouteTableId))
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
					log.Info("Revoked security group ingress permissions", "group", aws.StringValue(sg.GroupId))
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
					log.Info("Revoked security group egress permissions", "group", aws.StringValue(sg.GroupId))
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
				log.Info("Deleted security group", "group", aws.StringValue(sg.GroupId))
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
				log.Info("Deleted NAT gateway", "id", aws.StringValue(natGateway.NatGatewayId))
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
					log.Info("Detached internet gateway from VPC", "gateway id", aws.StringValue(igw.InternetGatewayId), "vpc", aws.StringValue(attachment.VpcId))
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
				log.Info("Deleted internet gateway", "id", aws.StringValue(igw.InternetGatewayId))
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
				log.Info("Deleted subnet", "id", aws.StringValue(subnet.SubnetId))
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

func (o *DestroyInfraOptions) DestroyVPCs(ctx context.Context, client ec2iface.EC2API) []error {
	var errs []error
	deleteVPC := func(out *ec2.DescribeVpcsOutput, _ bool) bool {
		for _, vpc := range out.Vpcs {
			var childErrs []error
			childErrs = append(errs, o.DestroyVPCEndpoints(ctx, client, vpc.VpcId)...)
			childErrs = append(errs, o.DestroyRouteTables(ctx, client, vpc.VpcId)...)
			childErrs = append(errs, o.DestroySecurityGroups(ctx, client, vpc.VpcId)...)
			childErrs = append(errs, o.DestroyNATGateways(ctx, client, vpc.VpcId)...)
			childErrs = append(errs, o.DestroySubnets(ctx, client, vpc.VpcId)...)
			if len(childErrs) > 0 {
				errs = append(errs, childErrs...)
				continue
			}
			_, err := client.DeleteVpcWithContext(ctx, &ec2.DeleteVpcInput{
				VpcId: vpc.VpcId,
			})
			if err != nil {
				errs = append(errs, err)
			} else {
				log.Info("Deleted VPC", "id", aws.StringValue(vpc.VpcId))
			}
		}
		return true
	}
	err := client.DescribeVpcsPagesWithContext(ctx,
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
				log.Info("Deleted DHCP options", "id", aws.StringValue(dhcpOpt.DhcpOptionsId))
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
			log.Info("Deleted EIP", "id", aws.StringValue(addr.AllocationId))
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
