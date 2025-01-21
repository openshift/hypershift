package kubevirt

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	component "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	workerServiceAccount = "kubevirt-csi-controller-sa"
)

func adaptKubeconfigSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	csrSigner := manifests.CSRSignerCASecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(csrSigner), csrSigner); err != nil {
		return fmt.Errorf("failed to get cluster-signer-ca secret: %v", err)
	}
	rootCA := manifests.RootCAConfigMap(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert configMap: %w", err)
	}

	return pki.ReconcileServiceAccountKubeconfig(secret, csrSigner, rootCA, cpContext.HCP, manifests.KubevirtCSIDriverTenantNamespaceStr, workerServiceAccount)
}
