package awsprivatelink

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/awsapi"
	"github.com/openshift/hypershift/support/upsert"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2v2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	route53sdk "github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/smithy-go"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/google/go-cmp/cmp"
	"go.uber.org/mock/gomock"
)

func Test_diffIDs(t *testing.T) {
	subnet1 := "1"
	subnet2 := "2"
	subnet3 := "3"
	type args struct {
		desired  []string
		existing []string
	}
	tests := []struct {
		name        string
		args        args
		wantAdded   []string
		wantRemoved []string
	}{
		{
			name: "no subnets, no change",
			args: args{
				desired:  []string{},
				existing: []string{},
			},
			wantAdded:   nil,
			wantRemoved: nil,
		},
		{
			name: "two subnet, no change",
			args: args{
				desired:  []string{subnet1, subnet2},
				existing: []string{subnet1, subnet2},
			},
			wantAdded:   nil,
			wantRemoved: nil,
		},
		{
			name: "one new subnet",
			args: args{
				desired:  []string{subnet1, subnet2},
				existing: []string{subnet1},
			},
			wantAdded:   []string{subnet2},
			wantRemoved: nil,
		},
		{
			name: "one removed subnet",
			args: args{
				desired:  []string{subnet1},
				existing: []string{subnet1, subnet2},
			},
			wantAdded:   nil,
			wantRemoved: []string{subnet2},
		},
		{
			name: "one removed subnet, one added subnet",
			args: args{
				desired:  []string{subnet1, subnet2},
				existing: []string{subnet2, subnet3},
			},
			wantAdded:   []string{subnet1},
			wantRemoved: []string{subnet3},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotAdded, gotRemoved := diffIDs(tt.args.desired, tt.args.existing)
			if !reflect.DeepEqual(gotAdded, tt.wantAdded) {
				t.Errorf("diffSubnetIDs() gotAdded = %v, want %v", gotAdded, tt.wantAdded)
			}
			if !reflect.DeepEqual(gotRemoved, tt.wantRemoved) {
				t.Errorf("diffSubnetIDs() gotRemoved = %v, want %v", gotRemoved, tt.wantRemoved)
			}
		})
	}
}

func Test_deduplicateSubnetsByAZ(t *testing.T) {
	tests := []struct {
		name               string
		subnetIDs          []string
		cachedAZs          map[string]string
		describeOutput     *ec2v2.DescribeSubnetsOutput
		describeErr        error
		expectDescribeCall bool
		expectedSubnets    []string
		expectErr          bool
	}{
		{
			name:            "When empty subnet list it should return empty",
			subnetIDs:       []string{},
			expectedSubnets: []string{},
		},
		{
			name:            "When single subnet it should pass through without DescribeSubnets call",
			subnetIDs:       []string{"subnet-aaa"},
			expectedSubnets: []string{"subnet-aaa"},
		},
		{
			name:      "When subnets in different AZs it should keep all",
			subnetIDs: []string{"subnet-aaa", "subnet-bbb"},
			describeOutput: &ec2v2.DescribeSubnetsOutput{
				Subnets: []ec2types.Subnet{
					{SubnetId: aws.String("subnet-aaa"), AvailabilityZone: aws.String("us-east-1a")},
					{SubnetId: aws.String("subnet-bbb"), AvailabilityZone: aws.String("us-east-1b")},
				},
			},
			expectDescribeCall: true,
			expectedSubnets:    []string{"subnet-aaa", "subnet-bbb"},
		},
		{
			name:      "When multiple subnets in same AZ it should keep lexicographically first",
			subnetIDs: []string{"subnet-bbb", "subnet-aaa"},
			describeOutput: &ec2v2.DescribeSubnetsOutput{
				Subnets: []ec2types.Subnet{
					{SubnetId: aws.String("subnet-bbb"), AvailabilityZone: aws.String("us-east-1a")},
					{SubnetId: aws.String("subnet-aaa"), AvailabilityZone: aws.String("us-east-1a")},
				},
			},
			expectDescribeCall: true,
			expectedSubnets:    []string{"subnet-aaa"},
		},
		{
			name:      "When mixed same-AZ and different-AZ subnets it should dedup correctly",
			subnetIDs: []string{"subnet-aaa", "subnet-bbb", "subnet-ccc"},
			describeOutput: &ec2v2.DescribeSubnetsOutput{
				Subnets: []ec2types.Subnet{
					{SubnetId: aws.String("subnet-aaa"), AvailabilityZone: aws.String("us-east-1a")},
					{SubnetId: aws.String("subnet-bbb"), AvailabilityZone: aws.String("us-east-1a")},
					{SubnetId: aws.String("subnet-ccc"), AvailabilityZone: aws.String("us-east-1b")},
				},
			},
			expectDescribeCall: true,
			expectedSubnets:    []string{"subnet-aaa", "subnet-ccc"},
		},
		{
			name:               "When cache covers all subnets it should not call DescribeSubnets",
			subnetIDs:          []string{"subnet-aaa", "subnet-bbb"},
			cachedAZs:          map[string]string{"subnet-aaa": "us-east-1a", "subnet-bbb": "us-east-1b"},
			expectDescribeCall: false,
			expectedSubnets:    []string{"subnet-aaa", "subnet-bbb"},
		},
		{
			name:      "When cache covers some subnets it should call DescribeSubnets only for new ones",
			subnetIDs: []string{"subnet-aaa", "subnet-bbb", "subnet-ccc"},
			cachedAZs: map[string]string{"subnet-aaa": "us-east-1a"},
			describeOutput: &ec2v2.DescribeSubnetsOutput{
				Subnets: []ec2types.Subnet{
					{SubnetId: aws.String("subnet-bbb"), AvailabilityZone: aws.String("us-east-1b")},
					{SubnetId: aws.String("subnet-ccc"), AvailabilityZone: aws.String("us-east-1c")},
				},
			},
			expectDescribeCall: true,
			expectedSubnets:    []string{"subnet-aaa", "subnet-bbb", "subnet-ccc"},
		},
		{
			name:               "When DescribeSubnets fails it should return error",
			subnetIDs:          []string{"subnet-aaa", "subnet-bbb"},
			describeErr:        fmt.Errorf("access denied"),
			expectDescribeCall: true,
			expectErr:          true,
		},
		{
			name:            "When a single edge-zone subnet is cached, it should preserve the shortcut",
			subnetIDs:       []string{"subnet-edge"},
			cachedAZs:       map[string]string{"subnet-edge": "us-east-1-atl-1a"},
			expectedSubnets: []string{"subnet-edge"},
		},
		{
			name:      "When a cold cache includes an edge zone, it should filter before deduplicating",
			subnetIDs: []string{"subnet-edge", "subnet-bbb", "subnet-aaa"},
			describeOutput: &ec2v2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{
				{SubnetId: aws.String("subnet-edge"), AvailabilityZone: aws.String("us-east-1-atl-1a")},
				{SubnetId: aws.String("subnet-bbb"), AvailabilityZone: aws.String("us-east-1a")},
				{SubnetId: aws.String("subnet-aaa"), AvailabilityZone: aws.String("us-east-1a")},
			}},
			expectDescribeCall: true,
			expectedSubnets:    []string{"subnet-aaa"},
		},
		{
			name:            "When a warm cache includes an edge zone, it should filter without an AWS lookup",
			subnetIDs:       []string{"subnet-edge", "subnet-aaa"},
			cachedAZs:       map[string]string{"subnet-edge": "us-east-1-atl-1a", "subnet-aaa": "us-east-1a"},
			expectedSubnets: []string{"subnet-aaa"},
		},
		{
			name:      "When a partial cache includes an edge zone, it should fetch only the missing subnet",
			subnetIDs: []string{"subnet-edge", "subnet-aaa"},
			cachedAZs: map[string]string{"subnet-edge": "us-east-1-atl-1a"},
			describeOutput: &ec2v2.DescribeSubnetsOutput{Subnets: []ec2types.Subnet{
				{SubnetId: aws.String("subnet-aaa"), AvailabilityZone: aws.String("us-east-1a")},
			}},
			expectDescribeCall: true,
			expectedSubnets:    []string{"subnet-aaa"},
		},
		{
			name:               "When metadata is missing for a subnet, it should keep the unknown candidate",
			subnetIDs:          []string{"subnet-edge", "subnet-unknown"},
			describeOutput:     &ec2v2.DescribeSubnetsOutput{},
			cachedAZs:          map[string]string{"subnet-edge": "us-east-1-atl-1a"},
			expectDescribeCall: true,
			expectedSubnets:    []string{"subnet-unknown"},
		},
		{
			name:      "When every cached subnet is excluded, it should return an error",
			subnetIDs: []string{"subnet-edge", "subnet-wavelength"},
			cachedAZs: map[string]string{"subnet-edge": "us-east-1-atl-1a", "subnet-wavelength": "us-east-1-wl1-bos-wlz-1"},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			mockCtrl := gomock.NewController(t)
			mockEC2 := awsapi.NewMockEC2API(mockCtrl)

			if tt.expectDescribeCall {
				var uncached []string
				for _, subnetID := range tt.subnetIDs {
					if _, cached := tt.cachedAZs[subnetID]; !cached {
						uncached = append(uncached, subnetID)
					}
				}
				mockEC2.EXPECT().DescribeSubnets(gomock.Any(), &ec2v2.DescribeSubnetsInput{SubnetIds: uncached}).Return(tt.describeOutput, tt.describeErr)
			}

			r := &AWSEndpointServiceReconciler{
				subnetAZCache: tt.cachedAZs,
			}

			gotSubnets, err := r.deduplicateSubnetsByAZ(context.Background(), mockEC2, tt.subnetIDs, "us-east-1")

			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(gotSubnets).To(Equal(tt.expectedSubnets))
		})
	}
}

