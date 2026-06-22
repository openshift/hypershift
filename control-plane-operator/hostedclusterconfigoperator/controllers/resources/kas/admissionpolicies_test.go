package kas

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	k8sadmissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/google/cel-go/cel"
)

func TestGenerateCelExpression(t *testing.T) {
	tests := []struct {
		name                 string
		usernames            []string
		expectedExpression   string
		shouldPassValidation bool
		inputObjects         map[string]interface{}
	}{
		{
			name:                 "single username, should pass validation, should pass",
			usernames:            []string{"user1"},
			expectedExpression:   "request.userInfo.username in ['user1'] || (has(object.spec) && has(oldObject.spec) && object.spec == oldObject.spec)",
			shouldPassValidation: true,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "user1",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "value",
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "value",
					},
				},
			},
		},
		{
			name:                 "multiple usernames, same spec, invalid user, should pass",
			usernames:            []string{"user2", "user3"},
			expectedExpression:   "request.userInfo.username in ['user2', 'user3'] || (has(object.spec) && has(oldObject.spec) && object.spec == oldObject.spec)",
			shouldPassValidation: true,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "user1",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "value",
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "value",
					},
				},
			},
		},
		{
			name:                 "multiple usernames, different spec, valid username, should pass",
			usernames:            []string{"user2", "user3"},
			expectedExpression:   "request.userInfo.username in ['user2', 'user3'] || (has(object.spec) && has(oldObject.spec) && object.spec == oldObject.spec)",
			shouldPassValidation: true,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "user3",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "value",
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "wrongValue",
					},
				},
			},
		},
		{
			name:                 "multiple usernames, different spec, invalid username, should not pass",
			usernames:            []string{"user2", "user3"},
			expectedExpression:   "request.userInfo.username in ['user2', 'user3'] || (has(object.spec) && has(oldObject.spec) && object.spec == oldObject.spec)",
			shouldPassValidation: false,
			inputObjects: map[string]interface{}{
				"request.userInfo.username": "user1",
				"object": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "value",
					},
				},
				"oldObject": map[string]interface{}{
					"spec": map[string]interface{}{
						"field": "wrongValue",
					},
				},
			},
		},
		{
			name:               "no usernames, should pass",
			usernames:          []string{},
			expectedExpression: "has(object.spec) && has(oldObject.spec) && object.spec == oldObject.spec",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			result := generateCelExpression(tt.usernames)
			g.Expect(result).To(Equal(tt.expectedExpression))

			if len(tt.usernames) != 0 {
				env, err := cel.NewEnv(
					cel.Variable("object", cel.MapType(cel.StringType, cel.DynType)),
					cel.Variable("oldObject", cel.MapType(cel.StringType, cel.DynType)),
					cel.Variable("request.userInfo.username", cel.StringType),
				)

				g.Expect(err).To(BeNil())

				ast, issues := env.Compile(result)
				g.Expect(issues).To(BeNil(), "Compile errors: %v", issues)

				prog, err := env.Program(ast)
				g.Expect(err).To(BeNil(), "Program errors: %v", err)

				out, _, err := prog.Eval(tt.inputObjects)
				g.Expect(err).To(BeNil())
				g.Expect(tt.shouldPassValidation).To(BeEquivalentTo(out.Value().(bool)))
			}
		})
	}
}

func failOnNthCreateOrUpdate(n int) upsert.CreateOrUpdateFN {
	call := 0
	return func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		call++
		if call == n {
			return controllerutil.OperationResultNone, fmt.Errorf("injected failure on call %d", n)
		}
		return upsert.New(false).CreateOrUpdate(ctx, c, obj, f)
	}
}

