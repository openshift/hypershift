package ocm

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

func ReconcileOpenShiftControllerManagerServiceCAWorkerManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	serviceCA := manifests.OpenShiftControllerManagerServiceCA()
	reconcileServiceCA(serviceCA)
	return util.ReconcileWorkerManifest(cm, serviceCA)
}

func reconcileServiceCA(cm *corev1.ConfigMap) {
	if cm.Annotations == nil {
		cm.Annotations = map[string]string{}
	}
	cm.Annotations["service.beta.openshift.io/inject-cabundle"] = "true"
}
