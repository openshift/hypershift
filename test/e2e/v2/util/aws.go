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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	supportawsutil "github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/support/util"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	ec2v2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/smithy-go"

	"k8s.io/apimachinery/pkg/util/wait"
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

// GetDefaultSecurityGroup retrieves a security group by ID.
func GetDefaultSecurityGroup(ctx context.Context, awsCreds, awsRegion, sgID string) (*ec2types.SecurityGroup, error) {
	awsSession := awsutil.NewSession(ctx, "e2e-ec2", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	ec2Client := ec2v2.NewFromConfig(*awsSession, func(o *ec2v2.Options) {
		o.Retryer = awsConfig()
	})

	describeSGResult, err := ec2Client.DescribeSecurityGroups(ctx, &ec2v2.DescribeSecurityGroupsInput{
		GroupIds: []string{sgID},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get security group: %w", err)
	}
	if len(describeSGResult.SecurityGroups) == 0 {
		return nil, fmt.Errorf("no security group found with ID %s", sgID)
	}
	return &describeSGResult.SecurityGroups[0], nil
}

func getIAMClient(ctx context.Context, awsCreds, awsRegion string) *iam.Client {
	awsSession := awsutil.NewSession(ctx, "e2e-iam", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	return iam.NewFromConfig(*awsSession, func(o *iam.Options) {
		o.Retryer = awsConfig()
	})
}

// PutRolePolicy attaches an inline policy to an IAM role and returns a cleanup function
// that deletes the policy. The caller is responsible for calling the cleanup function.
func PutRolePolicy(ctx context.Context, awsCreds, awsRegion, roleARN string, policy string) (func() error, error) {
	iamClient := getIAMClient(ctx, awsCreds, awsRegion)
	roleName := roleARN[strings.LastIndex(roleARN, "/")+1:]
	policyName := util.HashSimple(policy)

	_, err := iamClient.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       awsv2.String(roleName),
		PolicyName:     awsv2.String(policyName),
		PolicyDocument: awsv2.String(policy),
	})
	if err != nil {
		var nse *iamtypes.NoSuchEntityException
		if errors.As(err, &nse) {
			return nil, fmt.Errorf("role %s doesn't exist", roleARN)
		}
		return nil, fmt.Errorf("failed to put role policy: %w", err)
	}

	cleanupFunc := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, err := iamClient.DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{
			RoleName:   awsv2.String(roleName),
			PolicyName: awsv2.String(policyName),
		})
		if err != nil {
			var nse *iamtypes.NoSuchEntityException
			if errors.As(err, &nse) {
				return nil
			}
			return fmt.Errorf("failed to delete role policy: %w", err)
		}
		return nil
	}

	return cleanupFunc, nil
}

// CreateCapacityReservation creates an EC2 capacity reservation and returns its ID and a cleanup
// function that cancels the reservation. The caller is responsible for calling the cleanup function.
func CreateCapacityReservation(ctx context.Context, awsCreds, awsRegion, instanceType, availabilityZone string, instanceCount int32, infraID, clusterName string, additionalTags map[string]string) (string, func() error, error) {
	awsSession := awsutil.NewSession(ctx, "e2e-capacity-reservation", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	ec2Client := ec2v2.NewFromConfig(*awsSession, func(o *ec2v2.Options) {
		o.Retryer = awsConfig()
	})

	crTags := []ec2types.Tag{
		{Key: awsv2.String(supportawsutil.HypershiftInfraIDTagKey), Value: awsv2.String(infraID)},
		{Key: awsv2.String(supportawsutil.HypershiftClusterNameTagKey), Value: awsv2.String(clusterName)},
	}
	for k, v := range additionalTags {
		crTags = append(crTags, ec2types.Tag{Key: awsv2.String(k), Value: awsv2.String(v)})
	}
	result, err := ec2Client.CreateCapacityReservation(ctx, &ec2v2.CreateCapacityReservationInput{
		InstanceType:          awsv2.String(instanceType),
		InstancePlatform:      ec2types.CapacityReservationInstancePlatformLinuxUnix,
		AvailabilityZone:      awsv2.String(availabilityZone),
		InstanceCount:         awsv2.Int32(instanceCount),
		InstanceMatchCriteria: ec2types.InstanceMatchCriteriaTargeted,
		EndDateType:           ec2types.EndDateTypeLimited,
		EndDate:               awsv2.Time(time.Now().Add(2 * time.Hour)),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeCapacityReservation,
				Tags:         crTags,
			},
		},
	})
	if err != nil {
		return "", nil, fmt.Errorf("failed to create capacity reservation: %w", err)
	}

	crID := awsv2.ToString(result.CapacityReservation.CapacityReservationId)

	if err := wait.PollUntilContextTimeout(ctx, 5*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		desc, err := ec2Client.DescribeCapacityReservations(ctx, &ec2v2.DescribeCapacityReservationsInput{
			CapacityReservationIds: []string{crID},
		})
		if err != nil {
			return false, nil //nolint:nilerr
		}
		if len(desc.CapacityReservations) == 0 {
			return false, nil
		}
		switch desc.CapacityReservations[0].State {
		case ec2types.CapacityReservationStateActive:
			return true, nil
		case ec2types.CapacityReservationStateFailed, ec2types.CapacityReservationStateCancelled, ec2types.CapacityReservationStateExpired:
			return false, fmt.Errorf("capacity reservation %s entered terminal state %q", crID, desc.CapacityReservations[0].State)
		}
		return false, nil
	}); err != nil {
		return "", nil, fmt.Errorf("waiting for capacity reservation %s to become active: %w", crID, err)
	}

	cleanupFunc := func() error {
		cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, err := ec2Client.CancelCapacityReservation(cancelCtx, &ec2v2.CancelCapacityReservationInput{
			CapacityReservationId: awsv2.String(crID),
		})
		if err != nil {
			return fmt.Errorf("failed to cancel capacity reservation %s: %w", crID, err)
		}
		return nil
	}

	return crID, cleanupFunc, nil
}

func E2ETagsFromEnvironment() map[string]string {
	tags := map[string]string{supportawsutil.HypershiftSourceTagKey: "e2e"}
	if prowJobID := os.Getenv("PROW_JOB_ID"); prowJobID != "" {
		tags[supportawsutil.HypershiftProwJobIDTagKey] = prowJobID
	}
	return tags
}
