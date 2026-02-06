//go:build e2ev2
// +build e2ev2

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
	corev1 "k8s.io/api/core/v1"
)

// InfrastructureWorkload represents a control plane infrastructure workload
// running in management cluster namespaces, with its pod selector
type InfrastructureWorkload struct {
	Name        string
	Namespace   string
	PodSelector map[string]string // Exact label key-value match
	HasLabels   []string          // Label existence check (for dynamic values)
}

// GetControlPlaneInfrastructureWorkloads returns the list of known control plane infrastructure workloads
func GetControlPlaneInfrastructureWorkloads() []InfrastructureWorkload {
	return []InfrastructureWorkload{
		{
			Name:      "operator",
			Namespace: "hypershift",
			PodSelector: map[string]string{
				"app": "operator",
			},
		},
		{
			Name:      "external-dns",
			Namespace: "hypershift",
			PodSelector: map[string]string{
				"app": "external-dns",
			},
		},
		{
			Name:      "router",
			Namespace: "hypershift-sharedingress",
			PodSelector: map[string]string{
				"app": "router",
			},
		},
		{
			Name:      "placeholder",
			Namespace: "hypershift-request-serving-node-placeholders",
			HasLabels: []string{"hypershift.openshift.io/placeholder"},
		},
	}
}

// GetInfrastructureNamespaces returns unique namespaces from the infrastructure workload list
func GetInfrastructureNamespaces() []string {
	workloads := GetControlPlaneInfrastructureWorkloads()
	namespaceSet := make(map[string]struct{})
	for _, workload := range workloads {
		namespaceSet[workload.Namespace] = struct{}{}
	}

	namespaces := make([]string, 0, len(namespaceSet))
	for ns := range namespaceSet {
		namespaces = append(namespaces, ns)
	}
	return namespaces
}

// MatchesPod checks if a pod matches this workload's selector
func (w InfrastructureWorkload) MatchesPod(pod corev1.Pod) bool {
	// Check PodSelector (exact key-value match)
	if len(w.PodSelector) > 0 {
		for key, value := range w.PodSelector {
			podValue, exists := pod.Labels[key]
			if !exists || podValue != value {
				return false
			}
		}
		return true
	}

	// Check HasLabels (label existence)
	if len(w.HasLabels) > 0 {
		for _, label := range w.HasLabels {
			if _, exists := pod.Labels[label]; !exists {
				return false
			}
		}
		return true
	}

	return false
}
