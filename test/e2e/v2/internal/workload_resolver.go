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

package internal

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// GetWorkloadPodsBySelector returns all pods in the given namespace that match the workload's pod selector
func GetWorkloadPodsBySelector(ctx context.Context, client crclient.Client, namespace string, workload WorkloadSpec) ([]corev1.Pod, error) {
	podList := &corev1.PodList{}
	selector := labels.SelectorFromSet(workload.PodSelector)
	err := client.List(ctx, podList, &crclient.ListOptions{
		Namespace:     namespace,
		LabelSelector: selector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods for workload %s: %w", workload.Name, err)
	}
	return podList.Items, nil
}
