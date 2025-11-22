package util

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	operatorv1 "github.com/openshift/api/operator/v1"

	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// EnsureIngressOperatorConfiguration tests that the Ingress Operator configuration on the HostedCluster
// is properly reflected in the hosted cluster's IngressController and that the Ingress Operator doesn't report any errors via HCP conditions.
func EnsureIngressOperatorConfiguration(t *testing.T, ctx context.Context, mgmtClient crclient.Client, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureIngressOperatorConfiguration", func(t *testing.T) {
		AtLeast(t, Version421)
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
	})
}
