/*
Copyright 2026.

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

package hostedcluster

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
)

// minimalZonalSchedulingRequiredZones is the number of distinct zonal availability
// zones the management cluster must expose for the Minimal availability-zone
// scheduling policy to be applicable (etcd requires three zones for quorum spread).
const minimalZonalSchedulingRequiredZones = 3

// controlPlaneAvailabilityZoneSchedulingCondition computes the
// ControlPlaneAvailabilityZoneSchedulingAvailable condition for a HostedCluster that has
// opted into the Minimal availability-zone scheduling policy.
//
// It encodes two things that cannot be validated at admission time:
//   - Mutual exclusivity with the dedicated request-serving topology (annotation-driven,
//     and therefore not expressible in CEL). When both are requested, the dedicated
//     request-serving topology takes precedence and the Minimal policy is not applied.
//   - The management-cluster node contract: at least minimalZonalSchedulingRequiredZones
//     distinct zonal availability zones and at least one overflow node must exist.
func controlPlaneAvailabilityZoneSchedulingCondition(hcluster *hyperv1.HostedCluster, nodes []corev1.Node) metav1.Condition {
	condition := metav1.Condition{
		Type:               string(hyperv1.ControlPlaneAvailabilityZoneSchedulingAvailable),
		ObservedGeneration: hcluster.Generation,
	}

	if hcluster.Annotations[hyperv1.TopologyAnnotation] == hyperv1.DedicatedRequestServingComponentsTopology {
		condition.Status = metav1.ConditionFalse
		condition.Reason = hyperv1.ControlPlaneAvailabilityZoneSchedulingConflictsWithRequestServingReason
		condition.Message = "The dedicated request-serving topology is configured and takes precedence; the Minimal availability-zone scheduling policy is not applied."
		return condition
	}

	zonalZones := sets.New[string]()
	overflowNodes := 0
	for i := range nodes {
		switch nodes[i].Labels[hyperv1.ControlPlaneNodeRoleLabel] {
		case hyperv1.ControlPlaneNodeRoleZonal:
			if zone := nodes[i].Labels[corev1.LabelTopologyZone]; zone != "" {
				zonalZones.Insert(zone)
			}
		case hyperv1.ControlPlaneNodeRoleOverflow:
			overflowNodes++
		}
	}

	if zonalZones.Len() < minimalZonalSchedulingRequiredZones || overflowNodes == 0 {
		condition.Status = metav1.ConditionFalse
		condition.Reason = hyperv1.ControlPlaneAvailabilityZoneSchedulingNodeContractUnsatisfiedReason
		condition.Message = fmt.Sprintf("The management cluster does not satisfy the node contract for the Minimal availability-zone scheduling policy: found %d zonal availability zone(s) (need >= %d) and %d overflow node(s) (need >= 1).",
			zonalZones.Len(), minimalZonalSchedulingRequiredZones, overflowNodes)
		return condition
	}

	condition.Status = metav1.ConditionTrue
	condition.Reason = hyperv1.ControlPlaneAvailabilityZoneSchedulingAppliedReason
	condition.Message = "The Minimal availability-zone scheduling policy is in effect."
	return condition
}
