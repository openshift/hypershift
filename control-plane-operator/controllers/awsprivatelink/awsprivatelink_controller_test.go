package awsprivatelink

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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

type fakeVPCEndpointEC2Client struct {
	ec2iface.EC2API
	deleteOutput      *ec2.DeleteVpcEndpointsOutput
	deleteErr         error
	describeOutput    *ec2.DescribeVpcEndpointsOutput
	describeErr       error
	lastDeleteInput   *ec2.DeleteVpcEndpointsInput
	lastDescribeInput *ec2.DescribeVpcEndpointsInput
}

func (f *fakeVPCEndpointEC2Client) DeleteVpcEndpointsWithContext(_ context.Context, input *ec2.DeleteVpcEndpointsInput, _ ...request.Option) (*ec2.DeleteVpcEndpointsOutput, error) {
	f.lastDeleteInput = input
	if f.deleteOutput != nil {
		return f.deleteOutput, f.deleteErr
	}
	return &ec2.DeleteVpcEndpointsOutput{}, f.deleteErr
}

func (f *fakeVPCEndpointEC2Client) DescribeVpcEndpointsWithContext(_ context.Context, input *ec2.DescribeVpcEndpointsInput, _ ...request.Option) (*ec2.DescribeVpcEndpointsOutput, error) {
	f.lastDescribeInput = input
	return f.describeOutput, f.describeErr
}

func TestEnsureVPCEndpointDeleted(t *testing.T) {
	t.Parallel()
	const endpointID = "vpce-1"

	tests := []struct {
		name               string
		client             *fakeVPCEndpointEC2Client
		expectDeleted      bool
		expectError        bool
		expectDescribeCall bool
	}{
		{
			name: "When endpoint is not found it should report endpoint as deleted",
			client: &fakeVPCEndpointEC2Client{
				describeErr: awserr.New("InvalidVpcEndpointId.NotFound", "not found", nil),
			},
			expectDeleted:      true,
			expectError:        false,
			expectDescribeCall: true,
		},
		{
			name: "When endpoint still exists it should report endpoint deletion in progress",
			client: &fakeVPCEndpointEC2Client{
				describeOutput: &ec2.DescribeVpcEndpointsOutput{
					VpcEndpoints: []*ec2.VpcEndpoint{
						{
							VpcEndpointId: aws.String(endpointID),
						},
					},
				},
			},
			expectDeleted:      false,
			expectError:        false,
			expectDescribeCall: true,
		},
		{
			name: "When delete endpoint fails with unexpected error it should return an error",
			client: &fakeVPCEndpointEC2Client{
				deleteErr: awserr.New("UnauthorizedOperation", "denied", nil),
			},
			expectDeleted:      false,
			expectError:        true,
			expectDescribeCall: false,
		},
		{
			name: "When delete endpoint output has unsuccessful entries it should return an error",
			client: &fakeVPCEndpointEC2Client{
				deleteOutput: &ec2.DeleteVpcEndpointsOutput{
					Unsuccessful: []*ec2.UnsuccessfulItem{
						{
							ResourceId: aws.String(endpointID),
							Error: &ec2.UnsuccessfulItemError{
								Code:    aws.String("DependencyViolation"),
								Message: aws.String("resource is in use"),
							},
						},
					},
				},
			},
			expectDeleted:      false,
			expectError:        true,
			expectDescribeCall: false,
		},
		{
			name: "When describe endpoint fails with unexpected error it should return an error",
			client: &fakeVPCEndpointEC2Client{
				describeErr: awserr.New("RequestLimitExceeded", "throttled", nil),
			},
			expectDeleted:      false,
			expectError:        true,
			expectDescribeCall: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			deleted, err := ensureVPCEndpointDeleted(t.Context(), tc.client, endpointID)
			if tc.expectError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.expectError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if deleted != tc.expectDeleted {
				t.Fatalf("expected deleted=%t, got %t", tc.expectDeleted, deleted)
			}
			assertDeleteInputEndpointID(t, tc.client.lastDeleteInput, endpointID)
			if tc.expectDescribeCall {
				assertDescribeInputEndpointID(t, tc.client.lastDescribeInput, endpointID)
			} else if tc.client.lastDescribeInput != nil {
				t.Fatalf("did not expect describe call, but got input: %#v", tc.client.lastDescribeInput)
			}
		})
	}
}