func TestSelectEndpointSubnets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		region   string
		zones    map[string]string
		expected []string
		excluded []string
	}{
		{name: "When input is empty, it should return no subnets", region: "us-east-1", expected: []string{}},
		{
			name:     "When the region is empty with standard zones, it should preserve same-AZ deduplication",
			zones:    map[string]string{"subnet-aaa": "us-east-1a", "subnet-bbb": "us-east-1a", "subnet-ccc": "us-east-1b"},
			expected: []string{"subnet-aaa", "subnet-ccc"},
		},
		{
			name:     "When the region is empty with edge and unknown zones, it should retain candidates before deduplication",
			zones:    map[string]string{"subnet-edge": "us-east-1-atl-1a", "subnet-wave": "us-east-1-wl1-bos-wlz-1", "subnet-aaa": "", "subnet-bbb": ""},
			expected: []string{"subnet-aaa", "subnet-edge", "subnet-wave"},
		},
		{
			name:     "When standard and edge zones are mixed, it should keep one subnet per standard AZ",
			region:   "us-east-1",
			zones:    map[string]string{"subnet-aaa": "us-east-1a", "subnet-bbb": "us-east-1a", "subnet-ccc": "us-east-1b", "subnet-edge": "us-east-1-atl-1a", "subnet-wave": "us-east-1-wl1-bos-wlz-1"},
			expected: []string{"subnet-aaa", "subnet-ccc"},
			excluded: []string{"subnet-edge (us-east-1-atl-1a)", "subnet-wave (us-east-1-wl1-bos-wlz-1)"},
		},
		{
			name:   "When the region is GovCloud, it should accept its standard AZs",
			region: "us-gov-west-1", zones: map[string]string{"subnet-aaa": "us-gov-west-1a", "subnet-bbb": "us-gov-west-1b"},
			expected: []string{"subnet-aaa", "subnet-bbb"},
		},
		{
			name:   "When the region is China, it should accept its standard AZs",
			region: "cn-north-1", zones: map[string]string{"subnet-aaa": "cn-north-1a", "subnet-bbb": "cn-north-1b"},
			expected: []string{"subnet-aaa", "subnet-bbb"},
		},
		{
			name:     "When known zones do not match the region plus one lowercase letter, it should exclude all of them",
			region:   "us-east-1",
			zones:    map[string]string{"subnet-other": "us-west-2a", "subnet-upper": "us-east-1A", "subnet-extra": "us-east-1aa", "subnet-region": "us-east-1"},
			excluded: []string{"subnet-extra (us-east-1aa)", "subnet-other (us-west-2a)", "subnet-region (us-east-1)", "subnet-upper (us-east-1A)"},
		},
		{
			name:   "When zones are unknown, it should preserve their existing deduplication bucket",
			region: "us-east-1", zones: map[string]string{"subnet-aaa": "", "subnet-bbb": "", "subnet-edge": "us-east-1-atl-1a"},
			expected: []string{"subnet-aaa"}, excluded: []string{"subnet-edge (us-east-1-atl-1a)"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertions := NewGomegaWithT(t)
			var subnetIDs []string
			for subnetID := range test.zones {
				subnetIDs = append(subnetIDs, subnetID)
			}
			var records []string
			logger := funcr.New(func(prefix, args string) { records = append(records, args) }, funcr.Options{})
			selected, err := selectEndpointSubnets(subnetIDs, test.zones, test.region, logger)
			if test.expected == nil {
				assertions.Expect(errors.Is(err, errNoEligibleSubnets)).To(BeTrue())
				assertions.Expect(err.Error()).To(Equal("no eligible subnets for VPC endpoint: excluded subnets: " + strings.Join(test.excluded, ", ")))
			} else {
				assertions.Expect(err).NotTo(HaveOccurred())
			}
			assertions.Expect(selected).To(Equal(test.expected))
			assertions.Expect(records).To(HaveLen(len(test.excluded)))
			for _, excluded := range test.excluded {
				subnetID, zone, _ := strings.Cut(excluded, " (")
				assertions.Expect(strings.Join(records, "\n")).To(ContainSubstring(subnetID))
				assertions.Expect(strings.Join(records, "\n")).To(ContainSubstring(strings.TrimSuffix(zone, ")")))
			}
		})
	}
}

