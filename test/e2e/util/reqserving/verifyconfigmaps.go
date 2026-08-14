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
	scheduleraws "github.com/openshift/hypershift/hypershift-operator/controllers/scheduler/aws"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// VerifyRequestServingPlaceholderConfigMaps verifies that placeholder ConfigMaps are properly created
// and contain correct cluster identification data for request serving node allocation tracking.
func VerifyRequestServingPlaceholderConfigMaps(ctx context.Context, hc *hyperv1.HostedCluster) error {
	client, err := e2eutil.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	var lastErr error
	pollCtx, cancel := context.WithTimeout(ctx, DefaultVerificationTimeout)
	defer cancel()
	err = wait.PollUntilContextCancel(pollCtx, DefaultPollingInterval, true, func(pctx context.Context) (bool, error) {
		// Get placeholder ConfigMaps using the pairlabel selector
		placeholderConfigMaps := &corev1.ConfigMapList{}
		if err := client.List(pctx, placeholderConfigMaps,
			crclient.InNamespace(PlaceholderNamespace),
			crclient.HasLabels{"hypershift.openshift.io/pairlabel"}); err != nil {
			lastErr = fmt.Errorf("failed to list placeholder ConfigMaps: %w", err)
			return false, nil
		}
		if len(placeholderConfigMaps.Items) == 0 {
			lastErr = fmt.Errorf("no placeholder ConfigMaps found")
			return false, nil
		}

		expectedClusterIdentifier := fmt.Sprintf("%s-%s", hc.Namespace, hc.Name)
		var clusterConfigMaps []corev1.ConfigMap

		for _, cm := range placeholderConfigMaps.Items {
			if clusterNamespace, exists := cm.Data["clusterNamespace"]; exists && clusterNamespace == hc.Namespace {
				if clusterName, exists := cm.Data["clusterName"]; exists && clusterName == hc.Name {
					clusterConfigMaps = append(clusterConfigMaps, cm)
				}
			}
		}

		if len(clusterConfigMaps) != 1 {
			lastErr = fmt.Errorf("expected exactly 1 placeholder ConfigMap for HostedCluster %s, found %d", hc.Name, len(clusterConfigMaps))
			return false, nil
		}

		cm := clusterConfigMaps[0]
		if pairLabel, exists := cm.Labels["hypershift.openshift.io/pairlabel"]; exists {
			if pairLabel != cm.Name {
				lastErr = fmt.Errorf("ConfigMap %s has pairlabel value %s, expected to match ConfigMap name", cm.Name, pairLabel)
				return false, nil
			}
		} else {
			lastErr = fmt.Errorf("ConfigMap %s missing pairlabel", cm.Name)
			return false, nil
		}

		// Verify name matches paired-nodes label on request-serving nodes
		requestServingNodes := &corev1.NodeList{}
		if err := client.List(pctx, requestServingNodes, crclient.MatchingLabels{
			hyperv1.RequestServingComponentLabel: "true",
			hyperv1.HostedClusterLabel:           expectedClusterIdentifier,
		}); err != nil {
			lastErr = fmt.Errorf("failed to list request serving nodes: %w", err)
			return false, nil
		}
		expectedPairLabel := ""
		for _, node := range requestServingNodes.Items {
			if pairLabel, exists := node.Labels[scheduleraws.OSDFleetManagerPairedNodesLabel]; exists {
				expectedPairLabel = pairLabel
				break
			}
		}
		if expectedPairLabel != "" && cm.Name != expectedPairLabel {
			lastErr = fmt.Errorf("ConfigMap name %s does not match paired-nodes label value %s", cm.Name, expectedPairLabel)
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		if lastErr != nil {
			return lastErr
		}
		return err
	}
	return nil
}
