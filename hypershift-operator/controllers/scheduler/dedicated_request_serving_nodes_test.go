package scheduler

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	hyperapi "github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestNodeReaper(t *testing.T) {
	const clusterNamespace = "clusters"
	const nodeName = "n1"
	node := func(mods ...func(*corev1.Node)) *corev1.Node {
		n := &corev1.Node{}
		n.Name = nodeName
		n.Labels = map[string]string{hyperv1.RequestServingComponentLabel: "true"}
		for _, m := range mods {
			m(n)
		}
		return n
	}
	withCluster := func(name string) func(*corev1.Node) {
		return func(n *corev1.Node) {
			n.Labels[hyperv1.HostedClusterLabel] = fmt.Sprintf("%s-%s", clusterNamespace, name)
			n.Labels[HostedClusterNamespaceLabel] = clusterNamespace
			n.Labels[HostedClusterNameLabel] = name
		}
	}
	cluster := func(name string) *hyperv1.HostedCluster {
		c := &hyperv1.HostedCluster{}
		c.Namespace = clusterNamespace
		c.Name = name
		return c
	}

	tests := []struct {
		name         string
		existing     []client.Object
		expectDelete bool
	}{
		{
			name: "no associated cluster",
			existing: []client.Object{
				node(),
			},
		},
		{
			name: "associated existing cluster",
			existing: []client.Object{
				node(withCluster("c1")),
				cluster("c1"),
			},
		},
		{
			name: "associated with non-existent cluster",
			existing: []client.Object{
				node(withCluster("c1")),
			},
			expectDelete: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := &DedicatedServingComponentNodeReaper{
				Client: fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(test.existing...).Build(),
			}
			req := reconcile.Request{}
			req.Name = nodeName
			_, err := r.Reconcile(context.Background(), req)
			g := NewGomegaWithT(t)
			g.Expect(err).ToNot(HaveOccurred())
			if test.expectDelete {
				n := &corev1.Node{}
				err := r.Get(context.Background(), client.ObjectKeyFromObject(node()), n)
				g.Expect(err).ToNot(BeNil())
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}
		})
	}
}

