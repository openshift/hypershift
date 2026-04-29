package nodeclass

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	hyperapi "github.com/openshift/hypershift/support/api"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/upsert"

	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

const testInfraID = "test-infra"

func TestReconcileEC2NodeClass(t *testing.T) {
	userDataSecret := &corev1.Secret{
		Data: map[string][]byte{
			"value": []byte("test-userdata"),
		},
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				hyperkarpenterv1.UserDataAMILabel: "ami-123",
			},
		},
	}

	// Default HCP for test cases that don't specify their own
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra",
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
		},
	}

	testCases := []struct {
		name         string
		spec         hyperkarpenterv1.OpenshiftEC2NodeClassSpec
		hcp          *hyperv1.HostedControlPlane
		expectedSpec awskarpenterv1.EC2NodeClassSpec
	}{
		{
			name: "When OpenshiftEC2NodeClassSpec.spec is empty it should reconcile the EC2NodeClass with default values",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"kubernetes.io/role/internal-elb":                    "1",
							fmt.Sprintf("kubernetes.io/cluster/%s", testInfraID): "*",
						},
					},
				},
				SecurityGroupSelectorTerms: []awskarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": testInfraID,
						},
					},
				},
				BlockDeviceMappings: []*awskarpenterv1.BlockDeviceMapping{
					{
						DeviceName: ptr.To("/dev/xvda"),
						EBS: &awskarpenterv1.BlockDevice{
							VolumeSize: ptr.To(resource.MustParse("120Gi")),
							VolumeType: ptr.To("gp3"),
							Encrypted:  ptr.To(true),
						},
					},
				},
			},
		},
		{
			name: "when OpenshiftEC2NodeClassSpec.spec is defined, all fields should be mirrored",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"testKey": "testValue",
						},
						ID: "testID",
					},
				},
				SecurityGroupSelectorTerms: []hyperkarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"testKey": "testValue",
						},
						Name: "testName",
					},
				},
				IPAddressAssociation: hyperkarpenterv1.IPAddressAssociationPublic,
				Tags: map[string]string{
					"tag1": "value1",
				},
				BlockDeviceMappings: []hyperkarpenterv1.BlockDeviceMapping{
					{
						DeviceName: "xvdh",
						EBS: hyperkarpenterv1.BlockDevice{
							Encrypted:     hyperkarpenterv1.EncryptionStateEncrypted,
							VolumeSizeGiB: 20,
						},
					},
				},
				InstanceStorePolicy: hyperkarpenterv1.InstanceStorePolicyRAID0,
				Monitoring:          hyperkarpenterv1.MonitoringStateDetailed,
				MetadataOptions: hyperkarpenterv1.MetadataOptions{
					Access:                  hyperkarpenterv1.MetadataAccessHTTPEndpoint,
					HTTPIPProtocol:          hyperkarpenterv1.MetadataHTTPProtocolIPv4,
					HTTPPutResponseHopLimit: 1,
					HTTPTokens:              hyperkarpenterv1.MetadataHTTPTokensStateRequired,
				},
			},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"testKey": "testValue",
						},
						ID: "testID",
					},
				},
				SecurityGroupSelectorTerms: []awskarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"testKey": "testValue",
						},
						Name: "testName",
					},
				},
				AssociatePublicIPAddress: ptr.To(true),
				Tags: map[string]string{
					"tag1": "value1",
				},
				BlockDeviceMappings: []*awskarpenterv1.BlockDeviceMapping{
					{
						DeviceName: ptr.To("xvdh"),
						EBS: &awskarpenterv1.BlockDevice{
							Encrypted:  ptr.To(true),
							VolumeSize: ptr.To(resource.MustParse("20Gi")),
						},
					},
				},
				InstanceStorePolicy: ptr.To(awskarpenterv1.InstanceStorePolicyRAID0),
				DetailedMonitoring:  ptr.To(true),
				MetadataOptions: &awskarpenterv1.MetadataOptions{
					HTTPEndpoint:            ptr.To("enabled"),
					HTTPProtocolIPv6:        ptr.To("disabled"),
					HTTPPutResponseHopLimit: ptr.To(int64(1)),
					HTTPTokens:              ptr.To("required"),
				},
			},
		},
		{
			name: "When MetadataOptions is specified it should be mapped to EC2NodeClass",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				MetadataOptions: hyperkarpenterv1.MetadataOptions{
					Access:                  hyperkarpenterv1.MetadataAccessHTTPEndpoint,
					HTTPIPProtocol:          hyperkarpenterv1.MetadataHTTPProtocolIPv4,
					HTTPPutResponseHopLimit: 2,
					HTTPTokens:              hyperkarpenterv1.MetadataHTTPTokensStateRequired,
				},
			},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"kubernetes.io/role/internal-elb":                    "1",
							fmt.Sprintf("kubernetes.io/cluster/%s", testInfraID): "*",
						},
					},
				},
				SecurityGroupSelectorTerms: []awskarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": testInfraID,
						},
					},
				},
				BlockDeviceMappings: []*awskarpenterv1.BlockDeviceMapping{
					{
						DeviceName: ptr.To("/dev/xvda"),
						EBS: &awskarpenterv1.BlockDevice{
							VolumeSize: ptr.To(resource.MustParse("120Gi")),
							VolumeType: ptr.To("gp3"),
							Encrypted:  ptr.To(true),
						},
					},
				},
				MetadataOptions: &awskarpenterv1.MetadataOptions{
					HTTPEndpoint:            ptr.To("enabled"),
					HTTPProtocolIPv6:        ptr.To("disabled"),
					HTTPPutResponseHopLimit: ptr.To(int64(2)),
					HTTPTokens:              ptr.To("required"),
				},
			},
		},
		{
			name: "When MetadataOptions is nil it should not be set on EC2NodeClass",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"kubernetes.io/role/internal-elb":                    "1",
							fmt.Sprintf("kubernetes.io/cluster/%s", testInfraID): "*",
						},
					},
				},
				SecurityGroupSelectorTerms: []awskarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": testInfraID,
						},
					},
				},
				BlockDeviceMappings: []*awskarpenterv1.BlockDeviceMapping{
					{
						DeviceName: ptr.To("/dev/xvda"),
						EBS: &awskarpenterv1.BlockDevice{
							VolumeSize: ptr.To(resource.MustParse("120Gi")),
							VolumeType: ptr.To("gp3"),
							Encrypted:  ptr.To(true),
						},
					},
				},
			},
		},
		{
			name: "When MetadataOptions has only HTTPTokens set to optional it should allow IMDSv1",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				MetadataOptions: hyperkarpenterv1.MetadataOptions{
					HTTPTokens: hyperkarpenterv1.MetadataHTTPTokensStateOptional,
				},
			},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"kubernetes.io/role/internal-elb":                    "1",
							fmt.Sprintf("kubernetes.io/cluster/%s", testInfraID): "*",
						},
					},
				},
				SecurityGroupSelectorTerms: []awskarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": testInfraID,
						},
					},
				},
				BlockDeviceMappings: []*awskarpenterv1.BlockDeviceMapping{
					{
						DeviceName: ptr.To("/dev/xvda"),
						EBS: &awskarpenterv1.BlockDevice{
							VolumeSize: ptr.To(resource.MustParse("120Gi")),
							VolumeType: ptr.To("gp3"),
							Encrypted:  ptr.To(true),
						},
					},
				},
				MetadataOptions: &awskarpenterv1.MetadataOptions{
					HTTPTokens: ptr.To("optional"),
				},
			},
		},
		{
			name: "When HCP has instance-profile annotation it should set InstanceProfile on EC2NodeClass",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.AWSKarpenterDefaultInstanceProfile: "test-instance-profile",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: testInfraID,
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"kubernetes.io/role/internal-elb":                    "1",
							fmt.Sprintf("kubernetes.io/cluster/%s", testInfraID): "*",
						},
					},
				},
				SecurityGroupSelectorTerms: []awskarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": testInfraID,
						},
					},
				},
				BlockDeviceMappings: []*awskarpenterv1.BlockDeviceMapping{
					{
						DeviceName: ptr.To("/dev/xvda"),
						EBS: &awskarpenterv1.BlockDevice{
							VolumeSize: ptr.To(resource.MustParse("120Gi")),
							VolumeType: ptr.To("gp3"),
							Encrypted:  ptr.To(true),
						},
					},
				},
				InstanceProfile: ptr.To("test-instance-profile"),
			},
		},
		{
			name: "When HCP has empty instance-profile annotation it should NOT set InstanceProfile",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.AWSKarpenterDefaultInstanceProfile: "",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: testInfraID,
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"kubernetes.io/role/internal-elb":                    "1",
							fmt.Sprintf("kubernetes.io/cluster/%s", testInfraID): "*",
						},
					},
				},
				SecurityGroupSelectorTerms: []awskarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": testInfraID,
						},
					},
				},
				BlockDeviceMappings: []*awskarpenterv1.BlockDeviceMapping{
					{
						DeviceName: ptr.To("/dev/xvda"),
						EBS: &awskarpenterv1.BlockDevice{
							VolumeSize: ptr.To(resource.MustParse("120Gi")),
							VolumeType: ptr.To("gp3"),
							Encrypted:  ptr.To(true),
						},
					},
				},
			},
		},
		{
			name: "When HCP has no instance-profile annotation it should NOT set InstanceProfile",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"kubernetes.io/role/internal-elb":                    "1",
							fmt.Sprintf("kubernetes.io/cluster/%s", testInfraID): "*",
						},
					},
				},
				SecurityGroupSelectorTerms: []awskarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": testInfraID,
						},
					},
				},
				BlockDeviceMappings: []*awskarpenterv1.BlockDeviceMapping{
					{
						DeviceName: ptr.To("/dev/xvda"),
						EBS: &awskarpenterv1.BlockDevice{
							VolumeSize: ptr.To(resource.MustParse("120Gi")),
							VolumeType: ptr.To("gp3"),
							Encrypted:  ptr.To(true),
						},
					},
				},
			},
		},
		{
			name: "when platform tags exist in HostedControlPlane, they should be merged with nodeclass tags with platform tags taking precedence",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Tags: map[string]string{
					"nodeclass-tag":   "nodeclass-value",
					"conflicting-tag": "nodeclass-value", // This should be overridden by platform tag
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-managed", Value: "true"},
								{Key: "red-hat-clustertype", Value: "rosa"},
								{Key: "conflicting-tag", Value: "platform-value"},
							},
						},
					},
				},
			},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"kubernetes.io/role/internal-elb":                    "1",
							fmt.Sprintf("kubernetes.io/cluster/%s", testInfraID): "*",
						},
					},
				},
				SecurityGroupSelectorTerms: []awskarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": "test-infra",
						},
					},
				},
				Tags: map[string]string{
					"nodeclass-tag":       "nodeclass-value",
					"conflicting-tag":     "platform-value", // Platform tag wins
					"red-hat-managed":     "true",
					"red-hat-clustertype": "rosa",
				},
				BlockDeviceMappings: []*awskarpenterv1.BlockDeviceMapping{
					{
						DeviceName: ptr.To("/dev/xvda"),
						EBS: &awskarpenterv1.BlockDevice{
							VolumeSize: ptr.To(resource.MustParse("120Gi")),
							VolumeType: ptr.To("gp3"),
							Encrypted:  ptr.To(true),
						},
					},
				},
			},
		},
		{
			name: "when nodeclass has conflicting red-hat-clustertype tag, platform tag should take precedence",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				Tags: map[string]string{
					"red-hat-clustertype": "some-other-value", // This should be overridden by platform tag
					"nodeclass-only-tag":  "nodeclass-value",
				},
			},
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-clustertype", Value: "rosa"}, // This should override nodeclass tag
								{Key: "red-hat-managed", Value: "true"},
							},
						},
					},
				},
			},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"kubernetes.io/role/internal-elb":                    "1",
							fmt.Sprintf("kubernetes.io/cluster/%s", testInfraID): "*",
						},
					},
				},
				SecurityGroupSelectorTerms: []awskarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": "test-infra",
						},
					},
				},
				Tags: map[string]string{
					"red-hat-clustertype": "rosa",            // Platform tag won over nodeclass tag
					"red-hat-managed":     "true",            // Platform tag added
					"nodeclass-only-tag":  "nodeclass-value", // Nodeclass tag preserved
				},
				BlockDeviceMappings: []*awskarpenterv1.BlockDeviceMapping{
					{
						DeviceName: ptr.To("/dev/xvda"),
						EBS: &awskarpenterv1.BlockDevice{
							VolumeSize: ptr.To(resource.MustParse("120Gi")),
							VolumeType: ptr.To("gp3"),
							Encrypted:  ptr.To(true),
						},
					},
				},
			},
		},
		{
			name: "when CapacityReservationSelectorTerms are set it should mirror them to EC2NodeClass",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
				CapacityReservationSelectorTerms: []hyperkarpenterv1.CapacityReservationSelectorTerm{
					{
						Tags:                  map[string]string{"karpenter.sh/discovery": "my-cr"},
						ID:                    "cr-1234567890abcdef0",
						OwnerID:               "123456789012",
						InstanceMatchCriteria: hyperkarpenterv1.InstanceMatchCriteriaTargeted,
					},
				},
			},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"kubernetes.io/role/internal-elb":                    "1",
							fmt.Sprintf("kubernetes.io/cluster/%s", testInfraID): "*",
						},
					},
				},
				SecurityGroupSelectorTerms: []awskarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": testInfraID,
						},
					},
				},
				CapacityReservationSelectorTerms: []awskarpenterv1.CapacityReservationSelectorTerm{
					{
						Tags:                  map[string]string{"karpenter.sh/discovery": "my-cr"},
						ID:                    "cr-1234567890abcdef0",
						OwnerID:               "123456789012",
						InstanceMatchCriteria: "targeted",
					},
				},
				BlockDeviceMappings: []*awskarpenterv1.BlockDeviceMapping{
					{
						DeviceName: ptr.To("/dev/xvda"),
						EBS: &awskarpenterv1.BlockDevice{
							VolumeSize: ptr.To(resource.MustParse("120Gi")),
							VolumeType: ptr.To("gp3"),
							Encrypted:  ptr.To(true),
						},
					},
				},
			},
		},
		{
			name: "when CapacityReservationSelectorTerms are not set it should not set them on EC2NodeClass",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"kubernetes.io/role/internal-elb":                    "1",
							fmt.Sprintf("kubernetes.io/cluster/%s", testInfraID): "*",
						},
					},
				},
				SecurityGroupSelectorTerms: []awskarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": testInfraID,
						},
					},
				},
				BlockDeviceMappings: []*awskarpenterv1.BlockDeviceMapping{
					{
						DeviceName: ptr.To("/dev/xvda"),
						EBS: &awskarpenterv1.BlockDevice{
							VolumeSize: ptr.To(resource.MustParse("120Gi")),
							VolumeType: ptr.To("gp3"),
							Encrypted:  ptr.To(true),
						},
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			// Create HCP with test case annotations
			if tc.hcp == nil {
				tc.hcp = hcp
			}

			openshiftEC2NodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				Spec: tc.spec,
			}
			ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
			err := reconcileEC2NodeClass(context.Background(), ec2NodeClass, openshiftEC2NodeClass, tc.hcp, userDataSecret)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify basic fields, those fields should be the same regardless of OpenshiftEC2NodeClass spec.
			tc.expectedSpec.UserData = ptr.To("test-userdata")
			tc.expectedSpec.AMIFamily = ptr.To("Custom")
			tc.expectedSpec.AMISelectorTerms = []awskarpenterv1.AMISelectorTerm{
				{
					ID: "ami-123",
				},
			}

			g.Expect(ec2NodeClass.Spec).To(Equal(tc.expectedSpec))
		})
	}
}

