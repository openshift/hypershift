package nodepool

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/pkg/manifests"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSetAllMachinesLMCondition(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name           string
		machines       []capikubevirt.KubevirtMachine
		expectedStatus corev1.ConditionStatus
		expectedReason string
		messageSubstr  string
	}{
		{
			name: "When a KubevirtMachine has VMLiveMigratable=False it should set KubeVirtNodesLiveMigratable on the NodePool to False",
			machines: []capikubevirt.KubevirtMachine{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "worker-0"},
					Status: capikubevirt.KubevirtMachineStatus{
						Conditions: []metav1.Condition{
							{
								Type:    capikubevirt.VMLiveMigratableCondition,
								Status:  metav1.ConditionFalse,
								Reason:  "DiskNotShared",
								Message: "disk is not shared",
							},
						},
					},
				},
			},
			expectedStatus: corev1.ConditionFalse,
			expectedReason: "DiskNotShared",
			messageSubstr:  "worker-0",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewGomegaWithT(t)

			const (
				clusterNamespace = "clusters"
				clusterName      = "example"
				nodePoolName     = "workers"
			)
			controlPlaneNamespace := manifests.HostedControlPlaneNamespace(clusterNamespace, clusterName)

			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:       nodePoolName,
					Namespace:  clusterNamespace,
					Generation: 2,
				},
			}
			hostedCluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName,
					Namespace: clusterNamespace,
				},
			}

			objects := make([]client.Object, 0, len(tc.machines))
			for i := range tc.machines {
				machine := tc.machines[i]
				machine.Namespace = controlPlaneNamespace
				objects = append(objects, &machine)
			}

			reconciler := &NodePoolReconciler{
				Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build(),
			}

			err := reconciler.setAllMachinesLMCondition(context.Background(), nodePool, hostedCluster)
			g.Expect(err).NotTo(HaveOccurred())

			cond := FindStatusCondition(nodePool.Status.Conditions, hyperv1.NodePoolKubeVirtLiveMigratableType)
			g.Expect(cond).NotTo(BeNil())
			g.Expect(cond.Status).To(Equal(tc.expectedStatus))
			g.Expect(cond.Reason).To(Equal(tc.expectedReason))
			g.Expect(cond.Message).To(ContainSubstring(tc.messageSubstr))
			g.Expect(cond.ObservedGeneration).To(Equal(nodePool.Generation))
		})
	}
}
