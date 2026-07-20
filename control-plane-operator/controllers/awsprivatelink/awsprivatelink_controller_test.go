package awsprivatelink

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/route53"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/google/go-cmp/cmp"
	"go.uber.org/mock/gomock"
)

// fakeEC2Client implements ec2iface.EC2API for testing.
// Only the methods used by the deletion path are implemented via function fields.
// All other ec2iface.EC2API methods will panic if called (via the embedded nil interface).
type fakeEC2Client struct {
	ec2iface.EC2API

	describeSecurityGroupsFn     func(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error)
	revokeSecurityGroupIngressFn func(ctx context.Context, input *ec2.RevokeSecurityGroupIngressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error)
	revokeSecurityGroupEgressFn  func(ctx context.Context, input *ec2.RevokeSecurityGroupEgressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupEgressOutput, error)
	deleteSecurityGroupFn        func(ctx context.Context, input *ec2.DeleteSecurityGroupInput, opts ...request.Option) (*ec2.DeleteSecurityGroupOutput, error)
	deleteVpcEndpointsFn         func(ctx context.Context, input *ec2.DeleteVpcEndpointsInput, opts ...request.Option) (*ec2.DeleteVpcEndpointsOutput, error)
	describeVpcEndpointsFn       func(ctx context.Context, input *ec2.DescribeVpcEndpointsInput, opts ...request.Option) (*ec2.DescribeVpcEndpointsOutput, error)
}

func (f *fakeEC2Client) DescribeSecurityGroupsWithContext(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
	return f.describeSecurityGroupsFn(ctx, input, opts...)
}

func (f *fakeEC2Client) RevokeSecurityGroupIngressWithContext(ctx context.Context, input *ec2.RevokeSecurityGroupIngressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error) {
	return f.revokeSecurityGroupIngressFn(ctx, input, opts...)
}

func (f *fakeEC2Client) RevokeSecurityGroupEgressWithContext(ctx context.Context, input *ec2.RevokeSecurityGroupEgressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupEgressOutput, error) {
	return f.revokeSecurityGroupEgressFn(ctx, input, opts...)
}

func (f *fakeEC2Client) DeleteSecurityGroupWithContext(ctx context.Context, input *ec2.DeleteSecurityGroupInput, opts ...request.Option) (*ec2.DeleteSecurityGroupOutput, error) {
	return f.deleteSecurityGroupFn(ctx, input, opts...)
}

func (f *fakeEC2Client) DeleteVpcEndpointsWithContext(ctx context.Context, input *ec2.DeleteVpcEndpointsInput, opts ...request.Option) (*ec2.DeleteVpcEndpointsOutput, error) {
	return f.deleteVpcEndpointsFn(ctx, input, opts...)
}

func (f *fakeEC2Client) DescribeVpcEndpointsWithContext(ctx context.Context, input *ec2.DescribeVpcEndpointsInput, opts ...request.Option) (*ec2.DescribeVpcEndpointsOutput, error) {
	return f.describeVpcEndpointsFn(ctx, input, opts...)
}

// fakeRoute53Client implements route53iface.Route53API for testing.
// Only the methods used by the deletion path are implemented via function fields.
type fakeRoute53Client struct {
	route53iface.Route53API

	listResourceRecordSetsFn   func(ctx context.Context, input *route53.ListResourceRecordSetsInput, opts ...request.Option) (*route53.ListResourceRecordSetsOutput, error)
	changeResourceRecordSetsFn func(ctx context.Context, input *route53.ChangeResourceRecordSetsInput, opts ...request.Option) (*route53.ChangeResourceRecordSetsOutput, error)
}

func (f *fakeRoute53Client) ListResourceRecordSetsPagesWithContext(ctx context.Context, input *route53.ListResourceRecordSetsInput, fn func(*route53.ListResourceRecordSetsOutput, bool) bool, opts ...request.Option) error {
	output, err := f.listResourceRecordSetsFn(ctx, input, opts...)
	if err != nil {
		return err
	}
	fn(output, true)
	return nil
}

func (f *fakeRoute53Client) ChangeResourceRecordSetsWithContext(ctx context.Context, input *route53.ChangeResourceRecordSetsInput, opts ...request.Option) (*route53.ChangeResourceRecordSetsOutput, error) {
	return f.changeResourceRecordSetsFn(ctx, input, opts...)
}