func TestReconcileStatus(t *testing.T) {
	testCases := []struct {
		name                         string
		ec2NodeClassStatus           awskarpenterv1.EC2NodeClassStatus
		expectedCapacityReservations []hyperkarpenterv1.CapacityReservation
		expectedSubnets              []hyperkarpenterv1.Subnet
		expectedSecurityGroups       []hyperkarpenterv1.SecurityGroup
	}{
		{
			name: "when EC2NodeClass has capacity reservations it should mirror them to OpenshiftEC2NodeClass status",
			ec2NodeClassStatus: awskarpenterv1.EC2NodeClassStatus{
				CapacityReservations: []awskarpenterv1.CapacityReservation{
					{
						AvailabilityZone:      "us-east-1a",
						ID:                    "cr-1234567890abcdef0",
						InstanceMatchCriteria: "targeted",
						InstanceType:          "m5.large",
						OwnerID:               "123456789012",
						ReservationType:       "default",
						State:                 "active",
					},
				},
			},
			expectedCapacityReservations: []hyperkarpenterv1.CapacityReservation{
				{
					AvailabilityZone:      "us-east-1a",
					ID:                    "cr-1234567890abcdef0",
					InstanceMatchCriteria: hyperkarpenterv1.InstanceMatchCriteriaTargeted,
					InstanceType:          "m5.large",
					OwnerID:               "123456789012",
					ReservationType:       hyperkarpenterv1.CapacityReservationTypeDefault,
					State:                 hyperkarpenterv1.CapacityReservationStateActive,
				},
			},
		},
		{
			name: "when EC2NodeClass has subnets and security groups it should mirror them to OpenshiftEC2NodeClass status",
			ec2NodeClassStatus: awskarpenterv1.EC2NodeClassStatus{
				Subnets: []awskarpenterv1.Subnet{
					{ID: "subnet-abc123", Zone: "us-east-1a", ZoneID: "use1-az1"},
				},
				SecurityGroups: []awskarpenterv1.SecurityGroup{
					{ID: "sg-abc123", Name: "test-sg"},
				},
			},
			expectedSubnets: []hyperkarpenterv1.Subnet{
				{ID: "subnet-abc123", Zone: "us-east-1a", ZoneID: "use1-az1"},
			},
			expectedSecurityGroups: []hyperkarpenterv1.SecurityGroup{
				{ID: "sg-abc123", Name: "test-sg"},
			},
		},
		{
			name:                         "when EC2NodeClass has no capacity reservations it should leave status capacity reservations empty",
			ec2NodeClassStatus:           awskarpenterv1.EC2NodeClassStatus{},
			expectedCapacityReservations: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			scheme := runtime.NewScheme()
			g.Expect(hyperkarpenterv1.AddToScheme(scheme)).To(Succeed())

			openshiftNodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-nodeclass",
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(openshiftNodeClass).
				WithStatusSubresource(openshiftNodeClass).
				Build()

			r := &EC2NodeClassReconciler{guestClient: fakeClient}

			ec2NodeClass := &awskarpenterv1.EC2NodeClass{
				Status: tc.ec2NodeClassStatus,
			}

			err := r.reconcileStatus(context.Background(), ec2NodeClass, openshiftNodeClass)
			g.Expect(err).ToNot(HaveOccurred())

			// Re-fetch to verify what was persisted via status patch
			updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
			g.Expect(fakeClient.Get(context.Background(), client.ObjectKeyFromObject(openshiftNodeClass), updated)).To(Succeed())

			g.Expect(updated.Status.CapacityReservations).To(Equal(tc.expectedCapacityReservations))
			g.Expect(updated.Status.Subnets).To(Equal(tc.expectedSubnets))
			g.Expect(updated.Status.SecurityGroups).To(Equal(tc.expectedSecurityGroups))
		})
	}
}