func TestEnsureVPCEndpoint(t *testing.T) {
	for _, desired := range [][]string{{}, {"subnet-single"}} {
		t.Run(fmt.Sprintf("When AWS configuration is missing with %d subnets, it should preserve the configuration error without EC2 calls", len(desired)), func(t *testing.T) {
			assertions := NewGomegaWithT(t)
			mockEC2 := awsapi.NewMockEC2API(gomock.NewController(t))
			service := &hyperv1.AWSEndpointService{Spec: hyperv1.AWSEndpointServiceSpec{SubnetIDs: desired}}
			reconciler := &AWSEndpointServiceReconciler{}
			endpointID, entries, err := reconciler.ensureVPCEndpoint(t.Context(), mockEC2, service, &hyperv1.HostedControlPlane{}, logr.Discard())
			assertions.Expect(err).To(MatchError("AWS platform information not provided in HostedControlPlane"))
			assertions.Expect(endpointID).To(BeEmpty())
			assertions.Expect(entries).To(BeEmpty())
			assertions.Expect(service.Spec.SubnetIDs).To(Equal(desired))
		})
	}

	tests := []struct {
		name        string
		desired     []string
		zones       map[string]string
		warm        bool
		lookupError bool
		missingAWS  bool
		emptyRegion bool
		expected    []string
		expectError bool
	}{
		{
			name:    "When AWS configuration is missing with a cold cache, it should preserve configuration validation and existing endpoint updates",
			desired: []string{"subnet-bbb", "subnet-aaa", "subnet-edge"}, missingAWS: true,
			zones:    map[string]string{"subnet-bbb": "us-east-1a", "subnet-aaa": "us-east-1a", "subnet-edge": "us-east-1-atl-1a"},
			expected: []string{"subnet-aaa", "subnet-edge"},
		},
		{
			name:    "When AWS configuration is missing with a warm cache, it should preserve configuration validation and existing endpoint updates",
			desired: []string{"subnet-bbb", "subnet-aaa", "subnet-edge"}, missingAWS: true, warm: true,
			zones:    map[string]string{"subnet-bbb": "us-east-1a", "subnet-aaa": "us-east-1a", "subnet-edge": "us-east-1-atl-1a"},
			expected: []string{"subnet-aaa", "subnet-edge"},
		},
		{
			name:    "When AWS configuration has an empty region, it should retain candidates before deduplication",
			desired: []string{"subnet-bbb", "subnet-aaa", "subnet-edge"}, emptyRegion: true,
			zones:    map[string]string{"subnet-bbb": "us-east-1a", "subnet-aaa": "us-east-1a", "subnet-edge": "us-east-1-atl-1a"},
			expected: []string{"subnet-aaa", "subnet-edge"},
		},
		{
			name:     "When a cold cache has standard and edge subnets, it should use only standard subnets",
			desired:  []string{"subnet-edge", "subnet-aaa", "subnet-bbb"},
			zones:    map[string]string{"subnet-edge": "us-east-1-atl-1a", "subnet-aaa": "us-east-1a", "subnet-bbb": "us-east-1b"},
			expected: []string{"subnet-aaa", "subnet-bbb"},
		},
		{
			name:    "When a warm cache has standard and edge subnets, it should use only standard subnets",
			desired: []string{"subnet-edge", "subnet-aaa", "subnet-bbb"}, warm: true,
			zones:    map[string]string{"subnet-edge": "us-east-1-wl1-bos-wlz-1", "subnet-aaa": "us-east-1a", "subnet-bbb": "us-east-1b"},
			expected: []string{"subnet-aaa", "subnet-bbb"},
		},
		{
			name:    "When all inspected subnets are excluded, it should stop before any endpoint operation",
			desired: []string{"subnet-edge", "subnet-wave"},
			zones:   map[string]string{"subnet-edge": "us-east-1-atl-1a", "subnet-wave": "us-east-1-wl1-bos-wlz-1"}, expectError: true,
		},
		{
			name:    "When all cached subnets are excluded, it should stop before any endpoint operation",
			desired: []string{"subnet-edge", "subnet-wave"}, warm: true,
			zones: map[string]string{"subnet-edge": "us-east-1-atl-1a", "subnet-wave": "us-east-1-wl1-bos-wlz-1"}, expectError: true,
		},
		{
			name:    "When DescribeSubnets fails with a cached exclusion, it should use the complete original list",
			desired: []string{"subnet-edge", "subnet-aaa"}, lookupError: true,
			zones: map[string]string{"subnet-edge": "us-east-1-atl-1a"}, expected: []string{"subnet-edge", "subnet-aaa"},
		},
		{
			name:    "When zone metadata is missing, it should keep an unknown subnet alongside standard subnets",
			desired: []string{"subnet-edge", "subnet-unknown", "subnet-aaa"},
			zones:   map[string]string{"subnet-edge": "us-east-1-atl-1a", "subnet-aaa": "us-east-1a"}, expected: []string{"subnet-aaa", "subnet-unknown"},
		},
		{
			name:    "When only one cached edge subnet is desired, it should preserve the shortcut",
			desired: []string{"subnet-edge"}, warm: true,
			zones: map[string]string{"subnet-edge": "us-east-1-atl-1a"}, expected: []string{"subnet-edge"},
		},
		{
			name:    "When no subnet is desired, it should preserve the empty input shortcut",
			desired: []string{}, expected: []string{},
		},
		{
			name:    "When only standard subnets share an AZ, it should preserve deterministic deduplication",
			desired: []string{"subnet-bbb", "subnet-aaa"},
			zones:   map[string]string{"subnet-bbb": "us-east-1a", "subnet-aaa": "us-east-1a"}, expected: []string{"subnet-aaa"},
		},
	}
	for _, test := range tests {
		for _, existing := range []bool{false, true} {
			operation := "create"
			if existing {
				operation = "modify"
			}
			t.Run(test.name+" on "+operation, func(t *testing.T) {
				assertions := NewGomegaWithT(t)
				mockEC2 := awsapi.NewMockEC2API(gomock.NewController(t))
				reconciler := &AWSEndpointServiceReconciler{}
				if test.warm || test.lookupError {
					reconciler.subnetAZCache = make(map[string]string)
					for subnetID, zone := range test.zones {
						reconciler.subnetAZCache[subnetID] = zone
					}
				}
				if !test.warm && len(test.desired) > 1 {
					var uncached []string
					output := &ec2v2.DescribeSubnetsOutput{}
					for _, subnetID := range test.desired {
						if _, cached := reconciler.subnetAZCache[subnetID]; !cached {
							uncached = append(uncached, subnetID)
							if zone, known := test.zones[subnetID]; known {
								output.Subnets = append(output.Subnets, ec2types.Subnet{SubnetId: aws.String(subnetID), AvailabilityZone: aws.String(zone)})
							}
						}
					}
					var lookupErr error
					if test.lookupError {
						lookupErr = errors.New("synthetic DescribeSubnets failure")
					}
					mockEC2.EXPECT().DescribeSubnets(gomock.Any(), &ec2v2.DescribeSubnetsInput{SubnetIds: uncached}).Return(output, lookupErr)
				}
				service := &hyperv1.AWSEndpointService{
					Spec:   hyperv1.AWSEndpointServiceSpec{SubnetIDs: append([]string{}, test.desired...)},
					Status: hyperv1.AWSEndpointServiceStatus{EndpointServiceName: "synthetic-service", SecurityGroupID: "sg-synthetic"},
				}
				if existing {
					service.Status.EndpointID = "vpce-synthetic"
				}
				hcp := &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{Region: "us-east-1", CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
						VPC: "vpc-synthetic", Subnet: &hyperv1.AWSResourceReference{ID: aws.String("subnet-aaa")},
					}},
				}}}
				if test.missingAWS {
					hcp.Spec.Platform.AWS = nil
				} else if test.emptyRegion {
					hcp.Spec.Platform.AWS.Region = ""
				}
				expectConfigError := test.missingAWS && !existing
				if !test.expectError && !expectConfigError {
					if existing {
						mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), &ec2v2.DescribeVpcEndpointsInput{VpcEndpointIds: []string{"vpce-synthetic"}}).Return(&ec2v2.DescribeVpcEndpointsOutput{VpcEndpoints: []ec2types.VpcEndpoint{{
							VpcEndpointId: aws.String("vpce-synthetic"), ServiceName: aws.String("synthetic-service"), SubnetIds: []string{"subnet-aaa"},
							Groups: []ec2types.SecurityGroupIdentifier{{GroupId: aws.String("sg-synthetic")}},
						}}}, nil)
						var added, removed []string
						foundExisting := false
						for _, subnetID := range test.expected {
							if subnetID == "subnet-aaa" {
								foundExisting = true
							} else {
								added = append(added, subnetID)
							}
						}
						if !foundExisting {
							removed = []string{"subnet-aaa"}
						}
						if len(added) > 0 || len(removed) > 0 {
							mockEC2.EXPECT().ModifyVpcEndpoint(gomock.Any(), &ec2v2.ModifyVpcEndpointInput{
								VpcEndpointId: aws.String("vpce-synthetic"), AddSubnetIds: added, RemoveSubnetIds: removed,
							}).Return(&ec2v2.ModifyVpcEndpointOutput{}, nil)
						}
					} else {
						mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).Return(&ec2v2.DescribeVpcEndpointsOutput{}, nil)
						mockEC2.EXPECT().CreateVpcEndpoint(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, input *ec2v2.CreateVpcEndpointInput, options ...func(*ec2v2.Options)) (*ec2v2.CreateVpcEndpointOutput, error) {
							assertions.Expect(input.SubnetIds).To(Equal(test.expected))
							return &ec2v2.CreateVpcEndpointOutput{VpcEndpoint: &ec2types.VpcEndpoint{VpcEndpointId: aws.String("vpce-synthetic")}}, nil
						})
					}
				}
				endpointID, _, err := reconciler.ensureVPCEndpoint(t.Context(), mockEC2, service, hcp, logr.Discard())
				if test.expectError {
					assertions.Expect(errors.Is(err, errNoEligibleSubnets)).To(BeTrue())
					assertions.Expect(err.Error()).To(Equal("no eligible subnets for VPC endpoint: excluded subnets: subnet-edge (us-east-1-atl-1a), subnet-wave (us-east-1-wl1-bos-wlz-1)"))
					assertions.Expect(endpointID).To(BeEmpty())
					assertions.Expect(service.Spec.SubnetIDs).To(Equal(test.desired))
				} else if expectConfigError {
					assertions.Expect(err).To(MatchError("AWS platform information not provided in HostedControlPlane"))
					assertions.Expect(endpointID).To(BeEmpty())
					assertions.Expect(service.Spec.SubnetIDs).To(Equal(test.expected))
				} else {
					assertions.Expect(err).NotTo(HaveOccurred())
					assertions.Expect(endpointID).To(Equal("vpce-synthetic"))
					assertions.Expect(service.Spec.SubnetIDs).To(Equal(test.expected))
				}
			})
		}
	}
}

