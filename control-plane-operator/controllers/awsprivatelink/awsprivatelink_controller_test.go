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
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/support/awsapi"
	"github.com/openshift/hypershift/support/upsert"

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
	dto "github.com/prometheus/client_model/go"
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

func getHistogramSampleCount() uint64 {
	var m dto.Metric
	if err := endpointProvisioningDuration.Write(&m); err != nil {
		return 0
	}
	return m.Histogram.GetSampleCount()
}

func TestEndpointProvisioningDurationMetric(t *testing.T) {
	testCases := []struct {
		name               string
		existingConditions []metav1.Condition
		expectObservation  bool
	}{
		{
			name:               "When endpoint becomes available for the first time it should observe the provisioning duration",
			existingConditions: nil,
			expectObservation:  true,
		},
		{
			name: "When endpoint is already available it should not observe the provisioning duration again",
			existingConditions: []metav1.Condition{
				{
					Type:   string(hyperv1.AWSEndpointAvailable),
					Status: metav1.ConditionTrue,
					Reason: hyperv1.AWSSuccessReason,
				},
			},
			expectObservation: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			mockCtrl := gomock.NewController(t)

			scheme := runtime.NewScheme()
			_ = hyperv1.AddToScheme(scheme)

			awsEndpointSvc := &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "kube-apiserver-private",
					Namespace:         "clusters-test",
					Finalizers:        []string{finalizer},
					CreationTimestamp: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
				},
				Spec: hyperv1.AWSEndpointServiceSpec{
					NetworkLoadBalancerName: "test-nlb",
				},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointServiceName: "com.amazonaws.vpce-svc.test",
					EndpointID:          "vpce-12345",
					SecurityGroupID:     "sg-12345",
					Conditions:          tc.existingConditions,
				},
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "clusters-test",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
								VPC: "vpc-12345",
								Subnet: &hyperv1.AWSResourceReference{
									ID: aws.String("subnet-12345"),
								},
							},
							EndpointAccess: hyperv1.Private,
						},
					},
					Networking: hyperv1.ClusterNetworking{
						MachineNetwork: []hyperv1.MachineNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.0.0.0/16")},
						},
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(awsEndpointSvc, hcp).
				WithStatusSubresource(awsEndpointSvc).
				Build()

			mockBuilder := NewMockawsClientProvider(mockCtrl)
			mockEC2 := awsapi.NewMockEC2API(mockCtrl)
			mockRoute53 := awsapi.NewMockROUTE53API(mockCtrl)

			mockBuilder.EXPECT().initializeWithHCP(gomock.Any(), gomock.Any())
			mockBuilder.EXPECT().getClients(gomock.Any()).Return(mockEC2, mockRoute53, nil)

			// Security group already exists with correct permissions
			mockEC2.EXPECT().DescribeSecurityGroups(gomock.Any(), gomock.Any()).Return(&ec2v2.DescribeSecurityGroupsOutput{
				SecurityGroups: []ec2types.SecurityGroup{{
					GroupId: aws.String("sg-12345"),
					IpPermissions: []ec2types.IpPermission{{
						FromPort:   aws.Int32(6443),
						ToPort:     aws.Int32(6443),
						IpProtocol: aws.String("tcp"),
						IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/16"), Description: aws.String("Control plane service")}},
					}},
				}},
			}, nil)

			// Endpoint exists with no DNS entries (skips DNS record creation)
			mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).Return(&ec2v2.DescribeVpcEndpointsOutput{
				VpcEndpoints: []ec2types.VpcEndpoint{{
					VpcEndpointId: aws.String("vpce-12345"),
					ServiceName:   aws.String("com.amazonaws.vpce-svc.test"),
					SubnetIds:     []string{},
					Groups:        []ec2types.SecurityGroupIdentifier{{GroupId: aws.String("sg-12345")}},
					DnsEntries:    []ec2types.DnsEntry{},
				}},
			}, nil)

			reconciler := &AWSEndpointServiceReconciler{
				Client:                 fakeClient,
				CreateOrUpdateProvider: upsert.New(false),
				awsClientBuilder:       mockBuilder,
			}

			countBefore := getHistogramSampleCount()

			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log.WithName("test"))
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      awsEndpointSvc.Name,
					Namespace: awsEndpointSvc.Namespace,
				},
			})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result.RequeueAfter).To(Equal(5 * time.Minute))

			countAfter := getHistogramSampleCount()

			if tc.expectObservation {
				g.Expect(countAfter).To(Equal(countBefore+1), "expected histogram sample count to increase by 1")
			} else {
				g.Expect(countAfter).To(Equal(countBefore), "expected histogram sample count to remain unchanged")
			}
		})
	}
}