func TestReconcileStatusIdempotency(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(hyperkarpenterv1.AddToScheme(scheme)).To(Succeed())

	openshiftNodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-nodeclass",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(openshiftNodeClass).
		WithStatusSubresource(openshiftNodeClass).
		Build()

	r := &EC2NodeClassReconciler{guestClient: fakeClient}

	ec2NodeClass := &awskarpenterv1.EC2NodeClass{
		Status: awskarpenterv1.EC2NodeClassStatus{
			CapacityReservations: []awskarpenterv1.CapacityReservation{
				{
					AvailabilityZone:      "us-east-1a",
					ID:                    "cr-1234567890abcdef0",
					InstanceMatchCriteria: "targeted",
					InstanceType:          "m5.large",
					OwnerID:               "123456789012",
					ReservationType:       "default",
					State:                 "active",
				},
			},
			Subnets: []awskarpenterv1.Subnet{
				{ID: "subnet-abc123", Zone: "us-east-1a", ZoneID: "use1-az1"},
			},
			SecurityGroups: []awskarpenterv1.SecurityGroup{
				{ID: "sg-abc123", Name: "test-sg"},
			},
		},
	}

	// When reconcileStatus is called twice with the same upstream status it should not accumulate entries
	g.Expect(r.reconcileStatus(context.Background(), ec2NodeClass, openshiftNodeClass)).To(Succeed())

	updated := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
	g.Expect(fakeClient.Get(context.Background(), client.ObjectKeyFromObject(openshiftNodeClass), updated)).To(Succeed())

	g.Expect(r.reconcileStatus(context.Background(), ec2NodeClass, updated)).To(Succeed())

	final := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
	g.Expect(fakeClient.Get(context.Background(), client.ObjectKeyFromObject(openshiftNodeClass), final)).To(Succeed())

	// It should have exactly one entry for each, not two
	g.Expect(final.Status.CapacityReservations).To(HaveLen(1))
	g.Expect(final.Status.Subnets).To(HaveLen(1))
	g.Expect(final.Status.SecurityGroups).To(HaveLen(1))
}