func TestReconcileAWSEndpointService(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		noDNS         bool
		unknownName   bool
		private       bool
		cachedZone    bool
		failure       string
		expectedError string
	}{
		{name: "When an endpoint has no DNS entries, it should leave DNS status unchanged", noDNS: true},
		{name: "When the service has no DNS mapping, it should leave DNS status unchanged", unknownName: true},
		{name: "When the zone is uncached, it should cache the zone and remove stale public external names"},
		{name: "When the zone is cached, it should reuse it without a lookup", cachedZone: true},
		{name: "When the endpoint is private, it should reconcile API and OAuth external names", private: true},
		{name: "When zone lookup fails, it should preserve DNS status and return the error", failure: "lookup", expectedError: "zone lookup failed"},
		{name: "When record creation fails, it should preserve DNS status and return the error", failure: "record", expectedError: "record creation failed"},
		{name: "When external service creation fails, it should aggregate the errors", private: true, failure: "create", expectedError: "failed to create external services for private endpoints"},
		{name: "When listing stale services fails, it should return the error", failure: "list", expectedError: "cannot list private external services"},
		{name: "When deleting stale services fails, it should aggregate the errors", failure: "delete", expectedError: "failed to delete private external service"},
		{name: "When endpoint lookup fails, it should stop before DNS reconciliation", failure: "endpoint", expectedError: "endpoint lookup failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertions := NewGomegaWithT(t)
			mockController := gomock.NewController(t)
			ec2Client := awsapi.NewMockEC2API(mockController)
			route53Client := awsapi.NewMockROUTE53API(mockController)
			builder := &clientBuilder{}
			if test.cachedZone {
				builder.setLocalHostedZoneID("zone-test")
			}
			scheme := runtime.NewScheme()
			assertions.Expect(corev1.AddToScheme(scheme)).To(Succeed())
			assertions.Expect(hyperv1.AddToScheme(scheme)).To(Succeed())
			stale := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "stale", Namespace: "clusters-test", Labels: map[string]string{externalPrivateServiceLabel: "true"}}}
			stalePresence := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "stale-presence", Namespace: "clusters-test", Labels: map[string]string{externalPrivateServiceLabel: "false"}}}
			unrelated := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "unrelated", Namespace: "clusters-test"}}
			var created, deleted []string
			kubeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stale, stalePresence, unrelated).WithInterceptorFuncs(interceptor.Funcs{
				Create: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, options ...crclient.CreateOption) error {
					if test.failure == "create" {
						created = append(created, object.GetName())
						return errors.New("service creation failed")
					}
					return client.Create(ctx, object, options...)
				},
				List: func(ctx context.Context, client crclient.WithWatch, list crclient.ObjectList, options ...crclient.ListOption) error {
					if test.failure == "list" {
						return errors.New("service list failed")
					}
					return client.List(ctx, list, options...)
				},
				Delete: func(ctx context.Context, client crclient.WithWatch, object crclient.Object, options ...crclient.DeleteOption) error {
					if test.failure == "delete" {
						deleted = append(deleted, object.GetName())
						return errors.New("service deletion failed")
					}
					return client.Delete(ctx, object, options...)
				},
			}).Build()
			reconciler := &AWSEndpointServiceReconciler{Client: kubeClient, CreateOrUpdateProvider: upsert.New(false), awsClientBuilder: builder}
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters-test", UID: "hcp-test"},
				Spec:       hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{EndpointAccess: hyperv1.Public}}},
			}
			if test.private {
				hcp.Spec.Platform.AWS.EndpointAccess = hyperv1.Private
				hcp.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{
					{Service: hyperv1.APIServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route, Route: &hyperv1.RoutePublishingStrategy{Hostname: "api.example.com"}}},
					{Service: hyperv1.OAuthServer, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route, Route: &hyperv1.RoutePublishingStrategy{Hostname: "oauth.example.com"}}},
				}
			}
			service := &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: hcp.Namespace},
				Status:     hyperv1.AWSEndpointServiceStatus{EndpointID: "vpce-test", EndpointServiceName: "service-test", SecurityGroupID: "sg-test", DNSNames: []string{"previous"}, DNSZoneID: "previous-zone"},
			}
			if test.unknownName {
				service.Name = "unknown"
			}
			ec2Client.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(&ec2v2.DescribeSecurityGroupsOutput{SecurityGroups: []ec2types.SecurityGroup{{GroupId: aws.String("sg-test")}}}, nil)
			endpoint := ec2types.VpcEndpoint{VpcEndpointId: aws.String("vpce-test"), ServiceName: aws.String("service-test"), Groups: []ec2types.SecurityGroupIdentifier{{GroupId: aws.String("sg-test")}}}
			if !test.noDNS {
				endpoint.DnsEntries = []ec2types.DnsEntry{{DnsName: aws.String("endpoint.example.com")}}
			}
			var endpointError error
			if test.failure == "endpoint" {
				endpointError = errors.New("endpoint lookup failed")
			}
			ec2Client.EXPECT().DescribeVpcEndpoints(gomock.Any(), &ec2v2.DescribeVpcEndpointsInput{VpcEndpointIds: []string{"vpce-test"}}).Return(&ec2v2.DescribeVpcEndpointsOutput{VpcEndpoints: []ec2types.VpcEndpoint{endpoint}}, endpointError)
			if !test.noDNS && !test.unknownName && test.failure != "endpoint" {
				if !test.cachedZone {
					var lookupError error
					if test.failure == "lookup" {
						lookupError = errors.New("zone lookup failed")
					}
					route53Client.EXPECT().ListHostedZones(gomock.Any(), gomock.Any(), gomock.Any()).Return(&route53sdk.ListHostedZonesOutput{HostedZones: []route53types.HostedZone{{Id: aws.String("/hostedzone/zone-test"), Name: aws.String("test.hypershift.local."), Config: &route53types.HostedZoneConfig{PrivateZone: true}}}}, lookupError)
				}
				if test.failure != "lookup" {
					var recordError error
					if test.failure == "record" {
						recordError = errors.New("record creation failed")
					}
					route53Client.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, input *route53sdk.ChangeResourceRecordSetsInput, options ...func(*route53sdk.Options)) (*route53sdk.ChangeResourceRecordSetsOutput, error) {
						assertions.Expect(aws.ToString(input.HostedZoneId)).To(Equal("zone-test"))
						assertions.Expect(input.ChangeBatch.Changes).To(HaveLen(1))
						record := input.ChangeBatch.Changes[0]
						assertions.Expect(record.Action).To(Equal(route53types.ChangeActionUpsert))
						assertions.Expect(aws.ToString(record.ResourceRecordSet.Name)).To(Equal("api.test.hypershift.local"))
						assertions.Expect(record.ResourceRecordSet.Type).To(Equal(route53types.RRTypeCname))
						assertions.Expect(record.ResourceRecordSet.ResourceRecords).To(Equal([]route53types.ResourceRecord{{Value: aws.String("endpoint.example.com")}}))
						return &route53sdk.ChangeResourceRecordSetsOutput{}, recordError
					})
				}
			}
			ctx := logr.NewContext(t.Context(), logr.Discard())
			err := reconciler.reconcileAWSEndpointService(ctx, service, hcp, ec2Client, route53Client)
			if test.expectedError != "" {
				assertions.Expect(err).To(MatchError(ContainSubstring(test.expectedError)))
				if test.failure == "create" {
					assertions.Expect(created).To(ConsistOf("kube-apiserver-private-external", "oauth-private-external"))
					for _, serviceType := range []string{"api", "oauth"} {
						assertions.Expect(err.Error()).To(ContainSubstring("failed to reconcile " + serviceType + " external service: service creation failed"))
					}
				}
				if test.failure == "delete" {
					assertions.Expect(deleted).To(ConsistOf(stale.Name, stalePresence.Name))
					for _, name := range deleted {
						assertions.Expect(err.Error()).To(ContainSubstring("failed to delete private external service " + name + ": service deletion failed"))
					}
				}
			} else {
				assertions.Expect(err).NotTo(HaveOccurred())
			}
			if test.noDNS || test.unknownName || test.failure == "lookup" || test.failure == "record" || test.failure == "endpoint" {
				assertions.Expect(service.Status.DNSNames).To(Equal([]string{"previous"}))
				assertions.Expect(service.Status.DNSZoneID).To(Equal("previous-zone"))
			} else {
				assertions.Expect(service.Status.DNSNames).To(Equal([]string{"api.test.hypershift.local"}))
				assertions.Expect(service.Status.DNSZoneID).To(Equal("zone-test"))
				assertions.Expect(builder.getLocalHostedZoneID()).To(Equal("zone-test"))
			}
			assertions.Expect(kubeClient.Get(ctx, crclient.ObjectKeyFromObject(unrelated), &corev1.Service{})).To(Succeed())
			for _, staleService := range []*corev1.Service{stale, stalePresence} {
				lookupError := kubeClient.Get(ctx, crclient.ObjectKeyFromObject(staleService), &corev1.Service{})
				if test.expectedError == "" && !test.noDNS && !test.unknownName && !test.private {
					assertions.Expect(apierrors.IsNotFound(lookupError)).To(BeTrue())
				} else {
					assertions.Expect(lookupError).NotTo(HaveOccurred())
				}
			}
			if test.expectedError == "" && !test.noDNS && !test.unknownName {
				if test.private {
					for name, hostname := range map[string]string{"kube-apiserver-private-external": "api.example.com", "oauth-private-external": "oauth.example.com"} {
						external := &corev1.Service{}
						assertions.Expect(kubeClient.Get(ctx, crclient.ObjectKey{Namespace: hcp.Namespace, Name: name}, external)).To(Succeed())
						assertions.Expect(external.Spec.Type).To(Equal(corev1.ServiceTypeExternalName))
						assertions.Expect(external.Spec.ExternalName).To(Equal("endpoint.example.com"))
						assertions.Expect(external.Labels[externalPrivateServiceLabel]).To(Equal("true"))
						assertions.Expect(external.Annotations[hyperv1.ExternalDNSHostnameAnnotation]).To(Equal(hostname))
						assertions.Expect(external.OwnerReferences).To(HaveLen(1))
						assertions.Expect(external.OwnerReferences[0].UID).To(Equal(hcp.UID))
						assertions.Expect(external.OwnerReferences[0].Name).To(Equal(hcp.Name))
						assertions.Expect(external.OwnerReferences[0].Kind).To(Equal("HostedControlPlane"))
						assertions.Expect(external.OwnerReferences[0].APIVersion).To(Equal("hypershift.openshift.io/v1beta1"))
						assertions.Expect(external.OwnerReferences[0].Controller).To(Equal(aws.Bool(true)))
					}
				}
			}
		})
	}
}

