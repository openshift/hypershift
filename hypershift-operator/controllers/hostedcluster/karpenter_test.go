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

package hostedcluster

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	karpenterv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenter"
	karpenteroperatorv2 "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/karpenteroperator"
	"github.com/openshift/hypershift/support/api"

	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileAutoNodeEnabledCondition(t *testing.T) {
	hcpNamespace := "clusters-test"

	karpenterEnabledAutoNode := &hyperv1.AutoNode{
		Provisioner: &hyperv1.ProvisionerConfig{
			Name: hyperv1.ProvisionerKarpenter,
			Karpenter: &hyperv1.KarpenterConfig{
				Platform: hyperv1.AWSPlatform,
			},
		},
	}

	rolloutCompleteTrue := metav1.Condition{
		Type:   string(hyperv1.ControlPlaneComponentRolloutComplete),
		Status: metav1.ConditionTrue,
		Reason: hyperv1.AsExpectedReason,
	}
	rolloutCompleteFalse := metav1.Condition{
		Type:    string(hyperv1.ControlPlaneComponentRolloutComplete),
		Status:  metav1.ConditionFalse,
		Reason:  "WaitingForRollout",
		Message: "deployment not yet ready",
	}

	tests := map[string]struct {
		autoNode   *hyperv1.AutoNode
		components []hyperv1.ControlPlaneComponent
		want       metav1.Condition
	}{
		"When karpenter is enabled and components not yet created it should report progressing": {
			autoNode:   karpenterEnabledAutoNode,
			components: nil,
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is enabled and only one component exists it should report progressing": {
			autoNode: karpenterEnabledAutoNode,
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenteroperatorv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteTrue}},
				},
			},
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is enabled and one component is not rolled out it should report progressing": {
			autoNode: karpenterEnabledAutoNode,
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenteroperatorv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteTrue}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenterv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteFalse}},
				},
			},
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is enabled and both components are rolled out it should report ready": {
			autoNode: karpenterEnabledAutoNode,
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenteroperatorv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteTrue}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenterv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteTrue}},
				},
			},
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionTrue,
				Reason: hyperv1.AsExpectedReason,
			},
		},
		"When karpenter is disabled and components are still present it should report progressing": {
			autoNode: nil,
			components: []hyperv1.ControlPlaneComponent{
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenteroperatorv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteTrue}},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: karpenterv2.ComponentName, Namespace: hcpNamespace},
					Status:     hyperv1.ControlPlaneComponentStatus{Conditions: []metav1.Condition{rolloutCompleteTrue}},
				},
			},
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeProgressingReason,
			},
		},
		"When karpenter is disabled and no components are present it should report not configured": {
			autoNode:   nil,
			components: nil,
			want: metav1.Condition{
				Type:   string(hyperv1.AutoNodeEnabled),
				Status: metav1.ConditionFalse,
				Reason: hyperv1.AutoNodeNotConfiguredReason,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			objs := make([]hyperv1.ControlPlaneComponent, len(tc.components))
			copy(objs, tc.components)

			builder := fake.NewClientBuilder().WithScheme(api.Scheme)
			for i := range objs {
				builder = builder.WithStatusSubresource(&objs[i])
				builder = builder.WithObjects(&objs[i])
			}
			fakeClient := builder.Build()

			// Patch component status (WithObjects only sets spec; status requires explicit update).
			for i := range objs {
				if err := fakeClient.Status().Update(context.Background(), &objs[i]); err != nil {
					t.Fatalf("failed to update component status: %v", err)
				}
			}

			r := &HostedClusterReconciler{Client: fakeClient}
			hcluster := &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					AutoNode: tc.autoNode,
				},
			}

			got := r.reconcileAutoNodeEnabledCondition(context.Background(), hcluster, hcpNamespace)
			got.ObservedGeneration = 0
			got.Message = ""
			got.LastTransitionTime = metav1.Time{}

			if !equality.Semantic.DeepEqual(tc.want, got) {
				t.Errorf("expected %+v, got %+v", tc.want, got)
			}
		})
	}
}
