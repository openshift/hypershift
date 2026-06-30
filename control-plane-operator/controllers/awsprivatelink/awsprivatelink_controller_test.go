package awsprivatelink

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/google/go-cmp/cmp"
)

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
