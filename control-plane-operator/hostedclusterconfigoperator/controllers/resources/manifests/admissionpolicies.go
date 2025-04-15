package manifests

import (
	k8sadmissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ValidatingAdmissionPolicy(name string) *k8sadmissionv1.ValidatingAdmissionPolicy {
	return &k8sadmissionv1.ValidatingAdmissionPolicy{
		TypeMeta: metav1.TypeMeta{
			APIVersion: k8sadmissionv1.SchemeGroupVersion.String(),
			Kind:       "ValidatingAdmissionPolicy",
		},
		ObjectMeta: metav1.ObjectMeta{Name: name},
	}
}

func ValidatingAdmissionPolicyBinding(bindingName string) *k8sadmissionv1.ValidatingAdmissionPolicyBinding {
	return &k8sadmissionv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{Name: bindingName},
	}
}
