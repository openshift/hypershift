package awsprivatelink

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/awsapi"

	awsv2 "github.com/aws/aws-sdk-go-v2/aws"
	route53sdk "github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ec2/ec2iface"
	"go.uber.org/mock/gomock"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	"github.com/google/go-cmp/cmp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
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

// mockEC2Client is a hand-written mock for ec2iface.EC2API.
// ec2iface.EC2API has 500+ methods, making mockgen impractical (~37K lines).
// This follows the project-wide convention of hand-written embedded EC2 mocks.
type mockEC2Client struct {
	ec2iface.EC2API

	// VPC endpoint operations
	deleteVpcEndpointsErr      error
	describeVpcEndpointsErr    error
	describeVpcEndpointsOutput *ec2.DescribeVpcEndpointsOutput

	// Security group operations
	describeSecurityGroupsErr    error
	describeSecurityGroupsOutput *ec2.DescribeSecurityGroupsOutput
	revokeSecurityGroupIngressErr error
	revokeSecurityGroupEgressErr  error
	deleteSecurityGroupErr        error
}

func (m *mockEC2Client) DeleteVpcEndpointsWithContext(_ aws.Context, _ *ec2.DeleteVpcEndpointsInput, _ ...request.Option) (*ec2.DeleteVpcEndpointsOutput, error) {
	return &ec2.DeleteVpcEndpointsOutput{}, m.deleteVpcEndpointsErr
}

func (m *mockEC2Client) DescribeVpcEndpointsWithContext(_ aws.Context, _ *ec2.DescribeVpcEndpointsInput, _ ...request.Option) (*ec2.DescribeVpcEndpointsOutput, error) {
	if m.describeVpcEndpointsErr != nil {
		return nil, m.describeVpcEndpointsErr
	}
	return m.describeVpcEndpointsOutput, nil
}

func (m *mockEC2Client) DescribeSecurityGroupsWithContext(_ aws.Context, _ *ec2.DescribeSecurityGroupsInput, _ ...request.Option) (*ec2.DescribeSecurityGroupsOutput, error) {
	if m.describeSecurityGroupsErr != nil {
		return nil, m.describeSecurityGroupsErr
	}
	return m.describeSecurityGroupsOutput, nil
}

func (m *mockEC2Client) RevokeSecurityGroupIngressWithContext(_ aws.Context, _ *ec2.RevokeSecurityGroupIngressInput, _ ...request.Option) (*ec2.RevokeSecurityGroupIngressOutput, error) {
	if m.revokeSecurityGroupIngressErr != nil {
		return nil, m.revokeSecurityGroupIngressErr
	}
	return &ec2.RevokeSecurityGroupIngressOutput{}, nil
}

func (m *mockEC2Client) RevokeSecurityGroupEgressWithContext(_ aws.Context, _ *ec2.RevokeSecurityGroupEgressInput, _ ...request.Option) (*ec2.RevokeSecurityGroupEgressOutput, error) {
	if m.revokeSecurityGroupEgressErr != nil {
		return nil, m.revokeSecurityGroupEgressErr
	}
	return &ec2.RevokeSecurityGroupEgressOutput{}, nil
}

func (m *mockEC2Client) DeleteSecurityGroupWithContext(_ aws.Context, _ *ec2.DeleteSecurityGroupInput, _ ...request.Option) (*ec2.DeleteSecurityGroupOutput, error) {
	if m.deleteSecurityGroupErr != nil {
		return nil, m.deleteSecurityGroupErr
	}
	return &ec2.DeleteSecurityGroupOutput{}, nil
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
				mockEC2 := &mockEC2Client{
					// VPC endpoint already gone after delete request
					describeVpcEndpointsErr: awserr.New("InvalidVpcEndpointId.NotFound", "not found", nil),
					// Security group exists and can be cleaned up
					describeSecurityGroupsOutput: &ec2.DescribeSecurityGroupsOutput{
						SecurityGroups: []*ec2.SecurityGroup{{
							GroupId:             aws.String("sg-12345"),
							IpPermissions:       []*ec2.IpPermission{ingressPermission},
							IpPermissionsEgress: []*ec2.IpPermission{egressPermission},
						}},
					},
				}
				mockBuilder.EXPECT().getClients(gomock.Any()).Return(mockEC2, mockRoute53, nil)
				// DNS record exists and can be deleted
				mockRoute53.EXPECT().ListResourceRecordSets(gomock.Any(), gomock.Any()).Return(
					&route53sdk.ListResourceRecordSetsOutput{
						ResourceRecordSets: []route53types.ResourceRecordSet{{
							Name: awsv2.String("api.example.com."),
							Type: route53types.RRTypeCname,
							TTL:  awsv2.Int64(300),
							ResourceRecords: []route53types.ResourceRecord{
								{Value: awsv2.String("vpce-12345.vpce-svc.us-east-1.vpce.amazonaws.com")},
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
				mockEC2 := &mockEC2Client{}
				mockRoute53 := awsapi.NewMockROUTE53API(mockCtrl)
				mockBuilder.EXPECT().getClients(gomock.Any()).Return(mockEC2, mockRoute53, nil)
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
				mockEC2 := &mockEC2Client{
					deleteVpcEndpointsErr: fmt.Errorf("throttling"),
				}
				mockRoute53 := awsapi.NewMockROUTE53API(mockCtrl)
				mockBuilder.EXPECT().getClients(gomock.Any()).Return(mockEC2, mockRoute53, nil)
				return mockBuilder
			},
			expectError:     true,
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

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.awsEndpointSvc).
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

// TestReconcileDeletion_AfterControllerRestart demonstrates the bug OCPBUGS-74960.
//
// When the controller restarts, a new clientBuilder is created in SetupWithManager
// with initialized=false. The non-deletion reconcile path calls initializeWithHCP
// before getClients, but the deletion path calls getClients directly. If the first
// reconciliation after restart is a deletion, getClients returns "clients not
// initialized" and the code falls through to remove the finalizer, orphaning
// AWS resources (security groups, VPC endpoints, DNS records).
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

	// First reconciler: represents the controller before restart.
	// In normal operation, initializeWithHCP would have been called during
	// non-deletion reconciliations, making getClients work.
	// We don't need to run it; its existence is just for context.

	// Second reconciler: simulates a controller restart.
	// SetupWithManager creates a fresh clientBuilder{} (initialized=false).
	// The deletion path never calls initializeWithHCP, so getClients fails.
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
	// BUG: no error is returned despite cleanup being skipped.
	g.Expect(err).ToNot(HaveOccurred())

	// BUG: The finalizer was removed even though getClients failed and no AWS
	// cleanup occurred. The resources referenced in Status (sg-12345, vpce-12345,
	// api.example.com in zone Z1234567890) are now orphaned.
	updatedService := &hyperv1.AWSEndpointService{}
	getErr := fakeClient.Get(ctx, types.NamespacedName{
		Name:      awsEndpointSvc.Name,
		Namespace: awsEndpointSvc.Namespace,
	}, updatedService)
	g.Expect(getErr).To(HaveOccurred(), "object should be deleted after finalizer removal — AWS resources are now orphaned")
}