func TestGetUserDataSecret(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())

	nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-nodeclass",
		},
	}
	expectedNodePoolName := karpenterutil.KarpenterNodePoolName(nodeClass)

	testCases := []struct {
		name           string
		namespace      string
		nodeClass      *hyperkarpenterv1.OpenshiftEC2NodeClass
		objects        []client.Object
		expectedSecret string
		expectedError  error
	}{
		{
			name:      "when matching secret exists it should return the secret",
			namespace: "test-namespace",
			nodeClass: nodeClass,
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "matching-secret",
						Namespace:         "test-namespace",
						CreationTimestamp: metav1.Time{Time: time.Now()},
						Labels: map[string]string{
							karpenterutil.ManagedByKarpenterLabel: "true",
						},
						Annotations: map[string]string{
							hyperkarpenterv1.TokenSecretNodePoolAnnotation: "test-namespace/" + expectedNodePoolName,
						},
					},
				},
			},
			expectedSecret: "matching-secret",
		},
		{
			name:      "when multiple secrets exist it should return the one matching nodepool and not the token secret",
			namespace: "test-namespace",
			nodeClass: nodeClass,
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "other-secret",
						Namespace: "test-namespace",
						Labels: map[string]string{
							karpenterutil.ManagedByKarpenterLabel: "true",
						},
						Annotations: map[string]string{
							hyperkarpenterv1.TokenSecretNodePoolAnnotation: "test-namespace/other-nodepool",
						},
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "token-secret",
						Namespace: "test-namespace",
						Labels: map[string]string{
							karpenterutil.ManagedByKarpenterLabel: "true",
						},
						Annotations: map[string]string{
							nodepool.TokenSecretAnnotation:                 "true",
							hyperkarpenterv1.TokenSecretNodePoolAnnotation: "test-namespace/" + expectedNodePoolName,
						},
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "matching-secret",
						Namespace: "test-namespace",
						Labels: map[string]string{
							karpenterutil.ManagedByKarpenterLabel: "true",
						},
						Annotations: map[string]string{
							hyperkarpenterv1.TokenSecretNodePoolAnnotation: "test-namespace/" + expectedNodePoolName,
						},
					},
				},
			},
			expectedSecret: "matching-secret",
		},
		{
			name:          "when no secrets exist it should return errKarpenterUserDataSecretNotFound",
			namespace:     "test-namespace",
			nodeClass:     nodeClass,
			objects:       []client.Object{},
			expectedError: errKarpenterUserDataSecretNotFound,
		},
		{
			name:      "when secrets exist but none match nodepool it should return errKarpenterUserDataSecretNotFound",
			namespace: "test-namespace",
			nodeClass: nodeClass,
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "non-matching-secret",
						Namespace: "test-namespace",
						Labels: map[string]string{
							karpenterutil.ManagedByKarpenterLabel: "true",
						},
						Annotations: map[string]string{
							hyperkarpenterv1.TokenSecretNodePoolAnnotation: "test-namespace/other-nodepool",
						},
					},
				},
			},
			expectedError: errKarpenterUserDataSecretNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tc.objects...).
				Build()

			r := &EC2NodeClassReconciler{
				managementClient: fakeClient,
				Namespace:        tc.namespace,
			}

			secret, err := r.getUserDataSecret(t.Context(), tc.nodeClass)

			if tc.expectedError != nil {
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.Is(err, tc.expectedError)).To(BeTrue(), "expected error to wrap %v, got %v", tc.expectedError, err)
				g.Expect(secret).To(BeNil())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(secret).NotTo(BeNil())
				g.Expect(secret.Name).To(Equal(tc.expectedSecret))
			}
		})
	}
}

