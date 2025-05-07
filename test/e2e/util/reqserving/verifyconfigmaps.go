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

// VerifyRequestServingPlaceholderConfigMaps verifies that placeholder ConfigMaps are properly created
// and contain correct cluster identification data for request serving node allocation tracking.
func VerifyRequestServingPlaceholderConfigMaps(ctx context.Context, hc *hyperv1.HostedCluster) error {
	client, err := e2eutil.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	var lastErr error
	pollCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	err = wait.PollUntilContextCancel(pollCtx, 20*time.Second, true, func(pctx context.Context) (bool, error) {
		// Get placeholder ConfigMaps using the pairlabel selector
		placeholderConfigMaps := &corev1.ConfigMapList{}
		if err := client.List(pctx, placeholderConfigMaps,
			crclient.InNamespace("hypershift-request-serving-node-placeholders"),
			crclient.HasLabels{"hypershift.openshift.io/pairlabel"}); err != nil {
			lastErr = fmt.Errorf("failed to list placeholder ConfigMaps: %w", err)
			return false, nil
		}
		if len(placeholderConfigMaps.Items) == 0 {
			lastErr = fmt.Errorf("no placeholder ConfigMaps found")
			return false, nil
		}

		var errs []error
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
				errs = append(errs, fmt.Errorf("ConfigMap %s has pairlabel value %s, expected to match ConfigMap name", cm.Name, pairLabel))
			}
		} else {
			errs = append(errs, fmt.Errorf("ConfigMap %s missing pairlabel", cm.Name))
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
			if pairLabel, exists := node.Labels["osd-fleet-manager.openshift.io/paired-nodes"]; exists {
				expectedPairLabel = pairLabel
				break
			}
		}
		if expectedPairLabel != "" && cm.Name != expectedPairLabel {
			errs = append(errs, fmt.Errorf("ConfigMap name %s does not match paired-nodes label value %s", cm.Name, expectedPairLabel))
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
