package namespaces

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
