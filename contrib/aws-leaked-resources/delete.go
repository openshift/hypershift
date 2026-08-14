package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
)

const (
	maxRetries = 10
	retryDelay = 2 * time.Second
)

type ConfirmMode int

const (
	ConfirmAll  ConfirmMode = iota // delete without per-resource prompts
	ConfirmEach                    // prompt before each resource
)

type Deleter struct {
	EC2     EC2API
	ELBv2   ELBv2API
	Route53 Route53API
	IAM     IAMAPI
}

func (d *Deleter) DeleteAll(ctx context.Context, leaked []InfraSet, mode ConfirmMode) error {
	deletedVPCs := 0
	failedVPCs := 0
	deletedZones := 0
	failedZones := 0
	deletedOIDC := 0
	skipped := 0

	for _, infra := range leaked {
		if err := ctx.Err(); err != nil {
			fmt.Fprintf(os.Stderr, "\nCanceled.\n")
			break
		}

		if infra.Verdict != VerdictLeaked {
			continue
		}

		fmt.Fprintf(os.Stderr, "\n=== Infra set: %s ===\n", infra.InfraID)

		if mode == ConfirmEach {
			d.printDetailedInventory(ctx, infra)
			if !promptYN(fmt.Sprintf("Delete ALL resources for %s?", infra.InfraID)) {
				fmt.Fprintf(os.Stderr, "  Skipped.\n")
				skipped++
				continue
			}
		}

		for _, zone := range infra.HostedZones {
			if err := d.deleteHostedZone(ctx, zone.ZoneID, zone.Name); err != nil {
				fmt.Fprintf(os.Stderr, "  FAILED zone %s (%s): %v\n", zone.ZoneID, zone.Name, err)
				failedZones++
			} else {
				fmt.Fprintf(os.Stderr, "  Deleted zone: %s (%s)\n", zone.ZoneID, zone.Name)
				deletedZones++
			}
		}

		for _, oidcARN := range infra.OIDCProviders {
			if err := d.deleteOIDCProvider(ctx, oidcARN); err != nil {
				fmt.Fprintf(os.Stderr, "  FAILED OIDC provider %s: %v\n", oidcARN, err)
			} else {
				fmt.Fprintf(os.Stderr, "  Deleted OIDC provider: %s\n", oidcARN)
				deletedOIDC++
			}
		}

		for _, roleName := range infra.IAMRoles {
			if err := ctx.Err(); err != nil {
				break
			}
			if err := d.deleteIAMRole(ctx, roleName); err != nil {
				fmt.Fprintf(os.Stderr, "  FAILED IAM role %s: %v\n", roleName, err)
			} else {
				fmt.Fprintf(os.Stderr, "  Deleted IAM role: %s\n", roleName)
			}
		}

		for _, vpc := range infra.VPCs {
			if err := d.deleteVPC(ctx, vpc.VPCID, infra.InfraID); err != nil {
				fmt.Fprintf(os.Stderr, "  FAILED VPC %s (%s): %v\n", vpc.VPCID, vpc.Name, err)
				failedVPCs++
			} else {
				fmt.Fprintf(os.Stderr, "  Deleted VPC: %s (%s)\n", vpc.VPCID, vpc.Name)
				deletedVPCs++
			}
		}
	}

	fmt.Fprintf(os.Stderr, "\n==============================\n")
	fmt.Fprintf(os.Stderr, "  Deletion results\n")
	fmt.Fprintf(os.Stderr, "==============================\n")
	fmt.Fprintf(os.Stderr, "  VPCs:     %d deleted, %d failed\n", deletedVPCs, failedVPCs)
	fmt.Fprintf(os.Stderr, "  Zones:    %d deleted, %d failed\n", deletedZones, failedZones)
	fmt.Fprintf(os.Stderr, "  OIDC:     %d deleted\n", deletedOIDC)
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "  Skipped:  %d infra sets\n", skipped)
	}
	fmt.Fprintf(os.Stderr, "==============================\n")

	if failedVPCs > 0 || failedZones > 0 {
		return fmt.Errorf("%d VPCs and %d zones failed to delete", failedVPCs, failedZones)
	}

	return nil
}

