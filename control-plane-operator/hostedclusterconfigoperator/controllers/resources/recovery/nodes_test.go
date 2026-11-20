package recovery

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/upsert"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/strategicpatch"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/google/cel-go/cel"
)

func TestRecoverNodeStatus(t *testing.T) {
	t.Run("When recovery resources are reconciled, it should install the policy idempotently and report failures", func(t *testing.T) {
		tests := []struct {
			name        string
			failPolicy  bool
			failBinding bool
			errorText   string
		}{
			{name: "When recovery starts, it should install an idempotent policy limited to kubelet status updates"},
			{
				name:       "When policy creation fails, it should return an error without activating a binding",
				failPolicy: true,
				errorText:  "failed to reconcile node status recovery policy:",
			},
			{
				name:        "When binding creation fails, it should return an error so recovery can retry",
				failBinding: true,
				errorText:   "failed to reconcile node status recovery policy binding:",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				g := NewWithT(t)
				c := fake.NewClientBuilder().WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						switch obj.(type) {
						case *admissionregistrationv1.ValidatingAdmissionPolicy:
							if tt.failPolicy {
								return fmt.Errorf("policy creation failed")
							}
						case *admissionregistrationv1.ValidatingAdmissionPolicyBinding:
							if tt.failBinding {
								return fmt.Errorf("binding creation failed")
							}
						}
						return c.Create(ctx, obj, opts...)
					},
				}).Build()
				err := RecoverNodeStatus(t.Context(), c, upsert.New(false).CreateOrUpdate)
				if tt.errorText != "" {
					g.Expect(err).To(MatchError(ContainSubstring(tt.errorText)))
					bindings := &admissionregistrationv1.ValidatingAdmissionPolicyBindingList{}
					g.Expect(c.List(t.Context(), bindings)).To(Succeed())
					g.Expect(bindings.Items).To(BeEmpty())
					return
				}
				g.Expect(err).NotTo(HaveOccurred())
				policy := &admissionregistrationv1.ValidatingAdmissionPolicy{}
				key := client.ObjectKey{Name: nodeStatusRecoveryPolicyName}
				g.Expect(c.Get(t.Context(), key, policy)).To(Succeed())
				g.Expect(*policy.Spec.FailurePolicy).To(Equal(admissionregistrationv1.Ignore))
				g.Expect(policy.Spec.MatchConstraints.ResourceRules).To(HaveLen(1))
				rule := policy.Spec.MatchConstraints.ResourceRules[0]
				g.Expect(rule.Operations).To(ConsistOf(admissionregistrationv1.Update))
				g.Expect(rule.APIGroups).To(ConsistOf(""))
				g.Expect(rule.APIVersions).To(ConsistOf("v1"))
				g.Expect(rule.Resources).To(ConsistOf("nodes/status"))
				g.Expect(*rule.Scope).To(Equal(admissionregistrationv1.ClusterScope))
				binding := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
				bindingKey := client.ObjectKey{Name: policy.Name + "-binding"}
				g.Expect(c.Get(t.Context(), bindingKey, binding)).To(Succeed())
				g.Expect(binding.Spec.PolicyName).To(Equal(policy.Name))
				g.Expect(binding.Spec.ValidationActions).To(ConsistOf(admissionregistrationv1.Deny))

				g.Expect(RecoverNodeStatus(t.Context(), c, upsert.New(false).CreateOrUpdate)).To(Succeed())
				unchangedPolicy := &admissionregistrationv1.ValidatingAdmissionPolicy{}
				unchangedBinding := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
				g.Expect(c.Get(t.Context(), key, unchangedPolicy)).To(Succeed())
				g.Expect(c.Get(t.Context(), bindingKey, unchangedBinding)).To(Succeed())
				g.Expect(unchangedPolicy.ResourceVersion).To(Equal(policy.ResourceVersion))
				g.Expect(unchangedBinding.ResourceVersion).To(Equal(binding.ResourceVersion))
			})
		}
	})

	t.Run("When Node status patches arrive, it should reject stale kubelet heartbeats without blocking valid updates", func(t *testing.T) {
		tests := []struct {
			name         string
			username     string
			cachedReady  corev1.ConditionStatus
			desiredReady corev1.ConditionStatus
			storedReady  corev1.ConditionStatus
			noConditions bool
			registration bool
			allowed      bool
		}{
			{
				name:     "When a stale ready kubelet only patches heartbeat on an Unknown Node, it should force a fresh read",
				username: "system:node:worker", cachedReady: corev1.ConditionTrue,
				desiredReady: corev1.ConditionTrue, storedReady: corev1.ConditionUnknown,
			},
			{
				name:     "When a stale unhealthy kubelet only patches heartbeat on an Unknown Node, it should force a fresh read",
				username: "system:node:worker", cachedReady: corev1.ConditionFalse,
				desiredReady: corev1.ConditionFalse, storedReady: corev1.ConditionUnknown,
			},
			{
				name:     "When a healthy kubelet retries from a fresh Unknown Node, it should allow Ready True",
				username: "system:node:worker", cachedReady: corev1.ConditionUnknown,
				desiredReady: corev1.ConditionTrue, storedReady: corev1.ConditionUnknown, allowed: true,
			},
			{
				name:     "When an unhealthy kubelet retries from a fresh Unknown Node, it should allow Ready False",
				username: "system:node:worker", cachedReady: corev1.ConditionUnknown,
				desiredReady: corev1.ConditionFalse, storedReady: corev1.ConditionUnknown, allowed: true,
			},
			{
				name:     "When a node controller marks a Node Unknown, it should allow the update",
				username: "system:kube-controller-manager", cachedReady: corev1.ConditionTrue,
				desiredReady: corev1.ConditionUnknown, storedReady: corev1.ConditionTrue, allowed: true,
			},
			{
				name:     "When an administrator updates an Unknown Node, it should not apply kubelet validation",
				username: "system:admin", cachedReady: corev1.ConditionUnknown,
				desiredReady: corev1.ConditionUnknown, storedReady: corev1.ConditionUnknown, allowed: true,
			},
			{
				name:     "When a kubelet re-registers an Unknown Node, it should allow the capacity patch without a heartbeat",
				username: "system:node:worker", cachedReady: corev1.ConditionUnknown,
				desiredReady: corev1.ConditionUnknown, storedReady: corev1.ConditionUnknown,
				registration: true, allowed: true,
			},
			{
				name:     "When a healthy Node sends subsequent heartbeats, it should allow them without a fresh read",
				username: "system:node:worker", cachedReady: corev1.ConditionTrue,
				desiredReady: corev1.ConditionTrue, storedReady: corev1.ConditionTrue, allowed: true,
			},
			{
				name:     "When an unhealthy Node sends subsequent heartbeats, it should preserve Ready False",
				username: "system:node:worker", cachedReady: corev1.ConditionFalse,
				desiredReady: corev1.ConditionFalse, storedReady: corev1.ConditionFalse, allowed: true,
			},
			{
				name:     "When Node conditions have not been initialized, it should not reject unrelated status updates",
				username: "system:node:worker", noConditions: true, allowed: true,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				g := NewWithT(t)
				c := fake.NewClientBuilder().Build()
				g.Expect(RecoverNodeStatus(t.Context(), c, upsert.New(false).CreateOrUpdate)).To(Succeed())
				policy := &admissionregistrationv1.ValidatingAdmissionPolicy{}
				g.Expect(c.Get(t.Context(), client.ObjectKey{Name: nodeStatusRecoveryPolicyName}, policy)).To(Succeed())

				cached := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker"}}
				if !tt.noConditions {
					cached.Status.Conditions = []corev1.NodeCondition{{
						Type: corev1.NodeReady, Status: tt.cachedReady,
						LastHeartbeatTime: metav1.NewTime(time.Unix(100, 0)),
					}}
				}
				desired, stored := cached.DeepCopy(), cached.DeepCopy()
				if !tt.noConditions {
					desired.Status.Conditions[0].Status = tt.desiredReady
					if tt.registration {
						desired.Status.Capacity = corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")}
					} else {
						desired.Status.Conditions[0].LastHeartbeatTime = metav1.NewTime(time.Unix(200, 0))
					}
					stored.Status.Conditions[0].Status = tt.storedReady
				}
				oldJSON, err := json.Marshal(cached)
				g.Expect(err).NotTo(HaveOccurred())
				newJSON, err := json.Marshal(desired)
				g.Expect(err).NotTo(HaveOccurred())
				patch, err := strategicpatch.CreateTwoWayMergePatch(oldJSON, newJSON, corev1.Node{})
				g.Expect(err).NotTo(HaveOccurred())
				storedJSON, err := json.Marshal(stored)
				g.Expect(err).NotTo(HaveOccurred())
				mergedJSON, err := strategicpatch.StrategicMergePatch(storedJSON, patch, corev1.Node{})
				g.Expect(err).NotTo(HaveOccurred())
				merged := &corev1.Node{}
				g.Expect(json.Unmarshal(mergedJSON, merged)).To(Succeed())
				object, err := runtime.DefaultUnstructuredConverter.ToUnstructured(merged)
				g.Expect(err).NotTo(HaveOccurred())
				oldObject, err := runtime.DefaultUnstructuredConverter.ToUnstructured(stored)
				g.Expect(err).NotTo(HaveOccurred())

				env, err := cel.NewEnv(cel.Variable("object", cel.DynType), cel.Variable("oldObject", cel.DynType), cel.Variable("request", cel.DynType))
				g.Expect(err).NotTo(HaveOccurred())
				expression := "!(" + policy.Spec.MatchConditions[0].Expression + ") || (" + policy.Spec.Validations[0].Expression + ")"
				ast, issues := env.Compile(expression)
				g.Expect(issues.Err()).NotTo(HaveOccurred())
				program, err := env.Program(ast)
				g.Expect(err).NotTo(HaveOccurred())
				result, _, err := program.Eval(map[string]interface{}{
					"object":    object,
					"oldObject": oldObject,
					"request":   map[string]interface{}{"userInfo": map[string]interface{}{"username": tt.username}},
				})
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(result.Value()).To(Equal(tt.allowed))
				if tt.allowed && !tt.noConditions {
					g.Expect(merged.Status.Conditions[0].Status).To(Equal(tt.desiredReady))
				}
			})
		}
	})
}