func TestComputeReadyCondition(t *testing.T) {
	testCases := []struct {
		name                string
		conditions          []metav1.Condition
		expectedReadyStatus metav1.ConditionStatus
		expectedReadyReason string
		readyShouldChange   bool
	}{
		{
			name: "When VersionResolved is False it should set Ready to False",
			conditions: []metav1.Condition{
				{
					Type:    hyperkarpenterv1.ConditionTypeReady,
					Status:  metav1.ConditionTrue,
					Reason:  "Ready",
					Message: "EC2NodeClass is ready",
				},
				{
					Type:    hyperkarpenterv1.ConditionTypeVersionResolved,
					Status:  metav1.ConditionFalse,
					Reason:  hyperkarpenterv1.ConditionReasonResolutionFailed,
					Message: "Failed to resolve version \"4.17.0\": Cincinnati API unavailable",
				},
			},
			expectedReadyStatus: metav1.ConditionFalse,
			expectedReadyReason: hyperkarpenterv1.ConditionReasonResolutionFailed,
			readyShouldChange:   true,
		},
		{
			name: "When VersionResolved is True it should not override Ready",
			conditions: []metav1.Condition{
				{
					Type:    hyperkarpenterv1.ConditionTypeReady,
					Status:  metav1.ConditionTrue,
					Reason:  "Ready",
					Message: "EC2NodeClass is ready",
				},
				{
					Type:    hyperkarpenterv1.ConditionTypeVersionResolved,
					Status:  metav1.ConditionTrue,
					Reason:  hyperkarpenterv1.ConditionReasonVersionResolved,
					Message: "Version resolved",
				},
			},
			expectedReadyStatus: metav1.ConditionTrue,
			expectedReadyReason: "Ready",
			readyShouldChange:   false,
		},
		{
			name: "When VersionResolved condition is absent it should set Ready to False",
			conditions: []metav1.Condition{
				{
					Type:    hyperkarpenterv1.ConditionTypeReady,
					Status:  metav1.ConditionTrue,
					Reason:  "Ready",
					Message: "EC2NodeClass is ready",
				},
			},
			expectedReadyStatus: metav1.ConditionFalse,
			expectedReadyReason: hyperkarpenterv1.ConditionReasonResolutionFailed,
			readyShouldChange:   true,
		},
		{
			name: "When VersionResolved is Unknown it should set Ready to False",
			conditions: []metav1.Condition{
				{
					Type:    hyperkarpenterv1.ConditionTypeReady,
					Status:  metav1.ConditionTrue,
					Reason:  "Ready",
					Message: "EC2NodeClass is ready",
				},
				{
					Type:    hyperkarpenterv1.ConditionTypeVersionResolved,
					Status:  metav1.ConditionUnknown,
					Reason:  "Unknown",
					Message: "Version resolution status is unknown",
				},
			},
			expectedReadyStatus: metav1.ConditionFalse,
			expectedReadyReason: "Unknown",
			readyShouldChange:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			openshiftNodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test-nodeclass",
					Generation: 1,
				},
				Status: hyperkarpenterv1.OpenshiftEC2NodeClassStatus{
					Conditions: tc.conditions,
				},
			}

			r := &EC2NodeClassReconciler{}
			r.computeReadyCondition(openshiftNodeClass)

			readyCond := findCondition(openshiftNodeClass.Status.Conditions, hyperkarpenterv1.ConditionTypeReady)
			g.Expect(readyCond).NotTo(BeNil())
			g.Expect(readyCond.Status).To(Equal(tc.expectedReadyStatus))
			g.Expect(readyCond.Reason).To(Equal(tc.expectedReadyReason))
		})
	}
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i, c := range conditions {
		if c.Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

func TestKarpenterSecretPredicate(t *testing.T) {
	testCases := []struct {
		name           string
		namespace      string
		secret         *corev1.Secret
		eventType      string
		expectedResult bool
	}{
		{
			name:      "should accept Create event for karpenter secret in correct namespace",
			namespace: "test-namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "karpenter-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			eventType:      "Create",
			expectedResult: true,
		},
		{
			name:      "should accept Update event for karpenter secret in correct namespace",
			namespace: "test-namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "karpenter-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			eventType:      "Update",
			expectedResult: true,
		},
		{
			name:      "should reject Delete event for karpenter secret",
			namespace: "test-namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "karpenter-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			eventType:      "Delete",
			expectedResult: false,
		},
		{
			name:      "should reject Generic event for karpenter secret",
			namespace: "test-namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "karpenter-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			eventType:      "Generic",
			expectedResult: false,
		},
		{
			name:      "should reject secret in wrong namespace",
			namespace: "test-namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "karpenter-secret",
					Namespace: "wrong-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "true",
					},
				},
			},
			eventType:      "Create",
			expectedResult: false,
		},
		{
			name:      "should reject secret without ManagedByKarpenterLabel",
			namespace: "test-namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "regular-secret",
					Namespace: "test-namespace",
				},
			},
			eventType:      "Create",
			expectedResult: false,
		},
		{
			name:      "should reject secret with ManagedByKarpenterLabel set to false",
			namespace: "test-namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedByKarpenterLabel: "false",
					},
				},
			},
			eventType:      "Create",
			expectedResult: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			r := &EC2NodeClassReconciler{
				Namespace: tc.namespace,
			}

			pred := r.karpenterSecretPredicate()

			var result bool
			switch tc.eventType {
			case "Create":
				result = pred.Create(event.CreateEvent{Object: tc.secret})
			case "Update":
				result = pred.Update(event.UpdateEvent{ObjectNew: tc.secret, ObjectOld: tc.secret})
			case "Delete":
				result = pred.Delete(event.DeleteEvent{Object: tc.secret})
			case "Generic":
				result = pred.Generic(event.GenericEvent{Object: tc.secret})
			default:
				t.Fatalf("invalid event type: %s", tc.eventType)
			}

			g.Expect(result).To(Equal(tc.expectedResult))
		})
	}
}

