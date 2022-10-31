package hostedcontrolplane

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	corev1 "k8s.io/api/core/v1"
)

type signerReconciler func(*corev1.Secret, config.OwnerRef) error
type subReconciler func(target, ca *corev1.Secret, ownerRef config.OwnerRef) error

func (r *HostedControlPlaneReconciler) setupKASClientSigners(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	p *pki.PKIParams,
	createOrUpdate upsert.CreateOrUpdateFN,
) error {
	reconcileSigner := func(s *corev1.Secret, owner config.OwnerRef, reconciler signerReconciler) (*corev1.Secret, error) {
		applyFunc := func() error {
			return reconciler(s, owner)
		}

		if _, err := createOrUpdate(ctx, r, s, applyFunc); err != nil {
			return nil, fmt.Errorf("failed to reconcile secret '%s/%s': %v", s.Namespace, s.Name, err)
		}
		return s, nil
	}

	reconcileSub := func(target, ca *corev1.Secret, owner config.OwnerRef, reconciler subReconciler) (*corev1.Secret, error) {
		applyFunc := func() error {
			return reconciler(target, ca, owner)
		}

		if _, err := createOrUpdate(ctx, r, target, applyFunc); err != nil {
			return nil, fmt.Errorf("failed to reconcile secret '%s/%s': %v", target.Namespace, target.Name, err)
		}
		return target, nil
	}

	// ----------
	// aggregator
	// ----------

	// KAS aggregator client signer
	kasAggregateClientSigner, err := reconcileSigner(
		manifests.AggregatorClientSigner(hcp.Namespace),
		p.OwnerRef,
		pki.ReconcileAggregatorClientSigner,
	)
	if err != nil {
		return err
	}

	// KAS aggregator client cert
	if _, err := reconcileSub(
		manifests.KASAggregatorCertSecret(hcp.Namespace),
		kasAggregateClientSigner,
		p.OwnerRef,
		pki.ReconcileKASAggregatorCertSecret,
	); err != nil {
		return err
	}

	// KAS aggregator client CA
	kasAggregatorClientCA := manifests.AggregatorClientCAConfigMap(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kasAggregatorClientCA, func() error {
		return pki.ReconcileAggregatorClientCA(kasAggregatorClientCA, p.OwnerRef, kasAggregateClientSigner)
	}); err != nil {
		return fmt.Errorf("failed to reconcile combined CA: %w", err)
	}

	// ----------
	//	control-plane signer
	// ----------

	totalClientCABundle := []*corev1.Secret{}

	// signer
	kubeControlPlaneSigner, err := reconcileSigner(
		manifests.KubeControlPlaneSigner(hcp.Namespace),
		p.OwnerRef,
		pki.ReconcileKubeControlPlaneSigner,
	)
	if err != nil {
		return err
	}
	totalClientCABundle = append(totalClientCABundle, kubeControlPlaneSigner)

	// kube-scheduler client cert
	if _, err := reconcileSub(
		manifests.KubeSchedulerClientCertSecret(hcp.Namespace),
		kubeControlPlaneSigner,
		p.OwnerRef,
		pki.ReconcileKubeSchedulerClientCertSecret,
	); err != nil {
		return err
	}

	// KCM client cert
	if _, err := reconcileSub(
		manifests.KubeControllerManagerClientCertSecret(hcp.Namespace),
		kubeControlPlaneSigner,
		p.OwnerRef,
		pki.ReconcileKubeControllerManagerClientCertSecret,
	); err != nil {
		return err
	}

	// ----------
	//	KAS to kubelet signer
	// ----------

	// signer
	kasToKubeletSigner, err := reconcileSigner(
		manifests.KubeAPIServerToKubeletSigner(hcp.Namespace),
		p.OwnerRef,
		pki.ReconcileKASToKubeletSigner,
	)

	if err != nil {
		return err
	}
	totalClientCABundle = append(totalClientCABundle, kasToKubeletSigner)

	// KAS to kubelet client cert
	if _, err := reconcileSub(
		manifests.KASKubeletClientCertSecret(hcp.Namespace),
		kasToKubeletSigner,
		p.OwnerRef,
		pki.ReconcileKASKubeletClientCertSecret,
	); err != nil {
		return err
	}

	// ----------
	//	admin kubeconfig signer
	// ----------

	// signer
	adminKubeconfigSigner, err := reconcileSigner(
		manifests.SystemAdminSigner(hcp.Namespace),
		p.OwnerRef,
		pki.ReconcileAdminKubeconfigSigner,
	)

	if err != nil {
		return err
	}
	totalClientCABundle = append(totalClientCABundle, adminKubeconfigSigner)

	// system:admin client cert
	if _, err := reconcileSub(
		manifests.SystemAdminClientCertSecret(hcp.Namespace),
		adminKubeconfigSigner,
		p.OwnerRef,
		pki.ReconcileSystemAdminClientCertSecret,
	); err != nil {
		return err
	}

	// ----------
	//	CSR signer
	// ----------

	// signer
	csrSigner, err := reconcileSigner(
		manifests.CSRSignerCASecret(hcp.Namespace),
		p.OwnerRef,
		pki.ReconcileKubeCSRSigner,
	)

	if err != nil {
		return err
	}
	totalClientCABundle = append(totalClientCABundle, csrSigner)

	totalClientCA := manifests.TotalClientCABundle(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, totalClientCA, func() error {
		return pki.ReconcileTotalClientCA(
			totalClientCA,
			p.OwnerRef,
			totalClientCABundle...,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile combined CA: %w", err)
	}

	return nil
}
