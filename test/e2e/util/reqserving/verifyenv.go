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
	"math"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	scheduleraws "github.com/openshift/hypershift/hypershift-operator/controllers/scheduler/aws"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func VerifyRequestServingEnvironment(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx)
	client, err := e2eutil.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}
	csc := &schedulingv1alpha1.ClusterSizingConfiguration{}
	csc.Name = "cluster"
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(csc), csc); err != nil {
		return fmt.Errorf("failed to get ClusterSizingConfiguration: %w", err)
	}

	// Verify ClusterSizingConfiguration is valid
	if condition := meta.FindStatusCondition(csc.Status.Conditions, schedulingv1alpha1.ClusterSizingConfigurationValidType); condition == nil || condition.Status != metav1.ConditionTrue {
		return fmt.Errorf("ClusterSizingConfiguration is not valid: %v", condition)
	}

	// Verify request serving nodes have proper taints
	requestServingNodes := &corev1.NodeList{}
	if err := client.List(ctx, requestServingNodes, crclient.MatchingLabels{hyperv1.RequestServingComponentLabel: "true"}); err != nil {
		return fmt.Errorf("failed to list request serving nodes: %w", err)
	}

	for _, node := range requestServingNodes.Items {
		hasRequestServingTaint := false

		for _, taint := range node.Spec.Taints {
			if taint.Key == scheduleraws.ControlPlaneServingComponentTaint && taint.Value == "true" && taint.Effect == corev1.TaintEffectNoSchedule {
				hasRequestServingTaint = true
			}
		}

		if !hasRequestServingTaint {
			return fmt.Errorf("request serving node %s missing request-serving-component taint", node.Name)
		}
	}

	// Verify that the non-request serving nodes are created in all zones
	if csc.Spec.NonRequestServingNodesBufferPerZone != nil {
		// Determine how many non-request serving nodes should be present
		expectedCountPerZone := int(math.Ceil(csc.Spec.NonRequestServingNodesBufferPerZone.AsApproximateFloat64()))
		actualCount := map[string]int{}
		pollCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()
		err := wait.PollUntilContextCancel(pollCtx, 30*time.Second, true, func(ctx context.Context) (bool, error) {
			nodes := &corev1.NodeList{}
			if err := client.List(ctx, nodes, crclient.HasLabels{"hypershift.openshift.io/control-plane"}); err != nil {
				log.Error(err, "failed to list nodes")
				return false, nil
			}
			for _, node := range nodes.Items {
				if _, reqServing := node.Labels["hypershift.openshift.io/request-serving-component"]; reqServing {
					continue
				}
				actualCount[node.Labels[corev1.LabelTopologyZone]] = actualCount[node.Labels[corev1.LabelTopologyZone]] + 1
			}
			if len(actualCount) != 3 {
				log.Info("waiting for non-request serving nodes to be created in all zones", "zone count", len(actualCount))
				return false, nil
			}
			for zone, count := range actualCount {
				if count < expectedCountPerZone {
					log.Info("waiting for non-request serving nodes to be created in all zones", "zone", zone, "count", count, "expected", expectedCountPerZone)
					return false, nil
				}
			}
			return true, nil
		})
		if err != nil {
			return fmt.Errorf("failed to verify non-request serving nodes: %w", err)
		}
	}

	// Verify that placeholder nodes exist if configured
	for _, size := range csc.Spec.Sizes {
		if size.Management != nil && size.Management.Placeholders > 0 {
			pollCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
			defer cancel()
			err := wait.PollUntilContextCancel(pollCtx, 30*time.Second, true, func(ctx context.Context) (bool, error) {
				nodes := &corev1.NodeList{}
				if err := client.List(ctx, nodes, crclient.HasLabels{"hypershift.openshift.io/control-plane", "hypershift.openshift.io/request-serving-component"}); err != nil {
					log.Error(err, "failed to list nodes")
					return false, nil
				}
				nodePairs := map[string]int{}
				for _, node := range nodes.Items {
					if _, hasHC := node.Labels[hyperv1.HostedClusterLabel]; hasHC {
						continue
					}
					if nodeSize := node.Labels["hypershift.openshift.io/cluster-size"]; nodeSize != size.Name {
						continue
					}
					nodePairs[node.Labels[scheduleraws.OSDFleetManagerPairedNodesLabel]] = nodePairs[node.Labels[scheduleraws.OSDFleetManagerPairedNodesLabel]] + 1
				}
				for pair, count := range nodePairs {
					if count != 2 {
						log.Info("waiting for placeholder node pair", "pair", pair, "count", count)
						return false, nil
					}
				}
				if len(nodePairs) < size.Management.Placeholders {
					log.Info("waiting for count of placeholder pairs to be available", "pair count", len(nodePairs), "expected", size.Management.Placeholders)
					return false, nil
				}
				return true, nil
			})
			if err != nil {
				return fmt.Errorf("failed to verify placeholder nodes for size %s: %w", size.Name, err)
			}
		}
	}

	// Check if any size configuration has placeholders configured
	hasPlaceholders := false
	for _, size := range csc.Spec.Sizes {
		if size.Management != nil && size.Management.Placeholders > 0 {
			hasPlaceholders = true
			break
		}
	}

	// If placeholders are configured, verify the namespace exists
	if hasPlaceholders {
		placeholderNamespace := &corev1.Namespace{}
		if err := client.Get(ctx, types.NamespacedName{Name: "hypershift-request-serving-node-placeholders"}, placeholderNamespace); err != nil {
			return fmt.Errorf("placeholders configured in ClusterSizingConfiguration but hypershift-request-serving-node-placeholders namespace not found: %w", err)
		}
	}

	return nil
}
