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
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	corev1 "k8s.io/api/core/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// VerifyRequestServingPodLabels verifies that request serving pods do not have
// the hypershift.openshift.io/need-management-kas-access=true label.
func VerifyRequestServingPodLabels(ctx context.Context, hc *hyperv1.HostedCluster) error {
	cpNamespace := fmt.Sprintf("%s-%s", hc.Namespace, hc.Name)
	client, err := e2eutil.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get cluster client: %w", err)
	}

	// Get all pods with request-serving-component label
	requestServingPods := &corev1.PodList{}
	if err := client.List(ctx, requestServingPods,
		crclient.InNamespace(cpNamespace),
		crclient.MatchingLabels{hyperv1.RequestServingComponentLabel: "true"}); err != nil {
		return fmt.Errorf("failed to list request serving pods: %w", err)
	}

	var errs []error

	for _, pod := range requestServingPods.Items {
		// Check if the pod has the need-management-kas-access label set to true
		if labelValue, exists := pod.Labels["hypershift.openshift.io/need-management-kas-access"]; exists && labelValue == "true" {
			errs = append(errs, fmt.Errorf("request serving pod %s has need-management-kas-access=true label, this should not be present on request serving pods", pod.Name))
		}
	}

	return utilerrors.NewAggregate(errs)
}
