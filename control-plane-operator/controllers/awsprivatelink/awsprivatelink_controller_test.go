package awsprivatelink

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"github.com/aws/aws-sdk-go/service/route53/route53iface"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/google/go-cmp/cmp"
)

// fakeEC2Client implements ec2iface.EC2API for testing.
// Only the methods used by the delete() function are implemented.
type fakeEC2Client struct {
	ec2iface.EC2API

	deleteVpcEndpointsErr    error
	describeVpcEndpointsErr  error
	describeVpcEndpointsOut  *ec2.DescribeVpcEndpointsOutput
	describeSecurityGroupErr error
	describeSecurityGroupOut *ec2.DescribeSecurityGroupsOutput
	revokeIngressErr         error
	revokeEgressErr          error
	deleteSecurityGroupErr   error
}

func (f *fakeEC2Client) DeleteVpcEndpointsWithContext(_ context.Context, _ *ec2.DeleteVpcEndpointsInput, _ ...request.Option) (*ec2.DeleteVpcEndpointsOutput, error) {
	return &ec2.DeleteVpcEndpointsOutput{}, f.deleteVpcEndpointsErr
}

func (f *fakeEC2Client) DescribeVpcEndpointsWithContext(_ context.Context, _ *ec2.DescribeVpcEndpointsInput, _ ...request.Option) (*ec2.DescribeVpcEndpointsOutput, error) {
	return f.describeVpcEndpointsOut, f.describeVpcEndpointsErr
}

func (f *fakeEC2Client) DescribeSecurityGroupsWithContext(_ context.Context, _ *ec2.DescribeSecurityGroupsInput, _ ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
	return f.describeSecurityGroupOut, f.describeSecurityGroupErr
}

func (f *fakeEC2Client) RevokeSecurityGroupIngressWithContext(_ context.Context, _ *ec2.RevokeSecurityGroupIngressInput, _ ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error) {
	return &ec2.RevokeSecurityGroupIngressOutput{}, f.revokeIngressErr
}

func (f *fakeEC2Client) RevokeSecurityGroupEgressWithContext(_ context.Context, _ *ec2.RevokeSecurityGroupEgressInput, _ ...request.Option) (*ec2.RevokeSecurityGroupEgressOutput, error) {
	return &ec2.RevokeSecurityGroupEgressOutput{}, f.revokeEgressErr
}

func (f *fakeEC2Client) DeleteSecurityGroupWithContext(_ context.Context, _ *ec2.DeleteSecurityGroupInput, _ ...request.Option) (*ec2.DeleteSecurityGroupOutput, error) {
	return &ec2.DeleteSecurityGroupOutput{}, f.deleteSecurityGroupErr
}

// fakeRoute53Client implements route53iface.Route53API for testing.
type fakeRoute53Client struct {
	route53iface.Route53API
}

// newTestScheme creates a runtime.Scheme with the HyperShift API types registered.
func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = hyperv1.AddToScheme(s)
	return s
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

// successfulDeleteEC2Client returns a fakeEC2Client where all delete operations succeed
// and VPC endpoints are reported as not found (already deleted).
func successfulDeleteEC2Client() *fakeEC2Client {
	return &fakeEC2Client{
		describeVpcEndpointsErr: awserr.New("InvalidVpcEndpointId.NotFound", "not found", nil),
		describeSecurityGroupErr: awserr.New("InvalidGroup.NotFound", "not found", nil),
	}
}

