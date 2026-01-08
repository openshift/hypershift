package nodeclass

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"

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
	testCases := []struct {
		name           string
		hcpAnnotations map[string]string
		spec           hyperkarpenterv1.OpenshiftEC2NodeClassSpec
		expectedSpec   awskarpenterv1.EC2NodeClassSpec
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
			hcpAnnotations: map[string]string{
				hyperv1.AWSKarpenterDefaultInstanceProfile: "test-instance-profile",
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
				InstanceProfile: ptr.To("test-instance-profile"),
			},
		},
		{
			name: "When HCP has empty instance-profile annotation it should NOT set InstanceProfile",
			hcpAnnotations: map[string]string{
				hyperv1.AWSKarpenterDefaultInstanceProfile: "",
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
			},
		},
		{
			name:           "When HCP has no instance-profile annotation it should NOT set InstanceProfile",
			hcpAnnotations: map[string]string{},
			spec:           hyperkarpenterv1.OpenshiftEC2NodeClassSpec{},
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
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			// Create HCP with test case annotations
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: tc.hcpAnnotations,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					InfraID: testInfraID,
				},
			}

			openshiftEC2NodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				Spec: tc.spec,
			}
			ec2NodeClass := &awskarpenterv1.EC2NodeClass{}
			err := reconcileEC2NodeClass(ec2NodeClass, openshiftEC2NodeClass, hcp, userDataSecret)
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

	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-nodepool",
			Namespace: "test-namespace",
		},
	}

	testCases := []struct {
		name           string
		namespace      string
		hcp            *hyperv1.HostedControlPlane
		nodePool       *hyperv1.NodePool
		objects        []client.Object
		expectedSecret string
		expectedError  string
	}{
		{
			name:      "when matching secret exists it should return the secret",
			namespace: "test-namespace",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
				},
			},
			nodePool: nodePool,
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "matching-secret",
						Namespace:         "test-namespace",
						CreationTimestamp: metav1.Time{Time: time.Now()},
						Labels: map[string]string{
							karpenterutil.ManagedForKarpenterLabel: "true",
						},
						Annotations: map[string]string{
							hyperkarpenterv1.TokenSecretNodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
			},
			expectedSecret: "matching-secret",
		},
		{
			name:      "when multiple secrets exist it should return the one matching nodepool",
			namespace: "test-namespace",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
				},
			},
			nodePool: nodePool,
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "other-secret",
						Namespace:         "test-namespace",
						CreationTimestamp: metav1.Time{Time: time.Now().Add(-1 * time.Hour)},
						Labels: map[string]string{
							karpenterutil.ManagedForKarpenterLabel: "true",
						},
						Annotations: map[string]string{
							hyperkarpenterv1.TokenSecretNodePoolAnnotation: "test-namespace/other-nodepool",
						},
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "matching-secret",
						Namespace:         "test-namespace",
						CreationTimestamp: metav1.Time{Time: time.Now()},
						Labels: map[string]string{
							karpenterutil.ManagedForKarpenterLabel: "true",
						},
						Annotations: map[string]string{
							hyperkarpenterv1.TokenSecretNodePoolAnnotation: "test-namespace/test-nodepool",
						},
					},
				},
			},
			expectedSecret: "matching-secret",
		},
		{
			name:      "when no secrets exist it should return error",
			namespace: "test-namespace",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
				},
			},
			nodePool:      nodePool,
			objects:       []client.Object{},
			expectedError: "failed to find user data secret for nodepool test-nodepool",
		},
		{
			name:      "when secrets exist but none match nodepool it should return error",
			namespace: "test-namespace",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-hcp",
				},
			},
			nodePool: nodePool,
			objects: []client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "non-matching-secret",
						Namespace: "test-namespace",
						Labels: map[string]string{
							karpenterutil.ManagedForKarpenterLabel: "true",
						},
						Annotations: map[string]string{
							hyperkarpenterv1.TokenSecretNodePoolAnnotation: "test-namespace/other-nodepool",
						},
					},
				},
			},
			expectedError: "failed to find user data secret for nodepool test-nodepool",
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

			secret, err := r.getUserDataSecret(t.Context(), tc.nodePool)

			if tc.expectedError != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.expectedError)))
				g.Expect(secret).To(BeNil())
				return
			}

			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(secret).NotTo(BeNil())
			g.Expect(secret.Name).To(Equal(tc.expectedSecret))
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
			name:      "should accept Create event for karpenter-managed secret in correct namespace",
			namespace: "test-namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "karpenter-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedForKarpenterLabel: "true",
					},
				},
			},
			eventType:      "Create",
			expectedResult: true,
		},
		{
			name:      "should accept Update event for karpenter-managed secret in correct namespace",
			namespace: "test-namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "karpenter-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedForKarpenterLabel: "true",
					},
				},
			},
			eventType:      "Update",
			expectedResult: true,
		},
		{
			name:      "should reject Delete event for karpenter-managed secret",
			namespace: "test-namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "karpenter-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedForKarpenterLabel: "true",
					},
				},
			},
			eventType:      "Delete",
			expectedResult: false,
		},
		{
			name:      "should reject Generic event for karpenter-managed secret",
			namespace: "test-namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "karpenter-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedForKarpenterLabel: "true",
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
						karpenterutil.ManagedForKarpenterLabel: "true",
					},
				},
			},
			eventType:      "Create",
			expectedResult: false,
		},
		{
			name:      "should reject secret without ManagedForKarpenterLabel",
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
			name:      "should reject secret with ManagedForKarpenterLabel set to false",
			namespace: "test-namespace",
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "other-secret",
					Namespace: "test-namespace",
					Labels: map[string]string{
						karpenterutil.ManagedForKarpenterLabel: "false",
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

func TestHCPNodePool(t *testing.T) {
	testCases := []struct {
		name                      string
		hostedCluster             *hyperv1.HostedCluster
		openshiftEC2NodeClass     *hyperkarpenterv1.OpenshiftEC2NodeClass
		expectedName              string
		expectedNamespace         string
		expectedClusterName       string
		expectedReleaseImage      string
		expectedHasKarpenterLabel bool
	}{
		{
			name: "should create nodepool with correct name suffix and karpenter label",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters",
				},
			},
			openshiftEC2NodeClass: &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "default",
				},
				Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
					OpenShiftReleaseImageVersion: "quay.io/openshift-release-dev/ocp-release:4.18.0",
				},
			},
			expectedName:              "default-karpenter",
			expectedNamespace:         "clusters",
			expectedClusterName:       "test-cluster",
			expectedReleaseImage:      "quay.io/openshift-release-dev/ocp-release:4.18.0",
			expectedHasKarpenterLabel: true,
		},
		{
			name: "should use openshiftEC2NodeClass name in nodepool name",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "prod-cluster",
					Namespace: "prod-ns",
				},
			},
			openshiftEC2NodeClass: &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name: "gpu-nodes",
				},
				Spec: hyperkarpenterv1.OpenshiftEC2NodeClassSpec{
					OpenShiftReleaseImageVersion: "quay.io/openshift-release-dev/ocp-release:4.19.0",
				},
			},
			expectedName:              "gpu-nodes-karpenter",
			expectedNamespace:         "prod-ns",
			expectedClusterName:       "prod-cluster",
			expectedReleaseImage:      "quay.io/openshift-release-dev/ocp-release:4.19.0",
			expectedHasKarpenterLabel: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			np := hcpNodePool(tc.hostedCluster, tc.openshiftEC2NodeClass)

			g.Expect(np.Name).To(Equal(tc.expectedName))
			g.Expect(np.Namespace).To(Equal(tc.expectedNamespace))
			g.Expect(np.Spec.ClusterName).To(Equal(tc.expectedClusterName))
			g.Expect(np.Spec.Release.Image).To(Equal(tc.expectedReleaseImage))
			g.Expect(np.Spec.Arch).To(Equal(hyperv1.ArchitectureAMD64))
			g.Expect(*np.Spec.Replicas).To(Equal(int32(0)))

			// Verify the karpenter label is set
			if tc.expectedHasKarpenterLabel {
				g.Expect(np.Labels).To(HaveKeyWithValue(karpenterutil.ManagedForKarpenterLabel, "true"))
			}

			// Verify annotations map is initialized (needed for config version tracking)
			g.Expect(np.Annotations).NotTo(BeNil())
		})
	}
}