func TestRecordForService(t *testing.T) {
	testCases := []struct {
		name           string
		in             *hyperv1.AWSEndpointService
		serviceMapping []hyperv1.ServicePublishingStrategyMapping
		expected       []string
	}{
		{
			name: "Unknown service, no entry",
			in:   &hyperv1.AWSEndpointService{ObjectMeta: metav1.ObjectMeta{Name: "unknown"}},
		},
		{
			name:     "KAS service gets api entry",
			in:       &hyperv1.AWSEndpointService{ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private"}},
			expected: []string{"api"},
		},
		{
			name: "Router service gets api and apps entry when kas is exposed through route",
			in:   &hyperv1.AWSEndpointService{ObjectMeta: metav1.ObjectMeta{Name: "private-router"}},
			serviceMapping: []hyperv1.ServicePublishingStrategyMapping{{
				Service:                   hyperv1.APIServer,
				ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route},
			}},
			expected: []string{"api", "*.apps"},
		},
		{
			name:     "Router service gets apps entry only when kas is not exposed through route",
			in:       &hyperv1.AWSEndpointService{ObjectMeta: metav1.ObjectMeta{Name: "private-router"}},
			expected: []string{"*.apps"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{Spec: hyperv1.HostedControlPlaneSpec{Services: tc.serviceMapping}}
			actual := recordsForService(tc.in, hcp)
			if diff := cmp.Diff(actual, tc.expected); diff != "" {
				t.Errorf("actual differs from expected: %s", diff)
			}
		})
	}
}

func TestDiffPermissions(t *testing.T) {
	r := func(desc, cidr string) ec2types.IpRange {
		return ec2types.IpRange{
			Description: aws.String(desc),
			CidrIp:      aws.String(cidr),
		}
	}

	p := func(from, to int32, protocol string, ranges ...ec2types.IpRange) ec2types.IpPermission {
		return ec2types.IpPermission{
			FromPort:   aws.Int32(from),
			ToPort:     aws.Int32(to),
			IpProtocol: aws.String(protocol),
			IpRanges:   ranges,
		}
	}

	pp := func(perms ...ec2types.IpPermission) []ec2types.IpPermission {
		return perms
	}

	tests := []struct {
		actual   []ec2types.IpPermission
		required []ec2types.IpPermission
		expected []ec2types.IpPermission
	}{
		{
			actual: pp(),
			required: pp(
				p(100, 200, "tcp", r("test1", "1.1.1.1/32")),
				p(300, 400, "udp", r("test2", "2.2.2.2/16"), r("test3", "3.3.3.3/24")),
			),
			expected: pp(
				p(100, 200, "tcp", r("test1", "1.1.1.1/32")),
				p(300, 400, "udp", r("test2", "2.2.2.2/16"), r("test3", "3.3.3.3/24")),
			),
		},
		{
			actual: pp(
				p(50000, 60000, "tcp", r("", "10.0.0.0/16")),
				p(60000, 70000, "udp", r("test", "0.0.0.0/0")),
			),
			required: pp(
				p(50000, 60000, "tcp", r("", "10.0.0.0/16")),
			),
			expected: pp(),
		},
		{
			actual: pp(
				p(100, 200, "tcp", r("one", "10.0.0.0/16"), r("two", "127.0.0.1/32")),
				p(100, 200, "udp", r("one", "10.0.0.0/16"), r("two", "127.0.0.1/32")),
				p(300, 400, "tcp", r("one", "10.0.0.0/16")),
			),
			required: pp(
				p(100, 200, "tcp", r("one", "10.0.0.0/16"), r("two", "127.0.0.1/32")),
				p(100, 200, "udp", r("one", "10.0.0.0/16"), r("two", "127.0.0.1/32")),
				p(300, 400, "tcp", r("one", "10.0.0.0/16")),
				p(300, 400, "udp", r("one", "10.0.0.0/16")),
			),
			expected: pp(
				p(300, 400, "udp", r("one", "10.0.0.0/16")),
			),
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("test-%d", i), func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := diffPermissions(test.actual, test.required)
			g.Expect(result).To(Equal(test.expected))
		})
	}
}