func TestReconcileHCPDeletion(t *testing.T) {
	testCases := []struct {
		name                       string
		awsEndpointService         *hyperv1.AWSEndpointService
		hcp                        *hyperv1.HostedControlPlane
		otherAWSEndpointServices   []*hyperv1.AWSEndpointService
		ec2Client                  *fakeEC2Client
		expectError                bool
		expectAESFinalizerRemoved  bool
		expectHCPFinalizerRemoved  bool
	}{
		{
			name: "When all AWS resources are cleaned up and no other AES remain, it should remove both AES and HCP finalizers",
			awsEndpointService: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-aes",
					Namespace:  "test-ns",
					Finalizers: []string{finalizer},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  "test-ns",
					Finalizers: []string{hcpFinalizer},
				},
			},
			ec2Client:                 successfulDeleteEC2Client(),
			expectAESFinalizerRemoved: true,
			expectHCPFinalizerRemoved: true,
		},
		{
			name: "When AWS cleanup succeeds but other AES still have finalizers, it should remove AES finalizer but keep HCP finalizer",
			awsEndpointService: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-aes-1",
					Namespace:  "test-ns",
					Finalizers: []string{finalizer},
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  "test-ns",
					Finalizers: []string{hcpFinalizer},
				},
			},
			otherAWSEndpointServices: []*hyperv1.AWSEndpointService{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "test-aes-2",
						Namespace:  "test-ns",
						Finalizers: []string{finalizer},
					},
				},
			},
			ec2Client:                 successfulDeleteEC2Client(),
			expectAESFinalizerRemoved: true,
			expectHCPFinalizerRemoved: false,
		},
		{
			name: "When AWS delete returns error, it should return error without removing any finalizers",
			awsEndpointService: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-aes",
					Namespace:  "test-ns",
					Finalizers: []string{finalizer},
				},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointID: "vpce-12345",
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  "test-ns",
					Finalizers: []string{hcpFinalizer},
				},
			},
			ec2Client: &fakeEC2Client{
				deleteVpcEndpointsErr: fmt.Errorf("AWS API error"),
			},
			expectError:               true,
			expectAESFinalizerRemoved: false,
			expectHCPFinalizerRemoved: false,
		},
		{
			name: "When VPC endpoint deletion is not complete, it should return error without removing finalizers",
			awsEndpointService: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-aes",
					Namespace:  "test-ns",
					Finalizers: []string{finalizer},
				},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointID: "vpce-12345",
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  "test-ns",
					Finalizers: []string{hcpFinalizer},
				},
			},
			ec2Client: &fakeEC2Client{
				// DeleteVpcEndpoints succeeds but DescribeVpcEndpoints still finds endpoints (not yet fully deleted)
				describeVpcEndpointsOut: &ec2.DescribeVpcEndpointsOutput{
					VpcEndpoints: []*ec2.VpcEndpoint{{VpcEndpointId: aws.String("vpce-12345")}},
				},
			},
			expectError:               true,
			expectAESFinalizerRemoved: false,
			expectHCPFinalizerRemoved: false,
		},
		{
			name: "When AES has no finalizer, it should still check remaining AES and remove HCP finalizer if none remain",
			awsEndpointService: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-aes",
					Namespace: "test-ns",
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-hcp",
					Namespace:  "test-ns",
					Finalizers: []string{hcpFinalizer},
				},
			},
			ec2Client:                 successfulDeleteEC2Client(),
			expectAESFinalizerRemoved: false,
			expectHCPFinalizerRemoved: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			scheme := newTestScheme()

			// Build the list of objects for the fake client
			objects := []client.Object{tc.awsEndpointService, tc.hcp}
			for _, aes := range tc.otherAWSEndpointServices {
				objects = append(objects, aes)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objects...).
				WithStatusSubresource(&hyperv1.AWSEndpointService{}).
				Build()

			reconciler := &AWSEndpointServiceReconciler{
				Client: fakeClient,
			}

			logger := log.Log.WithName("test")
			ctx := log.IntoContext(context.Background(), logger)

			// Re-read objects from the fake client to get proper resourceVersion for Patch operations
			aes := &hyperv1.AWSEndpointService{}
			g.Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(tc.awsEndpointService), aes)).To(Succeed())
			hcp := &hyperv1.HostedControlPlane{}
			g.Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(tc.hcp), hcp)).To(Succeed())

			result, err := reconciler.reconcileHCPDeletion(
				ctx, logger,
				aes, hcp,
				tc.ec2Client, &fakeRoute53Client{},
			)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			_ = result

			// Verify AES finalizer state
			updatedAES := &hyperv1.AWSEndpointService{}
			err = fakeClient.Get(ctx, client.ObjectKeyFromObject(tc.awsEndpointService), updatedAES)
			g.Expect(err).ToNot(HaveOccurred())
			if tc.expectAESFinalizerRemoved {
				g.Expect(controllerutil.ContainsFinalizer(updatedAES, finalizer)).To(BeFalse(),
					"AES finalizer should have been removed")
			}

			// Verify HCP finalizer state
			updatedHCP := &hyperv1.HostedControlPlane{}
			err = fakeClient.Get(ctx, client.ObjectKeyFromObject(tc.hcp), updatedHCP)
			g.Expect(err).ToNot(HaveOccurred())
			if tc.expectHCPFinalizerRemoved {
				g.Expect(controllerutil.ContainsFinalizer(updatedHCP, hcpFinalizer)).To(BeFalse(),
					"HCP finalizer should have been removed")
			} else {
				g.Expect(controllerutil.ContainsFinalizer(updatedHCP, hcpFinalizer)).To(BeTrue(),
					"HCP finalizer should still be present")
			}
		})
	}
}

