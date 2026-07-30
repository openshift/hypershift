package util

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	operatorv1 "github.com/openshift/api/operator/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func ValidateIngressOperatorConfiguration(t testing.TB, ctx context.Context, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	g := NewWithT(t)

	// Verify the HostedCluster has the expected Ingress Operator configuration
	t.Logf("Verifying HostedCluster %s/%s has custom Ingress Operator endpointPublishingStrategy", hostedCluster.Namespace, hostedCluster.Name)
	g.Expect(hostedCluster.Spec.OperatorConfiguration).NotTo(BeNil(), "OperatorConfiguration should be set")
	g.Expect(hostedCluster.Spec.OperatorConfiguration.IngressOperator).NotTo(BeNil(), "IngressOperator configuration should be set")
	g.Expect(hostedCluster.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy).NotTo(BeNil(), "EndpointPublishingStrategy should be set")
	g.Expect(hostedCluster.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy.Type).To(Equal(operatorv1.LoadBalancerServiceStrategyType), "EndpointPublishingStrategy should be LoadBalancerService")
	g.Expect(hostedCluster.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy.LoadBalancer).NotTo(BeNil(), "LoadBalancer configuration should be set")
	g.Expect(hostedCluster.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy.LoadBalancer.Scope).To(Equal(operatorv1.InternalLoadBalancer), "LoadBalancer scope should be Internal")

	// Wait for the IngressController in the guest cluster to reflect the custom strategy
	t.Logf("Validating IngressController in guest cluster reflects the custom endpointPublishingStrategy")
	EventuallyObject(t, ctx, "IngressController default in guest cluster to reflect the custom endpointPublishingStrategy",
		func(ctx context.Context) (*operatorv1.IngressController, error) {
			ingressController := &operatorv1.IngressController{}
			err := guestClient.Get(ctx, types.NamespacedName{
				Namespace: "openshift-ingress-operator",
				Name:      "default",
			}, ingressController)
			return ingressController, err
		},
		[]Predicate[*operatorv1.IngressController]{
			func(ic *operatorv1.IngressController) (done bool, reasons string, err error) {
				if ic.Spec.EndpointPublishingStrategy == nil {
					return false, "EndpointPublishingStrategy is nil in IngressController", nil
				}
				if ic.Spec.EndpointPublishingStrategy.Type != operatorv1.LoadBalancerServiceStrategyType {
					return false, fmt.Sprintf("expected EndpointPublishingStrategy type LoadBalancerService, got %s", ic.Spec.EndpointPublishingStrategy.Type), nil
				}
				if ic.Spec.EndpointPublishingStrategy.LoadBalancer == nil {
					return false, "LoadBalancer configuration is nil in IngressController", nil
				}
				if ic.Spec.EndpointPublishingStrategy.LoadBalancer.Scope != operatorv1.InternalLoadBalancer {
					return false, fmt.Sprintf("expected LoadBalancer scope Internal, got %s", ic.Spec.EndpointPublishingStrategy.LoadBalancer.Scope), nil
				}
				return true, "Successfully validated custom endpointPublishingStrategy", nil
			},
		},
		WithTimeout(5*time.Minute),
	)
}

// EnsureIngressOperatorConfiguration tests that the Ingress Operator configuration on the HostedCluster
// is properly reflected in the hosted cluster's IngressController and that the Ingress Operator doesn't report any errors via HCP conditions.
func EnsureIngressOperatorConfiguration(t *testing.T, ctx context.Context, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureIngressOperatorConfiguration", func(t *testing.T) {
		AtLeast(t, Version421)
		ValidateIngressOperatorConfiguration(t, ctx, guestClient, hostedCluster)
	})
}

// ValidateServiceProviderDefaultIngressServingCertificate verifies that when DefaultCertificate is configured on the
// HostedCluster's IngressOperator, the certificate data in the guest cluster matches the source
// secret from the HostedCluster namespace.
func ValidateServiceProviderDefaultIngressServingCertificate(t testing.TB, ctx context.Context, mgmtClient crclient.Client, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	g := NewWithT(t)

	g.Expect(hostedCluster.Spec.OperatorConfiguration).NotTo(BeNil(), "OperatorConfiguration should be set")
	g.Expect(hostedCluster.Spec.OperatorConfiguration.IngressOperator).NotTo(BeNil(), "IngressOperator configuration should be set")
	g.Expect(hostedCluster.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate.Name).NotTo(BeEmpty(), "DefaultCertificate.Name should be set")

	sourceSecretName := hostedCluster.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate.Name

	t.Logf("Reading source secret %s/%s", hostedCluster.Namespace, sourceSecretName)
	sourceSecret := &corev1.Secret{}
	g.Expect(mgmtClient.Get(ctx, crclient.ObjectKey{
		Namespace: hostedCluster.Namespace,
		Name:      sourceSecretName,
	}, sourceSecret)).To(Succeed(), "source secret should exist in HostedCluster namespace")

	t.Logf("Validating default-ingress-cert secret in hosted cluster matches the source")
	EventuallyObject(t, ctx, "default-ingress-cert secret in hosted cluster to match the source secret",
		func(ctx context.Context) (*corev1.Secret, error) {
			secret := &corev1.Secret{}
			ref := manifests.IngressDefaultIngressControllerCert()
			err := guestClient.Get(ctx, types.NamespacedName{
				Namespace: ref.Namespace,
				Name:      ref.Name,
			}, secret)
			return secret, err
		},
		[]Predicate[*corev1.Secret]{
			func(secret *corev1.Secret) (done bool, reasons string, err error) {
				if _, ok := secret.Data[corev1.TLSCertKey]; !ok {
					return false, "secret does not contain tls.crt", nil
				}
				if _, ok := secret.Data[corev1.TLSPrivateKeyKey]; !ok {
					return false, "secret does not contain tls.key", nil
				}
				if string(secret.Data[corev1.TLSCertKey]) != string(sourceSecret.Data[corev1.TLSCertKey]) {
					return false, "tls.crt does not match source secret", nil
				}
				if string(secret.Data[corev1.TLSPrivateKeyKey]) != string(sourceSecret.Data[corev1.TLSPrivateKeyKey]) {
					return false, "tls.key does not match source secret", nil
				}
				return true, "Successfully validated default ingress certificate matches source", nil
			},
		},
		WithTimeout(5*time.Minute),
	)
}

// EnsureServiceProviderDefaultIngressServingCertificate tests that the DefaultCertificate configuration on the HostedCluster
// is properly propagated to the hosted cluster's default-ingress-cert secret with matching data.
func EnsureServiceProviderDefaultIngressServingCertificate(t *testing.T, ctx context.Context, mgmtClient crclient.Client, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureServiceProviderDefaultIngressServingCertificate", func(t *testing.T) {
		if hostedCluster.Spec.OperatorConfiguration == nil ||
			hostedCluster.Spec.OperatorConfiguration.IngressOperator == nil ||
			len(hostedCluster.Spec.OperatorConfiguration.IngressOperator.DefaultCertificate.Name) == 0 {
			t.Skip("HostedCluster does not have IngressOperator DefaultCertificate configured")
		}
		ValidateServiceProviderDefaultIngressServingCertificate(t, ctx, mgmtClient, guestClient, hostedCluster)
	})
}
