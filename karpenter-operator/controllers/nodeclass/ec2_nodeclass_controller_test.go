package nodeclass

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"

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
	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			InfraID: "test-infra",
		},
	}

	testCases := []struct {
		name         string
		spec         hyperkarpenterv1.OpenshiftEC2NodeClassSpec
		expectedSpec awskarpenterv1.EC2NodeClassSpec
	}{
		{
			name: "When OpenshiftEC2NodeClassSpec.spec is empty it should reconcile the EC2NodeClass with default values",
			spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{},
			expectedSpec: awskarpenterv1.EC2NodeClassSpec{
				SubnetSelectorTerms: []awskarpenterv1.SubnetSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": hcp.Spec.InfraID,
						},
					},
				},
				SecurityGroupSelectorTerms: []awskarpenterv1.SecurityGroupSelectorTerm{
					{
						Tags: map[string]string{
							"karpenter.sh/discovery": hcp.Spec.InfraID,
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			openshiftEC2NodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				Spec: tc.spec,
			}
			ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
			instanceProfile := "test-infra-worker-profile"
			err := reconcileEC2NodeClass(ec2NodeClass, openshiftEC2NodeClass, hcp, userDataSecret, instanceProfile)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify basic fields, those fields should be the same regardless of OpenshiftEC2NodeClass spec.
			tc.expectedSpec.UserData = ptr.To("test-userdata")
			tc.expectedSpec.AMIFamily = ptr.To("Custom")
			tc.expectedSpec.AMISelectorTerms = []awskarpenterv1.AMISelectorTerm{
				{
					ID: "ami-123",
				},
			}
			tc.expectedSpec.InstanceProfile = ptr.To(instanceProfile)

			g.Expect(ec2NodeClass.Spec).To(Equal(tc.expectedSpec))
		})
	}
}

func TestGetUserDataSecret(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(corev1.AddToScheme(scheme)).To(Succeed())

	testCases := []struct {
		name          string
		namespace     string
		hcp           *hyperv1.HostedControlPlane
		objects       []client.Object
		expectedError string
	}{
		{
			name:      "when multiple exist it should return newest secret",
			namespace: "test-namespace",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
				},
			},
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "older-secret",
						Namespace:         "test-namespace",
						CreationTimestamp: metav1.Time{Time: time.Now().Add(-1 * time.Hour)},
						Labels: map[string]string{
							hyperv1.NodePoolLabel: "test-hcp-karpenter",
						},
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "newer-secret",
						Namespace:         "test-namespace",
						CreationTimestamp: metav1.Time{Time: time.Now()},
						Labels: map[string]string{
							hyperv1.NodePoolLabel: "test-hcp-karpenter",
						},
					},
				},
			},
		},
		{
			name:      "when no secrets exist it should return error",
			namespace: "test-namespace",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
				},
			},
			objects:       []client.Object{},
			expectedError: "expected 1 secret, got 0",
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

			secret, err := r.getUserDataSecret(t.Context(), tc.hcp)

			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(tc.expectedError))
				g.Expect(secret).To(BeNil())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(secret).NotTo(BeNil())

			g.Expect(secret.Name).To(Equal("newer-secret"))
		})
	}
}

func TestUserDataSecretPredicate(t *testing.T) {
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
					Annotations: map[string]string{
						hyperv1.NodePoolLabel: "clusters/karpenter",
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
					Annotations: map[string]string{
						hyperv1.NodePoolLabel: "clusters/karpenter",
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
					Annotations: map[string]string{
						hyperv1.NodePoolLabel: "clusters/karpenter",
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
					Annotations: map[string]string{
						hyperv1.NodePoolLabel: "clusters/karpenter",
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
					Annotations: map[string]string{
						hyperv1.NodePoolLabel: "clusters/karpenter",
					},
				},
			},
			eventType:      "Create",
			expectedResult: false,
		},
		{
			name:      "should reject secret without incorrect NodePoolLabel annotation",
			namespace: "test-namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-secret",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						hyperv1.NodePoolLabel: "clusters/other",
					},
				},
			},
			eventType:      "Create",
			expectedResult: false,
		},
		{
			name:      "should reject secret without NodePoolLabel annotation",
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			r := &EC2NodeClassReconciler{
				Namespace: tc.namespace,
			}

			pred := r.userDataSecretPredicate()

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

func TestIsROSACluster(t *testing.T) {
	testCases := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected bool
	}{
		{
			name: "should return true for ROSA cluster with red-hat-clustertype tag",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-clustertype", Value: "rosa"},
								{Key: "red-hat-managed", Value: "true"},
							},
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "should return false for OSD cluster with red-hat-clustertype=osd tag",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-clustertype", Value: "osd"},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "should return false for cluster without red-hat-clustertype tag",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "some-other-tag", Value: "value"},
							},
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "should return false for cluster with nil AWS platform",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS:  nil,
					},
				},
			},
			expected: false,
		},
		{
			name: "should return false for cluster with no resource tags",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{},
						},
					},
				},
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			result := isROSACluster(tc.hcp)
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func TestGetInstanceProfileName(t *testing.T) {
	testCases := []struct {
		name            string
		hcp             *hyperv1.HostedControlPlane
		expectedProfile string
	}{
		{
			name: "ROSA cluster with environment label should use managed policies pattern",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cluster",
					Labels: map[string]string{
						"api.openshift.com/environment": "prod",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "abc123",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-clustertype", Value: "rosa"},
								{Key: "red-hat-managed", Value: "true"},
							},
						},
					},
				},
			},
			expectedProfile: "rosa-service-managed-prod-abc123-my-cluster-worker",
		},
		{
			name: "ROSA cluster with stage environment should use managed policies pattern",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "rosa-cluster",
					Labels: map[string]string{
						"api.openshift.com/environment": "stage",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "rosa123",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-clustertype", Value: "rosa"},
							},
						},
					},
				},
			},
			expectedProfile: "rosa-service-managed-stage-rosa123-rosa-cluster-worker",
		},
		{
			name: "ROSA cluster missing environment label should fallback to simple pattern",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "abc123",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-clustertype", Value: "rosa"},
							},
						},
					},
				},
			},
			expectedProfile: "abc123-worker",
		},
		{
			name: "OSD cluster should use simple pattern",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							ResourceTags: []hyperv1.AWSResourceTag{
								{Key: "red-hat-clustertype", Value: "osd"},
							},
						},
					},
				},
			},
			expectedProfile: "test-infra-worker",
		},
		{
			name: "Non-ROSA cluster without tags should use simple pattern",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cluster",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS:  &hyperv1.AWSPlatformSpec{},
					},
				},
			},
			expectedProfile: "test-infra-worker",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			result := getInstanceProfileName(tc.hcp)
			g.Expect(result).To(Equal(tc.expectedProfile))
		})
	}
}
