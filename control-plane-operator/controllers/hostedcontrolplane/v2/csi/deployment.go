package csi

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apiserver/pkg/authentication/serviceaccount"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {

	var (
		secret = manifests.KubevirtCSIDriverTenantKubeConfig(cpContext.HCP.Namespace)
		saNS   = manifests.KubevirtCSIDriverTenantNamespaceStr
		saName = "kubevirt-csi-controller-sa"
		cn     = serviceaccount.MakeUsername(saNS, saName)
	)
	// Create the Certs for CSI driver
	rootCA := manifests.RootCAConfigMap(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext.Context, crclient.ObjectKeyFromObject(rootCA), rootCA); err != nil {
		return fmt.Errorf("failed to get root ca cert secret: %w", err)
	}

	csrSigner := manifests.CSRSignerCASecret(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext.Context, crclient.ObjectKeyFromObject(csrSigner), csrSigner); err != nil {
		return fmt.Errorf("failed to get csr signer cert secret: %w", err)
	}

	if err := reconcileSignedCert(secret, csrSigner, config.OwnerRef{}, cn, serviceaccount.MakeGroupNames(serviceAccountNamespace), X509UsageClientAuth); err != nil {
		return fmt.Errorf("failed to reconcile serviceaccount client cert: %w", err)
	}
	svcURL := inClusterKASURL(hcp.Spec.Platform.Type)
	return ReconcileKubeConfig(secret, secret, ca, svcURL, "", manifests.KubeconfigScopeLocal, config.OwnerRef{})
	return nil
}

func adaptServiceAccount(cpContext component.WorkloadContext, sa *corev1.ServiceAccount) error {
	util.EnsurePullSecret(sa, common.PullSecret("").Name)

	return nil
}

func adaptRoleBinding(cpContext component.WorkloadContext, rb *rbacv1.RoleBinding) error {
	for i := range rb.Subjects {
		rb.Subjects[i].Namespace = cpContext.HCP.Namespace
	}

	return nil
}

func getStorageDriverType(hcp *hyperv1.HostedControlPlane) hyperv1.KubevirtStorageDriverConfigType {
	storageDriverType := hyperv1.DefaultKubevirtStorageDriverConfigType

	if hcp.Spec.Platform.Kubevirt != nil &&
		hcp.Spec.Platform.Kubevirt.StorageDriver != nil &&
		hcp.Spec.Platform.Kubevirt.StorageDriver.Type != "" {

		storageDriverType = hcp.Spec.Platform.Kubevirt.StorageDriver.Type
	}
	return storageDriverType
}
