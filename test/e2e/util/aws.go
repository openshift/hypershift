package util

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	awsutil "github.com/openshift/hypershift/cmd/infra/aws/util"
	"github.com/openshift/hypershift/support/awsapi"
	"github.com/openshift/hypershift/support/oidc"
	"github.com/openshift/hypershift/support/util"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	ec2v2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/smithy-go"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/go-logr/logr"
)

func GetKMSKeyArn(ctx context.Context, awsCreds, awsRegion, alias string) (*string, error) {
	if alias == "" {
		return awsv2.String(""), nil
	}

	awsSession := awsutil.NewSession(ctx, "e2e-kms", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	kmsClient := kms.NewFromConfig(*awsSession, func(o *kms.Options) {
		o.Retryer = awsConfig()
	})

	input := &kms.DescribeKeyInput{
		KeyId: awsv2.String(alias),
	}
	out, err := kmsClient.DescribeKey(ctx, input)
	if err != nil {
		return nil, err
	}
	if out.KeyMetadata == nil {
		return nil, fmt.Errorf("KMS key with alias %v doesn't exist", alias)
	}

	return out.KeyMetadata.Arn, nil
}

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

func GetS3Client(ctx context.Context, awsCreds, awsRegion string) awsapi.S3API {
	awsSession := awsutil.NewSession(ctx, "e2e-s3", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	return s3.NewFromConfig(*awsSession, func(o *s3.Options) {
		o.Retryer = awsConfig()
	})
}

func GetIAMClient(ctx context.Context, awsCreds, awsRegion string) awsapi.IAMAPI {
	awsSession := awsutil.NewSession(ctx, "e2e-iam", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	return iam.NewFromConfig(*awsSession, func(o *iam.Options) {
		o.Retryer = awsConfig()
	})
}

func GetSQSClient(ctx context.Context, awsCreds, awsRegion string) awsapi.SQSAPI {
	awsSession := awsutil.NewSession(ctx, "e2e-sqs", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	return sqs.NewFromConfig(*awsSession, func(o *sqs.Options) {
		o.Retryer = awsConfig()
	})
}

func PutRolePolicy(ctx context.Context, awsCreds, awsRegion, roleARN string, policy string) (func() error, error) {
	iamClient := GetIAMClient(ctx, awsCreds, awsRegion)
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

func DestroyOIDCProvider(ctx context.Context, log logr.Logger, iamClient awsapi.IAMAPI, issuerURL string) {
	oidcProviderList, err := iamClient.ListOpenIDConnectProviders(ctx, &iam.ListOpenIDConnectProvidersInput{})
	if err != nil {
		log.Error(err, "failed to list OIDC providers")
		return
	}

	providerName := strings.TrimPrefix(issuerURL, "https://")
	for _, provider := range oidcProviderList.OpenIDConnectProviderList {
		if strings.Contains(*provider.Arn, providerName) {
			_, err := iamClient.DeleteOpenIDConnectProvider(ctx, &iam.DeleteOpenIDConnectProviderInput{
				OpenIDConnectProviderArn: provider.Arn,
			})
			if err != nil {
				var nse *iamtypes.NoSuchEntityException
				if !errors.As(err, &nse) {
					log.Error(err, "Error deleting OIDC provider", "providerARN", provider.Arn)
					return
				}
			} else {
				log.Info("Deleted OIDC provider", "providerARN", provider.Arn)
			}
			break
		}
	}
}

// CreateTestSubnet creates a small (/28) subnet in the given VPC in the specified AZ,
// associates it with an existing private route table (one with a NAT gateway route),
// and returns the subnet ID plus a cleanup function that disassociates and deletes it.
// The subnet CIDR is chosen dynamically to avoid overlapping with any existing subnets.
func CreateTestSubnet(ctx context.Context, t *testing.T, client *ec2v2.Client, vpcID, az, infraID string) (string, func()) {
	t.Helper()

	// Fetch all existing subnets in the VPC to find a non-overlapping CIDR.
	existing, err := client.DescribeSubnets(ctx, &ec2v2.DescribeSubnetsInput{
		Filters: []ec2types.Filter{
			{Name: awsv2.String("vpc-id"), Values: []string{vpcID}},
		},
	})
	if err != nil {
		t.Fatalf("CreateTestSubnet: failed to list subnets in VPC %s: %v", vpcID, err)
	}

	// Collect all occupied networks.
	occupied := make([]*net.IPNet, 0, len(existing.Subnets))
	for _, s := range existing.Subnets {
		_, n, err := net.ParseCIDR(awsv2.ToString(s.CidrBlock))
		if err != nil {
			continue
		}
		occupied = append(occupied, n)
	}

	// Scan candidate /28 blocks in the 10.0.192.0/18 range (10.0.192.0–10.0.255.255)
	// which is well above the /20 private (max 10.0.175.255) and public allocations.
	_, searchSpace, _ := net.ParseCIDR("10.0.192.0/18")
	candidateCIDR, err := findFreeCIDR(searchSpace, 28, occupied)
	if err != nil {
		t.Fatalf("CreateTestSubnet: %v", err)
	}

	subnetName := fmt.Sprintf("%s-karpenter-test-subnet", infraID)
	createOut, err := client.CreateSubnet(ctx, &ec2v2.CreateSubnetInput{
		VpcId:            awsv2.String(vpcID),
		CidrBlock:        awsv2.String(candidateCIDR),
		AvailabilityZone: awsv2.String(az),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeSubnet,
				Tags: []ec2types.Tag{
					{Key: awsv2.String("Name"), Value: awsv2.String(subnetName)},
					{Key: awsv2.String(fmt.Sprintf("kubernetes.io/cluster/%s", infraID)), Value: awsv2.String("owned")},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateTestSubnet: failed to create subnet %s in %s: %v", candidateCIDR, az, err)
	}
	subnetID := awsv2.ToString(createOut.Subnet.SubnetId)
	t.Logf("CreateTestSubnet: created subnet %s (%s) in AZ %s", subnetID, candidateCIDR, az)

	// Find a private route table in this VPC (one that has a NAT gateway route).
	rtOut, err := client.DescribeRouteTables(ctx, &ec2v2.DescribeRouteTablesInput{
		Filters: []ec2types.Filter{
			{Name: awsv2.String("vpc-id"), Values: []string{vpcID}},
			{Name: awsv2.String("route.nat-gateway-id"), Values: []string{"nat-*"}},
		},
	})
	// Route table association is required for node launch. Fail fast rather than
	// letting WaitForReadyNodesByLabels time out on a node that can never come up.
	if err != nil || len(rtOut.RouteTables) == 0 {
		_, _ = client.DeleteSubnet(ctx, &ec2v2.DeleteSubnetInput{SubnetId: awsv2.String(subnetID)})
		t.Fatalf("CreateTestSubnet: no private route table with NAT gateway found in VPC %s (err: %v); deleted subnet %s", vpcID, err, subnetID)
	}

	routeTableID := awsv2.ToString(rtOut.RouteTables[0].RouteTableId)
	assocOut, err := client.AssociateRouteTable(ctx, &ec2v2.AssociateRouteTableInput{
		RouteTableId: awsv2.String(routeTableID),
		SubnetId:     awsv2.String(subnetID),
	})
	if err != nil {
		_, _ = client.DeleteSubnet(ctx, &ec2v2.DeleteSubnetInput{SubnetId: awsv2.String(subnetID)})
		t.Fatalf("CreateTestSubnet: failed to associate route table %s with subnet %s: %v; deleted subnet", routeTableID, subnetID, err)
	}

	associationID := awsv2.ToString(assocOut.AssociationId)
	t.Logf("CreateTestSubnet: associated route table %s with subnet %s (association %s)", routeTableID, subnetID, associationID)

	cleanup := func() {
		if _, err := client.DisassociateRouteTable(ctx, &ec2v2.DisassociateRouteTableInput{
			AssociationId: awsv2.String(associationID),
		}); err != nil {
			t.Logf("CreateTestSubnet cleanup: failed to disassociate route table from subnet %s: %v", subnetID, err)
		}
		// Retry DeleteSubnet because VPC endpoint ENIs in this subnet are cleaned
		// up asynchronously by AWS after ModifyVpcEndpoint removes the subnet.
		var lastErr error
		for attempt := 0; attempt < 12; attempt++ {
			if attempt > 0 {
				time.Sleep(10 * time.Second)
			}
			_, lastErr = client.DeleteSubnet(ctx, &ec2v2.DeleteSubnetInput{SubnetId: awsv2.String(subnetID)})
			if lastErr == nil {
				t.Logf("CreateTestSubnet cleanup: deleted subnet %s", subnetID)
				return
			}
			// Only retry on DependencyViolation; other errors are not transient.
			var apiErr smithy.APIError
			if errors.As(lastErr, &apiErr) && apiErr.ErrorCode() == "DependencyViolation" {
				t.Logf("CreateTestSubnet cleanup: subnet %s has dependencies (attempt %d/12), retrying in 10s", subnetID, attempt+1)
				continue
			}
			break
		}
		t.Logf("CreateTestSubnet cleanup: failed to delete subnet %s: %v", subnetID, lastErr)
	}
	return subnetID, cleanup
}

// findFreeCIDR scans the given search space for the first /prefixLen block that
// does not overlap with any of the occupied networks.
func findFreeCIDR(searchSpace *net.IPNet, prefixLen int, occupied []*net.IPNet) (string, error) {
	blockSize := uint32(1) << (32 - prefixLen)
	startIP := ipToUint32(searchSpace.IP.To4())
	_, endIP := networkRange(searchSpace)

	for ip := startIP; ip+blockSize-1 <= endIP; ip += blockSize {
		candidate := &net.IPNet{
			IP:   uint32ToIP(ip),
			Mask: net.CIDRMask(prefixLen, 32),
		}
		if !overlapsAny(candidate, occupied) {
			return candidate.String(), nil
		}
	}
	return "", fmt.Errorf("no free /%d block found in %s", prefixLen, searchSpace)
}

func overlapsAny(candidate *net.IPNet, occupied []*net.IPNet) bool {
	for _, o := range occupied {
		if candidate.Contains(o.IP) || o.Contains(candidate.IP) {
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

func networkRange(n *net.IPNet) (uint32, uint32) {
	start := ipToUint32(n.IP.To4())
	ones, bits := n.Mask.Size()
	size := uint32(1) << uint(bits-ones)
	return start, start + size - 1
}

func CleanupOIDCBucketObjects(ctx context.Context, log logr.Logger, s3Client awsapi.S3API, bucketName, issuerURL string) {
	providerID := issuerURL[strings.LastIndex(issuerURL, "/")+1:]

	objectsToDelete := []s3types.ObjectIdentifier{
		{
			Key: awsv2.String(providerID + "/.well-known/openid-configuration"),
		},
		{
			Key: awsv2.String(providerID + oidc.JWKSURI),
		},
	}

	if _, err := s3Client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: awsv2.String(bucketName),
		Delete: &s3types.Delete{Objects: objectsToDelete},
	}); err != nil {
		var nsbErr *s3types.NoSuchBucket
		if !errors.As(err, &nsbErr) {
			log.Error(err, "failed to delete OIDC objects from S3 bucket", "bucketName", bucketName)
		}
	}
}

// CreateCapacityReservation creates an EC2 capacity reservation and returns its ID and a cleanup function
// that cancels the reservation. The caller is responsible for calling the cleanup function.
func CreateCapacityReservation(ctx context.Context, awsCreds, awsRegion, instanceType, availabilityZone string, instanceCount int32) (string, func() error, error) {
	awsSession := awsutil.NewSession(ctx, "e2e-capacity-reservation", awsCreds, "", "", awsRegion)
	awsConfig := awsutil.NewConfig()
	ec2Client := ec2v2.NewFromConfig(*awsSession, func(o *ec2v2.Options) {
		o.Retryer = awsConfig()
	})

	result, err := ec2Client.CreateCapacityReservation(ctx, &ec2v2.CreateCapacityReservationInput{
		InstanceType:          awsv2.String(instanceType),
		InstancePlatform:      ec2types.CapacityReservationInstancePlatformLinuxUnix,
		AvailabilityZone:      awsv2.String(availabilityZone),
		InstanceCount:         awsv2.Int32(instanceCount),
		InstanceMatchCriteria: ec2types.InstanceMatchCriteriaTargeted,
		EndDateType:           ec2types.EndDateTypeLimited,
		EndDate:               awsv2.Time(time.Now().Add(2 * time.Hour)),
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
			return false, nil // transient error, retry
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