func TestHasAWSConfig(t *testing.T) {
	testCases := []struct {
		name     string
		platform *hyperv1.PlatformSpec
		expected bool
	}{
		{
			name:     "When platform is nil it should return false",
			platform: &hyperv1.PlatformSpec{},
			expected: false,
		},
		{
			name: "When platform type is not AWS it should return false",
			platform: &hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
				AWS:  &hyperv1.AWSPlatformSpec{},
			},
			expected: false,
		},
		{
			name: "When AWS field is nil it should return false",
			platform: &hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
			expected: false,
		},
		{
			name: "When CloudProviderConfig is nil it should return false",
			platform: &hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS:  &hyperv1.AWSPlatformSpec{},
			},
			expected: false,
		},
		{
			name: "When Subnet is nil it should return false",
			platform: &hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
						VPC: "vpc-12345",
					},
				},
			},
			expected: false,
		},
		{
			name: "When Subnet.ID is nil it should return false",
			platform: &hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
						VPC:    "vpc-12345",
						Subnet: &hyperv1.AWSResourceReference{},
					},
				},
			},
			expected: false,
		},
		{
			name: "When all fields are set it should return true",
			platform: &hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
						VPC: "vpc-12345",
						Subnet: &hyperv1.AWSResourceReference{
							ID: aws.String("subnet-12345"),
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(hasAWSConfig(tc.platform)).To(Equal(tc.expected))
		})
	}
}

func TestApiTagToEC2Tag(t *testing.T) {
	testCases := []struct {
		name     string
		svcName  string
		tags     []hyperv1.AWSResourceTag
		expected []ec2types.Tag
	}{
		{
			name:    "When no tags are provided it should return only the AWSEndpointService tag",
			svcName: "test-svc",
			tags:    []hyperv1.AWSResourceTag{},
			expected: []ec2types.Tag{
				{Key: aws.String("AWSEndpointService"), Value: aws.String("test-svc")},
			},
		},
		{
			name:    "When multiple tags are provided it should return all tags plus AWSEndpointService tag",
			svcName: "my-endpoint",
			tags: []hyperv1.AWSResourceTag{
				{Key: "env", Value: "prod"},
				{Key: "team", Value: "platform"},
			},
			expected: []ec2types.Tag{
				{Key: aws.String("env"), Value: aws.String("prod")},
				{Key: aws.String("team"), Value: aws.String("platform")},
				{Key: aws.String("AWSEndpointService"), Value: aws.String("my-endpoint")},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := apiTagToEC2Tag(tc.svcName, tc.tags)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestApiTagToEC2Filter(t *testing.T) {
	testCases := []struct {
		name     string
		svcName  string
		tags     []hyperv1.AWSResourceTag
		expected []ec2types.Filter
	}{
		{
			name:    "When no tags are provided it should return only the AWSEndpointService filter",
			svcName: "test-svc",
			tags:    []hyperv1.AWSResourceTag{},
			expected: []ec2types.Filter{
				{Name: aws.String("tag:AWSEndpointService"), Values: []string{"test-svc"}},
			},
		},
		{
			name:    "When multiple tags are provided it should return all filters with tag: prefix plus AWSEndpointService filter",
			svcName: "my-endpoint",
			tags: []hyperv1.AWSResourceTag{
				{Key: "env", Value: "prod"},
				{Key: "team", Value: "platform"},
			},
			expected: []ec2types.Filter{
				{Name: aws.String("tag:env"), Values: []string{"prod"}},
				{Name: aws.String("tag:team"), Values: []string{"platform"}},
				{Name: aws.String("tag:AWSEndpointService"), Values: []string{"my-endpoint"}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := apiTagToEC2Filter(tc.svcName, tc.tags)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestVpcEndpointPort(t *testing.T) {
	testCases := []struct {
		name     string
		svcName  string
		expected int32
	}{
		{
			name:     "When service is kube-apiserver-private it should return 6443",
			svcName:  "kube-apiserver-private",
			expected: 6443,
		},
		{
			name:     "When service is private-router it should return 443",
			svcName:  "private-router",
			expected: 443,
		},
		{
			name:     "When service is unknown it should return 443",
			svcName:  "unknown-service",
			expected: 443,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			svc := &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: tc.svcName},
			}
			g.Expect(vpcEndpointPort(svc)).To(Equal(tc.expected))
		})
	}
}

func TestZoneName(t *testing.T) {
	testCases := []struct {
		name     string
		hcpName  string
		expected string
	}{
		{
			name:     "When name is provided it should return name.hypershift.local",
			hcpName:  "my-cluster",
			expected: "my-cluster.hypershift.local",
		},
		{
			name:     "When name is empty it should return .hypershift.local",
			hcpName:  "",
			expected: ".hypershift.local",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(zoneName(tc.hcpName)).To(Equal(tc.expected))
		})
	}
}

func TestVpcEndpointSecurityGroupName(t *testing.T) {
	t.Run("When infraID and name are provided it should return {infraID}-vpce-{name}", func(t *testing.T) {
		g := NewGomegaWithT(t)
		g.Expect(vpcEndpointSecurityGroupName("test-infra", "kube-apiserver-private")).To(Equal("test-infra-vpce-kube-apiserver-private"))
	})
}

func TestHcpExternalNames(t *testing.T) {
	testCases := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected map[string]string
	}{
		{
			name: "When no strategies are configured it should return empty map",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{},
			},
			expected: map[string]string{},
		},
		{
			name: "When API server has Route strategy with hostname it should return api entry",
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
					},
				},
			},
			expected: map[string]string{"api": "api.example.com"},
		},
		{
			name: "When OAuth server has Route strategy with hostname it should return oauth entry",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
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
			expected: map[string]string{"oauth": "oauth.example.com"},
		},
		{
			name: "When both API and OAuth have Route with hostname it should return both entries",
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
			expected: map[string]string{"api": "api.example.com", "oauth": "oauth.example.com"},
		},
		{
			name: "When Route strategy has no hostname it should not include entry",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type:  hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{},
							},
						},
					},
				},
			},
			expected: map[string]string{},
		},
		{
			name: "When strategy type is LoadBalancer it should not include entry",
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
			name: "When Route field is nil it should not include entry",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
							},
						},
					},
				},
			},
			expected: map[string]string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(hcpExternalNames(tc.hcp)).To(Equal(tc.expected))
		})
	}
}

