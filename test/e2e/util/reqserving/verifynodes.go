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
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// VerifyRequestServingNodeAllocation verifies that request serving nodes are properly allocated
// to the HostedCluster with correct labels, taints, and cross-AZ distribution.
func VerifyRequestServingNodeAllocation(ctx context.Context, hc *hyperv1.HostedCluster) error {
	client, err := e2eutil.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	var lastErr error
	pollCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	err = wait.PollUntilContextCancel(pollCtx, 30*time.Second, true, func(pctx context.Context) (bool, error) {
		// Get nodes with request-serving-component label
		requestServingNodes := &corev1.NodeList{}
		if err := client.List(pctx, requestServingNodes, crclient.MatchingLabels{
			hyperv1.RequestServingComponentLabel: "true",
		}); err != nil {
			lastErr = fmt.Errorf("failed to list request serving nodes: %w", err)
			return false, nil
		}

		// Filter nodes belonging to this HostedCluster
		var clusterNodes []corev1.Node
		expectedClusterIdentifier := fmt.Sprintf("%s-%s", hc.Namespace, hc.Name)

		for _, node := range requestServingNodes.Items {
			if clusterID, exists := node.Labels[hyperv1.HostedClusterLabel]; exists && clusterID == expectedClusterIdentifier {
				clusterNodes = append(clusterNodes, node)
			}
		}

		var errs []error

		if len(clusterNodes) != 2 {
			lastErr = fmt.Errorf("expected exactly 2 request serving nodes for HostedCluster %s, found %d", hc.Name, len(clusterNodes))
			return false, nil
		}

		zones := make(map[string]bool)
		pairLabels := make(map[string]bool)

		for _, node := range clusterNodes {
			requiredLabels := map[string]string{
				hyperv1.HostedClusterLabel:                  expectedClusterIdentifier,
				"hypershift.openshift.io/cluster-name":      hc.Name,
				"hypershift.openshift.io/cluster-namespace": hc.Namespace,
				hyperv1.RequestServingComponentLabel:        "true",
				"hypershift.openshift.io/control-plane":     "true",
			}
			for labelKey, expectedValue := range requiredLabels {
				if actualValue, exists := node.Labels[labelKey]; !exists || actualValue != expectedValue {
					errs = append(errs, fmt.Errorf("node %s missing or incorrect label %s: expected %s, got %s", node.Name, labelKey, expectedValue, actualValue))
				}
			}

			if _, exists := node.Labels["hypershift.openshift.io/cluster-size"]; !exists {
				errs = append(errs, fmt.Errorf("node %s missing cluster-size label", node.Name))
			}

			if pairLabel, exists := node.Labels["osd-fleet-manager.openshift.io/paired-nodes"]; exists {
				pairLabels[pairLabel] = true
			} else {
				errs = append(errs, fmt.Errorf("node %s missing paired-nodes label", node.Name))
			}

			if zone, exists := node.Labels["topology.kubernetes.io/zone"]; exists {
				zones[zone] = true
			} else {
				errs = append(errs, fmt.Errorf("node %s missing zone label", node.Name))
			}

			hasRequestServingTaint := false
			hasHostedClusterTaint := false
			for _, taint := range node.Spec.Taints {
				if taint.Key == "hypershift.openshift.io/request-serving-component" && taint.Value == "true" && taint.Effect == corev1.TaintEffectNoSchedule {
					hasRequestServingTaint = true
				}
				if taint.Key == "hypershift.openshift.io/cluster" && taint.Value == expectedClusterIdentifier && taint.Effect == corev1.TaintEffectNoSchedule {
					hasHostedClusterTaint = true
				}
			}
			if !hasRequestServingTaint {
				errs = append(errs, fmt.Errorf("node %s missing request-serving-component taint", node.Name))
			}
			if !hasHostedClusterTaint {
				errs = append(errs, fmt.Errorf("node %s missing hosted-cluster taint value %s", node.Name, expectedClusterIdentifier))
			}
		}

		if len(zones) != 2 {
			errs = append(errs, fmt.Errorf("request serving nodes should be distributed across 2 availability zones, found %d zones: %v", len(zones), zones))
		}
		if len(pairLabels) != 1 {
			errs = append(errs, fmt.Errorf("request serving nodes should have the same paired-nodes label, found %d different labels: %v", len(pairLabels), pairLabels))
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
