package util

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	operatorv1 "github.com/openshift/api/operator/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		g.Expect(hostedCluster.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy.Type).To(Equal(operatorv1.NodePortServiceStrategyType), "EndpointPublishingStrategy should be NodePortService")

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
					if ic.Spec.EndpointPublishingStrategy.Type != operatorv1.NodePortServiceStrategyType {
						return false, fmt.Sprintf("expected EndpointPublishingStrategy type NodePortService, got %s", ic.Spec.EndpointPublishingStrategy.Type), nil
					}
					return true, "Successfully validated custom endpointPublishingStrategy", nil
				},
			},
			WithTimeout(5*time.Minute),
		)

		// Validate that the HostedControlPlane reports healthy conditions
		t.Logf("Validating Ingress Operator conditions on HostedControlPlane")
		hcpNamespace := fmt.Sprintf("%s-%s", hostedCluster.Namespace, hostedCluster.Name)
		EventuallyObject(t, ctx, fmt.Sprintf("HostedControlPlane %s/%s to have healthy Ingress Operator conditions", hcpNamespace, hostedCluster.Name),
			func(ctx context.Context) (*hyperv1.HostedControlPlane, error) {
				hcp := &hyperv1.HostedControlPlane{}
				err := mgmtClient.Get(ctx, types.NamespacedName{
					Namespace: hcpNamespace,
					Name:      hostedCluster.Name,
				}, hcp)
				return hcp, err
			},
			[]Predicate[*hyperv1.HostedControlPlane]{
				ConditionPredicate[*hyperv1.HostedControlPlane](Condition{
					Type:   "ingress.operator.openshift.io/Available",
					Status: metav1.ConditionTrue,
				}),
				ConditionPredicate[*hyperv1.HostedControlPlane](Condition{
					Type:   "ingress.operator.openshift.io/Progressing",
					Status: metav1.ConditionFalse,
				}),
				ConditionPredicate[*hyperv1.HostedControlPlane](Condition{
					Type:   "ingress.operator.openshift.io/Degraded",
					Status: metav1.ConditionFalse,
				}),
			},
			WithTimeout(10*time.Minute),
		)

		// Validate HostedCluster conditions are healthy
		ValidateHostedClusterConditions(t, ctx, mgmtClient, hostedCluster, true, 5*time.Minute)

		t.Logf("Successfully validated IngressOperator configuration for HostedCluster %s/%s", hostedCluster.Namespace, hostedCluster.Name)
	})
}