func TestReconcileDeletion(t *testing.T) {
	now := metav1.NewTime(time.Now())

	ingressPermission := ec2types.IpPermission{
		FromPort:   aws.Int32(6443),
		ToPort:     aws.Int32(6443),
		IpProtocol: aws.String("tcp"),
		IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/16")}},
	}
	egressPermission := ec2types.IpPermission{
		FromPort:   aws.Int32(0),
		ToPort:     aws.Int32(65535),
		IpProtocol: aws.String("-1"),
		IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
	}

	testCases := []struct {
		name            string
		awsEndpointSvc  *hyperv1.AWSEndpointService
		extraObjects    []crclient.Object
		setupMocks      func(ctrl *gomock.Controller) *MockawsClientProvider
		expectError     bool
		expectFinalizer bool
		expectRequeue   bool
	}{
		{
			name: "When all AWS resources are cleaned up successfully it should remove the finalizer",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "private-router",
					Namespace:         "clusters-test",
					DeletionTimestamp: &now,
					Finalizers:        []string{finalizer},
				},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointID:      "vpce-12345",
					SecurityGroupID: "sg-12345",
					DNSNames:        []string{"api.example.com"},
					DNSZoneID:       "Z1234567890",
				},
			},
			setupMocks: func(mockCtrl *gomock.Controller) *MockawsClientProvider {
				mockBuilder := NewMockawsClientProvider(mockCtrl)
				mockRoute53 := awsapi.NewMockROUTE53API(mockCtrl)
				mockEC2 := awsapi.NewMockEC2API(mockCtrl)
				// VPC endpoint: delete succeeds, describe returns not found
				mockEC2.EXPECT().DeleteVpcEndpoints(gomock.Any(), gomock.Any()).Return(&ec2v2.DeleteVpcEndpointsOutput{}, nil)
				mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).Return(nil, &smithy.GenericAPIError{Code: "InvalidVpcEndpointId.NotFound", Message: "not found"})
				// Security group exists and can be cleaned up
				mockEC2.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(&ec2v2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{{
						GroupId:             aws.String("sg-12345"),
						IpPermissions:       []ec2types.IpPermission{ingressPermission},
						IpPermissionsEgress: []ec2types.IpPermission{egressPermission},
					}},
				}, nil)
				mockEC2.EXPECT().RevokeSecurityGroupIngress(gomock.Any(), gomock.Any()).Return(&ec2v2.RevokeSecurityGroupIngressOutput{}, nil)
				mockEC2.EXPECT().RevokeSecurityGroupEgress(gomock.Any(), gomock.Any()).Return(&ec2v2.RevokeSecurityGroupEgressOutput{}, nil)
				mockEC2.EXPECT().DeleteSecurityGroup(gomock.Any(), gomock.Any()).Return(&ec2v2.DeleteSecurityGroupOutput{}, nil)
				mockBuilder.EXPECT().getClients(gomock.Any()).Return(mockEC2, mockRoute53, nil)
				// DNS record exists and can be deleted
				mockRoute53.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any()).Return(
					&route53sdk.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{{
							Name: aws.String("api.example.com."),
							Type: route53types.RRTypeCname,
							TTL:  aws.Int64(300),
							ResourceRecords: []route53types.ResourceRecord{
								{Value: aws.String("vpce-12345.vpce-svc.us-east-1.vpce.amazonaws.com")},
							},
						}},
					}, nil)
				mockRoute53.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any()).Return(
					&route53sdk.ChangeResourceRecordSetsOutput{}, nil)
				return mockBuilder
			},
			expectError:     false,
			expectFinalizer: false,
		},
		{
			name: "When status has no AWS resources it should remove the finalizer",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "private-router",
					Namespace:         "clusters-test",
					DeletionTimestamp: &now,
					Finalizers:        []string{finalizer},
				},
				Status: hyperv1.AWSEndpointServiceStatus{},
			},
			setupMocks: func(mockCtrl *gomock.Controller) *MockawsClientProvider {
				mockBuilder := NewMockawsClientProvider(mockCtrl)
				mockEC2 := awsapi.NewMockEC2API(mockCtrl)
				mockRoute53 := awsapi.NewMockROUTE53API(mockCtrl)
				mockBuilder.EXPECT().getClients(gomock.Any()).Return(mockEC2, mockRoute53, nil)
				return mockBuilder
			},
			expectError:     false,
			expectFinalizer: false,
		},
		{
			name: "When HCP exists after restart it should initialize clients and complete deletion",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "private-router",
					Namespace:         "clusters-test",
					DeletionTimestamp: &now,
					Finalizers:        []string{finalizer},
				},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointID:      "vpce-12345",
					SecurityGroupID: "sg-12345",
					DNSNames:        []string{"api.example.com"},
					DNSZoneID:       "Z1234567890",
				},
			},
			extraObjects: []crclient.Object{
				&hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-hcp",
						Namespace: "clusters-test",
					},
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							AWS: &hyperv1.AWSPlatformSpec{},
						},
					},
				},
			},
			setupMocks: func(mockCtrl *gomock.Controller) *MockawsClientProvider {
				mockBuilder := NewMockawsClientProvider(mockCtrl)
				mockRoute53 := awsapi.NewMockROUTE53API(mockCtrl)
				mockEC2 := awsapi.NewMockEC2API(mockCtrl)
				mockEC2.EXPECT().DeleteVpcEndpoints(gomock.Any(), gomock.Any()).Return(&ec2v2.DeleteVpcEndpointsOutput{}, nil)
				mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).Return(nil, &smithy.GenericAPIError{Code: "InvalidVpcEndpointId.NotFound", Message: "not found"})
				mockEC2.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(&ec2v2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{{
						GroupId:             aws.String("sg-12345"),
						IpPermissions:       []ec2types.IpPermission{ingressPermission},
						IpPermissionsEgress: []ec2types.IpPermission{egressPermission},
					}},
				}, nil)
				mockEC2.EXPECT().RevokeSecurityGroupIngress(gomock.Any(), gomock.Any()).Return(&ec2v2.RevokeSecurityGroupIngressOutput{}, nil)
				mockEC2.EXPECT().RevokeSecurityGroupEgress(gomock.Any(), gomock.Any()).Return(&ec2v2.RevokeSecurityGroupEgressOutput{}, nil)
				mockEC2.EXPECT().DeleteSecurityGroup(gomock.Any(), gomock.Any()).Return(&ec2v2.DeleteSecurityGroupOutput{}, nil)
				// Best-effort initialization: HCP exists, so initializeWithHCP is called.
				mockBuilder.EXPECT().initializeWithHCP(gomock.Any(), gomock.Any())
				mockBuilder.EXPECT().getClients(gomock.Any()).Return(mockEC2, mockRoute53, nil)
				mockRoute53.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any()).Return(
					&route53sdk.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{{
							Name: aws.String("api.example.com."),
							Type: route53types.RRTypeCname,
							TTL:  aws.Int64(300),
							ResourceRecords: []route53types.ResourceRecord{
								{Value: aws.String("vpce-12345.vpce-svc.us-east-1.vpce.amazonaws.com")},
							},
						}},
					}, nil)
				mockRoute53.EXPECT().ChangeResourceRecordSets(gomock.Any(), gomock.Any()).Return(
					&route53sdk.ChangeResourceRecordSetsOutput{}, nil)
				return mockBuilder
			},
			expectError:     false,
			expectFinalizer: false,
		},
		{
			name: "When VPC endpoint deletion fails it should return error and preserve the finalizer",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "private-router",
					Namespace:         "clusters-test",
					DeletionTimestamp: &now,
					Finalizers:        []string{finalizer},
				},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointID: "vpce-12345",
				},
			},
			setupMocks: func(mockCtrl *gomock.Controller) *MockawsClientProvider {
				mockBuilder := NewMockawsClientProvider(mockCtrl)
				mockEC2 := awsapi.NewMockEC2API(mockCtrl)
				mockRoute53 := awsapi.NewMockROUTE53API(mockCtrl)
				mockEC2.EXPECT().DeleteVpcEndpoints(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("throttling"))
				mockBuilder.EXPECT().getClients(gomock.Any()).Return(mockEC2, mockRoute53, nil)
				return mockBuilder
			},
			expectError:     true,
			expectFinalizer: true,
		},
		{
			name: "When security group deletion returns DependencyViolation it should requeue without error",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "private-router",
					Namespace:         "clusters-test",
					DeletionTimestamp: &now,
					Finalizers:        []string{finalizer},
				},
				Status: hyperv1.AWSEndpointServiceStatus{
					SecurityGroupID: "sg-12345",
				},
			},
			setupMocks: func(mockCtrl *gomock.Controller) *MockawsClientProvider {
				mockBuilder := NewMockawsClientProvider(mockCtrl)
				mockEC2 := awsapi.NewMockEC2API(mockCtrl)
				mockRoute53 := awsapi.NewMockROUTE53API(mockCtrl)
				mockEC2.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(&ec2v2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{{
						GroupId:             aws.String("sg-12345"),
						IpPermissions:       []ec2types.IpPermission{ingressPermission},
						IpPermissionsEgress: []ec2types.IpPermission{egressPermission},
					}},
				}, nil)
				mockEC2.EXPECT().RevokeSecurityGroupIngress(gomock.Any(), gomock.Any()).Return(&ec2v2.RevokeSecurityGroupIngressOutput{}, nil)
				mockEC2.EXPECT().RevokeSecurityGroupEgress(gomock.Any(), gomock.Any()).Return(&ec2v2.RevokeSecurityGroupEgressOutput{}, nil)
				mockEC2.EXPECT().DeleteSecurityGroup(gomock.Any(), gomock.Any()).Return(nil, &smithy.GenericAPIError{Code: "DependencyViolation", Message: "resource has a dependent object"})
				mockBuilder.EXPECT().getClients(gomock.Any()).Return(mockEC2, mockRoute53, nil)
				return mockBuilder
			},
			expectError:     false,
			expectRequeue:   true,
			expectFinalizer: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			mockCtrl := gomock.NewController(t)
			mockBuilder := tc.setupMocks(mockCtrl)

			scheme := runtime.NewScheme()
			_ = hyperv1.AddToScheme(scheme)

			objects := append([]crclient.Object{tc.awsEndpointSvc}, tc.extraObjects...)
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			reconciler := &AWSEndpointServiceReconciler{
				Client:           fakeClient,
				awsClientBuilder: mockBuilder,
			}

			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log.WithName("test"))
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      tc.awsEndpointSvc.Name,
					Namespace: tc.awsEndpointSvc.Namespace,
				},
			}

			result, err := reconciler.Reconcile(ctx, req)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			if tc.expectRequeue {
				g.Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			}

			// Verify finalizer state on the persisted object.
			// When the finalizer is removed from an object with DeletionTimestamp,
			// the fake client deletes the object (simulating garbage collection).
			updatedService := &hyperv1.AWSEndpointService{}
			getErr := fakeClient.Get(ctx, types.NamespacedName{
				Name:      tc.awsEndpointSvc.Name,
				Namespace: tc.awsEndpointSvc.Namespace,
			}, updatedService)
			if tc.expectFinalizer {
				g.Expect(getErr).ToNot(HaveOccurred(), "object should still exist when finalizer is preserved")
				g.Expect(controllerutil.ContainsFinalizer(updatedService, finalizer)).To(BeTrue())
			} else {
				// Object was deleted after finalizer removal — this confirms the
				// finalizer was successfully removed.
				g.Expect(getErr).To(HaveOccurred(), "object should be deleted after finalizer removal")
			}
		})
	}
}

