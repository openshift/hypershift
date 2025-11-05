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

package reqserving

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportutil "github.com/openshift/hypershift/support/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// VerifyRequestServingPodDistribution verifies that only the correct request serving pods
// are scheduled on request serving nodes with expected replica counts and proper labeling.
func VerifyRequestServingPodDistribution(ctx context.Context, hc *hyperv1.HostedCluster) error {
	cpNamespace := fmt.Sprintf("%s-%s", hc.Namespace, hc.Name)
	client, err := e2eutil.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	var lastErr error
	pollCtx, cancel := context.WithTimeout(ctx, ComplexVerificationTimeout)
	defer cancel()
	err = wait.PollUntilContextCancel(pollCtx, DefaultPollingInterval, true, func(pctx context.Context) (bool, error) {
		var errs []error

		// Build set of request-serving nodes for this HostedCluster
		requestServingNodes := &corev1.NodeList{}
		expectedClusterIdentifier := fmt.Sprintf("%s-%s", hc.Namespace, hc.Name)
		if err := client.List(pctx, requestServingNodes, crclient.MatchingLabels{
			hyperv1.RequestServingComponentLabel: "true",
			hyperv1.HostedClusterLabel:           expectedClusterIdentifier,
		}); err != nil {
			lastErr = fmt.Errorf("failed to list request serving nodes: %w", err)
			return false, nil
		}
		if len(requestServingNodes.Items) == 0 {
			lastErr = fmt.Errorf("no request serving nodes found for HostedCluster %s", hc.Name)
			return false, nil
		}
		requestServingNodeNames := make(map[string]bool)
		for _, node := range requestServingNodes.Items {
			requestServingNodeNames[node.Name] = true
		}

		// Verify pods are on request-serving nodes and properly labeled
		requestServingPods := &corev1.PodList{}
		if err := client.List(pctx, requestServingPods,
			crclient.InNamespace(cpNamespace),
			crclient.MatchingLabels{hyperv1.RequestServingComponentLabel: "true"}); err != nil {
			lastErr = fmt.Errorf("failed to list request serving pods: %w", err)
			return false, nil
		}

		for _, pod := range requestServingPods.Items {
			if pod.Spec.NodeName == "" {
				errs = append(errs, fmt.Errorf("request serving pod %s is not scheduled to any node", pod.Name))
				continue
			}
			if !requestServingNodeNames[pod.Spec.NodeName] {
				errs = append(errs, fmt.Errorf("request serving pod %s is scheduled on non-request-serving node %s", pod.Name, pod.Spec.NodeName))
			}
			requiredLabels := map[string]string{
				hyperv1.RequestServingComponentLabel:           "true",
				"hypershift.openshift.io/hosted-control-plane": cpNamespace,
			}
			for labelKey, expectedValue := range requiredLabels {
				if actualValue, exists := pod.Labels[labelKey]; !exists || actualValue != expectedValue {
					errs = append(errs, fmt.Errorf("pod %s missing or incorrect label %s: expected %s, got %s", pod.Name, labelKey, expectedValue, actualValue))
				}
			}
		}

		// Verify expected deployments are ready with expected replicas
		expectedDeployments := map[string]int{
			"kube-apiserver": 2,
			// "oauth-openshift":       2,
			"ignition-server-proxy": 2,
			"router":                2,
		}
		for depName, expectedReplicas := range expectedDeployments {
			dep := &appsv1.Deployment{}
			if err := client.Get(pctx, crclient.ObjectKey{Namespace: cpNamespace, Name: depName}, dep); err != nil {
				errs = append(errs, fmt.Errorf("failed to get deployment %s: %v", depName, err))
				continue
			}
			if dep.Spec.Replicas == nil || *dep.Spec.Replicas != int32(expectedReplicas) {
				errs = append(errs, fmt.Errorf("deployment %s has replicas %v, expected %d", depName, valueOrZero(dep.Spec.Replicas), expectedReplicas))
			}
			if !supportutil.IsDeploymentReady(pctx, dep) {
				errs = append(errs, fmt.Errorf("deployment %s not yet ready", depName))
			}
		}

		lastErr = utilerrors.NewAggregate(errs)
		return lastErr == nil, nil
	})
	if err != nil {
		if lastErr != nil {
			return lastErr
		}
		return err
	}
	return nil
}

func valueOrZero(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}
