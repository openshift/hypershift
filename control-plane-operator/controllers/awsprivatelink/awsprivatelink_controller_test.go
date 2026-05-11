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
	"github.com/openshift/hypershift/support/awsapi"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2v2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	route53sdk "github.com/aws/aws-sdk-go-v2/service/route53"
	route53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/smithy-go"

	corev1 "k8s.io/api/core/v1"
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
func TestHasAWSConfig(t *testing.T) {
	tests := []struct {
		name     string
		platform hyperv1.PlatformSpec
		expected bool
	}{
		{
			name: "When all AWS config fields are present, it should return true",
			platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
						Subnet: &hyperv1.AWSResourceReference{
							ID: aws.String("subnet-123"),
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "When platform type is not AWS, it should return false",
			platform: hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
			},
			expected: false,
		},
		{
			name: "When AWS spec is nil, it should return false",
			platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS:  nil,
			},
			expected: false,
		},
		{
			name: "When CloudProviderConfig is nil, it should return false",
			platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					CloudProviderConfig: nil,
				},
			},
			expected: false,
		},
		{
			name: "When Subnet is nil, it should return false",
			platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
						Subnet: nil,
					},
				},
			},
			expected: false,
		},
		{
			name: "When Subnet.ID is nil, it should return false",
			platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
						Subnet: &hyperv1.AWSResourceReference{
							ID: nil,
						},
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(hasAWSConfig(&tt.platform)).To(Equal(tt.expected))
		})
	}
}

func TestVPCEndpointPort(t *testing.T) {
	tests := []struct {
		name     string
		svcName  string
		expected int32
	}{
		{
			name:     "When service is kube-apiserver-private, it should return 6443",
			svcName:  "kube-apiserver-private",
			expected: 6443,
		},
		{
			name:     "When service is private-router, it should return 443",
			svcName:  "private-router",
			expected: 443,
		},
		{
			name:     "When service is an unknown name, it should return 443",
			svcName:  "some-other-service",
			expected: 443,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			aes := &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: tt.svcName},
			}
			g.Expect(vpcEndpointPort(aes)).To(Equal(tt.expected))
		})
	}
}

func TestVPCEndpointSecurityGroupName(t *testing.T) {
	tests := []struct {
		name         string
		infraID      string
		endpointName string
		expected     string
	}{
		{
			name:         "When given infraID and endpoint name, it should format correctly",
			infraID:      "my-infra",
			endpointName: "kube-apiserver-private",
			expected:     "my-infra-vpce-kube-apiserver-private",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(vpcEndpointSecurityGroupName(tt.infraID, tt.endpointName)).To(Equal(tt.expected))
		})
	}
}

func TestVPCEndpointSecurityGroupFilter(t *testing.T) {
	tests := []struct {
		name         string
		infraID      string
		endpointName string
	}{
		{
			name:         "When given infraID and endpoint name, it should return cluster tag and name tag filters",
			infraID:      "test-infra",
			endpointName: "kube-apiserver-private",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			filters := vpcEndpointSecurityGroupFilter(tt.infraID, tt.endpointName)
			g.Expect(filters).To(HaveLen(2))
			g.Expect(aws.ToString(filters[0].Name)).To(Equal("tag:kubernetes.io/cluster/test-infra"))
			g.Expect(filters[0].Values).To(Equal([]string{"owned"}))
			g.Expect(aws.ToString(filters[1].Name)).To(Equal("tag:Name"))
			g.Expect(filters[1].Values).To(Equal([]string{"test-infra-vpce-kube-apiserver-private"}))
		})
	}
}