func TestReconcileAWSEndpointService(t *testing.T) {
	// Standard helper to create a base HCP with AWS config for tests that need it.
	baseHCP := func() *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test",
				Namespace: "clusters-test",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				InfraID: "test-infra",
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AWSPlatform,
					AWS: &hyperv1.AWSPlatformSpec{
						CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
							VPC: "vpc-12345",
							Subnet: &hyperv1.AWSResourceReference{
								ID: aws.String("subnet-12345"),
							},
						},
						EndpointAccess: hyperv1.Private,
					},
				},
				Networking: hyperv1.ClusterNetworking{
					MachineNetwork: []hyperv1.MachineNetworkEntry{
						{CIDR: *ipnet.MustParseCIDR("10.0.0.0/16")},
					},
				},
			},
		}
	}

	// Standard mock setup for security group that already exists with correct permissions.
	setupSGMock := func(mockEC2 *awsapi.MockEC2API) {
		mockEC2.EXPECT().DescribeSecurityGroups(gomock.Any(), &ec2v2.DescribeSecurityGroupsInput{
			Filters: vpcEndpointSecurityGroupFilter("test-infra", "kube-apiserver-private"),
		}).Return(&ec2v2.DescribeSecurityGroupsOutput{
			SecurityGroups: []ec2types.SecurityGroup{{
				GroupId: aws.String("sg-12345"),
				IpPermissions: []ec2types.IpPermission{{
					FromPort:   aws.Int32(6443),
					ToPort:     aws.Int32(6443),
					IpProtocol: aws.String("tcp"),
					IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/16"), Description: aws.String("Control plane service")}},
				}},
			}},
		}, nil)
	}

	testCases := []struct {
		name           string
		awsEndpointSvc *hyperv1.AWSEndpointService
		hcp            *hyperv1.HostedControlPlane
		setupMocks     func(mockEC2 *awsapi.MockEC2API, mockRoute53 *awsapi.MockROUTE53API, mockBuilder *MockawsClientProvider)
		expectError    bool
		expectErrorMsg string
		expectEndpoint string
		expectResetID  bool
	}{
		{
			name: "When EndpointServiceName is empty it should return nil",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Status:     hyperv1.AWSEndpointServiceStatus{EndpointServiceName: ""},
			},
			hcp: baseHCP(),
			setupMocks: func(mockEC2 *awsapi.MockEC2API, mockRoute53 *awsapi.MockROUTE53API, mockBuilder *MockawsClientProvider) {
			},
			expectError: false,
		},
		{
			name: "When endpoint is not found with InvalidVpcEndpointId.NotFound it should reset EndpointID and return error",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointServiceName: "com.amazonaws.vpce-svc.test",
					EndpointID:          "vpce-12345",
					SecurityGroupID:     "sg-12345",
				},
			},
			hcp: baseHCP(),
			setupMocks: func(mockEC2 *awsapi.MockEC2API, mockRoute53 *awsapi.MockROUTE53API, mockBuilder *MockawsClientProvider) {
				setupSGMock(mockEC2)
				mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), &ec2v2.DescribeVpcEndpointsInput{
					VpcEndpointIds: []string{"vpce-12345"},
				}).Return(nil, &smithy.GenericAPIError{Code: "InvalidVpcEndpointId.NotFound", Message: "not found"})
			},
			expectError:    true,
			expectErrorMsg: "not found, resetting status",
			expectResetID:  true,
		},
		{
			name: "When endpoint links to wrong service it should delete and return error",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointServiceName: "com.amazonaws.vpce-svc.test",
					EndpointID:          "vpce-12345",
					SecurityGroupID:     "sg-12345",
				},
			},
			hcp: baseHCP(),
			setupMocks: func(mockEC2 *awsapi.MockEC2API, mockRoute53 *awsapi.MockROUTE53API, mockBuilder *MockawsClientProvider) {
				setupSGMock(mockEC2)
				mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), &ec2v2.DescribeVpcEndpointsInput{
					VpcEndpointIds: []string{"vpce-12345"},
				}).Return(&ec2v2.DescribeVpcEndpointsOutput{
					VpcEndpoints: []ec2types.VpcEndpoint{{
						VpcEndpointId: aws.String("vpce-12345"),
						ServiceName:   aws.String("com.amazonaws.vpce-svc.wrong"),
					}},
				}, nil)
				mockEC2.EXPECT().DeleteVpcEndpoints(gomock.Any(), &ec2v2.DeleteVpcEndpointsInput{
					VpcEndpointIds: []string{"vpce-12345"},
				}).Return(&ec2v2.DeleteVpcEndpointsOutput{}, nil)
			},
			expectError:    true,
			expectErrorMsg: "not pointing to the existing .Status.EndpointServiceName",
		},
		{
			name: "When endpoint subnets changed it should call ModifyVpcEndpoint",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Spec: hyperv1.AWSEndpointServiceSpec{
					SubnetIDs: []string{"subnet-new"},
				},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointServiceName: "com.amazonaws.vpce-svc.test",
					EndpointID:          "vpce-12345",
					SecurityGroupID:     "sg-12345",
				},
			},
			hcp: baseHCP(),
			setupMocks: func(mockEC2 *awsapi.MockEC2API, mockRoute53 *awsapi.MockROUTE53API, mockBuilder *MockawsClientProvider) {
				setupSGMock(mockEC2)
				mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), &ec2v2.DescribeVpcEndpointsInput{
					VpcEndpointIds: []string{"vpce-12345"},
				}).Return(&ec2v2.DescribeVpcEndpointsOutput{
					VpcEndpoints: []ec2types.VpcEndpoint{{
						VpcEndpointId: aws.String("vpce-12345"),
						ServiceName:   aws.String("com.amazonaws.vpce-svc.test"),
						SubnetIds:     []string{"subnet-old"},
						Groups:        []ec2types.SecurityGroupIdentifier{{GroupId: aws.String("sg-12345")}},
						DnsEntries:    []ec2types.DnsEntry{},
					}},
				}, nil)
				mockEC2.EXPECT().ModifyVpcEndpoint(gomock.Any(), &ec2v2.ModifyVpcEndpointInput{
					VpcEndpointId:   aws.String("vpce-12345"),
					AddSubnetIds:    []string{"subnet-new"},
					RemoveSubnetIds: []string{"subnet-old"},
				}).Return(&ec2v2.ModifyVpcEndpointOutput{}, nil)
			},
			expectError: false,
		},
		{
			name: "When no endpoint and hasAWSConfig is false it should return error",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointServiceName: "com.amazonaws.vpce-svc.test",
					SecurityGroupID:     "sg-12345",
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters-test"},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS:  &hyperv1.AWSPlatformSpec{},
					},
					Networking: hyperv1.ClusterNetworking{
						MachineNetwork: []hyperv1.MachineNetworkEntry{
							{CIDR: *ipnet.MustParseCIDR("10.0.0.0/16")},
						},
					},
				},
			},
			setupMocks: func(mockEC2 *awsapi.MockEC2API, mockRoute53 *awsapi.MockROUTE53API, mockBuilder *MockawsClientProvider) {
				setupSGMock(mockEC2)
			},
			expectError:    true,
			expectErrorMsg: "AWS platform information not provided",
		},
		{
			name: "When no endpoint and orphaned endpoint found it should adopt it",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointServiceName: "com.amazonaws.vpce-svc.test",
					SecurityGroupID:     "sg-12345",
				},
			},
			hcp: baseHCP(),
			setupMocks: func(mockEC2 *awsapi.MockEC2API, mockRoute53 *awsapi.MockROUTE53API, mockBuilder *MockawsClientProvider) {
				setupSGMock(mockEC2)
				mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), &ec2v2.DescribeVpcEndpointsInput{
					Filters: apiTagToEC2Filter("kube-apiserver-private", nil),
				}).Return(&ec2v2.DescribeVpcEndpointsOutput{
					VpcEndpoints: []ec2types.VpcEndpoint{{
						VpcEndpointId: aws.String("vpce-adopted"),
						ServiceName:   aws.String("com.amazonaws.vpce-svc.test"),
						DnsEntries:    []ec2types.DnsEntry{},
					}},
				}, nil)
			},
			expectError:    false,
			expectEndpoint: "vpce-adopted",
		},
		{
			name: "When no endpoint and orphan has wrong service it should delete and return error",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointServiceName: "com.amazonaws.vpce-svc.test",
					SecurityGroupID:     "sg-12345",
				},
			},
			hcp: baseHCP(),
			setupMocks: func(mockEC2 *awsapi.MockEC2API, mockRoute53 *awsapi.MockROUTE53API, mockBuilder *MockawsClientProvider) {
				setupSGMock(mockEC2)
				mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), &ec2v2.DescribeVpcEndpointsInput{
					Filters: apiTagToEC2Filter("kube-apiserver-private", nil),
				}).Return(&ec2v2.DescribeVpcEndpointsOutput{
					VpcEndpoints: []ec2types.VpcEndpoint{{
						VpcEndpointId: aws.String("vpce-orphan"),
						ServiceName:   aws.String("com.amazonaws.vpce-svc.wrong"),
					}},
				}, nil)
				mockEC2.EXPECT().DeleteVpcEndpoints(gomock.Any(), &ec2v2.DeleteVpcEndpointsInput{
					VpcEndpointIds: []string{"vpce-orphan"},
				}).Return(&ec2v2.DeleteVpcEndpointsOutput{}, nil)
			},
			expectError:    true,
			expectErrorMsg: "not pointing to the existing .Status.EndpointServiceName",
		},
		{
			name: "When no endpoint and no orphan found it should create new endpoint",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Spec: hyperv1.AWSEndpointServiceSpec{
					SubnetIDs: []string{"subnet-12345"},
				},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointServiceName: "com.amazonaws.vpce-svc.test",
					SecurityGroupID:     "sg-12345",
				},
			},
			hcp: baseHCP(),
			setupMocks: func(mockEC2 *awsapi.MockEC2API, mockRoute53 *awsapi.MockROUTE53API, mockBuilder *MockawsClientProvider) {
				setupSGMock(mockEC2)
				mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), &ec2v2.DescribeVpcEndpointsInput{
					Filters: apiTagToEC2Filter("kube-apiserver-private", nil),
				}).Return(&ec2v2.DescribeVpcEndpointsOutput{
					VpcEndpoints: []ec2types.VpcEndpoint{},
				}, nil)
				mockEC2.EXPECT().CreateVpcEndpoint(gomock.Any(), &ec2v2.CreateVpcEndpointInput{
					SecurityGroupIds: []string{"sg-12345"},
					ServiceName:      aws.String("com.amazonaws.vpce-svc.test"),
					VpcId:            aws.String("vpc-12345"),
					VpcEndpointType:  ec2types.VpcEndpointTypeInterface,
					SubnetIds:        []string{"subnet-12345"},
					TagSpecifications: []ec2types.TagSpecification{{
						ResourceType: ec2types.ResourceTypeVpcEndpoint,
						Tags:         apiTagToEC2Tag("kube-apiserver-private", nil),
					}},
				}).Return(&ec2v2.CreateVpcEndpointOutput{
					VpcEndpoint: &ec2types.VpcEndpoint{
						VpcEndpointId: aws.String("vpce-new"),
						DnsEntries:    []ec2types.DnsEntry{},
					},
				}, nil)
			},
			expectError:    false,
			expectEndpoint: "vpce-new",
		},
		{
			name: "When CreateVpcEndpoint fails it should return error with code",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointServiceName: "com.amazonaws.vpce-svc.test",
					SecurityGroupID:     "sg-12345",
				},
			},
			hcp: baseHCP(),
			setupMocks: func(mockEC2 *awsapi.MockEC2API, mockRoute53 *awsapi.MockROUTE53API, mockBuilder *MockawsClientProvider) {
				setupSGMock(mockEC2)
				mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), &ec2v2.DescribeVpcEndpointsInput{
					Filters: apiTagToEC2Filter("kube-apiserver-private", nil),
				}).Return(&ec2v2.DescribeVpcEndpointsOutput{
					VpcEndpoints: []ec2types.VpcEndpoint{},
				}, nil)
				mockEC2.EXPECT().CreateVpcEndpoint(gomock.Any(), &ec2v2.CreateVpcEndpointInput{
					SecurityGroupIds: []string{"sg-12345"},
					ServiceName:      aws.String("com.amazonaws.vpce-svc.test"),
					VpcId:            aws.String("vpc-12345"),
					VpcEndpointType:  ec2types.VpcEndpointTypeInterface,
					TagSpecifications: []ec2types.TagSpecification{{
						ResourceType: ec2types.ResourceTypeVpcEndpoint,
						Tags:         apiTagToEC2Tag("kube-apiserver-private", nil),
					}},
				}).Return(nil, &smithy.GenericAPIError{Code: "InvalidParameter", Message: "bad param"})
			},
			expectError:    true,
			expectErrorMsg: "InvalidParameter",
		},
		{
			name: "When DNS zone is not cached it should lookup zone and create record",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointServiceName: "com.amazonaws.vpce-svc.test",
					EndpointID:          "vpce-12345",
					SecurityGroupID:     "sg-12345",
				},
			},
			hcp: baseHCP(),
			setupMocks: func(mockEC2 *awsapi.MockEC2API, mockRoute53 *awsapi.MockROUTE53API, mockBuilder *MockawsClientProvider) {
				setupSGMock(mockEC2)
				mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), &ec2v2.DescribeVpcEndpointsInput{
					VpcEndpointIds: []string{"vpce-12345"},
				}).Return(&ec2v2.DescribeVpcEndpointsOutput{
					VpcEndpoints: []ec2types.VpcEndpoint{{
						VpcEndpointId: aws.String("vpce-12345"),
						ServiceName:   aws.String("com.amazonaws.vpce-svc.test"),
						SubnetIds:     []string{},
						Groups:        []ec2types.SecurityGroupIdentifier{{GroupId: aws.String("sg-12345")}},
						DnsEntries: []ec2types.DnsEntry{
							{DnsName: aws.String("vpce-12345.vpce-svc.us-east-1.vpce.amazonaws.com")},
						},
					}},
				}, nil)
				// Called once: only in the if-condition that checks whether the zone ID is cached.
				// The if-body does lookupZoneID + setLocalHostedZoneID without calling getLocalHostedZoneID again.
				mockBuilder.EXPECT().getLocalHostedZoneID().Return("").Times(1)
				// The paginator passes an options function as a variadic arg, so we need gomock.Any() for it.
				mockRoute53.EXPECT().ListHostedZones(gomock.Any(), &route53sdk.ListHostedZonesInput{}, gomock.Any()).Return(&route53sdk.ListHostedZonesOutput{
					HostedZones: []route53types.HostedZone{{
						Id:     aws.String("/hostedzone/Z12345"),
						Name:   aws.String("test.hypershift.local."),
						Config: &route53types.HostedZoneConfig{PrivateZone: true},
					}},
					IsTruncated: false,
				}, nil)
				mockBuilder.EXPECT().setLocalHostedZoneID("Z12345").Times(1)
				mockRoute53.EXPECT().ChangeResourceRecordSets(gomock.Any(), &route53sdk.ChangeResourceRecordSetsInput{
					HostedZoneId: aws.String("Z12345"),
					ChangeBatch: &route53types.ChangeBatch{
						Changes: []route53types.Change{{
							Action: route53types.ChangeActionUpsert,
							ResourceRecordSet: &route53types.ResourceRecordSet{
								Name: aws.String("api.test.hypershift.local"),
								Type: route53types.RRTypeCname,
								TTL:  aws.Int64(300),
								ResourceRecords: []route53types.ResourceRecord{{
									Value: aws.String("vpce-12345.vpce-svc.us-east-1.vpce.amazonaws.com"),
								}},
							},
						}},
					},
				}).Return(&route53sdk.ChangeResourceRecordSetsOutput{}, nil).Times(1)
			},
			expectError: false,
		},
		{
			name: "When DNS zone is cached it should skip lookup and create record directly",
			awsEndpointSvc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Status: hyperv1.AWSEndpointServiceStatus{
					EndpointServiceName: "com.amazonaws.vpce-svc.test",
					EndpointID:          "vpce-12345",
					SecurityGroupID:     "sg-12345",
				},
			},
			hcp: baseHCP(),
			setupMocks: func(mockEC2 *awsapi.MockEC2API, mockRoute53 *awsapi.MockROUTE53API, mockBuilder *MockawsClientProvider) {
				setupSGMock(mockEC2)
				mockEC2.EXPECT().DescribeVpcEndpoints(gomock.Any(), &ec2v2.DescribeVpcEndpointsInput{
					VpcEndpointIds: []string{"vpce-12345"},
				}).Return(&ec2v2.DescribeVpcEndpointsOutput{
					VpcEndpoints: []ec2types.VpcEndpoint{{
						VpcEndpointId: aws.String("vpce-12345"),
						ServiceName:   aws.String("com.amazonaws.vpce-svc.test"),
						SubnetIds:     []string{},
						Groups:        []ec2types.SecurityGroupIdentifier{{GroupId: aws.String("sg-12345")}},
						DnsEntries: []ec2types.DnsEntry{
							{DnsName: aws.String("vpce-12345.vpce-svc.us-east-1.vpce.amazonaws.com")},
						},
					}},
				}, nil)
				// Called twice: once in the if-condition that checks whether the zone ID is cached
				// (returns non-empty), and once in the else-body to retrieve the cached zone ID.
				mockBuilder.EXPECT().getLocalHostedZoneID().Return("Z-cached").Times(2)
				mockRoute53.EXPECT().ChangeResourceRecordSets(gomock.Any(), &route53sdk.ChangeResourceRecordSetsInput{
					HostedZoneId: aws.String("Z-cached"),
					ChangeBatch: &route53types.ChangeBatch{
						Changes: []route53types.Change{{
							Action: route53types.ChangeActionUpsert,
							ResourceRecordSet: &route53types.ResourceRecordSet{
								Name: aws.String("api.test.hypershift.local"),
								Type: route53types.RRTypeCname,
								TTL:  aws.Int64(300),
								ResourceRecords: []route53types.ResourceRecord{{
									Value: aws.String("vpce-12345.vpce-svc.us-east-1.vpce.amazonaws.com"),
								}},
							},
						}},
					},
				}).Return(&route53sdk.ChangeResourceRecordSetsOutput{}, nil).Times(1)
			},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			mockCtrl := gomock.NewController(t)
			mockEC2 := awsapi.NewMockEC2API(mockCtrl)
			mockRoute53 := awsapi.NewMockROUTE53API(mockCtrl)
			mockBuilder := NewMockawsClientProvider(mockCtrl)

			tc.setupMocks(mockEC2, mockRoute53, mockBuilder)

			scheme := runtime.NewScheme()
			_ = hyperv1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			reconciler := &AWSEndpointServiceReconciler{
				Client:           fakeClient,
				awsClientBuilder: mockBuilder,
			}

			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log.WithName("test"))
			err := reconciler.reconcileAWSEndpointService(ctx, tc.awsEndpointSvc, tc.hcp, mockEC2, mockRoute53)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.expectErrorMsg != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.expectErrorMsg))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			if tc.expectEndpoint != "" {
				g.Expect(tc.awsEndpointSvc.Status.EndpointID).To(Equal(tc.expectEndpoint))
			}

			if tc.expectResetID {
				g.Expect(tc.awsEndpointSvc.Status.EndpointID).To(BeEmpty())
			}
		})
	}
}