// TestReconcileDeletion_AfterControllerRestart verifies the fix for OCPBUGS-74960.
//
// When the controller restarts, a new clientBuilder is created in SetupWithManager
// with initialized=false. The deletion path now attempts best-effort initialization
// by listing HostedControlPlanes in the namespace. When the HCP is not found (already
// deleted), getClients returns "clients not initialized". The fix ensures the
// reconciler returns an error and preserves the finalizer so that AWS resources are
// not orphaned.
func TestReconcileDeletion_AfterControllerRestart(t *testing.T) {
	g := NewGomegaWithT(t)
	now := metav1.NewTime(time.Now())

	scheme := runtime.NewScheme()
	_ = hyperv1.AddToScheme(scheme)

	awsEndpointSvc := &hyperv1.AWSEndpointService{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "private-router",
			Namespace:         "clusters-test",
			DeletionTimestamp: &now,
			Finalizers:        []string{finalizer},
		},
		Status: hyperv1.AWSEndpointServiceStatus{
			SecurityGroupID: "sg-12345",
			EndpointID:      "vpce-12345",
			DNSNames:        []string{"api.example.com"},
			DNSZoneID:       "Z1234567890",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(awsEndpointSvc).
		Build()

	// Simulate a controller restart: SetupWithManager creates a fresh
	// clientBuilder{} (initialized=false). The deletion path attempts best-effort
	// initialization by listing HCPs, but none exist here, so getClients still
	// returns "clients not initialized".
	restartedReconciler := &AWSEndpointServiceReconciler{
		Client:           fakeClient,
		awsClientBuilder: &clientBuilder{}, // fresh, uninitialized — as created by SetupWithManager
	}

	ctx := ctrl.LoggerInto(context.Background(), ctrl.Log.WithName("test"))
	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      awsEndpointSvc.Name,
			Namespace: awsEndpointSvc.Namespace,
		},
	}

	_, err := restartedReconciler.Reconcile(ctx, req)
	// The reconciler must return an error so controller-runtime retries.
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("failed to get AWS clients"))

	// The finalizer must be preserved so the AWS resources (sg-12345, vpce-12345,
	// api.example.com in zone Z1234567890) are not orphaned.
	updatedService := &hyperv1.AWSEndpointService{}
	getErr := fakeClient.Get(ctx, types.NamespacedName{
		Name:      awsEndpointSvc.Name,
		Namespace: awsEndpointSvc.Namespace,
	}, updatedService)
	g.Expect(getErr).ToNot(HaveOccurred(), "object should still exist when finalizer is preserved")
	g.Expect(controllerutil.ContainsFinalizer(updatedService, finalizer)).To(BeTrue())
}