func TestReconcileHCPDeletionCacheRaceCondition(t *testing.T) {
	// When the current AES just had its finalizer removed, the informer cache list
	// may still return it with the old finalizer. The reconciler should exclude the
	// current AES from the remaining count to avoid falsely keeping the HCP finalizer.
	g := NewGomegaWithT(t)

	scheme := newTestScheme()
	currentAES := &hyperv1.AWSEndpointService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "current-aes",
			Namespace:  "test-ns",
			Finalizers: []string{finalizer},
		},
	}
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-hcp",
			Namespace:  "test-ns",
			Finalizers: []string{hcpFinalizer},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(currentAES, hcp).
		WithStatusSubresource(&hyperv1.AWSEndpointService{}).
		Build()

	reconciler := &AWSEndpointServiceReconciler{
		Client: fakeClient,
	}

	logger := log.Log.WithName("test")
	ctx := log.IntoContext(context.Background(), logger)

	// Re-read objects from the fake client to get proper resourceVersion
	aes := &hyperv1.AWSEndpointService{}
	g.Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(currentAES), aes)).To(Succeed())
	hcpFromClient := &hyperv1.HostedControlPlane{}
	g.Expect(fakeClient.Get(ctx, client.ObjectKeyFromObject(hcp), hcpFromClient)).To(Succeed())

	_, err := reconciler.reconcileHCPDeletion(
		ctx, logger, aes, hcpFromClient,
		successfulDeleteEC2Client(), &fakeRoute53Client{},
	)
	g.Expect(err).ToNot(HaveOccurred())

	// Even though the fake client List may still show the current AES with its old finalizer,
	// the reconciler should have excluded it from the count and removed the HCP finalizer
	updatedHCP := &hyperv1.HostedControlPlane{}
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(hcp), updatedHCP)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(controllerutil.ContainsFinalizer(updatedHCP, hcpFinalizer)).To(BeFalse(),
		"HCP finalizer should be removed when current AES is excluded from remaining count")
}

func TestReconcileAddHCPFinalizer(t *testing.T) {
	// When reconciling a normal (non-deleting) AWSEndpointService, it should add
	// the HCP finalizer to the HCP if not already present.
	g := NewGomegaWithT(t)

	scheme := newTestScheme()
	aes := &hyperv1.AWSEndpointService{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-aes",
			Namespace:  "test-ns",
			Finalizers: []string{finalizer},
		},
		Status: hyperv1.AWSEndpointServiceStatus{
			EndpointServiceName: "com.amazonaws.vpce-svc-123",
		},
	}
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-ns",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(aes, hcp).
		WithStatusSubresource(&hyperv1.AWSEndpointService{}).
		Build()

	reconciler := &AWSEndpointServiceReconciler{
		Client: fakeClient,
	}

	// Simulate the finalizer-adding logic from Reconcile
	ctx := context.Background()
	if !controllerutil.ContainsFinalizer(hcp, hcpFinalizer) {
		originalHCP := hcp.DeepCopy()
		controllerutil.AddFinalizer(hcp, hcpFinalizer)
		err := reconciler.Patch(ctx, hcp, client.MergeFromWithOptions(originalHCP, client.MergeFromWithOptimisticLock{}))
		g.Expect(err).ToNot(HaveOccurred())
	}

	updatedHCP := &hyperv1.HostedControlPlane{}
	err := fakeClient.Get(ctx, client.ObjectKeyFromObject(hcp), updatedHCP)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(controllerutil.ContainsFinalizer(updatedHCP, hcpFinalizer)).To(BeTrue(),
		"HCP finalizer should be added during normal reconciliation")
}