func TestApiTagToEC2Tag(t *testing.T) {
	tests := []struct {
		name     string
		svcName  string
		tags     []hyperv1.AWSResourceTag
		expected []ec2types.Tag
	}{
		{
			name:    "When no resource tags are provided, it should return only the AWSEndpointService tag",
			svcName: "my-svc",
			tags:    nil,
			expected: []ec2types.Tag{
				{Key: aws.String("AWSEndpointService"), Value: aws.String("my-svc")},
			},
		},
		{
			name:    "When resource tags are provided, it should include them plus the AWSEndpointService tag",
			svcName: "my-svc",
			tags: []hyperv1.AWSResourceTag{
				{Key: "env", Value: "prod"},
				{Key: "team", Value: "platform"},
			},
			expected: []ec2types.Tag{
				{Key: aws.String("env"), Value: aws.String("prod")},
				{Key: aws.String("team"), Value: aws.String("platform")},
				{Key: aws.String("AWSEndpointService"), Value: aws.String("my-svc")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := apiTagToEC2Tag(tt.svcName, tt.tags)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestApiTagToEC2Filter(t *testing.T) {
	tests := []struct {
		name     string
		svcName  string
		tags     []hyperv1.AWSResourceTag
		expected []ec2types.Filter
	}{
		{
			name:    "When no resource tags are provided, it should return only the AWSEndpointService filter",
			svcName: "my-svc",
			tags:    nil,
			expected: []ec2types.Filter{
				{Name: aws.String("tag:AWSEndpointService"), Values: []string{"my-svc"}},
			},
		},
		{
			name:    "When resource tags are provided, it should include them as tag filters plus the AWSEndpointService filter",
			svcName: "my-svc",
			tags: []hyperv1.AWSResourceTag{
				{Key: "env", Value: "prod"},
			},
			expected: []ec2types.Filter{
				{Name: aws.String("tag:env"), Values: []string{"prod"}},
				{Name: aws.String("tag:AWSEndpointService"), Values: []string{"my-svc"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := apiTagToEC2Filter(tt.svcName, tt.tags)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestZoneName(t *testing.T) {
	tests := []struct {
		name     string
		hcpName  string
		expected string
	}{
		{
			name:     "When given an HCP name, it should append the hypershift.local suffix",
			hcpName:  "my-cluster",
			expected: "my-cluster.hypershift.local",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(zoneName(tt.hcpName)).To(Equal(tt.expected))
		})
	}
}

func TestRouterZoneName(t *testing.T) {
	tests := []struct {
		name     string
		hcpName  string
		expected string
	}{
		{
			name:     "When given an HCP name, it should prepend apps and append the hypershift.local suffix",
			hcpName:  "my-cluster",
			expected: "apps.my-cluster.hypershift.local",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(RouterZoneName(tt.hcpName)).To(Equal(tt.expected))
		})
	}
}

func TestControllerName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "When given a name, it should append the observer suffix",
			input:    "kube-apiserver",
			expected: "kube-apiserver-observer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(ControllerName(tt.input)).To(Equal(tt.expected))
		})
	}
}

func TestNameMapper(t *testing.T) {
	tests := []struct {
		name           string
		watchedNames   []string
		incomingName   string
		incomingNS     string
		expectRequests int
	}{
		{
			name:           "When incoming object name matches a watched name, it should return a reconcile request",
			watchedNames:   []string{"kube-apiserver-private", "private-router"},
			incomingName:   "kube-apiserver-private",
			incomingNS:     "test-ns",
			expectRequests: 1,
		},
		{
			name:           "When incoming object name does not match any watched name, it should return nil",
			watchedNames:   []string{"kube-apiserver-private"},
			incomingName:   "other-service",
			incomingNS:     "test-ns",
			expectRequests: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			mapFn := nameMapper(tt.watchedNames)
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.incomingName,
					Namespace: tt.incomingNS,
				},
			}
			requests := mapFn(t.Context(), svc)
			g.Expect(requests).To(HaveLen(tt.expectRequests))
			if tt.expectRequests > 0 {
				g.Expect(requests[0].Name).To(Equal(tt.incomingName))
				g.Expect(requests[0].Namespace).To(Equal(tt.incomingNS))
			}
		})
	}
}

func TestHCPExternalNames(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected map[string]string
	}{
		{
			name: "When API and OAuth both have Route strategies with hostnames, it should return both",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{Hostname: "api.example.com"},
							},
						},
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{Hostname: "oauth.example.com"},
							},
						},
					},
				},
			},
			expected: map[string]string{
				"api":   "api.example.com",
				"oauth": "oauth.example.com",
			},
		},
		{
			name: "When no Route strategies are configured, it should return empty map",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			expected: map[string]string{},
		},
		{
			name: "When Route strategy has no hostname, it should not include that entry",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{Hostname: ""},
							},
						},
					},
				},
			},
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := hcpExternalNames(tt.hcp)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestReconcileExternalService(t *testing.T) {
	tests := []struct {
		name           string
		hostName       string
		targetCName    string
		expectedLabels map[string]string
	}{
		{
			name:        "When reconciling an external service, it should set type, labels, annotations, and external name",
			hostName:    "api.example.com",
			targetCName: "vpce-abc.vpce-svc.us-east-1.vpce.amazonaws.com",
			expectedLabels: map[string]string{
				externalPrivateServiceLabel: "true",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-ns",
					UID:       "test-uid",
				},
			}
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver-private-external",
					Namespace: "test-ns",
				},
			}

			err := reconcileExternalService(svc, hcp, tt.hostName, tt.targetCName)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeExternalName))
			g.Expect(svc.Spec.ExternalName).To(Equal(tt.targetCName))
			g.Expect(svc.Labels[externalPrivateServiceLabel]).To(Equal("true"))
			g.Expect(svc.Annotations[hyperv1.ExternalDNSHostnameAnnotation]).To(Equal(tt.hostName))
			g.Expect(svc.OwnerReferences).To(HaveLen(1))
			g.Expect(svc.OwnerReferences[0].Name).To(Equal("test-hcp"))
		})
	}
}