func Test_diffIDs(t *testing.T) {
	subnet1 := "1"
	subnet2 := "2"
	subnet3 := "3"
	type args struct {
		desired  []string
		existing []*string
	}
	tests := []struct {
		name        string
		args        args
		wantAdded   []*string
		wantRemoved []*string
	}{
		{
			name: "no subnets, no change",
			args: args{
				desired:  []string{},
				existing: []*string{},
			},
			wantAdded:   nil,
			wantRemoved: nil,
		},
		{
			name: "two subnet, no change",
			args: args{
				desired:  []string{subnet1, subnet2},
				existing: []*string{&subnet1, &subnet2},
			},
			wantAdded:   nil,
			wantRemoved: nil,
		},
		{
			name: "one new subnet",
			args: args{
				desired:  []string{subnet1, subnet2},
				existing: []*string{&subnet1},
			},
			wantAdded:   []*string{&subnet2},
			wantRemoved: nil,
		},
		{
			name: "one removed subnet",
			args: args{
				desired:  []string{subnet1},
				existing: []*string{&subnet1, &subnet2},
			},
			wantAdded:   nil,
			wantRemoved: []*string{&subnet2},
		},
		{
			name: "one removed subnet, one added subnet",
			args: args{
				desired:  []string{subnet1, subnet2},
				existing: []*string{&subnet2, &subnet3},
			},
			wantAdded:   []*string{&subnet1},
			wantRemoved: []*string{&subnet3},
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
	r := func(desc, cidr string) *ec2.IpRange {
		return &ec2.IpRange{
			Description: aws.String(desc),
			CidrIp:      aws.String(cidr),
		}
	}

	p := func(from, to int64, protocol string, ranges ...*ec2.IpRange) *ec2.IpPermission {
		return &ec2.IpPermission{
			FromPort:   aws.Int64(from),
			ToPort:     aws.Int64(to),
			IpProtocol: aws.String(protocol),
			IpRanges:   ranges,
		}
	}

	pp := func(perms ...*ec2.IpPermission) []*ec2.IpPermission {
		return perms
	}

	tests := []struct {
		actual   []*ec2.IpPermission
		required []*ec2.IpPermission
		expected []*ec2.IpPermission
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

type mockEC2Client struct {
	ec2iface.EC2API
	describeSubnetsOutput *ec2.DescribeSubnetsOutput
	describeSubnetsErr    error
}

func (m *mockEC2Client) DescribeSubnetsWithContext(_ context.Context, _ *ec2.DescribeSubnetsInput, _ ...request.Option) (*ec2.DescribeSubnetsOutput, error) {
	return m.describeSubnetsOutput, m.describeSubnetsErr
}

func TestDeduplicateSubnetsByAZ(t *testing.T) {
	tests := []struct {
		name      string
		subnetIDs []string
		cachedAZs map[string]string
		mock      *mockEC2Client
		want      []string
		wantErr   bool
	}{
		{
			name:      "When the subnet list is empty it should return empty",
			subnetIDs: []string{},
			want:      []string{},
		},
		{
			name:      "When there is a single subnet it should return it unchanged",
			subnetIDs: []string{"subnet-1"},
			want:      []string{"subnet-1"},
		},
		{
			name:      "When subnets are in different AZs it should keep all",
			subnetIDs: []string{"subnet-1", "subnet-2"},
			mock: &mockEC2Client{
				describeSubnetsOutput: &ec2.DescribeSubnetsOutput{
					Subnets: []*ec2.Subnet{
						{SubnetId: aws.String("subnet-1"), AvailabilityZone: aws.String("us-east-1a")},
						{SubnetId: aws.String("subnet-2"), AvailabilityZone: aws.String("us-east-1b")},
					},
				},
			},
			want: []string{"subnet-1", "subnet-2"},
		},
		{
			name:      "When subnets are in the same AZ it should pick the lexicographically first",
			subnetIDs: []string{"subnet-b", "subnet-a"},
			mock: &mockEC2Client{
				describeSubnetsOutput: &ec2.DescribeSubnetsOutput{
					Subnets: []*ec2.Subnet{
						{SubnetId: aws.String("subnet-b"), AvailabilityZone: aws.String("us-east-1a")},
						{SubnetId: aws.String("subnet-a"), AvailabilityZone: aws.String("us-east-1a")},
					},
				},
			},
			want: []string{"subnet-a"},
		},
		{
			name:      "When subnets span mixed AZs it should deduplicate only the shared AZ",
			subnetIDs: []string{"subnet-1", "subnet-2", "subnet-3"},
			mock: &mockEC2Client{
				describeSubnetsOutput: &ec2.DescribeSubnetsOutput{
					Subnets: []*ec2.Subnet{
						{SubnetId: aws.String("subnet-1"), AvailabilityZone: aws.String("us-east-1a")},
						{SubnetId: aws.String("subnet-2"), AvailabilityZone: aws.String("us-east-1a")},
						{SubnetId: aws.String("subnet-3"), AvailabilityZone: aws.String("us-east-1b")},
					},
				},
			},
			want: []string{"subnet-1", "subnet-3"},
		},
		{
			name:      "When all subnets are cached it should not call AWS",
			subnetIDs: []string{"subnet-1", "subnet-2"},
			cachedAZs: map[string]string{
				"subnet-1": "us-east-1a",
				"subnet-2": "us-east-1b",
			},
			want: []string{"subnet-1", "subnet-2"},
		},
		{
			name:      "When some subnets are cached it should only fetch uncached ones",
			subnetIDs: []string{"subnet-1", "subnet-2"},
			cachedAZs: map[string]string{
				"subnet-1": "us-east-1a",
			},
			mock: &mockEC2Client{
				describeSubnetsOutput: &ec2.DescribeSubnetsOutput{
					Subnets: []*ec2.Subnet{
						{SubnetId: aws.String("subnet-2"), AvailabilityZone: aws.String("us-east-1b")},
					},
				},
			},
			want: []string{"subnet-1", "subnet-2"},
		},
		{
			name:      "When the DescribeSubnets API fails it should return an error",
			subnetIDs: []string{"subnet-1", "subnet-2"},
			mock: &mockEC2Client{
				describeSubnetsErr: fmt.Errorf("access denied"),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			r := &AWSEndpointServiceReconciler{
				subnetAZCache: tt.cachedAZs,
			}
			var ec2Client ec2iface.EC2API
			if tt.mock != nil {
				ec2Client = tt.mock
			} else {
				ec2Client = &mockEC2Client{}
			}

			got, err := r.deduplicateSubnetsByAZ(context.Background(), ec2Client, tt.subnetIDs)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(got).To(Equal(tt.want))
		})
	}
}
func TestReconcileDeletion(t *testing.T) {
	now := metav1.NewTime(time.Now())

	ingressPermission := &ec2.IpPermission{
		FromPort:   aws.Int64(6443),
		ToPort:     aws.Int64(6443),
		IpProtocol: aws.String("tcp"),
		IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("10.0.0.0/16")}},
	}
	egressPermission := &ec2.IpPermission{
		FromPort:   aws.Int64(0),
		ToPort:     aws.Int64(65535),
		IpProtocol: aws.String("-1"),
		IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
	}

	testCases := []struct {
		name            string
		awsEndpointSvc  *hyperv1.AWSEndpointService
		extraObjects    []crclient.Object
		setupMocks      func(ctrl *gomock.Controller) (*MockawsClientProvider, ec2iface.EC2API, route53iface.Route53API)
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
			setupMocks: func(mockCtrl *gomock.Controller) (*MockawsClientProvider, ec2iface.EC2API, route53iface.Route53API) {
				mockBuilder := NewMockawsClientProvider(mockCtrl)
				fakeRoute53 := &fakeRoute53Client{
					listResourceRecordSetsFn: func(ctx context.Context, input *route53.ListResourceRecordSetsInput, opts ...request.Option) (*route53.ListResourceRecordSetsOutput, error) {
						return &route53.ListResourceRecordSetsOutput{
							ResourceRecordSets: []*route53.ResourceRecordSet{{
								Name: aws.String("api.example.com."),
								Type: aws.String("CNAME"),
								TTL:  aws.Int64(300),
								ResourceRecords: []*route53.ResourceRecord{
									{Value: aws.String("vpce-12345.vpce-svc.us-east-1.vpce.amazonaws.com")},
								},
							}},
						}, nil
					},
					changeResourceRecordSetsFn: func(ctx context.Context, input *route53.ChangeResourceRecordSetsInput, opts ...request.Option) (*route53.ChangeResourceRecordSetsOutput, error) {
						return &route53.ChangeResourceRecordSetsOutput{}, nil
					},
				}
				fakeEC2 := &fakeEC2Client{
					deleteVpcEndpointsFn: func(ctx context.Context, input *ec2.DeleteVpcEndpointsInput, opts ...request.Option) (*ec2.DeleteVpcEndpointsOutput, error) {
						return &ec2.DeleteVpcEndpointsOutput{}, nil
					},
					describeVpcEndpointsFn: func(ctx context.Context, input *ec2.DescribeVpcEndpointsInput, opts ...request.Option) (*ec2.DescribeVpcEndpointsOutput, error) {
						return nil, awserr.New("InvalidVpcEndpointId.NotFound", "not found", nil)
					},
					describeSecurityGroupsFn: func(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
						return &ec2.DescribeSecurityGroupsOutput{
							SecurityGroups: []*ec2.SecurityGroup{{
								GroupId:             aws.String("sg-12345"),
								IpPermissions:       []*ec2.IpPermission{ingressPermission},
								IpPermissionsEgress: []*ec2.IpPermission{egressPermission},
							}},
						}, nil
					},
					revokeSecurityGroupIngressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupIngressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error) {
						return &ec2.RevokeSecurityGroupIngressOutput{}, nil
					},
					revokeSecurityGroupEgressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupEgressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupEgressOutput, error) {
						return &ec2.RevokeSecurityGroupEgressOutput{}, nil
					},
					deleteSecurityGroupFn: func(ctx context.Context, input *ec2.DeleteSecurityGroupInput, opts ...request.Option) (*ec2.DeleteSecurityGroupOutput, error) {
						return &ec2.DeleteSecurityGroupOutput{}, nil
					},
				}
				mockBuilder.EXPECT().getClients().Return(fakeEC2, fakeRoute53, nil)
				return mockBuilder, fakeEC2, fakeRoute53
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
			setupMocks: func(mockCtrl *gomock.Controller) (*MockawsClientProvider, ec2iface.EC2API, route53iface.Route53API) {
				mockBuilder := NewMockawsClientProvider(mockCtrl)
				fakeEC2 := &fakeEC2Client{}
				fakeRoute53 := &fakeRoute53Client{}
				mockBuilder.EXPECT().getClients().Return(fakeEC2, fakeRoute53, nil)
				return mockBuilder, fakeEC2, fakeRoute53
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
			setupMocks: func(mockCtrl *gomock.Controller) (*MockawsClientProvider, ec2iface.EC2API, route53iface.Route53API) {
				mockBuilder := NewMockawsClientProvider(mockCtrl)
				fakeRoute53 := &fakeRoute53Client{
					listResourceRecordSetsFn: func(ctx context.Context, input *route53.ListResourceRecordSetsInput, opts ...request.Option) (*route53.ListResourceRecordSetsOutput, error) {
						return &route53.ListResourceRecordSetsOutput{
							ResourceRecordSets: []*route53.ResourceRecordSet{{
								Name: aws.String("api.example.com."),
								Type: aws.String("CNAME"),
								TTL:  aws.Int64(300),
								ResourceRecords: []*route53.ResourceRecord{
									{Value: aws.String("vpce-12345.vpce-svc.us-east-1.vpce.amazonaws.com")},
								},
							}},
						}, nil
					},
					changeResourceRecordSetsFn: func(ctx context.Context, input *route53.ChangeResourceRecordSetsInput, opts ...request.Option) (*route53.ChangeResourceRecordSetsOutput, error) {
						return &route53.ChangeResourceRecordSetsOutput{}, nil
					},
				}
				fakeEC2 := &fakeEC2Client{
					deleteVpcEndpointsFn: func(ctx context.Context, input *ec2.DeleteVpcEndpointsInput, opts ...request.Option) (*ec2.DeleteVpcEndpointsOutput, error) {
						return &ec2.DeleteVpcEndpointsOutput{}, nil
					},
					describeVpcEndpointsFn: func(ctx context.Context, input *ec2.DescribeVpcEndpointsInput, opts ...request.Option) (*ec2.DescribeVpcEndpointsOutput, error) {
						return nil, awserr.New("InvalidVpcEndpointId.NotFound", "not found", nil)
					},
					describeSecurityGroupsFn: func(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
						return &ec2.DescribeSecurityGroupsOutput{
							SecurityGroups: []*ec2.SecurityGroup{{
								GroupId:             aws.String("sg-12345"),
								IpPermissions:       []*ec2.IpPermission{ingressPermission},
								IpPermissionsEgress: []*ec2.IpPermission{egressPermission},
							}},
						}, nil
					},
					revokeSecurityGroupIngressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupIngressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error) {
						return &ec2.RevokeSecurityGroupIngressOutput{}, nil
					},
					revokeSecurityGroupEgressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupEgressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupEgressOutput, error) {
						return &ec2.RevokeSecurityGroupEgressOutput{}, nil
					},
					deleteSecurityGroupFn: func(ctx context.Context, input *ec2.DeleteSecurityGroupInput, opts ...request.Option) (*ec2.DeleteSecurityGroupOutput, error) {
						return &ec2.DeleteSecurityGroupOutput{}, nil
					},
				}
				// Best-effort initialization: HCP exists, so initializeWithHCP is called.
				mockBuilder.EXPECT().initializeWithHCP(gomock.Any(), gomock.Any())
				mockBuilder.EXPECT().getClients().Return(fakeEC2, fakeRoute53, nil)
				return mockBuilder, fakeEC2, fakeRoute53
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
			setupMocks: func(mockCtrl *gomock.Controller) (*MockawsClientProvider, ec2iface.EC2API, route53iface.Route53API) {
				mockBuilder := NewMockawsClientProvider(mockCtrl)
				fakeEC2 := &fakeEC2Client{
					deleteVpcEndpointsFn: func(ctx context.Context, input *ec2.DeleteVpcEndpointsInput, opts ...request.Option) (*ec2.DeleteVpcEndpointsOutput, error) {
						return nil, fmt.Errorf("throttling")
					},
				}
				fakeRoute53 := &fakeRoute53Client{}
				mockBuilder.EXPECT().getClients().Return(fakeEC2, fakeRoute53, nil)
				return mockBuilder, fakeEC2, fakeRoute53
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
			setupMocks: func(mockCtrl *gomock.Controller) (*MockawsClientProvider, ec2iface.EC2API, route53iface.Route53API) {
				mockBuilder := NewMockawsClientProvider(mockCtrl)
				fakeEC2 := &fakeEC2Client{
					describeSecurityGroupsFn: func(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
						return &ec2.DescribeSecurityGroupsOutput{
							SecurityGroups: []*ec2.SecurityGroup{{
								GroupId:             aws.String("sg-12345"),
								IpPermissions:       []*ec2.IpPermission{ingressPermission},
								IpPermissionsEgress: []*ec2.IpPermission{egressPermission},
							}},
						}, nil
					},
					revokeSecurityGroupIngressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupIngressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error) {
						return &ec2.RevokeSecurityGroupIngressOutput{}, nil
					},
					revokeSecurityGroupEgressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupEgressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupEgressOutput, error) {
						return &ec2.RevokeSecurityGroupEgressOutput{}, nil
					},
					deleteSecurityGroupFn: func(ctx context.Context, input *ec2.DeleteSecurityGroupInput, opts ...request.Option) (*ec2.DeleteSecurityGroupOutput, error) {
						return nil, awserr.New("DependencyViolation", "resource has a dependent object", nil)
					},
				}
				fakeRoute53 := &fakeRoute53Client{}
				mockBuilder.EXPECT().getClients().Return(fakeEC2, fakeRoute53, nil)
				return mockBuilder, fakeEC2, fakeRoute53
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
			mockBuilder, _, _ := tc.setupMocks(mockCtrl)

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
				// Object was deleted after finalizer removal -- this confirms the
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
		awsClientBuilder: &clientBuilder{}, // fresh, uninitialized -- as created by SetupWithManager
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
	ingressPermission := &ec2.IpPermission{
		FromPort:   aws.Int64(6443),
		ToPort:     aws.Int64(6443),
		IpProtocol: aws.String("tcp"),
		IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("10.0.0.0/16")}},
	}
	egressPermission := &ec2.IpPermission{
		FromPort:   aws.Int64(0),
		ToPort:     aws.Int64(65535),
		IpProtocol: aws.String("-1"),
		IpRanges:   []*ec2.IpRange{{CidrIp: aws.String("0.0.0.0/0")}},
	}

	sgWithPermissions := &ec2.DescribeSecurityGroupsOutput{
		SecurityGroups: []*ec2.SecurityGroup{{
			GroupId:             aws.String(sgID),
			IpPermissions:       []*ec2.IpPermission{ingressPermission},
			IpPermissionsEgress: []*ec2.IpPermission{egressPermission},
		}},
	}

	testCases := []struct {
		name                  string
		setupEC2              func() *fakeEC2Client
		expectedError         bool
		expectedErrorContains string
		expectedSentinel      error
	}{
		{
			name: "When security group is deleted successfully it should complete without error",
			setupEC2: func() *fakeEC2Client {
				return &fakeEC2Client{
					describeSecurityGroupsFn: func(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
						return sgWithPermissions, nil
					},
					revokeSecurityGroupIngressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupIngressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error) {
						return &ec2.RevokeSecurityGroupIngressOutput{}, nil
					},
					revokeSecurityGroupEgressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupEgressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupEgressOutput, error) {
						return &ec2.RevokeSecurityGroupEgressOutput{}, nil
					},
					deleteSecurityGroupFn: func(ctx context.Context, input *ec2.DeleteSecurityGroupInput, opts ...request.Option) (*ec2.DeleteSecurityGroupOutput, error) {
						return &ec2.DeleteSecurityGroupOutput{}, nil
					},
				}
			},
			expectedError: false,
		},
		{
			name: "When security group is not found it should return nil",
			setupEC2: func() *fakeEC2Client {
				return &fakeEC2Client{
					describeSecurityGroupsFn: func(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
						return nil, awserr.New("InvalidGroup.NotFound", "The security group does not exist", nil)
					},
				}
			},
			expectedError: false,
		},
		{
			name: "When describe returns empty list it should return nil",
			setupEC2: func() *fakeEC2Client {
				return &fakeEC2Client{
					describeSecurityGroupsFn: func(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
						return &ec2.DescribeSecurityGroupsOutput{
							SecurityGroups: []*ec2.SecurityGroup{},
						}, nil
					},
				}
			},
			expectedError: false,
		},
		{
			name: "When revoking ingress returns DependencyViolation it should return error for retry",
			setupEC2: func() *fakeEC2Client {
				return &fakeEC2Client{
					describeSecurityGroupsFn: func(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
						return sgWithPermissions, nil
					},
					revokeSecurityGroupIngressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupIngressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error) {
						return nil, awserr.New("DependencyViolation", "resource has a dependent object", nil)
					},
				}
			},
			expectedError:    true,
			expectedSentinel: errDependencyViolation,
		},
		{
			name: "When revoking egress returns DependencyViolation it should return error for retry",
			setupEC2: func() *fakeEC2Client {
				return &fakeEC2Client{
					describeSecurityGroupsFn: func(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
						return sgWithPermissions, nil
					},
					revokeSecurityGroupIngressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupIngressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error) {
						return &ec2.RevokeSecurityGroupIngressOutput{}, nil
					},
					revokeSecurityGroupEgressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupEgressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupEgressOutput, error) {
						return nil, awserr.New("DependencyViolation", "resource has a dependent object", nil)
					},
				}
			},
			expectedError:    true,
			expectedSentinel: errDependencyViolation,
		},
		{
			name: "When deleting security group returns DependencyViolation it should return error for retry",
			setupEC2: func() *fakeEC2Client {
				return &fakeEC2Client{
					describeSecurityGroupsFn: func(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
						return sgWithPermissions, nil
					},
					revokeSecurityGroupIngressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupIngressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error) {
						return &ec2.RevokeSecurityGroupIngressOutput{}, nil
					},
					revokeSecurityGroupEgressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupEgressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupEgressOutput, error) {
						return &ec2.RevokeSecurityGroupEgressOutput{}, nil
					},
					deleteSecurityGroupFn: func(ctx context.Context, input *ec2.DeleteSecurityGroupInput, opts ...request.Option) (*ec2.DeleteSecurityGroupOutput, error) {
						return nil, awserr.New("DependencyViolation", "resource has a dependent object", nil)
					},
				}
			},
			expectedError:    true,
			expectedSentinel: errDependencyViolation,
		},
		{
			name: "When revoking ingress returns other error it should return that error",
			setupEC2: func() *fakeEC2Client {
				return &fakeEC2Client{
					describeSecurityGroupsFn: func(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
						return sgWithPermissions, nil
					},
					revokeSecurityGroupIngressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupIngressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error) {
						return nil, awserr.New("InternalError", "internal error", nil)
					},
				}
			},
			expectedError:         true,
			expectedErrorContains: "failed to revoke security group " + sgID + " ingress rules",
		},
		{
			name: "When security group has no ingress rules it should skip revoke ingress",
			setupEC2: func() *fakeEC2Client {
				return &fakeEC2Client{
					describeSecurityGroupsFn: func(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
						return &ec2.DescribeSecurityGroupsOutput{
							SecurityGroups: []*ec2.SecurityGroup{{
								GroupId:             aws.String(sgID),
								IpPermissions:       []*ec2.IpPermission{},
								IpPermissionsEgress: []*ec2.IpPermission{egressPermission},
							}},
						}, nil
					},
					revokeSecurityGroupEgressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupEgressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupEgressOutput, error) {
						return &ec2.RevokeSecurityGroupEgressOutput{}, nil
					},
					deleteSecurityGroupFn: func(ctx context.Context, input *ec2.DeleteSecurityGroupInput, opts ...request.Option) (*ec2.DeleteSecurityGroupOutput, error) {
						return &ec2.DeleteSecurityGroupOutput{}, nil
					},
				}
			},
			expectedError: false,
		},
		{
			name: "When security group has no egress rules it should skip revoke egress",
			setupEC2: func() *fakeEC2Client {
				return &fakeEC2Client{
					describeSecurityGroupsFn: func(ctx context.Context, input *ec2.DescribeSecurityGroupsInput, opts ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
						return &ec2.DescribeSecurityGroupsOutput{
							SecurityGroups: []*ec2.SecurityGroup{{
								GroupId:             aws.String(sgID),
								IpPermissions:       []*ec2.IpPermission{ingressPermission},
								IpPermissionsEgress: []*ec2.IpPermission{},
							}},
						}, nil
					},
					revokeSecurityGroupIngressFn: func(ctx context.Context, input *ec2.RevokeSecurityGroupIngressInput, opts ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error) {
						return &ec2.RevokeSecurityGroupIngressOutput{}, nil
					},
					deleteSecurityGroupFn: func(ctx context.Context, input *ec2.DeleteSecurityGroupInput, opts ...request.Option) (*ec2.DeleteSecurityGroupOutput, error) {
						return &ec2.DeleteSecurityGroupOutput{}, nil
					},
				}
			},
			expectedError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			fakeEC2 := tc.setupEC2()

			reconciler := &AWSEndpointServiceReconciler{}
			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log.WithName("test"))

			err := reconciler.deleteSecurityGroup(ctx, fakeEC2, sgID)

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
				// No HCP found -> no initializeWithHCP call.
				// getClients returns "clients not initialized" to simulate uninitialized state.
				mockBuilder.EXPECT().getClients().Return(nil, nil, fmt.Errorf("clients not initialized"))
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
			// different AWS account -- a mocked error simulates this deterministically.
			name:   "When SharedVPC client is initialized without role ARNs it should fail to create AWS session",
			hasHCP: false,
			setupMocks: func(mockCtrl *gomock.Controller) *MockawsClientProvider {
				mockBuilder := NewMockawsClientProvider(mockCtrl)
				// No HCP -> no initializeWithHCP call.
				// getClients returns a deterministic session-creation failure, simulating
				// what would happen when SharedVPC role ARNs are missing after an HCP deletion.
				mockBuilder.EXPECT().getClients().Return(nil, nil, fmt.Errorf("failed to create AWS session: no region configured"))
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
