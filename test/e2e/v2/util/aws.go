//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"

	supportawsutil "github.com/openshift/hypershift/support/awsutil"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	ec2v2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/smithy-go"
)

// CreateTestSubnet creates a small (/28) subnet in the given VPC in the specified AZ,
// associates it with an existing private route table (one with a NAT gateway route),
// and returns the subnet ID plus a cleanup function that disassociates and deletes it.
// The subnet CIDR is chosen dynamically to avoid overlapping with any existing subnets.
func CreateTestSubnet(ctx context.Context, client *ec2v2.Client, vpcID, az, infraID, clusterName string, additionalTags map[string]string) (string, func(), error) {
	existing, err := client.DescribeSubnets(ctx, &ec2v2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{{Name: awsv2.String("vpc-id"), Values: []string{vpcID}}},
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to list subnets in VPC %s: %w", vpcID, err)
	}

	occupied := make([]*net.IPNet, 0, len(existing.Subnets))
	for _, subnet := range existing.Subnets {
		_, network, err := net.ParseCIDR(awsv2.ToString(subnet.CidrBlock))
		if err != nil {
			continue
		}
		occupied = append(occupied, network)
	}

	_, searchSpace, _ := net.ParseCIDR("10.0.192.0/18")
	candidateCIDR, err := findFreeCIDR(searchSpace, 28, occupied)
	if err != nil {
		return "", nil, err
	}

	subnetName := fmt.Sprintf("%s-karpenter-test-subnet", infraID)
	subnetTags := []ec2types.Tag{
		{Key: awsv2.String("Name"), Value: awsv2.String(subnetName)},
		{Key: awsv2.String(fmt.Sprintf("kubernetes.io/cluster/%s", infraID)), Value: awsv2.String("owned")},
		{Key: awsv2.String(supportawsutil.HypershiftInfraIDTagKey), Value: awsv2.String(infraID)},
		{Key: awsv2.String(supportawsutil.HypershiftClusterNameTagKey), Value: awsv2.String(clusterName)},
	}
	for key, value := range additionalTags {
		subnetTags = append(subnetTags, ec2types.Tag{Key: awsv2.String(key), Value: awsv2.String(value)})
	}

	createOut, err := client.CreateSubnet(ctx, &ec2v2.CreateSubnetInput{
		VpcId:            awsv2.String(vpcID),
		CidrBlock:        awsv2.String(candidateCIDR),
		AvailabilityZone: awsv2.String(az),
		TagSpecifications: []ec2types.TagSpecification{{
			ResourceType: ec2types.ResourceTypeSubnet,
			Tags:         subnetTags,
		}},
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to create subnet %s in %s: %w", candidateCIDR, az, err)
	}
	subnetID := awsv2.ToString(createOut.Subnet.SubnetId)
	GinkgoWriter.Printf("CreateTestSubnet: created subnet %s (%s) in AZ %s\n", subnetID, candidateCIDR, az)

	rtOut, err := client.DescribeRouteTables(ctx, &ec2v2.DescribeRouteTablesInput{
		Filters: []ec2types.Filter{
			{Name: awsv2.String("vpc-id"), Values: []string{vpcID}},
			{Name: awsv2.String("route.nat-gateway-id"), Values: []string{"nat-*"}},
		},
	})
	if err != nil {
		_, _ = client.DeleteSubnet(ctx, &ec2v2.DeleteSubnetInput{SubnetId: awsv2.String(subnetID)})
		return "", nil, fmt.Errorf("no private route table with NAT gateway found in VPC %s (err: %w); deleted subnet %s", vpcID, err, subnetID)
	}
	if len(rtOut.RouteTables) == 0 {
		_, _ = client.DeleteSubnet(ctx, &ec2v2.DeleteSubnetInput{SubnetId: awsv2.String(subnetID)})
		return "", nil, fmt.Errorf("no private route table with NAT gateway found in VPC %s; deleted subnet %s", vpcID, subnetID)
	}

	routeTableID := awsv2.ToString(rtOut.RouteTables[0].RouteTableId)
	assocOut, err := client.AssociateRouteTable(ctx, &ec2v2.AssociateRouteTableInput{
		RouteTableId: awsv2.String(routeTableID),
		SubnetId:     awsv2.String(subnetID),
	})
	if err != nil {
		_, _ = client.DeleteSubnet(ctx, &ec2v2.DeleteSubnetInput{SubnetId: awsv2.String(subnetID)})
		return "", nil, fmt.Errorf("failed to associate route table %s with subnet %s: %w; deleted subnet", routeTableID, subnetID, err)
	}

	associationID := awsv2.ToString(assocOut.AssociationId)
	GinkgoWriter.Printf("CreateTestSubnet: associated route table %s with subnet %s (association %s)\n", routeTableID, subnetID, associationID)

	cleanup := func() {
		if _, err := client.DisassociateRouteTable(ctx, &ec2v2.DisassociateRouteTableInput{AssociationId: awsv2.String(associationID)}); err != nil {
			GinkgoWriter.Printf("CreateTestSubnet cleanup: failed to disassociate route table from subnet %s: %v\n", subnetID, err)
		}
		var lastErr error
		for attempt := 0; attempt < 12; attempt++ {
			if attempt > 0 {
				time.Sleep(10 * time.Second)
			}
			_, lastErr = client.DeleteSubnet(ctx, &ec2v2.DeleteSubnetInput{SubnetId: awsv2.String(subnetID)})
			if lastErr == nil {
				GinkgoWriter.Printf("CreateTestSubnet cleanup: deleted subnet %s\n", subnetID)
				return
			}
			var apiErr smithy.APIError
			if errors.As(lastErr, &apiErr) && apiErr.ErrorCode() == "DependencyViolation" {
				GinkgoWriter.Printf("CreateTestSubnet cleanup: subnet %s has dependencies (attempt %d/12), retrying in 10s\n", subnetID, attempt+1)
				continue
			}
			break
		}
		GinkgoWriter.Printf("CreateTestSubnet cleanup: failed to delete subnet %s: %v\n", subnetID, lastErr)
	}
	return subnetID, cleanup, nil
}

func findFreeCIDR(searchSpace *net.IPNet, prefixLen int, occupied []*net.IPNet) (string, error) {
	blockSize := uint32(1) << (32 - prefixLen)
	startIP := ipToUint32(searchSpace.IP.To4())
	_, endIP := networkRange(searchSpace)
	for ip := startIP; ip+blockSize-1 <= endIP; ip += blockSize {
		candidate := &net.IPNet{IP: uint32ToIP(ip), Mask: net.CIDRMask(prefixLen, 32)}
		if !overlapsAny(candidate, occupied) {
			return candidate.String(), nil
		}
	}
	return "", fmt.Errorf("no free /%d block found in %s", prefixLen, searchSpace)
}

func overlapsAny(candidate *net.IPNet, occupied []*net.IPNet) bool {
	for _, network := range occupied {
		if candidate.Contains(network.IP) || network.Contains(candidate.IP) {
			return true
		}
	}
	return false
}

func ipToUint32(ip net.IP) uint32 {
	return binary.BigEndian.Uint32(ip.To4())
}

func uint32ToIP(n uint32) net.IP {
	ip := make(net.IP, 4)
	binary.BigEndian.PutUint32(ip, n)
	return ip
}

func networkRange(network *net.IPNet) (uint32, uint32) {
	start := ipToUint32(network.IP.To4())
	ones, bits := network.Mask.Size()
	size := uint32(1) << uint(bits-ones)
	return start, start + size - 1
}

func E2ETagsFromEnvironment() map[string]string {
	tags := map[string]string{supportawsutil.HypershiftSourceTagKey: "e2e"}
	if prowJobID := os.Getenv("PROW_JOB_ID"); prowJobID != "" {
		tags[supportawsutil.HypershiftProwJobIDTagKey] = prowJobID
	}
	return tags
}
