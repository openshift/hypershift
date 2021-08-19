package ocm

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/alknopfler/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/alknopfler/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/alknopfler/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

func ReconcileOpenShiftControllerManagerNamespaceWorkerManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	ns := manifests.OpenShiftControllerManagerNamespace()
	return util.ReconcileWorkerManifest(cm, ns)
}
