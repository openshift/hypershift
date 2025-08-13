package scheduler

import (
	"fmt"
	"math/rand/v2"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	hyperapi "github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"

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
			_, err := r.Reconcile(t.Context(), req)
			g := NewGomegaWithT(t)
			g.Expect(err).ToNot(HaveOccurred())
			if test.expectDelete {
				n := &corev1.Node{}
				err := r.Get(t.Context(), client.ObjectKeyFromObject(node()), n)
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
			_, err := r.Reconcile(t.Context(), req)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			if test.checkScheduledCluster {
				actual := hostedcluster()
				err := c.Get(t.Context(), client.ObjectKeyFromObject(actual), actual)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(actual.Annotations).To(HaveKey(hyperv1.HostedClusterScheduledAnnotation))
			}
			if test.checkScheduledNodes {
				hc := hostedcluster()
				nodeList := &corev1.NodeList{}
				err := c.List(t.Context(), nodeList)
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

func TestHostedClusterSchedulerAndSizer(t *testing.T) {
	sizingConfig := &schedulingv1alpha1.ClusterSizingConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: schedulingv1alpha1.ClusterSizingConfigurationSpec{
			Sizes: []schedulingv1alpha1.SizeConfiguration{
				{
					Name: "small",
					Criteria: schedulingv1alpha1.NodeCountCriteria{
						From: 0,
						To:   ptr.To(uint32(1)),
					},
					Management: &schedulingv1alpha1.Management{
						Placeholders: 2,
					},
					Effects: &schedulingv1alpha1.Effects{
						KASGoMemLimit: ptr.To("1GiB"),
					},
				},
				{
					Name: "medium",
					Criteria: schedulingv1alpha1.NodeCountCriteria{
						From: 2,
						To:   ptr.To(uint32(2)),
					},
					Effects: &schedulingv1alpha1.Effects{
						KASGoMemLimit: ptr.To("2GiB"),
					},
				},
				{
					Name: "large",
					Criteria: schedulingv1alpha1.NodeCountCriteria{
						From: 3,
						To:   nil,
					},
					Effects: &schedulingv1alpha1.Effects{
						KASGoMemLimit: ptr.To("3GiB"),
					},
				},
			},
		},
		Status: schedulingv1alpha1.ClusterSizingConfigurationStatus{
			Conditions: []metav1.Condition{
				{
					Type:   schedulingv1alpha1.ClusterSizingConfigurationValidType,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}
	hostedcluster := func(mods ...func(*hyperv1.HostedCluster)) *hyperv1.HostedCluster {
		hc := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "test-namespace",
				Name:      "test",
				Labels: map[string]string{
					hyperv1.HostedClusterSizeLabel: "small",
				},
				Annotations: map[string]string{
					hyperv1.TopologyAnnotation: hyperv1.DedicatedRequestServingComponentsTopology,
				},
				Finalizers: []string{
					schedulerFinalizer,
				},
			},
		}
		for _, m := range mods {
			m(hc)
		}
		return hc
	}
	withSize := func(size string) func(*hyperv1.HostedCluster) {
		return func(hc *hyperv1.HostedCluster) {
			hc.Labels[hyperv1.HostedClusterSizeLabel] = size
		}
	}
	scheduledHC := func(hc *hyperv1.HostedCluster) {
		hc.Annotations[hyperv1.HostedClusterScheduledAnnotation] = "true"
		sizeLabel := hc.Labels[hyperv1.HostedClusterSizeLabel]
		hc.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation] = fmt.Sprintf("%s=%s", hyperv1.NodeSizeLabel, sizeLabel)
		for _, sizeCfg := range sizingConfig.Spec.Sizes {
			if sizeCfg.Name == sizeLabel {
				hc.Annotations[hyperv1.KubeAPIServerGOMemoryLimitAnnotation] = ptr.Deref(sizeCfg.Effects.KASGoMemLimit, "")
				break
			}
		}
	}

	node := func(name, zone, sizeLabel, OSDFleetManagerPairedNodesID string, mods ...func(*corev1.Node)) *corev1.Node {
		n := &corev1.Node{}
		n.Name = name
		n.Labels = map[string]string{
			OSDFleetManagerPairedNodesLabel:      OSDFleetManagerPairedNodesID,
			hyperv1.RequestServingComponentLabel: "true",
			"topology.kubernetes.io/zone":        zone,
			hyperv1.NodeSizeLabel:                sizeLabel,
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
			n.Spec.Taints = append(n.Spec.Taints, corev1.Taint{
				Key:    HostedClusterTaint,
				Value:  clusterKey(hc),
				Effect: corev1.TaintEffectNoSchedule,
			})
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

	placeHolderDeployment := func(name string) *appsv1.Deployment {
		labels := map[string]string{
			PlaceholderLabel:               name,
			hyperv1.HostedClusterSizeLabel: "small",
		}
		return &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:  placeholderNamespace,
				Name:       name,
				Labels:     labels,
				Generation: 1,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: ptr.To[int32](2),
				Selector: &metav1.LabelSelector{
					MatchLabels: labels,
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: labels,
					},
				},
			},
			Status: appsv1.DeploymentStatus{
				AvailableReplicas:  2,
				ReadyReplicas:      2,
				UpdatedReplicas:    2,
				Replicas:           2,
				ObservedGeneration: 1,
			},
		}
	}

	provisionedDeployment := func(d *appsv1.Deployment, size string, nodes []corev1.Node) []client.Object {
		labels := map[string]string{
			PlaceholderLabel:               d.Name,
			hyperv1.HostedClusterSizeLabel: size,
		}
		var result []client.Object
		d.Labels = labels
		d.Generation = 1
		d.Spec = appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](2),
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
			},
		}
		d.Status = appsv1.DeploymentStatus{
			AvailableReplicas:  2,
			ReadyReplicas:      2,
			UpdatedReplicas:    2,
			Replicas:           2,
			ObservedGeneration: 1,
		}
		result = append(result, d)
		for _, n := range nodes {
			result = append(result, &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod-" + n.Name,
					Namespace: placeholderNamespace,
					Labels:    labels,
				},
				Spec: corev1.PodSpec{
					NodeName: n.Name,
				},
			})
		}

		return result
	}

	placeHolderPod := func(depName, name string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: placeholderNamespace,
				Name:      name,
				Labels: map[string]string{
					PlaceholderLabel:               depName,
					hyperv1.HostedClusterSizeLabel: "small",
				},
			},
			Spec: corev1.PodSpec{
				NodeName: name,
			},
		}
	}

	placeholderResources := func(count int) []client.Object {
		result := []client.Object{}
		for i := 0; i < count; i++ {
			name := fmt.Sprintf("placeholder-%d", i)
			result = append(result, placeHolderDeployment(name))
			result = append(result, placeHolderPod(name, name+"-zone-a"))
			result = append(result, placeHolderPod(name, name+"-zone-b"))
			result = append(result, node((name+"-zone-a"), "zone-a", "small", "id1"))
			result = append(result, node((name+"-zone-b"), "zone-b", "small", "id1"))
		}
		return result
	}

	tests := []struct {
		name                  string
		hc                    *hyperv1.HostedCluster
		nodes                 []client.Object
		additionalObjects     []client.Object
		checkScheduledNodes   bool
		checkScheduledCluster bool
		expectError           bool
		expectPlaceholder     bool
	}{

		{
			name: "scheduled hosted cluster with 2 existing Nodes",
			hc:   hostedcluster(scheduledHC),
			nodes: nodes(
				node("n1", "zone-a", "small", "id1", withCluster(hostedcluster())),
				node("n2", "zone-b", "small", "id1", withCluster(hostedcluster())),
			),
			checkScheduledCluster: true,
			checkScheduledNodes:   true,
		},
		{
			name: "ensure allocated cluster node is labeled for cluster",
			hc:   hostedcluster(),
			nodes: nodes(
				node("n1", "zone-a", "small", "id1", withCluster(hostedcluster())),
				node("n2", "zone-b", "small", "id1"),
			),
			checkScheduledNodes: true,
		},
		{
			name: "ensure hosted cluster is annotated properly when nodes are scheduled",
			hc:   hostedcluster(),
			nodes: nodes(
				node("n1", "zone-a", "small", "id1", withCluster(hostedcluster())),
				node("n2", "zone-b", "small", "id1", withCluster(hostedcluster())),
			),
			checkScheduledCluster: true,
			checkScheduledNodes:   true,
		},
		{
			name:              "expect placeholder deployment when no nodes are available",
			hc:                hostedcluster(withSize("medium")),
			expectPlaceholder: true,
		},
		{
			name: "expect placeholder deployment when only one node is available",
			hc:   hostedcluster(withSize("medium")),
			nodes: nodes(
				node("n1", "zone-a", "small", "id1", withCluster(hostedcluster())),
			),
			expectPlaceholder: true,
		},
		{
			name:                "use existing placeholders for small cluster",
			hc:                  hostedcluster(),
			additionalObjects:   placeholderResources(3),
			checkScheduledNodes: true,
		},
		{
			name: "expect placeholder deployment when not the right size",
			hc:   hostedcluster(scheduledHC, withSize("medium")),
			nodes: nodes(
				node("n1", "zone-a", "small", "id1", withCluster(hostedcluster())),
				node("n2", "zone-b", "small", "id1", withCluster(hostedcluster())),
			),
			expectPlaceholder: true,
		},
		{
			name: "label nodes when placeholder deployment is ready",
			hc:   hostedcluster(withSize("medium")),
			additionalObjects: provisionedDeployment(placeholderDeployment(hostedcluster()), "medium", []corev1.Node{
				*(node("n1", "zone-a", "medium", "pair1")),
				*(node("n2", "zone-b", "medium", "pair1")),
			}),
			nodes: nodes(
				node("n1", "zone-a", "medium", "pair1"),
				node("n2", "zone-b", "medium", "pair1"),
			),
			checkScheduledNodes: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(test.hc, sizingConfig).WithObjects(test.nodes...).WithObjects(test.additionalObjects...).Build()
			r := &DedicatedServingComponentSchedulerAndSizer{
				Client:         c,
				createOrUpdate: controllerutil.CreateOrUpdate,
			}
			req := reconcile.Request{}
			req.Name = hostedcluster().Name
			req.Namespace = hostedcluster().Namespace
			_, err := r.Reconcile(t.Context(), req)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			if test.checkScheduledCluster {
				actual := hostedcluster()
				err := c.Get(t.Context(), client.ObjectKeyFromObject(actual), actual)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(actual.Annotations).To(HaveKey(hyperv1.HostedClusterScheduledAnnotation))
				sizeLabel := actual.Labels[hyperv1.HostedClusterSizeLabel]
				g.Expect(actual.Annotations[hyperv1.RequestServingNodeAdditionalSelectorAnnotation]).To(Equal(fmt.Sprintf("%s=%s", hyperv1.NodeSizeLabel, sizeLabel)))
				for _, sizeCfg := range sizingConfig.Spec.Sizes {
					if sizeCfg.Name == sizeLabel {
						g.Expect(actual.Annotations[hyperv1.KubeAPIServerGOMemoryLimitAnnotation]).To(Equal(ptr.Deref(sizeCfg.Effects.KASGoMemLimit, "")))
						break
					}
				}
			}
			if test.checkScheduledNodes {
				hc := hostedcluster()
				nodeList := &corev1.NodeList{}
				err := c.List(t.Context(), nodeList)
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
			if test.expectPlaceholder {
				deployment := placeholderDeployment(test.hc)
				err := c.Get(t.Context(), client.ObjectKeyFromObject(deployment), deployment)
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestTakenNodePairLabels(t *testing.T) {
	g := NewGomegaWithT(t)
	node := func(name, fleetManagerLabel string) *corev1.Node {
		n := &corev1.Node{}
		n.Name = name
		n.Labels = map[string]string{
			OSDFleetManagerPairedNodesLabel: fleetManagerLabel,
			hyperv1.HostedClusterLabel:      "cluster",
		}
		return n
	}
	baselineNodes := make([]client.Object, 0, 20)
	for i := 0; i < 20; i++ {
		baselineNodes = append(baselineNodes, node(fmt.Sprintf("node-%d", i), fmt.Sprintf("pair-%d", i/2)))
	}
	r := DedicatedServingComponentSchedulerAndSizer{
		Client: fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(baselineNodes...).Build(),
	}
	baseline, err := r.takenNodePairLabels(t.Context())
	g.Expect(err).ToNot(HaveOccurred())

	for i := 0; i < 10; i++ {
		nodeIndices := rand.Perm(20)
		nodes := make([]client.Object, 0, 20)
		for _, index := range nodeIndices {
			n := node(fmt.Sprintf("node-%d", index), fmt.Sprintf("pair-%d", index/2))
			nodes = append(nodes, n)
		}
		r := DedicatedServingComponentSchedulerAndSizer{
			Client: fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(nodes...).Build(),
		}
		result, err := r.takenNodePairLabels(t.Context())
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(result).To(Equal(baseline))
	}
}

func TestFilterNodeEvents(t *testing.T) {
	tests := []struct {
		name          string
		baselineNodes []client.Object
		incomingNode  client.Object
		expected      []reconcile.Request
	}{
		{
			name:          "Incoming node is not a request serving node",
			baselineNodes: []client.Object{},
			incomingNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "node-1",
					Labels: map[string]string{},
				},
			},
			expected: nil,
		},
		{
			name:          "Incoming node is already a dedicated request serving node",
			baselineNodes: []client.Object{},
			incomingNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-1",
					Labels: map[string]string{
						hyperv1.RequestServingComponentLabel: "true",
						OSDFleetManagerPairedNodesLabel:      "serving-1",
						hyperv1.HostedClusterLabel:           "namespace-cluster",
						HostedClusterNameLabel:               "cluster",
						HostedClusterNamespaceLabel:          "namespace",
					},
				},
			},
			expected: []reconcile.Request{
				{
					NamespacedName: types.NamespacedName{
						Namespace: "namespace",
						Name:      "cluster",
					},
				},
			},
		},
		{
			name: "Incoming node is a request serving node, no hostedcluster label, no matching pair",
			baselineNodes: []client.Object{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
						Labels: map[string]string{
							hyperv1.RequestServingComponentLabel: "true",
							OSDFleetManagerPairedNodesLabel:      "serving-1",
							hyperv1.HostedClusterLabel:           "namespace-cluster",
							HostedClusterNameLabel:               "cluster",
							HostedClusterNamespaceLabel:          "namespace",
						},
					},
				},
			},
			incomingNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-2",
					Labels: map[string]string{
						hyperv1.RequestServingComponentLabel: "true",
						OSDFleetManagerPairedNodesLabel:      "serving-2",
					},
				},
			},
			expected: nil,
		},
		{
			name: "Incoming node is a request serving node, no hostedcluster label, but existing pair with hostedcluster",
			baselineNodes: []client.Object{
				&corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node-1",
						Labels: map[string]string{
							hyperv1.RequestServingComponentLabel: "true",
							OSDFleetManagerPairedNodesLabel:      "serving-1",
							hyperv1.HostedClusterLabel:           "namespace-cluster",
							HostedClusterNameLabel:               "cluster",
							HostedClusterNamespaceLabel:          "namespace",
						},
					},
				},
			},
			incomingNode: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-2",
					Labels: map[string]string{
						hyperv1.RequestServingComponentLabel: "true",
						OSDFleetManagerPairedNodesLabel:      "serving-1",
					},
				},
			},
			expected: []reconcile.Request{
				{
					NamespacedName: types.NamespacedName{
						Namespace: "namespace",
						Name:      "cluster",
					},
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			r := DedicatedServingComponentSchedulerAndSizer{
				Client: fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(test.baselineNodes...).Build(),
			}
			actual := r.filterNodeEvents(t.Context(), test.incomingNode)
			g.Expect(actual).To(Equal(test.expected))
		})
	}
}