func TestReconcileNonDeletion(t *testing.T) {
	testCases := []struct {
		name           string
		objects        []crclient.Object
		expectError    bool
		expectErrorMsg string
		expectRequeue  bool
		expectEmpty    bool
	}{
		{
			name:        "When AWSEndpointService is not found it should return empty result",
			objects:     []crclient.Object{},
			expectEmpty: true,
		},
		{
			name: "When EndpointServiceName is empty it should return empty result",
			objects: []crclient.Object{
				&hyperv1.AWSEndpointService{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "kube-apiserver-private",
						Namespace:  "clusters-test",
						Finalizers: []string{finalizer},
					},
					Status: hyperv1.AWSEndpointServiceStatus{
						EndpointServiceName: "",
					},
				},
			},
			expectEmpty: true,
		},
		{
			name: "When no HostedControlPlane exists it should return empty result",
			objects: []crclient.Object{
				&hyperv1.AWSEndpointService{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "kube-apiserver-private",
						Namespace:  "clusters-test",
						Finalizers: []string{finalizer},
					},
					Status: hyperv1.AWSEndpointServiceStatus{
						EndpointServiceName: "com.amazonaws.vpce-svc.test",
					},
				},
			},
			expectEmpty: true,
		},
		{
			name: "When multiple HostedControlPlanes exist it should return error",
			objects: []crclient.Object{
				&hyperv1.AWSEndpointService{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "kube-apiserver-private",
						Namespace:  "clusters-test",
						Finalizers: []string{finalizer},
					},
					Status: hyperv1.AWSEndpointServiceStatus{
						EndpointServiceName: "com.amazonaws.vpce-svc.test",
					},
				},
				&hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-1",
						Namespace: "clusters-test",
					},
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{}},
					},
				},
				&hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hcp-2",
						Namespace: "clusters-test",
					},
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{}},
					},
				},
			},
			expectError:    true,
			expectErrorMsg: "unexpected number of HostedControlPlanes",
		},
		{
			name: "When reconcileAWSEndpointService fails it should set condition to False",
			objects: []crclient.Object{
				&hyperv1.AWSEndpointService{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "kube-apiserver-private",
						Namespace:  "clusters-test",
						Finalizers: []string{finalizer},
					},
					Status: hyperv1.AWSEndpointServiceStatus{
						EndpointServiceName: "com.amazonaws.vpce-svc.test",
						SecurityGroupID:     "sg-12345",
					},
				},
				&hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test",
						Namespace: "clusters-test",
					},
					Spec: hyperv1.HostedControlPlaneSpec{
						InfraID: "test-infra",
						Platform: hyperv1.PlatformSpec{
							Type: hyperv1.AWSPlatform,
							AWS:  &hyperv1.AWSPlatformSpec{},
						},
						Networking: hyperv1.ClusterNetworking{
							MachineNetwork: []hyperv1.MachineNetworkEntry{
								{CIDR: *ipnet.MustParseCIDR("10.0.0.0/16")},
							},
						},
					},
				},
			},
			expectError:    true,
			expectErrorMsg: "cannot list security groups",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			mockCtrl := gomock.NewController(t)

			scheme := runtime.NewScheme()
			_ = hyperv1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			clientBuilder := fake.NewClientBuilder().WithScheme(scheme)
			if len(tc.objects) > 0 {
				clientBuilder = clientBuilder.WithObjects(tc.objects...)
				// Register status subresource for AWSEndpointService objects.
				for _, obj := range tc.objects {
					if _, ok := obj.(*hyperv1.AWSEndpointService); ok {
						clientBuilder = clientBuilder.WithStatusSubresource(obj)
					}
				}
			}
			fakeClient := clientBuilder.Build()

			mockBuilder := NewMockawsClientProvider(mockCtrl)

			// Set up mock expectations only for cases that reach the AWS client calls.
			if tc.expectErrorMsg == "cannot list security groups" {
				mockBuilder.EXPECT().initializeWithHCP(gomock.Any(), gomock.Any()).Times(1)
				mockEC2 := awsapi.NewMockEC2API(mockCtrl)
				mockRoute53 := awsapi.NewMockROUTE53API(mockCtrl)
				mockBuilder.EXPECT().getClients(gomock.Any()).Return(mockEC2, mockRoute53, nil).Times(1)
				// DescribeSecurityGroups with Filters (from GetSecurityGroup) returns error.
				mockEC2.EXPECT().DescribeSecurityGroups(gomock.Any(), &ec2v2.DescribeSecurityGroupsInput{
					Filters: vpcEndpointSecurityGroupFilter("test-infra", "kube-apiserver-private"),
				}).Return(nil, fmt.Errorf("cannot list security groups")).Times(1)
			}

			reconciler := &AWSEndpointServiceReconciler{
				Client:           fakeClient,
				awsClientBuilder: mockBuilder,
			}

			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log.WithName("test"))
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      "kube-apiserver-private",
					Namespace: "clusters-test",
				},
			})

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.expectErrorMsg != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.expectErrorMsg))
				}
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			if tc.expectEmpty {
				g.Expect(result).To(Equal(ctrl.Result{}))
			}

			if tc.expectRequeue {
				g.Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			}
		})
	}
}

