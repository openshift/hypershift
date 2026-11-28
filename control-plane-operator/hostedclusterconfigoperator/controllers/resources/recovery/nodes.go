package recovery

import (
	"context"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/upsert"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const nodeStatusRecoveryPolicyName = "hypershift-node-status-recovery"

// RecoverNodeStatus makes stale kubelet heartbeats retry with a fresh Node after restoration.
func RecoverNodeStatus(ctx context.Context, c client.Client, createOrUpdate upsert.CreateOrUpdateFN) error {
	policy := manifests.ValidatingAdmissionPolicy(nodeStatusRecoveryPolicyName)
	if _, err := createOrUpdate(ctx, c, policy, func() error {
		policy.Spec = admissionregistrationv1.ValidatingAdmissionPolicySpec{
			// A recovery aid must not block status updates on evaluation errors.
			FailurePolicy: ptr.To(admissionregistrationv1.Ignore),
			MatchConstraints: &admissionregistrationv1.MatchResources{
				MatchPolicy:       ptr.To(admissionregistrationv1.Equivalent),
				NamespaceSelector: &metav1.LabelSelector{},
				ObjectSelector:    &metav1.LabelSelector{},
				ResourceRules: []admissionregistrationv1.NamedRuleWithOperations{{
					RuleWithOperations: admissionregistrationv1.RuleWithOperations{
						Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Update},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{""},
							APIVersions: []string{"v1"},
							Resources:   []string{"nodes/status"},
							Scope:       ptr.To(admissionregistrationv1.ClusterScope),
						},
					},
				}},
			},
			MatchConditions: []admissionregistrationv1.MatchCondition{{
				Name:       "kubelet-updating-own-status",
				Expression: "request.userInfo.username == 'system:node:' + object.metadata.name",
			}},
			// Reject a heartbeat that preserves Ready=Unknown so the kubelet retries with a fresh GET.
			// Registration can legitimately patch status without changing the heartbeat.
			Validations: []admissionregistrationv1.Validation{{
				Expression: `!has(object.status) || !has(object.status.conditions) ||
					!has(oldObject.status) || !has(oldObject.status.conditions) ||
					!object.status.conditions.exists(c, c.type == 'Ready' && c.status == 'Unknown' &&
						has(c.lastHeartbeatTime) && oldObject.status.conditions.exists(previous,
							previous.type == 'Ready' && previous.status == 'Unknown' &&
							(!has(previous.lastHeartbeatTime) || previous.lastHeartbeatTime != c.lastHeartbeatTime)))`,
				Message: "kubelet must refresh Node status before posting a heartbeat for Ready=Unknown",
			}},
		}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile node status recovery policy: %w", err)
	}

	// Keep the policy after recovery: a kubelet may reconnect after the restore annotation is removed.
	binding := manifests.ValidatingAdmissionPolicyBinding(nodeStatusRecoveryPolicyName + "-binding")
	if _, err := createOrUpdate(ctx, c, binding, func() error {
		binding.Spec.PolicyName = policy.Name
		binding.Spec.ValidationActions = []admissionregistrationv1.ValidationAction{admissionregistrationv1.Deny}
		return nil
	}); err != nil {
		return fmt.Errorf("failed to reconcile node status recovery policy binding: %w", err)
	}
	return nil
}