func TestAMISelectorTerms(t *testing.T) {
	testCases := []struct {
		name           string
		userDataSecret *corev1.Secret
		platform       hyperv1.PlatformType
		expectedError  string
		expectedAMIs   []awskarpenterv1.AMISelectorTerm
	}{
		{
			name:     "when user data secret is created for supported platform, and labels exist it should return the expected AMIs",
			platform: hyperv1.AWSPlatform,
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ArchToAMILabelKey(hyperv1.ArchitectureAMD64): "ami-123",
						karpenterutil.ArchToAMILabelKey(hyperv1.ArchitectureARM64): "ami-456",
					},
				},
			},
			expectedAMIs: []awskarpenterv1.AMISelectorTerm{
				{
					ID: "ami-123",
				},
				{
					ID: "ami-456",
				},
			},
		},
		{
			name:     "when user data secret is created for unsupported platform, and labels exist it should return an error",
			platform: hyperv1.AzurePlatform,
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ArchToAMILabelKey(hyperv1.ArchitectureAMD64): "ami-123",
						karpenterutil.ArchToAMILabelKey(hyperv1.ArchitectureARM64): "ami-456",
					},
				},
			},
			expectedError: "failed to get supported architectures: unsupported platform: Azure",
		},
		{
			name:     "when user data secret is created for supported platform, but no AMIs labels exist it should return an error",
			platform: hyperv1.AWSPlatform,
			userDataSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "user-data-secret",
					Namespace: "test-namespace",
					Labels:    map[string]string{},
				},
			},
			expectedError: "no AMIs found for supported architectures: [amd64 arm64]",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			amis, err := AMISelectorTerms(tc.userDataSecret, tc.platform)
			if tc.expectedError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.expectedError))
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(amis).To(Equal(tc.expectedAMIs))
		})
	}
}

