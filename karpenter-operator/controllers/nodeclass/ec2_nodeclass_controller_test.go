package nodeclass

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	nodepool "github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	hyperapi "github.com/openshift/hypershift/support/api"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/upsert"

	awskarpenterv1 "github.com/aws/karpenter-provider-aws/pkg/apis/v1"

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
							"karpenter.sh/discovery": testInfraID,
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
				AssociatePublicIPAddress: ptr.To(true),
				Tags: map[string]string{
					"tag1": "value1",
				},
				BlockDeviceMappings: []*hyperkarpenterv1.BlockDeviceMapping{
					{
						DeviceName: ptr.To("xvdh"),
						EBS: &hyperkarpenterv1.BlockDevice{
							Encrypted:  ptr.To(true),
							VolumeSize: resource.NewQuantity(20, resource.DecimalSI),
						},
					},
				},
				InstanceStorePolicy: ptr.To(hyperkarpenterv1.InstanceStorePolicyRAID0),
				DetailedMonitoring:  ptr.To(true),
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
							VolumeSize: resource.NewQuantity(20, resource.DecimalSI),
						},
					},
				},
				InstanceStorePolicy: ptr.To(awskarpenterv1.InstanceStorePolicyRAID0),
				DetailedMonitoring:  ptr.To(true),
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
				},
			},
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": testInfraID,
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
				},
			},
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": testInfraID,
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
							"karpenter.sh/discovery": testInfraID,
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
							"karpenter.sh/discovery": "test-infra",
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
							"karpenter.sh/discovery": "test-infra",
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
