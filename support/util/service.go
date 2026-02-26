package util

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExtractLoadBalancerIP extracts the LoadBalancer IP from a Service's status and returns whether it's valid.
func ExtractLoadBalancerIP(svc *corev1.Service) (string, bool) {
	if len(svc.Status.LoadBalancer.Ingress) == 0 {
		return "", false
	}

	loadBalancerIP := svc.Status.LoadBalancer.Ingress[0].IP
	if loadBalancerIP == "" {
		return "", false
	}

	return loadBalancerIP, true
}

// ExtractHostedControlPlaneOwnerName finds the HostedControlPlane owner reference name from a list of OwnerReferences.
func ExtractHostedControlPlaneOwnerName(ownerRefs []metav1.OwnerReference) string {
	for _, ownerRef := range ownerRefs {
		if ownerRef.Kind == "HostedControlPlane" && ownerRef.APIVersion == hyperv1.GroupVersion.String() {
			return ownerRef.Name
		}
	}
	return ""
}
