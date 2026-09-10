//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	operatorv1 "github.com/openshift/api/operator/v1"

	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// ValidateIngressOperatorConfiguration verifies that the HostedCluster's
// ingress publishing strategy is reflected in the hosted cluster IngressController.
func ValidateIngressOperatorConfiguration(ctx context.Context, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) error {
	GinkgoWriter.Printf("Verifying HostedCluster %s/%s has custom Ingress Operator endpointPublishingStrategy\n", hostedCluster.Namespace, hostedCluster.Name)
	if hostedCluster.Spec.OperatorConfiguration == nil {
		return fmt.Errorf("OperatorConfiguration should be set")
	}
	if hostedCluster.Spec.OperatorConfiguration.IngressOperator == nil {
		return fmt.Errorf("IngressOperator configuration should be set")
	}
	strategy := hostedCluster.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy
	if strategy == nil {
		return fmt.Errorf("EndpointPublishingStrategy should be set")
	}
	if strategy.Type != operatorv1.LoadBalancerServiceStrategyType {
		return fmt.Errorf("EndpointPublishingStrategy should be LoadBalancerService, got %s", strategy.Type)
	}
	if strategy.LoadBalancer == nil {
		return fmt.Errorf("LoadBalancer configuration should be set")
	}
	if strategy.LoadBalancer.Scope != operatorv1.InternalLoadBalancer {
		return fmt.Errorf("LoadBalancer scope should be Internal, got %s", strategy.LoadBalancer.Scope)
	}

	GinkgoWriter.Println("Validating IngressController in hosted cluster reflects the custom endpointPublishingStrategy")
	return EventuallyObject(ctx, "IngressController default in hosted cluster to reflect the custom endpointPublishingStrategy",
		func(ctx context.Context) (*operatorv1.IngressController, error) {
			ingressController := &operatorv1.IngressController{}
			err := guestClient.Get(ctx, types.NamespacedName{
				Namespace: "openshift-ingress-operator",
				Name:      "default",
			}, ingressController)
			return ingressController, err
		},
		[]e2eutil.Predicate[*operatorv1.IngressController]{
			func(ic *operatorv1.IngressController) (bool, string, error) {
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
