package namespaces

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ReconcileOpenShiftInfraNamespace(ns *corev1.Namespace) error {
	if ns.Labels == nil {
		ns.Labels = map[string]string{}
	}
	ns.Labels["pod-security.kubernetes.io/enforce"] = "privileged"
	ns.Labels["pod-security.kubernetes.io/audit"] = "privileged"
	ns.Labels["pod-security.kubernetes.io/warn"] = "privileged"
	return nil
}

func ReconcileKubeAPIServerNamespace(ns *corev1.Namespace) error {
	ensureLabels(ns, map[string]string{"openshift.io/cluster-monitoring": "true"})
	return nil
}

func ReconcileOpenShiftIngressNamespace(ns *corev1.Namespace) error {
	ensureLabels(ns, map[string]string{
		"network.openshift.io/policy-group": "ingress",
		"openshift.io/cluster-monitoring":   "true",
	})
	return nil
}

func ensureLabels(obj metav1.Object, labels map[string]string) {
	objLabels := obj.GetLabels()
	if objLabels == nil {
		objLabels = map[string]string{}
	}
	for k, v := range labels {
		objLabels[k] = v
	}
	obj.SetLabels(objLabels)
}