// retryDelete executes fn up to maxRetries times with retryDelay between attempts.
// Respects context cancellation between retries so Ctrl+C works immediately.
func retryDelete(ctx context.Context, desc string, fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%s canceled: %w", desc, err)
		}
		if err := fn(); err != nil {
			lastErr = err
			if attempt < maxRetries {
				fmt.Fprintf(os.Stderr, "      retry %d/%d %s: %v\n", attempt, maxRetries, desc, err)
				if err := sleepWithContext(ctx, retryDelay); err != nil {
					return fmt.Errorf("%s canceled during retry: %w", desc, err)
				}
			}
			continue
		}
		if attempt > 1 {
			fmt.Fprintf(os.Stderr, "      %s succeeded on attempt %d/%d\n", desc, attempt, maxRetries)
		}
		return nil
	}
	return fmt.Errorf("%s failed after %d attempts: %w", desc, maxRetries, lastErr)
}

// deleteVPC removes a VPC and ALL its dependent resources in the correct order.
// Cascade: ELBs → VPC Endpoint Services → VPCE → NAT → (wait) → EIPs → IGW → RTB → ENI → Subnets → SG → VPC
func (d *Deleter) deleteVPC(ctx context.Context, vpcID, infraID string) error {
	fmt.Fprintf(os.Stderr, "    Cleaning sub-resources for %s...\n", vpcID)

	steps := []struct {
		name string
		fn   func()
	}{
		{"ELBs", func() { d.deleteLoadBalancers(ctx, vpcID) }},
		{"VPCE services", func() { d.deleteVPCEndpointServices(ctx, infraID) }},
		{"VPCE", func() { d.deleteVPCEndpoints(ctx, vpcID) }},
	}

	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("canceled during %s: %w", step.name, err)
		}
		step.fn()
	}

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("canceled: %w", err)
	}

	natEIPs := d.collectNATGatewayEIPs(ctx, vpcID)
	if natDeleted := d.deleteNATGateways(ctx, vpcID); natDeleted > 0 {
		if err := d.waitForNATGatewaysDrained(ctx, vpcID); err != nil {
			return fmt.Errorf("canceled during NAT wait: %w", err)
		}
	}

	postNATSteps := []struct {
		name string
		fn   func()
	}{
		{"EIPs", func() { d.releaseEIPs(ctx, natEIPs) }},
		{"IGWs", func() { d.deleteInternetGateways(ctx, vpcID) }},
		{"RTBs", func() { d.deleteRouteTables(ctx, vpcID) }},
		{"ENIs", func() { d.deleteNetworkInterfaces(ctx, vpcID) }},
		{"subnets", func() { d.deleteSubnets(ctx, vpcID) }},
		{"SGs", func() { d.deleteSecurityGroups(ctx, vpcID) }},
	}

	for _, step := range postNATSteps {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("canceled during %s: %w", step.name, err)
		}
		step.fn()
	}

	return retryDelete(ctx, "VPC "+vpcID, func() error {
		_, err := d.EC2.DeleteVpc(ctx, &ec2.DeleteVpcInput{VpcId: aws.String(vpcID)})
		return err
	})
}

