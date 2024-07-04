package hostedcontrolplane

import (
	"context"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

type signerReconciler func(*corev1.Secret, config.OwnerRef) error
type subReconciler func(target, ca *corev1.Secret, ownerRef config.OwnerRef, validity time.Duration) error

func (r *HostedControlPlaneReconciler) setupKASClientSigners(
	ctx context.Context,
	hcp *hyperv1.HostedControlPlane,
	p *pki.PKIParams,
	createOrUpdate upsert.CreateOrUpdateFN,
	rootCASecret *corev1.Secret,
	additionalClientCAs ...*corev1.ConfigMap,
) error {
	var (
		validity *time.Duration
		err      error
	)

	if validity, err = certs.GenerateCertValidity(ptr.To(hcp.Annotations[hyperv1.SelfSignedCertificateValidityAnnotation])); err != nil {
		return fmt.Errorf("failed to parse server cert validity from annotation %s: %w", hyperv1.SelfSignedCertificateValidityAnnotation, err)
	}

	reconcileSigner := func(s *corev1.Secret, reconciler signerReconciler) (*corev1.Secret, error) {
		applyFunc := func() error {
			return reconciler(s, p.OwnerRef)
		}

		if _, err := createOrUpdate(ctx, r, s, applyFunc); err != nil {
			return nil, fmt.Errorf("failed to reconcile secret '%s/%s': %v", s.Namespace, s.Name, err)
		}
		return s, nil
	}

	reconcileSub := func(target, ca *corev1.Secret, reconciler subReconciler) (*corev1.Secret, error) {
		applyFunc := func() error {
			return reconciler(target, ca, p.OwnerRef, *validity)
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
		pki.ReconcileAggregatorClientSigner,
	)
	if err != nil {
		return err
	}

	// KAS aggregator client cert
	if _, err := reconcileSub(
		manifests.KASAggregatorCertSecret(hcp.Namespace),
		kasAggregateClientSigner,
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
	kubeletClientCABundle := []*corev1.Secret{}

	// signer
	kubeControlPlaneSigner, err := reconcileSigner(
		manifests.KubeControlPlaneSigner(hcp.Namespace),
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
		pki.ReconcileKubeSchedulerClientCertSecret,
	); err != nil {
		return err
	}

	// KCM client cert
	if _, err := reconcileSub(
		manifests.KubeControllerManagerClientCertSecret(hcp.Namespace),
		kubeControlPlaneSigner,
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
		pki.ReconcileKASToKubeletSigner,
	)

	if err != nil {
		return err
	}
	totalClientCABundle = append(totalClientCABundle, kasToKubeletSigner)
	kubeletClientCABundle = append(kubeletClientCABundle, kasToKubeletSigner)

	// KAS to kubelet client cert
	if _, err := reconcileSub(
		manifests.KASKubeletClientCertSecret(hcp.Namespace),
		kasToKubeletSigner,
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
		pki.ReconcileKubeCSRSigner,
	)

	if err != nil {
		return err
	}
	totalClientCABundle = append(totalClientCABundle, csrSigner)
	kubeletClientCABundle = append(kubeletClientCABundle, csrSigner)

	// KAS bootstrap client cert secret
	if _, err := reconcileSub(
		manifests.KASMachineBootstrapClientCertSecret(hcp.Namespace),
		csrSigner,
		pki.ReconcileKASMachineBootstrapClientCertSecret,
	); err != nil {
		return err
	}

	// OpenShift Authenticator
	openshiftAuthenticatorCertSecret := manifests.OpenshiftAuthenticatorCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, openshiftAuthenticatorCertSecret, func() error {
		return pki.ReconcileOpenShiftAuthenticatorCertSecret(openshiftAuthenticatorCertSecret, csrSigner, p.OwnerRef, *validity)
	}); err != nil {
		return fmt.Errorf("failed to reconcile openshift authenticator cert: %w", err)
	}

	// Metrics client cert
	metricsClientCert := manifests.MetricsClientCertSecret(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, metricsClientCert, func() error {
		return pki.ReconcileMetricsSAClientCertSecret(metricsClientCert, csrSigner, p.OwnerRef, *validity)
	}); err != nil {
		return fmt.Errorf("failed to reconcile metrics client cert secret: %w", err)
	}

	totalClientCA := manifests.TotalClientCABundle(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, totalClientCA, func() error {
		return pki.ReconcileTotalClientCA(
			totalClientCA,
			p.OwnerRef,
			additionalClientCAs,
			totalClientCABundle...,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile combined CA: %w", err)
	}

	kubeletClientCA := manifests.KubeletClientCABundle(hcp.Namespace)
	if _, err := createOrUpdate(ctx, r, kubeletClientCA, func() error {
		return pki.ReconcileKubeletClientCA(
			kubeletClientCA,
			p.OwnerRef,
			kubeletClientCABundle...,
		)
	}); err != nil {
		return fmt.Errorf("failed to reconcile kubelet client CA: %w", err)
	}

	return nil
}
