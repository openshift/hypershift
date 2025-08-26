package registry

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"

	imageregistryv1 "github.com/openshift/api/imageregistry/v1"

	k8sadmissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	controllerutil "sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestReconcileRegistryConfigManagementStateValidatingAdmissionPolicy(t *testing.T) {
	tests := []struct {
		name                    string
		hcp                     *hyperv1.HostedControlPlane
		expectCreation          bool
		expectedExpression      string
		expectedLogMessage      string
		expectError             bool
		expectedValidationCount int
		expectedResourceRules   int
	}{
		{
			name: "When cluster is active it should set expression to restrict managementState",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
					// DeletionTimestamp is zero (not set)
				},
			},
			expectCreation:          true,
			expectedExpression:      "object.spec.managementState != 'Removed' || request.userInfo.username == 'system:hosted-cluster-config'",
			expectedLogMessage:      "Cluster is active, enforcing registry management state admission policy",
			expectError:             false,
			expectedValidationCount: 1,
			expectedResourceRules:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Create a fake client
			scheme := hyperapi.Scheme
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			// Create a mock createOrUpdate function
			var createdVAP *k8sadmissionv1.ValidatingAdmissionPolicy
			var createdBinding *k8sadmissionv1.ValidatingAdmissionPolicyBinding

			mockCreateOrUpdate := func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				if err := f(); err != nil {
					return controllerutil.OperationResultNone, err
				}

				// Capture the created objects for verification
				switch o := obj.(type) {
				case *k8sadmissionv1.ValidatingAdmissionPolicy:
					createdVAP = o
				case *k8sadmissionv1.ValidatingAdmissionPolicyBinding:
					createdBinding = o
				}

				return controllerutil.OperationResultCreated, nil
			}

			// Create a context with a logger
			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)

			// Call the function
			err := reconcileRegistryConfigManagementStateValidatingAdmissionPolicy(ctx, tt.hcp, fakeClient, mockCreateOrUpdate)

			// Verify error expectations
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}

			if !tt.expectCreation {
				// During deletion, resources should be deleted, not created
				g.Expect(createdVAP).To(BeNil(), "Did not expect ValidatingAdmissionPolicy to be created during deletion")
				g.Expect(createdBinding).To(BeNil(), "Did not expect ValidatingAdmissionPolicyBinding to be created during deletion")
				return
			}

			// Verify the admission policy was created with correct settings
			g.Expect(createdVAP).NotTo(BeNil(), "Expected ValidatingAdmissionPolicy to be created")

			// Verify the policy name
			g.Expect(createdVAP.Name).To(Equal(AdmissionPolicyNameManagementState))

			// Verify validations
			g.Expect(createdVAP.Spec.Validations).To(HaveLen(tt.expectedValidationCount))

			if len(createdVAP.Spec.Validations) > 0 {
				validation := createdVAP.Spec.Validations[0]
				g.Expect(validation.Expression).To(Equal(tt.expectedExpression))
			}

			// Verify match constraints
			g.Expect(createdVAP.Spec.MatchConstraints).NotTo(BeNil())

			g.Expect(createdVAP.Spec.MatchConstraints.ResourceRules).To(HaveLen(tt.expectedResourceRules))

			if len(createdVAP.Spec.MatchConstraints.ResourceRules) > 0 {
				rule := createdVAP.Spec.MatchConstraints.ResourceRules[0]

				// Verify API group
				g.Expect(rule.Rule.APIGroups).To(HaveLen(1))
				g.Expect(rule.Rule.APIGroups[0]).To(Equal(imageregistryv1.GroupVersion.Group))

				// Verify API version
				g.Expect(rule.Rule.APIVersions).To(HaveLen(1))
				g.Expect(rule.Rule.APIVersions[0]).To(Equal(imageregistryv1.GroupVersion.Version))

				// Verify resources
				g.Expect(rule.Rule.Resources).To(HaveLen(1))
				g.Expect(rule.Rule.Resources[0]).To(Equal("configs"))

				// Verify operations
				expectedOps := []k8sadmissionv1.OperationType{"CREATE", "UPDATE"}
				g.Expect(rule.Operations).To(HaveLen(len(expectedOps)))
			}

			// Verify the binding was created
			g.Expect(createdBinding).NotTo(BeNil(), "Expected ValidatingAdmissionPolicyBinding to be created")

			g.Expect(createdBinding.Spec.PolicyName).To(Equal(AdmissionPolicyNameManagementState))

			g.Expect(createdBinding.Spec.ValidationActions).To(HaveLen(1))
			g.Expect(createdBinding.Spec.ValidationActions[0]).To(Equal(k8sadmissionv1.Deny))
		})
	}
}

func TestReconcileRegistryConfigValidatingAdmissionPolicies(t *testing.T) {
	tests := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		expectError bool
	}{
		{
			name: "When reconciliation succeeds it should return no error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
			},
			expectError: false,
		},
		{
			name: "When cluster is being deleted it should still succeed",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-cluster",
					Namespace:         "test-namespace",
					DeletionTimestamp: &metav1.Time{Time: time.Now()},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Create a fake client
			scheme := hyperapi.Scheme
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

			// Create a mock createOrUpdate function
			mockCreateOrUpdate := func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				if err := f(); err != nil {
					return controllerutil.OperationResultNone, err
				}
				return controllerutil.OperationResultCreated, nil
			}

			// Create a context with a logger
			ctx := ctrl.LoggerInto(context.Background(), ctrl.Log)

			// Call the function
			err := ReconcileRegistryConfigValidatingAdmissionPolicies(ctx, tt.hcp, fakeClient, mockCreateOrUpdate)

			// Verify error expectations
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