func formatDurationShort(d time.Duration) string {
	hours := int(d.Hours())
	if hours < 48 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dd%dh", hours/24, hours%24)
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func (d *Deleter) deleteLoadBalancers(ctx context.Context, vpcID string) {
	if d.ELBv2 == nil {
		return
	}

	var marker *string
	for {
		out, err := d.ELBv2.DescribeLoadBalancers(ctx, &elbv2.DescribeLoadBalancersInput{Marker: marker})
		if err != nil {
			fmt.Fprintf(os.Stderr, "      WARN: list ELBs: %v\n", err)
			return
		}

		for _, lb := range out.LoadBalancers {
			if aws.ToString(lb.VpcId) != vpcID {
				continue
			}
			name := aws.ToString(lb.LoadBalancerName)
			arn := aws.ToString(lb.LoadBalancerArn)
			if err := retryDelete(ctx, "ELB "+name, func() error {
				_, err := d.ELBv2.DeleteLoadBalancer(ctx, &elbv2.DeleteLoadBalancerInput{LoadBalancerArn: aws.String(arn)})
				return err
			}); err != nil {
				fmt.Fprintf(os.Stderr, "      %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "      Deleted ELB: %s\n", name)
			}
		}

		if out.NextMarker == nil {
			break
		}
		marker = out.NextMarker
	}
}

func (d *Deleter) deleteVPCEndpointServices(ctx context.Context, infraID string) {
	var serviceIDs []string
	var nextToken *string
	for {
		out, err := d.EC2.DescribeVpcEndpointServiceConfigurations(ctx, &ec2.DescribeVpcEndpointServiceConfigurationsInput{
			NextToken: nextToken,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "      WARN: list VPCE services: %v\n", err)
			return
		}

		for _, svc := range out.ServiceConfigurations {
			for _, tag := range svc.Tags {
				key := aws.ToString(tag.Key)
				if strings.HasPrefix(key, "kubernetes.io/cluster/") {
					if strings.TrimPrefix(key, "kubernetes.io/cluster/") == infraID {
						serviceIDs = append(serviceIDs, aws.ToString(svc.ServiceId))
					}
					break
				}
			}
		}

		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}

	if len(serviceIDs) > 0 {
		if err := retryDelete(ctx, fmt.Sprintf("VPCE services (%d)", len(serviceIDs)), func() error {
			_, err := d.EC2.DeleteVpcEndpointServiceConfigurations(ctx, &ec2.DeleteVpcEndpointServiceConfigurationsInput{ServiceIds: serviceIDs})
			return err
		}); err != nil {
			fmt.Fprintf(os.Stderr, "      %v\n", err)
		} else {
			for _, id := range serviceIDs {
				fmt.Fprintf(os.Stderr, "      Deleted VPCE service: %s\n", id)
			}
		}
	}
}

func (d *Deleter) deleteVPCEndpoints(ctx context.Context, vpcID string) {
	out, err := d.EC2.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{
		Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "      WARN: list VPCE: %v\n", err)
		return
	}

	var ids []string
	for _, ep := range out.VpcEndpoints {
		ids = append(ids, aws.ToString(ep.VpcEndpointId))
	}

	if len(ids) > 0 {
		if err := retryDelete(ctx, fmt.Sprintf("VPCE (%d)", len(ids)), func() error {
			_, err := d.EC2.DeleteVpcEndpoints(ctx, &ec2.DeleteVpcEndpointsInput{VpcEndpointIds: ids})
			return err
		}); err != nil {
			fmt.Fprintf(os.Stderr, "      %v\n", err)
		}
	}
}

func (d *Deleter) deleteNATGateways(ctx context.Context, vpcID string) int {
	out, err := d.EC2.DescribeNatGateways(ctx, &ec2.DescribeNatGatewaysInput{
		Filter: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
			{Name: aws.String("state"), Values: []string{"available", "pending", "failed"}},
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "      WARN: list NAT: %v\n", err)
		return 0
	}

	deleted := 0
	for _, nat := range out.NatGateways {
		natID := aws.ToString(nat.NatGatewayId)
		if err := retryDelete(ctx, "NAT "+natID, func() error {
			_, err := d.EC2.DeleteNatGateway(ctx, &ec2.DeleteNatGatewayInput{NatGatewayId: aws.String(natID)})
			return err
		}); err != nil {
			fmt.Fprintf(os.Stderr, "      %v\n", err)
		} else {
			deleted++
		}
	}

	return deleted
}

const (
	natDrainRetries  = 12
	natDrainInterval = 5 * time.Second
)

func (d *Deleter) waitForNATGatewaysDrained(ctx context.Context, vpcID string) error {
	fmt.Fprintf(os.Stderr, "      Waiting for NAT gateways to release ENIs...\n")
	for attempt := 1; attempt <= natDrainRetries; attempt++ {
		if err := sleepWithContext(ctx, natDrainInterval); err != nil {
			return err
		}
		out, err := d.EC2.DescribeNatGateways(ctx, &ec2.DescribeNatGatewaysInput{
			Filter: []ec2types.Filter{
				{Name: aws.String("vpc-id"), Values: []string{vpcID}},
				{Name: aws.String("state"), Values: []string{"deleting"}},
			},
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "      WARN: check NAT state: %v\n", err)
			continue
		}
		if len(out.NatGateways) == 0 {
			fmt.Fprintf(os.Stderr, "      NAT gateways drained after %d checks\n", attempt)
			return nil
		}
		fmt.Fprintf(os.Stderr, "      %d NAT gateways still draining (check %d/%d)...\n", len(out.NatGateways), attempt, natDrainRetries)
	}
	fmt.Fprintf(os.Stderr, "      WARN: NAT gateways did not fully drain after %d checks, proceeding anyway\n", natDrainRetries)
	return nil
}

// collectNATGatewayEIPs returns the AllocationIDs of EIPs used by NAT gateways in this VPC.
// Must be called BEFORE deleting the NAT gateways.
func (d *Deleter) collectNATGatewayEIPs(ctx context.Context, vpcID string) []string {
	out, err := d.EC2.DescribeNatGateways(ctx, &ec2.DescribeNatGatewaysInput{
		Filter: []ec2types.Filter{
			{Name: aws.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		return nil
	}

	var allocIDs []string
	for _, nat := range out.NatGateways {
		for _, addr := range nat.NatGatewayAddresses {
			if addr.AllocationId != nil {
				allocIDs = append(allocIDs, aws.ToString(addr.AllocationId))
			}
		}
	}
	return allocIDs
}

// releaseEIPs releases only the EIPs identified by the given AllocationIDs.
func (d *Deleter) releaseEIPs(ctx context.Context, allocIDs []string) {
	for _, allocID := range allocIDs {
		out, err := d.EC2.DescribeAddresses(ctx, &ec2.DescribeAddressesInput{
			AllocationIds: []string{allocID},
		})
		if err != nil {
			continue
		}
		for _, addr := range out.Addresses {
			ip := aws.ToString(addr.PublicIp)
			if err := retryDelete(ctx, "EIP "+ip, func() error {
				_, err := d.EC2.ReleaseAddress(ctx, &ec2.ReleaseAddressInput{AllocationId: aws.String(allocID)})
				return err
			}); err != nil {
				fmt.Fprintf(os.Stderr, "      %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "      Released EIP: %s (%s)\n", allocID, ip)
			}
		}
	}
}

func (d *Deleter) deleteInternetGateways(ctx context.Context, vpcID string) {
	out, err := d.EC2.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
		Filters: []ec2types.Filter{{Name: aws.String("attachment.vpc-id"), Values: []string{vpcID}}},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "      WARN: list IGW: %v\n", err)
		return
	}

	for _, igw := range out.InternetGateways {
		igwID := aws.ToString(igw.InternetGatewayId)
		_ = retryDelete(ctx, "detach IGW "+igwID, func() error {
			_, err := d.EC2.DetachInternetGateway(ctx, &ec2.DetachInternetGatewayInput{
				InternetGatewayId: aws.String(igwID), VpcId: aws.String(vpcID),
			})
			return err
		})
		if err := retryDelete(ctx, "IGW "+igwID, func() error {
			_, err := d.EC2.DeleteInternetGateway(ctx, &ec2.DeleteInternetGatewayInput{InternetGatewayId: aws.String(igwID)})
			return err
		}); err != nil {
			fmt.Fprintf(os.Stderr, "      %v\n", err)
		}
	}
}

func (d *Deleter) deleteRouteTables(ctx context.Context, vpcID string) {
	out, err := d.EC2.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
		Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "      WARN: list RTB: %v\n", err)
		return
	}

	for _, rt := range out.RouteTables {
		isMain := false
		for _, a := range rt.Associations {
			if aws.ToBool(a.Main) {
				isMain = true
				break
			}
		}
		if isMain {
			continue
		}

		rtID := aws.ToString(rt.RouteTableId)
		for _, a := range rt.Associations {
			if !aws.ToBool(a.Main) {
				_, _ = d.EC2.DisassociateRouteTable(ctx, &ec2.DisassociateRouteTableInput{
					AssociationId: a.RouteTableAssociationId,
				})
			}
		}

		if err := retryDelete(ctx, "RTB "+rtID, func() error {
			_, err := d.EC2.DeleteRouteTable(ctx, &ec2.DeleteRouteTableInput{RouteTableId: aws.String(rtID)})
			return err
		}); err != nil {
			fmt.Fprintf(os.Stderr, "      %v\n", err)
		}
	}
}

func (d *Deleter) deleteNetworkInterfaces(ctx context.Context, vpcID string) {
	out, err := d.EC2.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "      WARN: list ENI: %v\n", err)
		return
	}

	for _, eni := range out.NetworkInterfaces {
		eniID := aws.ToString(eni.NetworkInterfaceId)
		if eni.Attachment != nil && eni.Attachment.AttachmentId != nil {
			_ = retryDelete(ctx, "detach ENI "+eniID, func() error {
				_, err := d.EC2.DetachNetworkInterface(ctx, &ec2.DetachNetworkInterfaceInput{
					AttachmentId: eni.Attachment.AttachmentId, Force: aws.Bool(true),
				})
				return err
			})
		}
		if err := retryDelete(ctx, "ENI "+eniID, func() error {
			_, err := d.EC2.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{NetworkInterfaceId: aws.String(eniID)})
			return err
		}); err != nil {
			fmt.Fprintf(os.Stderr, "      %v\n", err)
		}
	}
}

