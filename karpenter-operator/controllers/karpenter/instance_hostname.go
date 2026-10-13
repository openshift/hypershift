package karpenter

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/openshift/hypershift/support/awsapi"
	"github.com/openshift/hypershift/support/awsutil"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	ctrl "sigs.k8s.io/controller-runtime"
	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// matchesInstanceHostname reports whether nodeName is a hostname one of the EC2 instances of the nodeClaims can have.
// The kubelet takes its node name from the instance's hostname, which is either the EC2 PrivateDnsName or,
// if the VPC's DHCP option set has a custom domain-name, the first label of the PrivateDnsName followed by that domain.
func matchesInstanceHostname(ctx context.Context, nodeName string, nodeClaims []karpenterv1.NodeClaim, ec2Client awsapi.EC2API) (bool, error) {
	instances, err := describeInstances(ctx, nodeClaims, ec2Client)
	if err != nil || len(instances) == 0 {
		return false, err
	}

	// Check all instances for an exact PrivateDnsName match before any DHCP lookup,
	// so nodes in VPCs with the default domain never depend on it.
	if slices.ContainsFunc(instances, func(instance ec2types.Instance) bool {
		return aws.ToString(instance.PrivateDnsName) == nodeName
	}) {
		return true, nil
	}

	return matchesDHCPDomainHostname(ctx, nodeName, instances, ec2Client)
}

func describeInstances(ctx context.Context, nodeClaims []karpenterv1.NodeClaim, ec2Client awsapi.EC2API) ([]ec2types.Instance, error) {
	ec2InstanceIDs := []string{}
	for _, claim := range nodeClaims {
		providerID := claim.Status.ProviderID
		separator := strings.LastIndex(providerID, "/")
		if separator < 0 || separator == len(providerID)-1 {
			continue
		}
		instanceID := providerID[separator+1:]

		ec2InstanceIDs = append(ec2InstanceIDs, instanceID)
	}

	if len(ec2InstanceIDs) == 0 {
		return nil, nil
	}

	output, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: ec2InstanceIDs,
	})
	if err != nil {
		return nil, err
	}

	instances := []ec2types.Instance{}
	for _, reservation := range output.Reservations {
		instances = append(instances, reservation.Instances...)
	}
	return instances, nil
}

// matchesDHCPDomainHostname reports whether nodeName is the first label of an instance's PrivateDnsName
// followed by the domain of the DHCP option set of the instance's VPC.
func matchesDHCPDomainHostname(ctx context.Context, nodeName string, instances []ec2types.Instance, ec2Client awsapi.EC2API) (bool, error) {
	domainsByVPC := map[string][]string{}
	for _, instance := range instances {
		vpcID := aws.ToString(instance.VpcId)
		if vpcID == "" {
			continue
		}

		// Look up each VPC's DHCP domains only once per call.
		domains, ok := domainsByVPC[vpcID]
		if !ok {
			var err error
			domains, err = getDHCPDomainNames(ctx, ec2Client, vpcID)
			if awsutil.IsPermissionsError(err) {
				// Without the permissions for the DHCP lookup, only the PrivateDnsName can match.
				// Retrying does not help until they are granted, and Karpenter replaces nodes that
				// do not register, so their new CSRs are checked again.
				ctrl.LoggerFrom(ctx).Info("Cannot look up the DHCP domain of the VPC", "vpcID", vpcID, "error", err.Error())
				return false, nil
			}
			if err != nil {
				return false, err
			}
			domainsByVPC[vpcID] = domains
		}

		// Replace the domain of the PrivateDnsName with the DHCP domain:
		// ip-10-0-1-2.eu-central-1.compute.internal -> ip-10-0-1-2.<domain>
		// The node's hostname uses only the first DHCP domain.
		hostLabel, _, _ := strings.Cut(aws.ToString(instance.PrivateDnsName), ".")
		if len(domains) > 0 && hostnamesEqual(hostLabel+"."+domains[0], nodeName) {
			return true, nil
		}
	}

	ctrl.LoggerFrom(ctx).Info("Node name does not match any EC2 instance hostname",
		"nodeName", nodeName, "dhcpDomainsByVPC", domainsByVPC)
	return false, nil
}

// getDHCPDomainNames returns the domains of the domain-name option of the VPC's DHCP option set.
// AWS allows multiple domains separated by whitespace.
func getDHCPDomainNames(ctx context.Context, ec2Client awsapi.EC2API, vpcID string) ([]string, error) {
	vpcs, err := ec2Client.DescribeVpcs(ctx, &ec2.DescribeVpcsInput{VpcIds: []string{vpcID}})
	if err != nil {
		return nil, err
	}

	if len(vpcs.Vpcs) == 0 {
		return nil, fmt.Errorf("vpc %s not found", vpcID)
	}

	dhcpOptionsID := aws.ToString(vpcs.Vpcs[0].DhcpOptionsId)
	// "default" denotes no DHCP option set associated with the VPC.
	if dhcpOptionsID == "" || dhcpOptionsID == "default" {
		return nil, nil
	}

	dhcpOptions, err := ec2Client.DescribeDhcpOptions(ctx, &ec2.DescribeDhcpOptionsInput{
		DhcpOptionsIds: []string{dhcpOptionsID},
	})
	if err != nil {
		return nil, err
	}

	if len(dhcpOptions.DhcpOptions) == 0 {
		return nil, fmt.Errorf("dhcp options %s not found", dhcpOptionsID)
	}

	domains := []string{}
	for _, config := range dhcpOptions.DhcpOptions[0].DhcpConfigurations {
		if aws.ToString(config.Key) != "domain-name" {
			continue
		}
		for _, value := range config.Values {
			domains = append(domains, strings.Fields(aws.ToString(value.Value))...)
		}
	}
	return domains, nil
}

// hostnamesEqual compares hostnames case-insensitively and ignores a trailing dot.
// The kubelet lowercases its node name and the node name has no trailing dot, while the DHCP domain-name can have both.
func hostnamesEqual(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}