func TestDeleteSecurityGroup(t *testing.T) {
	sgID := "sg-12345"
	ingressPermission := ec2types.IpPermission{
		FromPort:   aws.Int32(6443),
		ToPort:     aws.Int32(6443),
		IpProtocol: aws.String("tcp"),
		IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/16")}},
	}
	egressPermission := ec2types.IpPermission{
		FromPort:   aws.Int32(0),
		ToPort:     aws.Int32(65535),
		IpProtocol: aws.String("-1"),
		IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
	}

	sgWithPermissions := &ec2v2.DescribeSecurityGroupsOutput{
		SecurityGroups: []ec2types.SecurityGroup{{
			GroupId:             aws.String(sgID),
			IpPermissions:       []ec2types.IpPermission{ingressPermission},
			IpPermissionsEgress: []ec2types.IpPermission{egressPermission},
		}},
	}

	testCases := []struct {
		name                  string
		setupEC2Mock          func(*gomock.Controller) *awsapi.MockEC2API
		expectedError         bool
		expectedErrorContains string
		expectedSentinel      error
	}{
		{
			name: "When security group is deleted successfully it should complete without error",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(sgWithPermissions, nil)
				m.EXPECT().RevokeSecurityGroupIngress(gomock.Any(), gomock.Any()).Return(&ec2v2.RevokeSecurityGroupIngressOutput{}, nil)
				m.EXPECT().RevokeSecurityGroupEgress(gomock.Any(), gomock.Any()).Return(&ec2v2.RevokeSecurityGroupEgressOutput{}, nil)
				m.EXPECT().DeleteSecurityGroup(gomock.Any(), gomock.Any()).Return(&ec2v2.DeleteSecurityGroupOutput{}, nil)
				return m
			},
			expectedError: false,
		},
		{
			name: "When security group is not found it should return nil",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(nil, &smithy.GenericAPIError{Code: "InvalidGroup.NotFound", Message: "The security group does not exist"})
				return m
			},
			expectedError: false,
		},
		{
			name: "When describe returns empty list it should return nil",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(&ec2v2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{},
				}, nil)
				return m
			},
			expectedError: false,
		},
		{
			name: "When revoking ingress returns DependencyViolation it should return error for retry",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(sgWithPermissions, nil)
				m.EXPECT().RevokeSecurityGroupIngress(gomock.Any(), gomock.Any()).Return(nil, &smithy.GenericAPIError{Code: "DependencyViolation", Message: "resource has a dependent object"})
				return m
			},
			expectedError:    true,
			expectedSentinel: errDependencyViolation,
		},
		{
			name: "When revoking egress returns DependencyViolation it should return error for retry",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(sgWithPermissions, nil)
				m.EXPECT().RevokeSecurityGroupIngress(gomock.Any(), gomock.Any()).Return(&ec2v2.RevokeSecurityGroupIngressOutput{}, nil)
				m.EXPECT().RevokeSecurityGroupEgress(gomock.Any(), gomock.Any()).Return(nil, &smithy.GenericAPIError{Code: "DependencyViolation", Message: "resource has a dependent object"})
				return m
			},
			expectedError:    true,
			expectedSentinel: errDependencyViolation,
		},
		{
			name: "When deleting security group returns DependencyViolation it should return error for retry",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(sgWithPermissions, nil)
				m.EXPECT().RevokeSecurityGroupIngress(gomock.Any(), gomock.Any()).Return(&ec2v2.RevokeSecurityGroupIngressOutput{}, nil)
				m.EXPECT().RevokeSecurityGroupEgress(gomock.Any(), gomock.Any()).Return(&ec2v2.RevokeSecurityGroupEgressOutput{}, nil)
				m.EXPECT().DeleteSecurityGroup(gomock.Any(), gomock.Any()).Return(nil, &smithy.GenericAPIError{Code: "DependencyViolation", Message: "resource has a dependent object"})
				return m
			},
			expectedError:    true,
			expectedSentinel: errDependencyViolation,
		},
		{
			name: "When revoking ingress returns other error it should return that error",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(sgWithPermissions, nil)
				m.EXPECT().RevokeSecurityGroupIngress(gomock.Any(), gomock.Any()).Return(nil, &smithy.GenericAPIError{Code: "InternalError", Message: "internal error"})
				return m
			},
			expectedError:         true,
			expectedErrorContains: "failed to revoke security group " + sgID + " ingress rules",
		},
		{
			name: "When security group has no ingress rules it should skip revoke ingress",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(&ec2v2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{{
						GroupId:             aws.String(sgID),
						IpPermissions:       []ec2types.IpPermission{},
						IpPermissionsEgress: []ec2types.IpPermission{egressPermission},
					}},
				}, nil)
				m.EXPECT().RevokeSecurityGroupEgress(gomock.Any(), gomock.Any()).Return(&ec2v2.RevokeSecurityGroupEgressOutput{}, nil)
				m.EXPECT().DeleteSecurityGroup(gomock.Any(), gomock.Any()).Return(&ec2v2.DeleteSecurityGroupOutput{}, nil)
				return m
			},
			expectedError: false,
		},
		{
			name: "When security group has no egress rules it should skip revoke egress",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(&ec2v2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{{
						GroupId:             aws.String(sgID),
						IpPermissions:       []ec2types.IpPermission{ingressPermission},
						IpPermissionsEgress: []ec2types.IpPermission{},
					}},
				}, nil)
				m.EXPECT().RevokeSecurityGroupIngress(gomock.Any(), gomock.Any()).Return(&ec2v2.RevokeSecurityGroupIngressOutput{}, nil)
				m.EXPECT().DeleteSecurityGroup(gomock.Any(), gomock.Any()).Return(&ec2v2.DeleteSecurityGroupOutput{}, nil)
				return m
			},
			expectedError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			mockCtrl := gomock.NewController(t)
			mockEC2 := tc.setupEC2Mock(mockCtrl)

			reconciler := &AWSEndpointServiceReconciler{}
			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log.WithName("test"))

			err := reconciler.deleteSecurityGroup(ctx, mockEC2, sgID)

			if tc.expectedError {
				g.Expect(err).To(HaveOccurred())
				if tc.expectedErrorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrorContains))
				}
				if tc.expectedSentinel != nil {
					g.Expect(errors.Is(err, tc.expectedSentinel)).To(BeTrue(), "expected error to wrap %v", tc.expectedSentinel)
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

// TestReconcileDeletionSharedVPC documents the remaining SharedVPC leak scenario.
//
// In SharedVPC clusters, the clientBuilder needs role ARNs from the HostedControlPlane
// (hcp.Spec.Platform.AWS.SharedVPC.RolesRef) to assume cross-account roles for EC2
// and Route53 operations. These ARNs are only stored in-memory in the clientBuilder
// after initializeWithHCP is called.
//
// The deletion path now attempts best-effort initialization by listing HCPs in the
// namespace. However, when the operator restarts during deletion and the HCP has
// already been deleted:
//   - The best-effort List finds no HCP, so initializeWithHCP is not called
//   - getClients fails with "clients not initialized"
//   - The fix preserves the finalizer, but retries will never succeed
//   - After 10 minutes, the hypershift-operator force-removes the CPO finalizer,
//     orphaning the security group, VPC endpoint, and DNS records
//
// A proper fix requires persisting the SharedVPC role ARNs in the AWSEndpointService
// status so the deletion path can authenticate independently of the HCP.
func TestReconcileDeletionSharedVPC(t *testing.T) {
	now := metav1.NewTime(time.Now())

	testCases := []struct {
		name                string
		hasHCP              bool
		setupMocks          func(ctrl *gomock.Controller) *MockawsClientProvider
		expectError         bool
		expectErrorContains string
		expectFinalizer     bool
	}{
		{
			// This is the core SharedVPC leak scenario: operator restarted, HCP already
			// deleted, role ARNs lost. The controller errors on every retry because it
			// cannot initialize clients without the HCP. After the 10-minute grace period
			// the hypershift-operator will force-remove the finalizer, leaking resources.
			name:   "When SharedVPC operator restarts with no HCP it should return error and preserve finalizer",
			hasHCP: false,
			setupMocks: func(mockCtrl *gomock.Controller) *MockawsClientProvider {
				mockBuilder := NewMockawsClientProvider(mockCtrl)
				// No HCP found → no initializeWithHCP call.
				// getClients returns "clients not initialized" to simulate uninitialized state.
				mockBuilder.EXPECT().getClients(gomock.Any()).Return(nil, nil, fmt.Errorf("clients not initialized"))
				return mockBuilder
			},
			expectError:         true,
			expectErrorContains: "clients not initialized",
			expectFinalizer:     true,
		},
		{
			// This scenario shows what happens if the clientBuilder is re-initialized
			// without the SharedVPC role ARNs (e.g. a naive fix that initializes without
			// the HCP). getClients proceeds past the "not initialized" check but creates
			// clients with default pod credentials instead of assuming the cross-account
			// SharedVPC roles. In production the subsequent delete calls would fail with
			// AccessDenied because the security group and VPC endpoint live in a
			// different AWS account — a mocked error simulates this deterministically.
			name:   "When SharedVPC client is initialized without role ARNs it should fail to create AWS session",
			hasHCP: false,
			setupMocks: func(mockCtrl *gomock.Controller) *MockawsClientProvider {
				mockBuilder := NewMockawsClientProvider(mockCtrl)
				// No HCP → no initializeWithHCP call.
				// getClients returns a deterministic session-creation failure, simulating
				// what would happen when SharedVPC role ARNs are missing after an HCP deletion.
				mockBuilder.EXPECT().getClients(gomock.Any()).Return(nil, nil, fmt.Errorf("failed to create AWS session: no region configured"))
				return mockBuilder
			},
			expectError:         true,
			expectErrorContains: "failed to",
			expectFinalizer:     true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			mockCtrl := gomock.NewController(t)
			mockBuilder := tc.setupMocks(mockCtrl)

			scheme := runtime.NewScheme()
			_ = hyperv1.AddToScheme(scheme)

			awsEndpointService := &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "private-router",
					Namespace:         "clusters-sharedvpc",
					DeletionTimestamp: &now,
					Finalizers:        []string{finalizer},
				},
				Status: hyperv1.AWSEndpointServiceStatus{
					SecurityGroupID: "sg-shared-12345",
					EndpointID:      "vpce-shared-12345",
					DNSNames:        []string{"api.example.com"},
					DNSZoneID:       "Z1234567890",
				},
			}

			objects := []crclient.Object{awsEndpointService}
			if tc.hasHCP {
				hcp := &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-hcp",
						Namespace: "clusters-sharedvpc",
					},
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							AWS: &hyperv1.AWSPlatformSpec{
								SharedVPC: &hyperv1.AWSSharedVPC{
									RolesRef: hyperv1.AWSSharedVPCRolesRef{
										ControlPlaneARN: "arn:aws:iam::123456789012:role/shared-vpc-endpoint-role",
										IngressARN:      "arn:aws:iam::123456789012:role/shared-vpc-route53-role",
									},
								},
							},
						},
					},
				}
				objects = append(objects, hcp)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				Build()

			reconciler := &AWSEndpointServiceReconciler{
				Client:           fakeClient,
				awsClientBuilder: mockBuilder,
			}

			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log.WithName("test"))
			req := reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "private-router",
					Namespace: "clusters-sharedvpc",
				},
			}

			_, err := reconciler.Reconcile(ctx, req)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.expectErrorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.expectErrorContains))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			// Verify finalizer state
			updatedService := &hyperv1.AWSEndpointService{}
			err = fakeClient.Get(ctx, types.NamespacedName{
				Name:      "private-router",
				Namespace: "clusters-sharedvpc",
			}, updatedService)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(controllerutil.ContainsFinalizer(updatedService, finalizer)).To(Equal(tc.expectFinalizer))
		})
	}
}