func (d *Deleter) deleteSubnets(ctx context.Context, vpcID string) {
	out, err := d.EC2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "      WARN: list subnets: %v\n", err)
		return
	}

	for _, subnet := range out.Subnets {
		subID := aws.ToString(subnet.SubnetId)
		if err := retryDelete(ctx, "subnet "+subID, func() error {
			_, err := d.EC2.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{SubnetId: aws.String(subID)})
			return err
		}); err != nil {
			fmt.Fprintf(os.Stderr, "      %v\n", err)
		}
	}
}

func (d *Deleter) deleteSecurityGroups(ctx context.Context, vpcID string) {
	out, err := d.EC2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "      WARN: list SG: %v\n", err)
		return
	}

	// First pass: revoke rules
	for _, sg := range out.SecurityGroups {
		if aws.ToString(sg.GroupName) == "default" {
			continue
		}
		sgID := aws.ToString(sg.GroupId)
		if len(sg.IpPermissions) > 0 {
			_, _ = d.EC2.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
				GroupId: aws.String(sgID), IpPermissions: sg.IpPermissions,
			})
		}
		if len(sg.IpPermissionsEgress) > 0 {
			_, _ = d.EC2.RevokeSecurityGroupEgress(ctx, &ec2.RevokeSecurityGroupEgressInput{
				GroupId: aws.String(sgID), IpPermissions: sg.IpPermissionsEgress,
			})
		}
	}

	// Second pass: delete
	for _, sg := range out.SecurityGroups {
		if aws.ToString(sg.GroupName) == "default" {
			continue
		}
		sgID := aws.ToString(sg.GroupId)
		if err := retryDelete(ctx, "SG "+sgID, func() error {
			_, err := d.EC2.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: aws.String(sgID)})
			return err
		}); err != nil {
			fmt.Fprintf(os.Stderr, "      %v\n", err)
		}
	}
}

