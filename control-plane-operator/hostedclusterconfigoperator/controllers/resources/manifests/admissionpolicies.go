package manifests

import (
	k8sadmissionv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ValidatingAdmissionPolicy(name string) *k8sadmissionv1beta1.ValidatingAdmissionPolicy {
	return &k8sadmissionv1beta1.ValidatingAdmissionPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: k8sadmissionv1beta1.SchemeGroupVersion.String(),
			Kind:       "ValidatingAdmissionPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

func ValidatingAdmissionPolicyBinding(bindingName, policyName, paramName string) *k8sadmissionv1beta1.ValidatingAdmissionPolicyBinding {
	return &k8sadmissionv1beta1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: bindingName},
		Spec: k8sadmissionv1beta1.ValidatingAdmissionPolicyBindingSpec{
			PolicyName:        policyName,
			ValidationActions: []k8sadmissionv1beta1.ValidationAction{k8sadmissionv1beta1.Deny},
		},
	}
}

func WithPolicyMatch(resources, apiVersion, apiGroup []string, operations []k8sadmissionv1beta1.OperationType, policy *k8sadmissionv1beta1.ValidatingAdmissionPolicy) *k8sadmissionv1beta1.ValidatingAdmissionPolicy {
	policyCP := policy.DeepCopy()
	policyCP.Spec.MatchConstraints = &k8sadmissionv1beta1.MatchResources{
		ResourceRules: []k8sadmissionv1beta1.NamedRuleWithOperations{
			{
				RuleWithOperations: k8sadmissionv1beta1.RuleWithOperations{
					Operations: operations,
					Rule: k8sadmissionv1beta1.Rule{
						APIGroups:   apiGroup,
						APIVersions: apiVersion,
						Resources:   resources,
					},
				},
			},
		},
	}

	return policyCP
}

func WithMatchConditions(matchConditions []k8sadmissionv1beta1.MatchCondition, policy *k8sadmissionv1beta1.ValidatingAdmissionPolicy) *k8sadmissionv1beta1.ValidatingAdmissionPolicy {
	policyCP := policy.DeepCopy()
	policyCP.Spec.MatchConditions = matchConditions
	return policyCP
}

func WithValidations(validations []k8sadmissionv1beta1.Validation, policy *k8sadmissionv1beta1.ValidatingAdmissionPolicy) *k8sadmissionv1beta1.ValidatingAdmissionPolicy {
	policyCP := policy.DeepCopy()
	policyCP.Spec.Validations = validations
	return policyCP
}