func assertDeleteInputEndpointID(t *testing.T, input *ec2.DeleteVpcEndpointsInput, endpointID string) {
	t.Helper()
	if input == nil {
		t.Fatal("expected DeleteVpcEndpointsWithContext to be called")
	}
	if len(input.VpcEndpointIds) != 1 {
		t.Fatalf("expected one endpoint ID in delete input, got %d", len(input.VpcEndpointIds))
	}
	if aws.StringValue(input.VpcEndpointIds[0]) != endpointID {
		t.Fatalf("expected delete input endpoint ID %q, got %q", endpointID, aws.StringValue(input.VpcEndpointIds[0]))
	}
}

func assertDescribeInputEndpointID(t *testing.T, input *ec2.DescribeVpcEndpointsInput, endpointID string) {
	t.Helper()
	if input == nil {
		t.Fatal("expected DescribeVpcEndpointsWithContext to be called")
	}
	found := false
	for _, id := range input.VpcEndpointIds {
		if aws.StringValue(id) == endpointID {
			found = true
			break
		}
	}
	if !found {
		for _, filter := range input.Filters {
			for _, value := range filter.Values {
				if aws.StringValue(value) == endpointID {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	if !found {
		t.Fatalf("expected describe input to contain endpoint ID %q", endpointID)
	}
}

func TestShouldRetrySecurityGroupDeletion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
		want bool
	}{
		{
			name: "When delete security group returns dependency violation it should retry",
			code: "DependencyViolation",
			want: true,
		},
		{
			name: "When delete security group returns another code it should not retry",
			code: "UnauthorizedOperation",
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shouldRetrySecurityGroupDeletion(tc.code)
			if got != tc.want {
				t.Fatalf("shouldRetrySecurityGroupDeletion(%q) = %t, want %t", tc.code, got, tc.want)
			}
		})
	}
}

func TestShouldIgnoreSecurityGroupRevokePermissionError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
		want bool
	}{
		{
			name: "When revoke security group permission returns InvalidPermission.NotFound it should ignore the error",
			code: "InvalidPermission.NotFound",
			want: true,
		},
		{
			name: "When revoke security group permission returns another code it should not ignore the error",
			code: "UnauthorizedOperation",
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := shouldIgnoreSecurityGroupRevokePermissionError(tc.code)
			if got != tc.want {
				t.Fatalf("shouldIgnoreSecurityGroupRevokePermissionError(%q) = %t, want %t", tc.code, got, tc.want)
			}
		})
	}
}

func TestHostedControlPlane(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		namespace string
		hcps      []hyperv1.HostedControlPlane
		wantNil   bool
		wantErr   bool
	}{
		{
			name:      "When no hosted control plane exists it should return nil without error",
			namespace: "test-ns",
			hcps:      nil,
			wantNil:   true,
		},
		{
			name:      "When one hosted control plane exists it should return that hosted control plane",
			namespace: "test-ns",
			hcps: []hyperv1.HostedControlPlane{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-1",
						Namespace: "test-ns",
					},
				},
			},
		},
		{
			name:      "When more than one hosted control plane exists it should return an error",
			namespace: "test-ns",
			hcps: []hyperv1.HostedControlPlane{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-1",
						Namespace: "test-ns",
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-2",
						Namespace: "test-ns",
					},
				},
			},
			wantErr: true,
		},
		{
			name:      "When hosted control plane exists in another namespace it should return nil without error",
			namespace: "test-ns",
			hcps: []hyperv1.HostedControlPlane{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-1",
						Namespace: "other-ns",
					},
				},
			},
			wantNil: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clientObjects := make([]ctrlclient.Object, 0, len(tc.hcps))
			for i := range tc.hcps {
				hcp := tc.hcps[i].DeepCopy()
				clientObjects = append(clientObjects, hcp)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(clientObjects...).
				Build()

			r := &AWSEndpointServiceReconciler{
				Client: fakeClient,
			}

			hcp, err := r.hostedControlPlane(t.Context(), tc.namespace)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantNil {
				if hcp != nil {
					t.Fatalf("expected nil hosted control plane, got %s", hcp.Name)
				}
				return
			}

			if hcp == nil {
				t.Fatalf("expected hosted control plane, got nil")
			}
			if hcp.Name != tc.hcps[0].Name {
				t.Fatalf("expected hosted control plane %s, got %s", tc.hcps[0].Name, hcp.Name)
			}
		})
	}
}