func (d *Deleter) deleteHostedZone(ctx context.Context, zoneID, zoneName string) error {
	var changes []route53types.Change
	input := &route53.ListResourceRecordSetsInput{
		HostedZoneId: aws.String(zoneID),
	}
	for {
		rrOut, err := d.Route53.ListResourceRecordSets(ctx, input)
		if err != nil {
			return err
		}

		for _, rr := range rrOut.ResourceRecordSets {
			if rr.Type == route53types.RRTypeNs || rr.Type == route53types.RRTypeSoa {
				continue
			}
			changes = append(changes, route53types.Change{
				Action: route53types.ChangeActionDelete, ResourceRecordSet: &rr,
			})
		}

		if !rrOut.IsTruncated {
			break
		}
		input.StartRecordName = rrOut.NextRecordName
		input.StartRecordType = rrOut.NextRecordType
		input.StartRecordIdentifier = rrOut.NextRecordIdentifier
	}

	if len(changes) > 0 {
		if err := retryDelete(ctx, "records in "+zoneName, func() error {
			_, err := d.Route53.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
				HostedZoneId: aws.String(zoneID),
				ChangeBatch:  &route53types.ChangeBatch{Changes: changes},
			})
			return err
		}); err != nil {
			return err
		}
	}

	return retryDelete(ctx, "zone "+zoneName, func() error {
		_, err := d.Route53.DeleteHostedZone(ctx, &route53.DeleteHostedZoneInput{Id: aws.String(zoneID)})
		return err
	})
}

func (d *Deleter) deleteOIDCProvider(ctx context.Context, arn string) error {
	return retryDelete(ctx, "OIDC "+arn, func() error {
		_, err := d.IAM.DeleteOpenIDConnectProvider(ctx, &iam.DeleteOpenIDConnectProviderInput{
			OpenIDConnectProviderArn: aws.String(arn),
		})
		return err
	})
}

