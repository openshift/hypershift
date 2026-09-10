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

package util

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestWaitForReadyNodesByNodePool(t *testing.T) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "workers"},
		Spec: hyperv1.NodePoolSpec{
			Replicas: ptr.To[int32](1),
		},
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "worker-0",
			Labels: map[string]string{hyperv1.NodePoolLabel: nodePool.Name},
		},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type:   corev1.NodeReady,
			Status: corev1.ConditionTrue,
		}}},
	}
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(node).Build()

	got, err := WaitForReadyNodesByNodePool(t.Context(), client, nodePool, hyperv1.AWSPlatform)
	if err != nil {
		t.Fatalf("WaitForReadyNodesByNodePool returned an unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != node.Name {
		t.Fatalf("WaitForReadyNodesByNodePool returned %#v, want node %q", got, node.Name)
	}
}

func TestWaitForNReadyNodesWithOptions(t *testing.T) {
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "worker-0",
			Labels: map[string]string{"role": "worker"},
		},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{
			Type:   corev1.NodeReady,
			Status: corev1.ConditionTrue,
		}}},
	}
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(node).Build()

	got, err := WaitForNReadyNodesWithOptions(t.Context(), client, 1, hyperv1.AWSPlatform, "for test",
		WithClientOptions(crclient.MatchingLabelsSelector{Selector: labels.SelectorFromSet(labels.Set{"role": "worker"})}),
		WithCollectionPredicates(func(nodes []*corev1.Node) (bool, string, error) {
			return len(nodes) == 1, "expected one worker node", nil
		}),
		WithPredicates(func(node *corev1.Node) (bool, string, error) {
			return node.Name == "worker-0", "expected worker-0", nil
		}),
	)
	if err != nil {
		t.Fatalf("WaitForNReadyNodesWithOptions returned an unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Name != node.Name {
		t.Fatalf("WaitForNReadyNodesWithOptions returned %#v, want node %q", got, node.Name)
	}
}

func TestWaitForNodePoolConfigUpdateComplete(t *testing.T) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "workers"},
	}
	client := &transitioningNodePoolClient{
		Client:   fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build(),
		nodePool: nodePool,
	}

	if err := WaitForNodePoolConfigUpdateComplete(t.Context(), client, nodePool); err != nil {
		t.Fatalf("WaitForNodePoolConfigUpdateComplete returned an unexpected error: %v", err)
	}
	if client.gets != 2 {
		t.Fatalf("WaitForNodePoolConfigUpdateComplete performed %d GETs, want 2", client.gets)
	}
}

type transitioningNodePoolClient struct {
	crclient.Client
	nodePool *hyperv1.NodePool
	gets     int
}

func (c *transitioningNodePoolClient) Get(ctx context.Context, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
	if _, ok := obj.(*hyperv1.NodePool); !ok {
		return c.Client.Get(ctx, key, obj, opts...)
	}

	c.gets++
	updated := c.nodePool.DeepCopy()
	status := metav1.ConditionFalse
	if c.gets == 1 {
		status = metav1.ConditionTrue
	}
	updated.Status.Conditions = []hyperv1.NodePoolCondition{{
		Type:   hyperv1.NodePoolUpdatingConfigConditionType,
		Status: corev1.ConditionStatus(status),
	}}
	obj.(*hyperv1.NodePool).ObjectMeta = *updated.ObjectMeta.DeepCopy()
	obj.(*hyperv1.NodePool).Spec = *updated.Spec.DeepCopy()
	obj.(*hyperv1.NodePool).Status = *updated.Status.DeepCopy()
	return nil
}
