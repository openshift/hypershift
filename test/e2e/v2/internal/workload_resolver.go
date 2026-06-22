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
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	maxOwnerTraversalDepth = 10
)

// WorkloadInfo represents a top-level workload (Deployment, StatefulSet, Job, or CronJob)
type WorkloadInfo struct {
	Kind      string
	Name      string
	Namespace string
	UID       types.UID
}

// ListWorkloads returns all workloads (Deployments, StatefulSets, Jobs, CronJobs) in the given namespace,
// sorted deterministically by Kind, then Name
func ListWorkloads(ctx context.Context, client crclient.Client, namespace string) ([]WorkloadInfo, error) {
	var workloads []WorkloadInfo

	// List Deployments
	deployments := &appsv1.DeploymentList{}
	if err := client.List(ctx, deployments, crclient.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list deployments: %w", err)
	}
	for _, deploy := range deployments.Items {
		workloads = append(workloads, WorkloadInfo{
			Kind:      "Deployment",
			Name:      deploy.Name,
			Namespace: deploy.Namespace,
			UID:       deploy.UID,
		})
	}

	// List StatefulSets
	statefulSets := &appsv1.StatefulSetList{}
	if err := client.List(ctx, statefulSets, crclient.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list statefulsets: %w", err)
	}
	for _, ss := range statefulSets.Items {
		workloads = append(workloads, WorkloadInfo{
			Kind:      "StatefulSet",
			Name:      ss.Name,
			Namespace: ss.Namespace,
			UID:       ss.UID,
		})
	}

	// List Jobs (standalone, not owned by CronJob)
	jobs := &batchv1.JobList{}
	if err := client.List(ctx, jobs, crclient.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list jobs: %w", err)
	}
	for _, job := range jobs.Items {
		// Only include Jobs that are not owned by a CronJob
		isOwnedByCronJob := false
		for _, ownerRef := range job.OwnerReferences {
			if ownerRef.Kind == "CronJob" {
				isOwnedByCronJob = true
				break
			}
		}
		if !isOwnedByCronJob {
			workloads = append(workloads, WorkloadInfo{
				Kind:      "Job",
				Name:      job.Name,
				Namespace: job.Namespace,
				UID:       job.UID,
			})
		}
	}

	// List CronJobs
	cronJobs := &batchv1.CronJobList{}
	if err := client.List(ctx, cronJobs, crclient.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list cronjobs: %w", err)
	}
	for _, cj := range cronJobs.Items {
		workloads = append(workloads, WorkloadInfo{
			Kind:      "CronJob",
			Name:      cj.Name,
			Namespace: cj.Namespace,
			UID:       cj.UID,
		})
	}

	// Sort deterministically: first by Kind, then by Name
	sort.Slice(workloads, func(i, j int) bool {
		if workloads[i].Kind != workloads[j].Kind {
			return workloads[i].Kind < workloads[j].Kind
		}
		return workloads[i].Name < workloads[j].Name
	})

	return workloads, nil
}

// GetWorkloadPods returns all pods belonging to a given workload by traversing owner references
func GetWorkloadPods(ctx context.Context, client crclient.Client, workload WorkloadInfo) ([]corev1.Pod, error) {
	var pods []corev1.Pod

	// List all pods in the namespace
	podList := &corev1.PodList{}
	if err := client.List(ctx, podList, crclient.InNamespace(workload.Namespace)); err != nil {
		return nil, fmt.Errorf("failed to list pods: %w", err)
	}

	// Filter pods that belong to this workload
	for _, pod := range podList.Items {
		if belongsToWorkload(ctx, client, &pod, workload) {
			pods = append(pods, pod)
		}
	}

	return pods, nil
}