//nolint:gocyclo // sequential AWS inventory queries
func (d *Deleter) printDetailedInventory(ctx context.Context, infra InfraSet) {
	if infra.TestType != "" {
		fmt.Fprintf(os.Stderr, "  Test type: %s\n", infra.TestType)
	}
	if infra.Namespace != "" {
		fmt.Fprintf(os.Stderr, "  Namespace: %s\n", infra.Namespace)
	}
	if infra.ProwLink != "" {
		fmt.Fprintf(os.Stderr, "  Prow:      %s\n", infra.ProwLink)
	}
	if oldest := oldestVPC(infra.VPCs); !oldest.IsZero() {
		age := time.Since(oldest)
		hours := int(age.Hours())
		ageStr := fmt.Sprintf("%dh", hours)
		if hours >= 48 {
			ageStr = fmt.Sprintf("%dd%dh", hours/24, hours%24)
		}
		fmt.Fprintf(os.Stderr, "  Created:   %s (%s ago)\n", oldest.Format("2006-01-02 15:04 UTC"), ageStr)
	} else {
		fmt.Fprintf(os.Stderr, "  Created:   unable to determine (no timestamped sub-resources found)\n")
	}
	for _, vpc := range infra.VPCs {
		if !vpc.ExpirationDate.IsZero() {
			if time.Now().After(vpc.ExpirationDate) {
				fmt.Fprintf(os.Stderr, "  Expired:   %s (expired %s ago)\n", vpc.ExpirationDate.Format("2006-01-02 15:04 UTC"), formatDurationShort(time.Since(vpc.ExpirationDate)))
			} else {
				fmt.Fprintf(os.Stderr, "  Expires:   %s (in %s)\n", vpc.ExpirationDate.Format("2006-01-02 15:04 UTC"), formatDurationShort(time.Until(vpc.ExpirationDate)))
			}
			break
		}
	}
	fmt.Fprintf(os.Stderr, "  Resources to delete:\n")

	for _, vpc := range infra.VPCs {
		fmt.Fprintf(os.Stderr, "    VPC: %s (%s)\n", vpc.VPCID, vpc.Name)
		vpcID := vpc.VPCID

		if d.ELBv2 != nil {
			if out, err := d.ELBv2.DescribeLoadBalancers(ctx, &elbv2.DescribeLoadBalancersInput{}); err == nil {
				for _, lb := range out.LoadBalancers {
					if aws.ToString(lb.VpcId) == vpcID {
						fmt.Fprintf(os.Stderr, "      ELB:     %s (%s, %s)\n",
							aws.ToString(lb.LoadBalancerName), string(lb.Type), string(lb.Scheme))
					}
				}
			}
		}

		if out, err := d.EC2.DescribeVpcEndpoints(ctx, &ec2.DescribeVpcEndpointsInput{
			Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
		}); err == nil {
			for _, ep := range out.VpcEndpoints {
				fmt.Fprintf(os.Stderr, "      VPCE:    %s (%s, %s)\n",
					aws.ToString(ep.VpcEndpointId), string(ep.VpcEndpointType), aws.ToString(ep.ServiceName))
			}
		}

		if out, err := d.EC2.DescribeNatGateways(ctx, &ec2.DescribeNatGatewaysInput{
			Filter: []ec2types.Filter{
				{Name: aws.String("vpc-id"), Values: []string{vpcID}},
				{Name: aws.String("state"), Values: []string{"available", "pending", "failed"}},
			},
		}); err == nil {
			for _, nat := range out.NatGateways {
				name := ""
				for _, t := range nat.Tags {
					if aws.ToString(t.Key) == "Name" {
						name = aws.ToString(t.Value)
					}
				}
				ip := ""
				if len(nat.NatGatewayAddresses) > 0 {
					ip = aws.ToString(nat.NatGatewayAddresses[0].PublicIp)
				}
				fmt.Fprintf(os.Stderr, "      NAT:     %s (%s, ip=%s)\n", aws.ToString(nat.NatGatewayId), name, ip)
			}
		}

		if out, err := d.EC2.DescribeInternetGateways(ctx, &ec2.DescribeInternetGatewaysInput{
			Filters: []ec2types.Filter{{Name: aws.String("attachment.vpc-id"), Values: []string{vpcID}}},
		}); err == nil {
			for _, igw := range out.InternetGateways {
				name := ""
				for _, t := range igw.Tags {
					if aws.ToString(t.Key) == "Name" {
						name = aws.ToString(t.Value)
					}
				}
				fmt.Fprintf(os.Stderr, "      IGW:     %s (%s)\n", aws.ToString(igw.InternetGatewayId), name)
			}
		}

		if out, err := d.EC2.DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
			Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
		}); err == nil {
			for _, sn := range out.Subnets {
				name := ""
				for _, t := range sn.Tags {
					if aws.ToString(t.Key) == "Name" {
						name = aws.ToString(t.Value)
					}
				}
				fmt.Fprintf(os.Stderr, "      Subnet:  %s (%s, %s)\n", aws.ToString(sn.SubnetId), name, aws.ToString(sn.CidrBlock))
			}
		}

		if out, err := d.EC2.DescribeRouteTables(ctx, &ec2.DescribeRouteTablesInput{
			Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
		}); err == nil {
			for _, rt := range out.RouteTables {
				isMain := false
				for _, a := range rt.Associations {
					if aws.ToBool(a.Main) {
						isMain = true
					}
				}
				if isMain {
					continue
				}
				name := ""
				for _, t := range rt.Tags {
					if aws.ToString(t.Key) == "Name" {
						name = aws.ToString(t.Value)
					}
				}
				fmt.Fprintf(os.Stderr, "      RTB:     %s (%s)\n", aws.ToString(rt.RouteTableId), name)
			}
		}

		if out, err := d.EC2.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
			Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
		}); err == nil {
			for _, sg := range out.SecurityGroups {
				if aws.ToString(sg.GroupName) == "default" {
					continue
				}
				fmt.Fprintf(os.Stderr, "      SG:      %s (%s)\n", aws.ToString(sg.GroupId), aws.ToString(sg.GroupName))
			}
		}

		if out, err := d.EC2.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
			Filters: []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{vpcID}}},
		}); err == nil {
			for _, eni := range out.NetworkInterfaces {
				desc := aws.ToString(eni.Description)
				if desc == "" {
					desc = string(eni.InterfaceType)
				}
				fmt.Fprintf(os.Stderr, "      ENI:     %s (%s)\n", aws.ToString(eni.NetworkInterfaceId), desc)
			}
		}
	}

	for _, z := range infra.HostedZones {
		zType := "public"
		if z.Private {
			zType = "private"
		}
		fmt.Fprintf(os.Stderr, "    Zone: %s (%s, %s, %d records)\n", z.Name, z.ZoneID, zType, z.Records)
	}
	for _, arn := range infra.OIDCProviders {
		fmt.Fprintf(os.Stderr, "    OIDC: %s\n", arn)
	}
	for _, role := range infra.IAMRoles {
		fmt.Fprintf(os.Stderr, "    IAM role: %s\n", role)
	}
	fmt.Fprintf(os.Stderr, "\n")
}

