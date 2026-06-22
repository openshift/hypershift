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

	"github.com/go-logr/logr"
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

	if condition := meta.FindStatusCondition(csc.Status.Conditions, schedulingv1alpha1.ClusterSizingConfigurationValidType); condition == nil || condition.Status != metav1.ConditionTrue {
		return fmt.Errorf("ClusterSizingConfiguration is not valid: %v", condition)
	}

	if err := verifyRequestServingNodeTaints(ctx, client); err != nil {
		return err
	}

	if err := verifyNonRequestServingNodes(ctx, log, client, csc); err != nil {
		return err
	}

	if err := verifyPlaceholderNodes(ctx, log, client, csc); err != nil {
		return err
	}

	return verifyPlaceholderNamespace(ctx, client, csc)
}

func verifyRequestServingNodeTaints(ctx context.Context, client crclient.Client) error {
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
	return nil
}

func verifyNonRequestServingNodes(ctx context.Context, log logr.Logger, client crclient.Client, csc *schedulingv1alpha1.ClusterSizingConfiguration) error {
	if csc.Spec.NonRequestServingNodesBufferPerZone == nil {
		return nil
	}
	expectedCountPerZone := int(math.Ceil(csc.Spec.NonRequestServingNodesBufferPerZone.AsApproximateFloat64()))
	pollCtx, cancel := context.WithTimeout(ctx, ComplexVerificationTimeout)
	defer cancel()
	err := wait.PollUntilContextCancel(pollCtx, DefaultPollingInterval, true, func(ctx context.Context) (bool, error) {
		actualCount := map[string]int{}
		nodes := &corev1.NodeList{}
		if err := client.List(ctx, nodes, crclient.HasLabels{ControlPlaneNodeLabel}); err != nil {
			log.Error(err, "failed to list nodes")
			return false, nil
		}
		for _, node := range nodes.Items {
			if _, reqServing := node.Labels[hyperv1.RequestServingComponentLabel]; reqServing {
				continue
			}
			if zone, exists := node.Labels[corev1.LabelTopologyZone]; !exists || zone == "" {
				continue
			}
			actualCount[node.Labels[corev1.LabelTopologyZone]] = actualCount[node.Labels[corev1.LabelTopologyZone]] + 1
		}
		if len(actualCount) < 3 {
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
	return nil
}

func verifyPlaceholderNodes(ctx context.Context, log logr.Logger, client crclient.Client, csc *schedulingv1alpha1.ClusterSizingConfiguration) error {
	for _, size := range csc.Spec.Sizes {
		if size.Management == nil || size.Management.Placeholders <= 0 {
			continue
		}
		if err := verifyPlaceholderNodesForSize(ctx, log, client, size); err != nil {
			return err
		}
	}
	return nil
}

func verifyPlaceholderNodesForSize(ctx context.Context, log logr.Logger, client crclient.Client, size schedulingv1alpha1.SizeConfiguration) error {
	pollCtx, cancel := context.WithTimeout(ctx, ComplexVerificationTimeout)
	defer cancel()
	err := wait.PollUntilContextCancel(pollCtx, DefaultPollingInterval, true, func(ctx context.Context) (bool, error) {
		nodes := &corev1.NodeList{}
		if err := client.List(ctx, nodes, crclient.HasLabels{ControlPlaneNodeLabel, hyperv1.RequestServingComponentLabel}); err != nil {
			log.Error(err, "failed to list nodes")
			return false, nil
		}
		nodePairs := map[string]int{}
		for _, node := range nodes.Items {
			if _, hasHC := node.Labels[hyperv1.HostedClusterLabel]; hasHC {
				continue
			}
			if nodeSize := node.Labels[hyperv1.NodeSizeLabel]; nodeSize != size.Name {
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
	return nil
}

func verifyPlaceholderNamespace(ctx context.Context, client crclient.Client, csc *schedulingv1alpha1.ClusterSizingConfiguration) error {
	hasPlaceholders := false
	for _, size := range csc.Spec.Sizes {
		if size.Management != nil && size.Management.Placeholders > 0 {
			hasPlaceholders = true
			break
		}
	}
	if !hasPlaceholders {
		return nil
	}
	placeholderNS := &corev1.Namespace{}
	if err := client.Get(ctx, types.NamespacedName{Name: PlaceholderNamespace}, placeholderNS); err != nil {
		return fmt.Errorf("placeholders configured in ClusterSizingConfiguration but %s namespace not found: %w", PlaceholderNamespace, err)
	}
	return nil
}