func TestUpdateConfigVersionAnnotation(t *testing.T) {
	g := NewWithT(t)

	scheme := runtime.NewScheme()
	g.Expect(hyperkarpenterv1.AddToScheme(scheme)).To(Succeed())

	testCases := []struct {
		name                string
		initialAnnotations  map[string]string
		newVersion          string
		expectedAnnotations map[string]string
	}{
		{
			name:               "should set annotation when none exists",
			initialAnnotations: nil,
			newVersion:         "abc123",
			expectedAnnotations: map[string]string{
				openshiftEC2NodeClassAnnotationCurrentConfigVersion: "abc123",
			},
		},
		{
			name: "should update annotation when it exists",
			initialAnnotations: map[string]string{
				openshiftEC2NodeClassAnnotationCurrentConfigVersion: "old-hash",
			},
			newVersion: "new-hash",
			expectedAnnotations: map[string]string{
				openshiftEC2NodeClassAnnotationCurrentConfigVersion: "new-hash",
			},
		},
		{
			name: "should preserve other annotations",
			initialAnnotations: map[string]string{
				"other-annotation": "other-value",
				openshiftEC2NodeClassAnnotationCurrentConfigVersion: "old-hash",
			},
			newVersion: "updated-hash",
			expectedAnnotations: map[string]string{
				"other-annotation": "other-value",
				openshiftEC2NodeClassAnnotationCurrentConfigVersion: "updated-hash",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			openshiftEC2NodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-nodeclass",
					Annotations: tc.initialAnnotations,
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(openshiftEC2NodeClass).
				Build()

			r := &EC2NodeClassReconciler{
				guestClient: fakeClient,
			}

			err := r.updateConfigVersionAnnotation(t.Context(), openshiftEC2NodeClass, tc.newVersion)
			g.Expect(err).NotTo(HaveOccurred())

			// Verify the annotation was updated
			g.Expect(openshiftEC2NodeClass.Annotations).To(Equal(tc.expectedAnnotations))
		})
	}
}