func TestReconcileAWSEndpointSecurityGroup(t *testing.T) {
	baseHCP := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters-test"},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra",
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS: &hyperv1.AWSPlatformSpec{
					CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
						VPC: "vpc-12345",
						Subnet: &hyperv1.AWSResourceReference{
							ID: aws.String("subnet-12345"),
						},
					},
				},
			},
			Networking: hyperv1.ClusterNetworking{
				MachineNetwork: []hyperv1.MachineNetworkEntry{
					{CIDR: *ipnet.MustParseCIDR("10.0.0.0/16")},
				},
			},
		},
	}

	correctPermissions := []ec2types.IpPermission{{
		FromPort:   aws.Int32(6443),
		ToPort:     aws.Int32(6443),
		IpProtocol: aws.String("tcp"),
		IpRanges:   []ec2types.IpRange{{CidrIp: aws.String("10.0.0.0/16"), Description: aws.String("Control plane service")}},
	}}

	sgFilter := &ec2v2.DescribeSecurityGroupsInput{
		Filters: vpcEndpointSecurityGroupFilter("test-infra", "kube-apiserver-private"),
	}

	testCases := []struct {
		name        string
		svc         *hyperv1.AWSEndpointService
		setupMock   func(*awsapi.MockEC2API)
		expectError bool
		expectSGID  string
	}{
		{
			name: "When SG exists with correct permissions it should not call AuthorizeIngress",
			svc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Status:     hyperv1.AWSEndpointServiceStatus{SecurityGroupID: "sg-12345"},
			},
			setupMock: func(m *awsapi.MockEC2API) {
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), sgFilter).Return(&ec2v2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{{
						GroupId:       aws.String("sg-12345"),
						IpPermissions: correctPermissions,
					}},
				}, nil).Times(1)
			},
			expectError: false,
			expectSGID:  "sg-12345",
		},
		{
			name: "When SG exists with missing permissions it should call AuthorizeIngress",
			svc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Status:     hyperv1.AWSEndpointServiceStatus{SecurityGroupID: "sg-12345"},
			},
			setupMock: func(m *awsapi.MockEC2API) {
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), sgFilter).Return(&ec2v2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{{
						GroupId:       aws.String("sg-12345"),
						IpPermissions: []ec2types.IpPermission{},
					}},
				}, nil).Times(1)
				m.EXPECT().AuthorizeSecurityGroupIngress(gomock.Any(), &ec2v2.AuthorizeSecurityGroupIngressInput{
					GroupId:       aws.String("sg-12345"),
					IpPermissions: correctPermissions,
				}).Return(&ec2v2.AuthorizeSecurityGroupIngressOutput{}, nil).Times(1)
			},
			expectError: false,
			expectSGID:  "sg-12345",
		},
		{
			name: "When AuthorizeIngress returns Duplicate it should not return error",
			svc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Status:     hyperv1.AWSEndpointServiceStatus{SecurityGroupID: "sg-12345"},
			},
			setupMock: func(m *awsapi.MockEC2API) {
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), sgFilter).Return(&ec2v2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{{
						GroupId:       aws.String("sg-12345"),
						IpPermissions: []ec2types.IpPermission{},
					}},
				}, nil).Times(1)
				m.EXPECT().AuthorizeSecurityGroupIngress(gomock.Any(), &ec2v2.AuthorizeSecurityGroupIngressInput{
					GroupId:       aws.String("sg-12345"),
					IpPermissions: correctPermissions,
				}).Return(nil, &smithy.GenericAPIError{Code: "InvalidPermission.Duplicate", Message: "duplicate"}).Times(1)
			},
			expectError: false,
			expectSGID:  "sg-12345",
		},
		{
			name: "When status SG ID mismatches found SG it should update status",
			svc: &hyperv1.AWSEndpointService{
				ObjectMeta: metav1.ObjectMeta{Name: "kube-apiserver-private", Namespace: "clusters-test"},
				Status:     hyperv1.AWSEndpointServiceStatus{SecurityGroupID: "sg-old"},
			},
			setupMock: func(m *awsapi.MockEC2API) {
				m.EXPECT().DescribeSecurityGroups(gomock.Any(), sgFilter).Return(&ec2v2.DescribeSecurityGroupsOutput{
					SecurityGroups: []ec2types.SecurityGroup{{
						GroupId:       aws.String("sg-new"),
						IpPermissions: correctPermissions,
					}},
				}, nil).Times(1)
			},
			expectError: false,
			expectSGID:  "sg-new",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			mockCtrl := gomock.NewController(t)
			mockEC2 := awsapi.NewMockEC2API(mockCtrl)
			tc.setupMock(mockEC2)

			reconciler := &AWSEndpointServiceReconciler{}
			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log.WithName("test"))
			err := reconciler.reconcileAWSEndpointSecurityGroup(ctx, mockEC2, tc.svc, baseHCP)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			if tc.expectSGID != "" {
				g.Expect(tc.svc.Status.SecurityGroupID).To(Equal(tc.expectSGID))
			}
		})
	}
}