func TestHostedClusterScheduler(t *testing.T) {
	hostedcluster := func(mods ...func(*hyperv1.HostedCluster)) *hyperv1.HostedCluster {
		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test-namespace",
				Name:      "test",
				Annotations: map[string]string{
					hyperv1.TopologyAnnotation: hyperv1.DedicatedRequestServingComponentsTopology,
				},
			},
		}
		for _, m := range mods {
			m(hc)
		}
		return hc
	}
	deletedHC := func(hc *hyperv1.HostedCluster) {
		now := metav1.Now()
		hc.DeletionTimestamp = &now
		hc.Finalizers = []string{"necessary"} // fake client needs finalizers when a deletionTimestamp is set
	}
	scheduledHC := func(hc *hyperv1.HostedCluster) {
		hc.Annotations[hyperv1.HostedClusterScheduledAnnotation] = "true"
	}
	hcName := func(name string) func(*hyperv1.HostedCluster) {
		return func(hc *hyperv1.HostedCluster) {
			hc.Name = name
		}
	}
	_ = hcName

	node := func(name, zone, OSDFleetManagerPairedNodesID string, mods ...func(*corev1.Node)) *corev1.Node {
		n := &corev1.Node{}
		n.Name = name
		n.Labels = map[string]string{
			OSDFleetManagerPairedNodesLabel:      OSDFleetManagerPairedNodesID,
			hyperv1.RequestServingComponentLabel: "true",
			"topology.kubernetes.io/zone":        zone,
		}
		for _, m := range mods {
			m(n)
		}
		return n
	}

	withCluster := func(hc *hyperv1.HostedCluster) func(*corev1.Node) {
		return func(n *corev1.Node) {
			n.Labels[HostedClusterNameLabel] = hc.Name
			n.Labels[HostedClusterNamespaceLabel] = hc.Namespace
			n.Labels[hyperv1.HostedClusterLabel] = fmt.Sprintf("%s-%s", hc.Namespace, hc.Name)
		}
	}

	nodeZone := func(n *corev1.Node) string {
		return n.Labels["topology.kubernetes.io/zone"]
	}

	nodes := func(n ...*corev1.Node) []client.Object {
		result := make([]client.Object, 0, len(n))
		for _, node := range n {
			result = append(result, node)
		}
		return result
	}

	tests := []struct {
		name                  string
		hc                    *hyperv1.HostedCluster
		nodes                 []client.Object
		checkScheduledNodes   bool
		checkScheduledCluster bool
		expectError           bool
	}{
		{
			name: "deleted hosted cluster",
			hc:   hostedcluster(deletedHC),
		},
		{
			name: "scheduled hosted cluster with 2 existing Nodes",
			hc:   hostedcluster(scheduledHC),
			nodes: nodes(
				node("n1", "zone-a", "id1", withCluster(hostedcluster())),
				node("n2", "zone-b", "id1", withCluster(hostedcluster())),
			),
		},
		{
			name: "available nodes",
			hc:   hostedcluster(),
			nodes: nodes(
				node("n1", "zone-a", "id1"),
				node("n2", "zone-a", "id2"),
				node("n3", "zone-b", "id1"),
				node("n4", "zone-c", "id2")),
			checkScheduledNodes:   true,
			checkScheduledCluster: true,
		},
		{
			name: "available node, existing assigned node",
			hc:   hostedcluster(),
			nodes: nodes(
				node("n1", "zone-a", "id1", withCluster(hostedcluster())),
				node("n2", "zone-b", "id1")),
			checkScheduledNodes:   true,
			checkScheduledCluster: true,
		},
		{
			name: "When there's no paired Nodes in different AZs it should fail",
			hc:   hostedcluster(),
			nodes: nodes(
				node("n1", "zone-a", "id1"),
				node("n2", "zone-a", "id1"),
				node("n3", "zone-b", "id2"),
				node("n4", "zone-c", "id2")),
			expectError: true,
		},
		{
			name: "When all Nodes are already labeled with other HC it should fail",
			hc:   hostedcluster(),
			nodes: nodes(
				node("n1", "zone-a", "id1", withCluster(hostedcluster(hcName("other")))),
				node("n2", "zone-b", "id2", withCluster(hostedcluster(hcName("other"))))),
			expectError: true,
		},
		{
			name: "When HostedCluster is scheduled, without 2 existing Nodes and there's available Nodes it should find them",
			hc:   hostedcluster(scheduledHC),
			nodes: nodes(
				node("n1", "zone-a", "id1", withCluster(hostedcluster())),
				node("n2", "zone-b", "id1")),
			checkScheduledNodes:   true,
			checkScheduledCluster: true,
		},
		{
			name: "When HostedCluster is scheduled, without 2 existing Nodes and there's no Nodes available it should fail",
			hc:   hostedcluster(scheduledHC),
			nodes: nodes(
				node("n1", "zone-a", "id1", withCluster(hostedcluster())),
				node("n2", "zone-b", "id2")),
			expectError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(test.hc).WithObjects(test.nodes...).Build()
			r := &DedicatedServingComponentScheduler{
				Client:         c,
				createOrUpdate: controllerutil.CreateOrUpdate,
			}
			req := reconcile.Request{}
			req.Name = hostedcluster().Name
			req.Namespace = hostedcluster().Namespace
			_, err := r.Reconcile(context.Background(), req)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			if test.checkScheduledCluster {
				actual := hostedcluster()
				err := c.Get(context.Background(), client.ObjectKeyFromObject(actual), actual)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(actual.Annotations).To(HaveKey(hyperv1.HostedClusterScheduledAnnotation))
			}
			if test.checkScheduledNodes {
				hc := hostedcluster()
				nodeList := &corev1.NodeList{}
				err := c.List(context.Background(), nodeList)
				g.Expect(err).ToNot(HaveOccurred())
				scheduledNodeIndices := []int{}
				for i, node := range nodeList.Items {
					if _, hasLabel := node.Labels[hyperv1.HostedClusterLabel]; hasLabel {
						scheduledNodeIndices = append(scheduledNodeIndices, i)
						g.Expect(node.Labels[hyperv1.HostedClusterLabel]).To(Equal(fmt.Sprintf("%s-%s", hc.Namespace, hc.Name)))
						g.Expect(node.Labels[HostedClusterNameLabel]).To(Equal(hc.Name))
						g.Expect(node.Labels[HostedClusterNamespaceLabel]).To(Equal(hc.Namespace))
						g.Expect(node.Spec.Taints).To(ContainElement(corev1.Taint{
							Key:    HostedClusterTaint,
							Value:  fmt.Sprintf("%s-%s", hc.Namespace, hc.Name),
							Effect: corev1.TaintEffectNoSchedule,
						}))
					}
				}
				g.Expect(scheduledNodeIndices).To(HaveLen(2))
				g.Expect(nodeZone(&nodeList.Items[scheduledNodeIndices[0]])).ToNot(Equal(nodeZone(&nodeList.Items[scheduledNodeIndices[1]])))
				g.Expect(nodeList.Items[scheduledNodeIndices[0]].Labels[OSDFleetManagerPairedNodesLabel]).To(Equal(nodeList.Items[scheduledNodeIndices[1]].Labels[OSDFleetManagerPairedNodesLabel]))
			}
		})
	}
}