func TestReconcileAdmissionPolicy(t *testing.T) {
	tests := []struct {
		name           string
		createOrUpdate upsert.CreateOrUpdateFN
		wantErr        bool
		errSubstr      string
	}{
		{
			name:           "When createOrUpdate succeeds, it should create VAP and binding without error",
			createOrUpdate: upsert.New(false).CreateOrUpdate,
		},
		{
			name: "When VAP createOrUpdate fails, it should return a wrapped error",
			createOrUpdate: func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				if _, ok := obj.(*k8sadmissionv1.ValidatingAdmissionPolicy); ok {
					return controllerutil.OperationResultNone, fmt.Errorf("API conflict")
				}
				return upsert.New(false).CreateOrUpdate(ctx, c, obj, f)
			},
			wantErr:   true,
			errSubstr: "failed to create/update Validating Admission Policy with name",
		},
		{
			name: "When binding createOrUpdate fails, it should return a wrapped error",
			createOrUpdate: func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				if _, ok := obj.(*k8sadmissionv1.ValidatingAdmissionPolicyBinding); ok {
					return controllerutil.OperationResultNone, fmt.Errorf("API conflict")
				}
				return upsert.New(false).CreateOrUpdate(ctx, c, obj, f)
			},
			wantErr:   true,
			errSubstr: "failed to create/update Validating Admission Policy Binding with name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			scheme := fake.NewClientBuilder().Build().Scheme()
			g.Expect(k8sadmissionv1.AddToScheme(scheme)).To(Succeed())
			c := fake.NewClientBuilder().WithScheme(scheme).Build()

			ap := &AdmissionPolicy{
				Name:        "test-policy",
				Validations: []k8sadmissionv1.Validation{{Expression: "true", Message: "allowed"}},
				MatchConstraints: &k8sadmissionv1.MatchResources{
					ResourceRules: []k8sadmissionv1.NamedRuleWithOperations{
						{
							RuleWithOperations: k8sadmissionv1.RuleWithOperations{
								Operations: []k8sadmissionv1.OperationType{"CREATE"},
							},
						},
					},
				},
			}

			err := ap.reconcileAdmissionPolicy(t.Context(), c, tt.createOrUpdate)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestReconcileKASValidatingAdmissionPolicies(t *testing.T) {
	tests := []struct {
		name           string
		createOrUpdate upsert.CreateOrUpdateFN
		wantErr        bool
		errSubstr      string
	}{
		{
			name:           "When all sub-reconcilers succeed, it should return no error",
			createOrUpdate: upsert.New(false).CreateOrUpdate,
		},
		{
			name: "When Config VAP reconcile fails, it should return a wrapped error",
			createOrUpdate: func(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				return controllerutil.OperationResultNone, fmt.Errorf("injected failure")
			},
			wantErr:   true,
			errSubstr: "failed to reconcile Config Validating Admission Policy",
		},
		{
			name:           "When Mirror VAP reconcile fails, it should return a wrapped error",
			createOrUpdate: failOnNthCreateOrUpdate(3),
			wantErr:        true,
			errSubstr:      "failed to reconcile Mirror Validating Admission Policies",
		},
		{
			name:           "When ICSP VAP reconcile fails, it should return a wrapped ICSP error",
			createOrUpdate: failOnNthCreateOrUpdate(5),
			wantErr:        true,
			errSubstr:      "error reconciling ICSP Validating Admission Policy",
		},
		{
			name:           "When Infra VAP reconcile fails, it should return a wrapped error",
			createOrUpdate: failOnNthCreateOrUpdate(7),
			wantErr:        true,
			errSubstr:      "failed to reconcile Infrastructure Validating Admission Policy",
		},
		{
			name:           "When ConfigMaps VAP reconcile fails, it should return a wrapped ConfigMaps error",
			createOrUpdate: failOnNthCreateOrUpdate(9),
			wantErr:        true,
			errSubstr:      "error reconciling mirrored ConfigMaps Validating Admission Policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			scheme := fake.NewClientBuilder().Build().Scheme()
			g.Expect(k8sadmissionv1.AddToScheme(scheme)).To(Succeed())
			c := fake.NewClientBuilder().WithScheme(scheme).Build()

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: "test-ns"},
			}

			err := ReconcileKASValidatingAdmissionPolicies(t.Context(), hcp, c, tt.createOrUpdate)
			if tt.wantErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errSubstr))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