func TestDeleteEndpointIfWrongService(t *testing.T) {
	tests := []struct {
		name                string
		endpointServiceName string
		expectedServiceName string
		setupEC2Mock        func(*gomock.Controller) *awsapi.MockEC2API
		expectError         bool
	}{
		{
			name:                "When endpoint points to the correct service, it should return nil",
			endpointServiceName: "com.amazonaws.vpce-svc-abc",
			expectedServiceName: "com.amazonaws.vpce-svc-abc",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				return awsapi.NewMockEC2API(mockCtrl)
			},
			expectError: false,
		},
		{
			name:                "When endpoint points to wrong service, it should delete and return error",
			endpointServiceName: "com.amazonaws.vpce-svc-wrong",
			expectedServiceName: "com.amazonaws.vpce-svc-correct",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DeleteVpcEndpoints(gomock.Any(), gomock.Any()).Return(&ec2v2.DeleteVpcEndpointsOutput{}, nil)
				return m
			},
			expectError: true,
		},
		{
			name:                "When endpoint points to wrong service and delete fails, it should return the delete error",
			endpointServiceName: "com.amazonaws.vpce-svc-wrong",
			expectedServiceName: "com.amazonaws.vpce-svc-correct",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DeleteVpcEndpoints(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("access denied"))
				return m
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			mockCtrl := gomock.NewController(t)
			mockEC2 := tt.setupEC2Mock(mockCtrl)

			endpoint := ec2types.VpcEndpoint{
				VpcEndpointId: aws.String("vpce-123"),
				ServiceName:   aws.String(tt.endpointServiceName),
			}
			ctx := ctrl.LoggerInto(t.Context(), ctrl.Log.WithName("test"))
			err := deleteEndpointIfWrongService(ctx, mockEC2, endpoint, tt.expectedServiceName, ctrl.Log.WithName("test"))
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestModifyEndpointIfNeeded(t *testing.T) {
	tests := []struct {
		name                string
		specSubnetIDs       []string
		endpointSubnetIDs   []string
		specSecurityGroupID string
		endpointGroups      []ec2types.SecurityGroupIdentifier
		setupEC2Mock        func(*gomock.Controller) *awsapi.MockEC2API
		expectError         bool
	}{
		{
			name:                "When subnets and security groups are unchanged, it should not modify",
			specSubnetIDs:       []string{"subnet-1", "subnet-2"},
			endpointSubnetIDs:   []string{"subnet-1", "subnet-2"},
			specSecurityGroupID: "sg-123",
			endpointGroups:      []ec2types.SecurityGroupIdentifier{{GroupId: aws.String("sg-123")}},
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				return awsapi.NewMockEC2API(mockCtrl)
			},
			expectError: false,
		},
		{
			name:                "When subnets have changed, it should call ModifyVpcEndpoint",
			specSubnetIDs:       []string{"subnet-1", "subnet-3"},
			endpointSubnetIDs:   []string{"subnet-1", "subnet-2"},
			specSecurityGroupID: "sg-123",
			endpointGroups:      []ec2types.SecurityGroupIdentifier{{GroupId: aws.String("sg-123")}},
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().ModifyVpcEndpoint(gomock.Any(), gomock.Any()).Return(&ec2v2.ModifyVpcEndpointOutput{}, nil)
				return m
			},
			expectError: false,
		},
		{
			name:                "When security group needs adding, it should call ModifyVpcEndpoint",
			specSubnetIDs:       []string{"subnet-1"},
			endpointSubnetIDs:   []string{"subnet-1"},
			specSecurityGroupID: "sg-new",
			endpointGroups:      []ec2types.SecurityGroupIdentifier{{GroupId: aws.String("sg-old")}},
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().ModifyVpcEndpoint(gomock.Any(), gomock.Any()).Return(&ec2v2.ModifyVpcEndpointOutput{}, nil)
				return m
			},
			expectError: false,
		},
		{
			name:                "When ModifyVpcEndpoint fails, it should return error",
			specSubnetIDs:       []string{"subnet-1", "subnet-3"},
			endpointSubnetIDs:   []string{"subnet-1", "subnet-2"},
			specSecurityGroupID: "sg-123",
			endpointGroups:      []ec2types.SecurityGroupIdentifier{{GroupId: aws.String("sg-123")}},
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().ModifyVpcEndpoint(gomock.Any(), gomock.Any()).Return(nil, fmt.Errorf("throttling"))
				return m
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			mockCtrl := gomock.NewController(t)
			mockEC2 := tt.setupEC2Mock(mockCtrl)

			aes := &hyperv1.AWSEndpointService{
				Spec: hyperv1.AWSEndpointServiceSpec{
					SubnetIDs: tt.specSubnetIDs,
				},
				Status: hyperv1.AWSEndpointServiceStatus{
					SecurityGroupID: tt.specSecurityGroupID,
				},
			}

			endpoint := ec2types.VpcEndpoint{
				SubnetIds: tt.endpointSubnetIDs,
				Groups:    tt.endpointGroups,
			}

			ctx := ctrl.LoggerInto(t.Context(), ctrl.Log.WithName("test"))
			err := modifyEndpointIfNeeded(ctx, mockEC2, aes, endpoint, "vpce-123", ctrl.Log.WithName("test"))
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestReconcileExistingEndpoint(t *testing.T) {
	tests := []struct {
		name             string
		endpointID       string
		setupEC2Mock     func(*gomock.Controller) *awsapi.MockEC2API
		expectError      bool
		expectEndpointID string
	}{
		{
			name:       "When DescribeVpcEndpoints returns empty results, it should reset EndpointID and return error",
			endpointID: "vpce-123",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).Return(&ec2v2.DescribeVpcEndpointsOutput{
					VpcEndpoints: []ec2types.VpcEndpoint{},
				}, nil)
				return m
			},
			expectError:      true,
			expectEndpointID: "",
		},
		{
			name:       "When DescribeVpcEndpoints returns NotFound API error, it should reset EndpointID and return error",
			endpointID: "vpce-gone",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).Return(nil, &smithy.GenericAPIError{Code: "InvalidVpcEndpointId.NotFound", Message: "not found"})
				return m
			},
			expectError:      true,
			expectEndpointID: "",
		},
		{
			name:       "When endpoint exists and matches service, it should return the endpoint ID",
			endpointID: "vpce-active",
			setupEC2Mock: func(mockCtrl *gomock.Controller) *awsapi.MockEC2API {
				m := awsapi.NewMockEC2API(mockCtrl)
				m.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).Return(&ec2v2.DescribeVpcEndpointsOutput{
					VpcEndpoints: []ec2types.VpcEndpoint{
						{
							VpcEndpointId: aws.String("vpce-active"),
							ServiceName:   aws.String("com.amazonaws.vpce-svc-test"),
							SubnetIds:     []string{"subnet-1"},
							Groups:        []ec2types.SecurityGroupIdentifier{{GroupId: aws.String("sg-test")}},
						},
					},
				}, nil)
				return m
			},
			expectError:      false,
			expectEndpointID: "vpce-active",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			mockCtrl := gomock.NewController(t)
			mockEC2 := tt.setupEC2Mock(mockCtrl)

			awsEndpointService := &hyperv1.AWSEndpointService{
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointID:          tt.endpointID,
					EndpointServiceName: "com.amazonaws.vpce-svc-test",
					SecurityGroupID:     "sg-test",
				},
				Spec: hyperv1.AWSEndpointServiceSpec{
					SubnetIDs: []string{"subnet-1"},
				},
			}

			r := &AWSEndpointServiceReconciler{}
			ctx := ctrl.LoggerInto(t.Context(), ctrl.Log.WithName("test"))
			resultID, _, err := r.reconcileExistingEndpoint(ctx, mockEC2, awsEndpointService, tt.endpointID, ctrl.Log.WithName("test"))
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(resultID).To(Equal(tt.expectEndpointID))
			g.Expect(awsEndpointService.Status.EndpointID).To(Equal(tt.expectEndpointID))
		})
	}
}

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

func TestExtractNLBName(t *testing.T) {
	testCases := []struct {
		name     string
		hostname string
		expected string
	}{
		{
			name:     "When standard NLB hostname it should extract name without hyphens",
			hostname: "a1b2c3d4e5f6g7-1234567890abcdef.elb.us-east-1.amazonaws.com",
			expected: "a1b2c3d4e5f6g7",
		},
		{
			name:     "When EKS Auto Mode NLB hostname it should extract full name with hyphens",
			hostname: "k8s-clusters-kubeapis-db6fee3a62-8008741421d14306.elb.us-east-1.amazonaws.com",
			expected: "k8s-clusters-kubeapis-db6fee3a62",
		},
		{
			name:     "When hostname has no hyphens it should return the first label as-is",
			hostname: "somename.elb.us-east-1.amazonaws.com",
			expected: "somename",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(extractNLBName(tc.hostname)).To(Equal(tc.expected))
		})
	}
}