// belongsToWorkload checks if a pod belongs to the given workload by traversing owner references
func belongsToWorkload(ctx context.Context, client crclient.Client, pod *corev1.Pod, workload WorkloadInfo) bool {
	if len(pod.OwnerReferences) == 0 {
		return false // Pods without owners are skipped
	}

	// Traverse owner references to find the top-level workload
	visited := make(map[types.UID]bool)
	currentUID := pod.UID
	visited[currentUID] = true

	owner := pod.OwnerReferences[0]
	depth := 0

	for depth < maxOwnerTraversalDepth {
		ownerUID := owner.UID
		if visited[ownerUID] {
			break // Cycle detected
		}
		visited[ownerUID] = true

		// Check if this owner matches our workload
		if owner.Kind == workload.Kind && owner.Name == workload.Name && ownerUID == workload.UID {
			return true
		}

		// If we found a top-level workload type (Deployment, StatefulSet, CronJob), stop
		if owner.Kind == "Deployment" || owner.Kind == "StatefulSet" || owner.Kind == "CronJob" {
			break
		}

		// Follow the chain for ReplicaSet -> Deployment or Job -> CronJob
		var nextOwner *metav1.OwnerReference
		switch owner.Kind {
		case "ReplicaSet":
			rs := &appsv1.ReplicaSet{}
			if err := client.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}, rs); err != nil {
				break
			}
			if len(rs.OwnerReferences) > 0 && rs.OwnerReferences[0].Kind == "Deployment" {
				nextOwner = &rs.OwnerReferences[0]
			}
		case "Job":
			job := &batchv1.Job{}
			if err := client.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}, job); err != nil {
				break
			}
			if len(job.OwnerReferences) > 0 && job.OwnerReferences[0].Kind == "CronJob" {
				nextOwner = &job.OwnerReferences[0]
			}
		default:
			break
		}

		if nextOwner == nil {
			break
		}

		owner = *nextOwner
		depth++
	}

	return false
}

// ResolveWorkloadForPod attempts to resolve the top-level workload for a pod
// Returns nil if the pod doesn't belong to a recognized workload
func ResolveWorkloadForPod(ctx context.Context, client crclient.Client, pod *corev1.Pod) (*WorkloadInfo, error) {
	if len(pod.OwnerReferences) == 0 {
		return nil, nil // Disowned pods are skipped
	}

	visited := make(map[types.UID]bool)
	currentUID := pod.UID
	visited[currentUID] = true

	owner := pod.OwnerReferences[0]
	depth := 0
	currentKind := owner.Kind
	currentName := owner.Name
	currentUID = owner.UID

	for depth < maxOwnerTraversalDepth {
		ownerUID := owner.UID
		if visited[ownerUID] {
			break // Cycle detected
		}
		visited[ownerUID] = true

		currentKind = owner.Kind
		currentName = owner.Name
		currentUID = ownerUID

		// If we found a top-level workload type, we're done
		if owner.Kind == "Deployment" || owner.Kind == "StatefulSet" || owner.Kind == "CronJob" {
			break
		}

		// Follow the chain for ReplicaSet -> Deployment or Job -> CronJob
		var nextOwner *metav1.OwnerReference
		switch owner.Kind {
		case "ReplicaSet":
			rs := &appsv1.ReplicaSet{}
			if err := client.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}, rs); err != nil {
				break
			}
			if len(rs.OwnerReferences) > 0 && rs.OwnerReferences[0].Kind == "Deployment" {
				nextOwner = &rs.OwnerReferences[0]
			}
		case "Job":
			job := &batchv1.Job{}
			if err := client.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: owner.Name}, job); err != nil {
				break
			}
			if len(job.OwnerReferences) > 0 && job.OwnerReferences[0].Kind == "CronJob" {
				nextOwner = &job.OwnerReferences[0]
			} else {
				// Standalone Job (not owned by CronJob) - this is a top-level workload
				break
			}
		default:
			break
		}

		if nextOwner == nil {
			break
		}

		owner = *nextOwner
		depth++
	}

	// Only return if we found a recognized workload type
	if currentKind == "Deployment" || currentKind == "StatefulSet" || currentKind == "Job" || currentKind == "CronJob" {
		return &WorkloadInfo{
			Kind:      currentKind,
			Name:      currentName,
			Namespace: pod.Namespace,
			UID:       currentUID,
		}, nil
	}

	return nil, nil
}

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