func TestReconcileKarpenterSubnetsConfigMap(t *testing.T) {
	const testNamespace = "clusters-my-cluster"

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: testNamespace,
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: testInfraID,
		},
	}

	testCases := []struct {
		name                string
		guestObjects        []client.Object
		managementObjects   []client.Object
		expectConfigMap     bool
		expectedSubnetCount int
		expectedSubnets     []string
	}{
		{
			name:         "When there are no OpenshiftEC2NodeClass resources it should delete the ConfigMap",
			guestObjects: []client.Object{},
			managementObjects: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      karpenterutil.KarpenterSubnetsConfigMapName,
						Namespace: testNamespace,
					},
				},
			},
			expectConfigMap: false,
		},
		{
			name: "When OpenshiftEC2NodeClass resources have subnets in status it should create ConfigMap with aggregated subnet IDs",
			guestObjects: []client.Object{
				&hyperkarpenterv1.OpenshiftEC2NodeClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "nodeclass-1",
					},
					Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
						SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{
							{ID: "subnet-aaa"},
						},
					},
					Status: hyperkarpenterv1.OpenshiftEC2NodeClassStatus{
						Subnets: []hyperkarpenterv1.Subnet{
							{ID: "subnet-aaa", Zone: "us-east-1a"},
							{ID: "subnet-bbb", Zone: "us-east-1b"},
						},
					},
				},
			},
			expectConfigMap:     true,
			expectedSubnetCount: 2,
			expectedSubnets:     []string{"subnet-aaa", "subnet-bbb"},
		},
		{
			name: "When multiple OpenshiftEC2NodeClass resources have overlapping subnets it should deduplicate subnet IDs",
			guestObjects: []client.Object{
				&hyperkarpenterv1.OpenshiftEC2NodeClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "nodeclass-1",
					},
					Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
						SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{
							{ID: "subnet-shared"},
						},
					},
					Status: hyperkarpenterv1.OpenshiftEC2NodeClassStatus{
						Subnets: []hyperkarpenterv1.Subnet{
							{ID: "subnet-shared", Zone: "us-east-1a"},
							{ID: "subnet-aaa", Zone: "us-east-1b"},
						},
					},
				},
				&hyperkarpenterv1.OpenshiftEC2NodeClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "nodeclass-2",
					},
					Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
						SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{
							{ID: "subnet-bbb"},
						},
					},
					Status: hyperkarpenterv1.OpenshiftEC2NodeClassStatus{
						Subnets: []hyperkarpenterv1.Subnet{
							{ID: "subnet-shared", Zone: "us-east-1a"},
							{ID: "subnet-bbb", Zone: "us-east-1c"},
						},
					},
				},
			},
			expectConfigMap:     true,
			expectedSubnetCount: 3,
			expectedSubnets:     []string{"subnet-aaa", "subnet-bbb", "subnet-shared"},
		},
		{
			name: "When OpenshiftEC2NodeClass has nil SubnetSelectorTerms it should still include its status subnets",
			guestObjects: []client.Object{
				&hyperkarpenterv1.OpenshiftEC2NodeClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
					},
					Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
						SubnetSelectorTerms: nil,
					},
					Status: hyperkarpenterv1.OpenshiftEC2NodeClassStatus{
						Subnets: []hyperkarpenterv1.Subnet{
							{ID: "subnet-default-1", Zone: "us-east-1a"},
							{ID: "subnet-default-2", Zone: "us-east-1b"},
						},
					},
				},
			},
			expectConfigMap:     true,
			expectedSubnetCount: 2,
			expectedSubnets:     []string{"subnet-default-1", "subnet-default-2"},
		},
		{
			name: "When OpenshiftEC2NodeClass resources have no subnets in status it should delete the ConfigMap",
			guestObjects: []client.Object{
				&hyperkarpenterv1.OpenshiftEC2NodeClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "nodeclass-1",
					},
					Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
						SubnetSelectorTerms: []hyperkarpenterv1.SubnetSelectorTerm{
							{ID: "subnet-aaa"},
						},
					},
					Status: hyperkarpenterv1.OpenshiftEC2NodeClassStatus{},
				},
			},
			expectConfigMap: false,
		},
		{
			name: "When an OpenshiftEC2NodeClass is being deleted it should exclude its subnets from the ConfigMap",
			guestObjects: []client.Object{
				&hyperkarpenterv1.OpenshiftEC2NodeClass{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "being-deleted",
						DeletionTimestamp: &metav1.Time{Time: time.Now()},
						Finalizers:        []string{finalizer},
					},
					Status: hyperkarpenterv1.OpenshiftEC2NodeClassStatus{
						Subnets: []hyperkarpenterv1.Subnet{
							{ID: "subnet-being-deleted", Zone: "us-east-1a"},
						},
					},
				},
				&hyperkarpenterv1.OpenshiftEC2NodeClass{
					ObjectMeta: metav1.ObjectMeta{
						Name: "remaining",
					},
					Status: hyperkarpenterv1.OpenshiftEC2NodeClassStatus{
						Subnets: []hyperkarpenterv1.Subnet{
							{ID: "subnet-keep", Zone: "us-east-1b"},
						},
					},
				},
			},
			expectConfigMap:     true,
			expectedSubnetCount: 1,
			expectedSubnets:     []string{"subnet-keep"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			managementClient := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithObjects(tc.managementObjects...).
				Build()

			guestClient := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
				WithObjects(tc.guestObjects...).
				Build()

			// Patch status for guest objects since fake client WithObjects doesn't set status
			for _, obj := range tc.guestObjects {
				if nc, ok := obj.(*hyperkarpenterv1.OpenshiftEC2NodeClass); ok {
					if err := guestClient.Status().Update(context.Background(), nc); err != nil {
						t.Fatalf("failed to set status on OpenshiftEC2NodeClass: %v", err)
					}
				}
			}

			r := &EC2NodeClassReconciler{
				Namespace:              testNamespace,
				managementClient:       managementClient,
				guestClient:            guestClient,
				CreateOrUpdateProvider: upsert.New(false),
			}

			err := r.reconcileKarpenterSubnetsConfigMap(context.Background(), hcp)
			g.Expect(err).NotTo(HaveOccurred())

			cm := &corev1.ConfigMap{}
			getErr := managementClient.Get(context.Background(), client.ObjectKey{
				Namespace: testNamespace,
				Name:      karpenterutil.KarpenterSubnetsConfigMapName,
			}, cm)

			if !tc.expectConfigMap {
				g.Expect(getErr).To(HaveOccurred(), "ConfigMap should have been deleted")
				return
			}

			g.Expect(getErr).NotTo(HaveOccurred(), "ConfigMap should exist")
			g.Expect(cm.Labels).To(HaveKeyWithValue("hypershift.openshift.io/managed-by", "karpenter"))
			g.Expect(cm.Labels).To(HaveKeyWithValue("hypershift.openshift.io/infra-id", testInfraID))

			subnetIDsJSON := cm.Data["subnetIDs"]
			g.Expect(subnetIDsJSON).NotTo(BeEmpty())

			var subnetIDs []string
			g.Expect(json.Unmarshal([]byte(subnetIDsJSON), &subnetIDs)).To(Succeed())
			g.Expect(subnetIDs).To(HaveLen(tc.expectedSubnetCount))
			g.Expect(subnetIDs).To(ConsistOf(tc.expectedSubnets))
		})
	}
}