func (d *Deleter) deleteIAMRole(ctx context.Context, roleName string) error {
	// Detach managed policies
	if polOut, err := d.IAM.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
		RoleName: aws.String(roleName),
	}); err == nil {
		for _, pol := range polOut.AttachedPolicies {
			_, _ = d.IAM.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
				RoleName:  aws.String(roleName),
				PolicyArn: pol.PolicyArn,
			})
		}
	}

	// Delete inline policies
	if polOut, err := d.IAM.ListRolePolicies(ctx, &iam.ListRolePoliciesInput{
		RoleName: aws.String(roleName),
	}); err == nil {
		for _, polName := range polOut.PolicyNames {
			_, _ = d.IAM.DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{
				RoleName:   aws.String(roleName),
				PolicyName: aws.String(polName),
			})
		}
	}

	// Remove from instance profiles and delete them
	if profOut, err := d.IAM.ListInstanceProfilesForRole(ctx, &iam.ListInstanceProfilesForRoleInput{
		RoleName: aws.String(roleName),
	}); err == nil {
		for _, prof := range profOut.InstanceProfiles {
			_, _ = d.IAM.RemoveRoleFromInstanceProfile(ctx, &iam.RemoveRoleFromInstanceProfileInput{
				RoleName:            aws.String(roleName),
				InstanceProfileName: prof.InstanceProfileName,
			})
			_, _ = d.IAM.DeleteInstanceProfile(ctx, &iam.DeleteInstanceProfileInput{
				InstanceProfileName: prof.InstanceProfileName,
			})
		}
	}

	return retryDelete(ctx, "IAM role "+roleName, func() error {
		_, err := d.IAM.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: aws.String(roleName)})
		return err
	})
}