func TestMapVAPToOpenShiftEC2NodeClasses(t *testing.T) {
	testCases := []struct {
		name             string
		vapName          string
		nodeClasses      []client.Object
		expectedRequests int
	}{
		{
			name:    "When the VAP matches the expected name it should enqueue all OpenshiftEC2NodeClasses",
			vapName: "karpenter.ec2nodeclass.hypershift.io",
			nodeClasses: []client.Object{
				&hyperkarpenterv1.OpenshiftEC2NodeClass{ObjectMeta: metav1.ObjectMeta{Name: "nc-1"}},
				&hyperkarpenterv1.OpenshiftEC2NodeClass{ObjectMeta: metav1.ObjectMeta{Name: "nc-2"}},
			},
			expectedRequests: 2,
		},
		{
			name:    "When the VAP name does not match it should not enqueue any requests",
			vapName: "unrelated-policy",
			nodeClasses: []client.Object{
				&hyperkarpenterv1.OpenshiftEC2NodeClass{ObjectMeta: metav1.ObjectMeta{Name: "nc-1"}},
			},
			expectedRequests: 0,
		},
		{
			name:             "When the VAP matches but no OpenshiftEC2NodeClasses exist it should return empty",
			vapName:          "karpenter.ec2nodeclass.hypershift.io",
			nodeClasses:      []client.Object{},
			expectedRequests: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			guestClient := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithObjects(tc.nodeClasses...).
				Build()

			r := &EC2NodeClassReconciler{guestClient: guestClient}

			vap := &admissionv1.ValidatingAdmissionPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: tc.vapName},
			}

			requests := r.mapVAPToOpenShiftEC2NodeClasses(context.Background(), vap)
			g.Expect(requests).To(HaveLen(tc.expectedRequests))
		})
	}
}

func TestMapVAPBindingToOpenShiftEC2NodeClasses(t *testing.T) {
	testCases := []struct {
		name             string
		bindingName      string
		nodeClasses      []client.Object
		expectedRequests int
	}{
		{
			name:        "When the VAPBinding matches the expected name it should enqueue all OpenshiftEC2NodeClasses",
			bindingName: "karpenter-binding.ec2nodeclass.hypershift.io",
			nodeClasses: []client.Object{
				&hyperkarpenterv1.OpenshiftEC2NodeClass{ObjectMeta: metav1.ObjectMeta{Name: "nc-1"}},
				&hyperkarpenterv1.OpenshiftEC2NodeClass{ObjectMeta: metav1.ObjectMeta{Name: "nc-2"}},
			},
			expectedRequests: 2,
		},
		{
			name:        "When the VAPBinding name does not match it should not enqueue any requests",
			bindingName: "unrelated-binding",
			nodeClasses: []client.Object{
				&hyperkarpenterv1.OpenshiftEC2NodeClass{ObjectMeta: metav1.ObjectMeta{Name: "nc-1"}},
			},
			expectedRequests: 0,
		},
		{
			name:             "When the VAPBinding matches but no OpenshiftEC2NodeClasses exist it should return empty",
			bindingName:      "karpenter-binding.ec2nodeclass.hypershift.io",
			nodeClasses:      []client.Object{},
			expectedRequests: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			guestClient := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithObjects(tc.nodeClasses...).
				Build()

			r := &EC2NodeClassReconciler{guestClient: guestClient}

			binding := &admissionv1.ValidatingAdmissionPolicyBinding{
				ObjectMeta: metav1.ObjectMeta{Name: tc.bindingName},
			}

			requests := r.mapVAPBindingToOpenShiftEC2NodeClasses(context.Background(), binding)
			g.Expect(requests).To(HaveLen(tc.expectedRequests))
		})
	}
}
