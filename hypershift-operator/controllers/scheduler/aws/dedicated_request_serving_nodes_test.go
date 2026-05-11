package scheduler

import (
	"fmt"
	"math/rand/v2"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster"
	schedulerutil "github.com/openshift/hypershift/hypershift-operator/controllers/scheduler/util"
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
		expectedPairLabel     string
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
			expectedPairLabel:     "id1",
		},
		{
			name: "available node, existing assigned node",
			hc:   hostedcluster(),
			nodes: nodes(
				node("n1", "zone-a", "id1", withCluster(hostedcluster())),
				node("n2", "zone-b", "id1")),
			checkScheduledNodes:   true,
			checkScheduledCluster: true,
			expectedPairLabel:     "id1",
		},
		{
			name: "When there's no paired Nodes in different AZs it should fail",
			hc:   hostedcluster(),
			nodes: nodes(
				node("n1", "zone-a", "id1"),
				node("n2", "zone-a", "id1"),
				node("n3", "zone-b", "id2"),
				node("n4", "zone-c", "id2")),
			expectError:       true,
			expectedPairLabel: "id1",
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
			expectedPairLabel:     "id1",
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
				g.Expect(actual.Annotations[hyperv1.AWSLoadBalancerTargetNodesAnnotation]).
					To(Equal(OSDFleetManagerPairedNodesLabel + "=" + test.expectedPairLabel))
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
	pairConfigMap := func(hc *hyperv1.HostedCluster, pairLabel string) *corev1.ConfigMap {
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: placeholderNamespace,
				Name:      pairLabel,
				Labels: map[string]string{
					pairLabelKey: pairLabel,
				},
			},
			Data: map[string]string{
				clusterNamespaceKey: hc.Namespace,
				clusterNameKey:      hc.Name,
			},
		}
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
			name: "When a scheduled hosted cluster loses one node from its pair, it should claim and label an available replacement node from the same pair",
			hc:   hostedcluster(scheduledHC),
			nodes: nodes(
				node("n1", "zone-a", "small", "id1", withCluster(hostedcluster())),
				node("n3", "zone-b", "small", "id1"),
			),
			additionalObjects: []client.Object{
				pairConfigMap(hostedcluster(), "id1"),
			},
			checkScheduledCluster: true,
			checkScheduledNodes:   true,
		},
		{
			name: "When a scheduled hosted cluster loses one node and no same-pair replacement is available, it should create placeholder deployment",
			hc:   hostedcluster(scheduledHC),
			nodes: nodes(
				node("n1", "zone-a", "small", "id1", withCluster(hostedcluster())),
				node("n4", "zone-b", "small", "id2"),
			),
			additionalObjects: []client.Object{
				pairConfigMap(hostedcluster(), "id1"),
			},
			expectPlaceholder: true,
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

func TestIsNodePairedWith(t *testing.T) {
	tests := []struct {
		name      string
		candidate *corev1.Node
		existing  map[string]*corev1.Node
		expected  bool
	}{
		{
			name: "When there are no existing nodes, it should return true",
			candidate: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						OSDFleetManagerPairedNodesLabel: "pair-a",
					},
				},
			},
			existing: map[string]*corev1.Node{},
			expected: true,
		},
		{
			name: "When the candidate has the same pair label as an existing node, it should return true",
			candidate: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						OSDFleetManagerPairedNodesLabel: "pair-a",
					},
				},
			},
			existing: map[string]*corev1.Node{
				"zone-x": {
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							OSDFleetManagerPairedNodesLabel: "pair-a",
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "When the candidate has a different pair label from all existing nodes, it should return false",
			candidate: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						OSDFleetManagerPairedNodesLabel: "pair-b",
					},
				},
			},
			existing: map[string]*corev1.Node{
				"zone-x": {
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							OSDFleetManagerPairedNodesLabel: "pair-a",
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When multiple existing nodes have mixed pair labels, it should return true if any matches",
			candidate: &corev1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						OSDFleetManagerPairedNodesLabel: "pair-b",
					},
				},
			},
			existing: map[string]*corev1.Node{
				"zone-x": {
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							OSDFleetManagerPairedNodesLabel: "pair-a",
						},
					},
				},
				"zone-y": {
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							OSDFleetManagerPairedNodesLabel: "pair-b",
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			actual := isNodePairedWith(test.candidate, test.existing)
			g.Expect(actual).To(Equal(test.expected))
		})
	}
}

func TestGoalNodesByZone(t *testing.T) {
	tests := []struct {
		name         string
		goalNodes    []corev1.Node
		expectedKeys []string
	}{
		{
			name:         "When there are no goal nodes, it should return empty map",
			goalNodes:    nil,
			expectedKeys: nil,
		},
		{
			name: "When nodes are in different zones, it should return one node per zone",
			goalNodes: []corev1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{corev1.LabelTopologyZone: "us-east-1a"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "n2", Labels: map[string]string{corev1.LabelTopologyZone: "us-east-1b"}}},
			},
			expectedKeys: []string{"us-east-1a", "us-east-1b"},
		},
		{
			name: "When multiple nodes are in the same zone, it should keep only the first",
			goalNodes: []corev1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{corev1.LabelTopologyZone: "us-east-1a"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "n2", Labels: map[string]string{corev1.LabelTopologyZone: "us-east-1a"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "n3", Labels: map[string]string{corev1.LabelTopologyZone: "us-east-1b"}}},
			},
			expectedKeys: []string{"us-east-1a", "us-east-1b"},
		},
		{
			name: "When a node has no zone label, it should be skipped",
			goalNodes: []corev1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "n2", Labels: map[string]string{corev1.LabelTopologyZone: "us-east-1b"}}},
			},
			expectedKeys: []string{"us-east-1b"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			r := &DedicatedServingComponentSchedulerAndSizer{}
			result := r.goalNodesByZone(test.goalNodes)
			g.Expect(result).To(HaveLen(len(test.expectedKeys)))
			for _, key := range test.expectedKeys {
				g.Expect(result).To(HaveKey(key))
			}
		})
	}
}

func TestNodeNamesByZoneMap(t *testing.T) {
	tests := []struct {
		name     string
		input    map[string]corev1.Node
		expected map[string]string
	}{
		{
			name:     "When there are no nodes, it should return empty map",
			input:    map[string]corev1.Node{},
			expected: map[string]string{},
		},
		{
			name: "When there are nodes, it should map zone to node name",
			input: map[string]corev1.Node{
				"zone-a": {ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
				"zone-b": {ObjectMeta: metav1.ObjectMeta{Name: "node-2"}},
			},
			expected: map[string]string{
				"zone-a": "node-1",
				"zone-b": "node-2",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			result := nodeNamesByZoneMap(test.input)
			g.Expect(result).To(Equal(test.expected))
		})
	}
}

func TestResolvePairLabelFromNodes(t *testing.T) {
	tests := []struct {
		name        string
		nodes       []corev1.Node
		expected    string
		expectError bool
	}{
		{
			name:        "When there are no nodes, it should return an error",
			nodes:       nil,
			expectError: true,
		},
		{
			name: "When the first node has a pair label, it should return that label",
			nodes: []corev1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{OSDFleetManagerPairedNodesLabel: "pair-x"}}},
				{ObjectMeta: metav1.ObjectMeta{Name: "n2", Labels: map[string]string{OSDFleetManagerPairedNodesLabel: "pair-x"}}},
			},
			expected: "pair-x",
		},
		{
			name: "When the first node has no pair label, it should return an error",
			nodes: []corev1.Node{
				{ObjectMeta: metav1.ObjectMeta{Name: "n1", Labels: map[string]string{}}},
			},
			expectError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			r := &DedicatedServingComponentSchedulerAndSizer{}
			result, err := r.resolvePairLabelFromNodes(test.nodes)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(result).To(Equal(test.expected))
			}
		})
	}
}

func TestFindExistingNodesForCluster(t *testing.T) {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "hc1",
		},
	}
	hcValue := "ns-hc1"

	tests := []struct {
		name          string
		nodeList      *corev1.NodeList
		expectedNames []string
	}{
		{
			name:          "When there are no nodes, it should return empty map",
			nodeList:      &corev1.NodeList{},
			expectedNames: nil,
		},
		{
			name: "When a node matches the cluster, it should be included",
			nodeList: &corev1.NodeList{
				Items: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "n1",
							Labels: map[string]string{
								"topology.kubernetes.io/zone": "zone-a",
								hyperv1.HostedClusterLabel:    hcValue,
							},
						},
					},
				},
			},
			expectedNames: []string{"n1"},
		},
		{
			name: "When a node is being deleted, it should be skipped",
			nodeList: &corev1.NodeList{
				Items: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:              "n1",
							DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
							Finalizers:        []string{"test"},
							Labels: map[string]string{
								"topology.kubernetes.io/zone": "zone-a",
								hyperv1.HostedClusterLabel:    hcValue,
							},
						},
					},
				},
			},
			expectedNames: nil,
		},
		{
			name: "When a node has no zone label, it should be skipped",
			nodeList: &corev1.NodeList{
				Items: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "n1",
							Labels: map[string]string{
								hyperv1.HostedClusterLabel: hcValue,
							},
						},
					},
				},
			},
			expectedNames: nil,
		},
		{
			name: "When a node belongs to a different cluster, it should be skipped",
			nodeList: &corev1.NodeList{
				Items: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "n1",
							Labels: map[string]string{
								"topology.kubernetes.io/zone": "zone-a",
								hyperv1.HostedClusterLabel:    "other-ns-other-hc",
							},
						},
					},
				},
			},
			expectedNames: nil,
		},
		{
			name: "When nodes are in different zones for the same cluster, it should return both",
			nodeList: &corev1.NodeList{
				Items: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "n1",
							Labels: map[string]string{
								"topology.kubernetes.io/zone": "zone-a",
								hyperv1.HostedClusterLabel:    hcValue,
							},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "n2",
							Labels: map[string]string{
								"topology.kubernetes.io/zone": "zone-b",
								hyperv1.HostedClusterLabel:    hcValue,
							},
						},
					},
				},
			},
			expectedNames: []string{"n1", "n2"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			r := &DedicatedServingComponentScheduler{}
			result := r.findExistingNodesForCluster(t.Context(), test.nodeList, hc)
			g.Expect(result).To(HaveLen(len(test.expectedNames)))
			for _, name := range test.expectedNames {
				found := false
				for _, node := range result {
					if node.Name == name {
						found = true
						break
					}
				}
				g.Expect(found).To(BeTrue(), "expected node %s not found in result", name)
			}
		})
	}
}

func TestFindAvailableNodes(t *testing.T) {
	tests := []struct {
		name          string
		nodeList      *corev1.NodeList
		existing      map[string]*corev1.Node
		expectedLen   int
		expectedNames []string
	}{
		{
			name: "When an unassigned node is in a new zone with matching pair label, it should be selected",
			nodeList: &corev1.NodeList{
				Items: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "n2",
							Labels: map[string]string{
								"topology.kubernetes.io/zone":   "zone-b",
								OSDFleetManagerPairedNodesLabel: "pair-1",
							},
						},
					},
				},
			},
			existing: map[string]*corev1.Node{
				"zone-a": {
					ObjectMeta: metav1.ObjectMeta{
						Name: "n1",
						Labels: map[string]string{
							OSDFleetManagerPairedNodesLabel: "pair-1",
						},
					},
				},
			},
			expectedLen:   2,
			expectedNames: []string{"n2"},
		},
		{
			name: "When a node has no zone label, it should be skipped",
			nodeList: &corev1.NodeList{
				Items: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:   "n2",
							Labels: map[string]string{},
						},
					},
				},
			},
			existing:    map[string]*corev1.Node{},
			expectedLen: 0,
		},
		{
			name: "When a node is already assigned to a cluster, it should be skipped",
			nodeList: &corev1.NodeList{
				Items: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "n2",
							Labels: map[string]string{
								"topology.kubernetes.io/zone":   "zone-b",
								hyperv1.HostedClusterLabel:      "other-cluster",
								OSDFleetManagerPairedNodesLabel: "pair-1",
							},
						},
					},
				},
			},
			existing:    map[string]*corev1.Node{},
			expectedLen: 0,
		},
		{
			name: "When a node has a non-matching pair label, it should be skipped",
			nodeList: &corev1.NodeList{
				Items: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "n2",
							Labels: map[string]string{
								"topology.kubernetes.io/zone":   "zone-b",
								OSDFleetManagerPairedNodesLabel: "pair-2",
							},
						},
					},
				},
			},
			existing: map[string]*corev1.Node{
				"zone-a": {
					ObjectMeta: metav1.ObjectMeta{
						Name: "n1",
						Labels: map[string]string{
							OSDFleetManagerPairedNodesLabel: "pair-1",
						},
					},
				},
			},
			expectedLen: 1,
		},
		{
			name: "When a zone already has a node in use, it should be skipped",
			nodeList: &corev1.NodeList{
				Items: []corev1.Node{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name: "n2",
							Labels: map[string]string{
								"topology.kubernetes.io/zone":   "zone-a",
								OSDFleetManagerPairedNodesLabel: "pair-1",
							},
						},
					},
				},
			},
			existing: map[string]*corev1.Node{
				"zone-a": {
					ObjectMeta: metav1.ObjectMeta{
						Name: "n1",
						Labels: map[string]string{
							OSDFleetManagerPairedNodesLabel: "pair-1",
						},
					},
				},
			},
			expectedLen: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			r := &DedicatedServingComponentScheduler{}
			r.findAvailableNodes(t.Context(), test.nodeList, test.existing)
			g.Expect(test.existing).To(HaveLen(test.expectedLen))
			for _, name := range test.expectedNames {
				found := false
				for _, node := range test.existing {
					if node.Name == name {
						found = true
						break
					}
				}
				g.Expect(found).To(BeTrue(), "expected node %s not found in result", name)
			}
		})
	}
}

func TestUpdateHostedClusterAnnotations(t *testing.T) {
	tests := []struct {
		name                string
		nodesToUse          map[string]*corev1.Node
		expectedAnnotations map[string]string
	}{
		{
			name: "When nodes have GoMemLimit, LBSubnets, and pair label, it should set all annotations",
			nodesToUse: map[string]*corev1.Node{
				"zone-a": {
					ObjectMeta: metav1.ObjectMeta{
						Name: "n1",
						Labels: map[string]string{
							schedulerutil.GoMemLimitLabel:   "4096",
							schedulerutil.LBSubnetsLabel:    "subnet-1.subnet-2",
							OSDFleetManagerPairedNodesLabel: "pair-1",
						},
					},
				},
			},
			expectedAnnotations: map[string]string{
				hyperv1.HostedClusterScheduledAnnotation:     "true",
				hyperv1.KubeAPIServerGOMemoryLimitAnnotation: "4096",
				hyperv1.AWSLoadBalancerSubnetsAnnotation:     "subnet-1,subnet-2",
				hyperv1.AWSLoadBalancerTargetNodesAnnotation: OSDFleetManagerPairedNodesLabel + "=pair-1",
			},
		},
		{
			name: "When nodes have no optional labels, it should only set the scheduled annotation",
			nodesToUse: map[string]*corev1.Node{
				"zone-a": {
					ObjectMeta: metav1.ObjectMeta{
						Name:   "n1",
						Labels: map[string]string{},
					},
				},
			},
			expectedAnnotations: map[string]string{
				hyperv1.HostedClusterScheduledAnnotation: "true",
			},
		},
		{
			name: "When LBSubnets use periods as separators, it should replace with commas",
			nodesToUse: map[string]*corev1.Node{
				"zone-a": {
					ObjectMeta: metav1.ObjectMeta{
						Name: "n1",
						Labels: map[string]string{
							schedulerutil.LBSubnetsLabel: "subnet-a.subnet-b.subnet-c",
						},
					},
				},
			},
			expectedAnnotations: map[string]string{
				hyperv1.HostedClusterScheduledAnnotation: "true",
				hyperv1.AWSLoadBalancerSubnetsAnnotation: "subnet-a,subnet-b,subnet-c",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:   "test-ns",
					Name:        "test-hc",
					Annotations: map[string]string{},
				},
			}
			c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hc).Build()
			r := &DedicatedServingComponentScheduler{Client: c}
			err := r.updateHostedClusterAnnotations(t.Context(), hc, test.nodesToUse)
			g.Expect(err).ToNot(HaveOccurred())

			updated := &hyperv1.HostedCluster{}
			err = c.Get(t.Context(), client.ObjectKeyFromObject(hc), updated)
			g.Expect(err).ToNot(HaveOccurred())
			for key, value := range test.expectedAnnotations {
				g.Expect(updated.Annotations).To(HaveKeyWithValue(key, value))
			}
		})
	}
}

func TestHandleDeletion(t *testing.T) {
	now := metav1.Now()

	tests := []struct {
		name              string
		hc                *hyperv1.HostedCluster
		additionalObjects []client.Object
		expectFinalizer   bool
	}{
		{
			name: "When HC has no scheduler finalizer, it should return without changes",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         "test-ns",
					Name:              "test-hc",
					DeletionTimestamp: &now,
					Finalizers:        []string{"other-finalizer"},
					Annotations:       map[string]string{},
				},
			},
			expectFinalizer: false,
		},
		{
			name: "When HC still has the hostedcluster finalizer, it should wait and keep the scheduler finalizer",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         "test-ns",
					Name:              "test-hc",
					DeletionTimestamp: &now,
					Finalizers:        []string{schedulerFinalizer, hostedcluster.HostedClusterFinalizer},
					Annotations:       map[string]string{},
				},
			},
			expectFinalizer: true,
		},
		{
			name: "When HC has scheduler finalizer and no hostedcluster finalizer, it should remove the finalizer",
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Namespace:         "test-ns",
					Name:              "test-hc",
					DeletionTimestamp: &now,
					Finalizers:        []string{schedulerFinalizer},
					Annotations:       map[string]string{},
				},
			},
			expectFinalizer: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			objs := []client.Object{test.hc}
			objs = append(objs, test.additionalObjects...)
			c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(objs...).Build()
			r := &DedicatedServingComponentSchedulerAndSizer{
				Client:         c,
				createOrUpdate: controllerutil.CreateOrUpdate,
			}
			_, err := r.handleDeletion(t.Context(), test.hc)
			g.Expect(err).ToNot(HaveOccurred())

			updated := &hyperv1.HostedCluster{}
			err = c.Get(t.Context(), client.ObjectKeyFromObject(test.hc), updated)
			if test.expectFinalizer {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(controllerutil.ContainsFinalizer(updated, schedulerFinalizer)).To(BeTrue())
			} else {
				if err == nil {
					g.Expect(controllerutil.ContainsFinalizer(updated, schedulerFinalizer)).To(BeFalse())
				}
			}
		})
	}
}

func TestClassifyDedicatedNodes(t *testing.T) {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "hc1",
		},
	}
	hcKey := "ns-hc1"

	mkNode := func(name, hcLabel, pairLabel, sizeLabel string, deleting bool) corev1.Node {
		n := corev1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: name,
				Labels: map[string]string{
					hyperv1.RequestServingComponentLabel: "true",
					hyperv1.NodeSizeLabel:                sizeLabel,
					OSDFleetManagerPairedNodesLabel:      pairLabel,
				},
			},
		}
		if hcLabel != "" {
			n.Labels[hyperv1.HostedClusterLabel] = hcLabel
		}
		if deleting {
			now := metav1.Now()
			n.DeletionTimestamp = &now
			n.Finalizers = []string{"test"}
		}
		return n
	}

	tests := []struct {
		name              string
		nodes             []client.Object
		desiredSize       string
		expectedGoalLen   int
		expectedAvailLen  int
		expectedPairLabel string
	}{
		{
			name:             "When there are no nodes, it should return empty slices",
			nodes:            nil,
			desiredSize:      "small",
			expectedGoalLen:  0,
			expectedAvailLen: 0,
		},
		{
			name: "When nodes are labeled for the cluster with matching size and pair, they should be goal nodes",
			nodes: []client.Object{
				func() client.Object { n := mkNode("n1", hcKey, "pair-1", "small", false); return &n }(),
				func() client.Object { n := mkNode("n2", hcKey, "pair-1", "small", false); return &n }(),
			},
			desiredSize:       "small",
			expectedGoalLen:   2,
			expectedAvailLen:  0,
			expectedPairLabel: "pair-1",
		},
		{
			name: "When nodes have no cluster label, they should be available nodes",
			nodes: []client.Object{
				func() client.Object { n := mkNode("n1", "", "pair-1", "small", false); return &n }(),
			},
			desiredSize:      "small",
			expectedGoalLen:  0,
			expectedAvailLen: 1,
		},
		{
			name: "When a node is being deleted, it should be skipped entirely",
			nodes: []client.Object{
				func() client.Object { n := mkNode("n1", hcKey, "pair-1", "small", true); return &n }(),
			},
			desiredSize:      "small",
			expectedGoalLen:  0,
			expectedAvailLen: 0,
		},
		{
			name: "When a cluster node has wrong size, it should not be a goal node",
			nodes: []client.Object{
				func() client.Object { n := mkNode("n1", hcKey, "pair-1", "medium", false); return &n }(),
			},
			desiredSize:       "small",
			expectedGoalLen:   0,
			expectedAvailLen:  0,
			expectedPairLabel: "pair-1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(test.nodes...).Build()
			r := &DedicatedServingComponentSchedulerAndSizer{Client: c}
			goalNodes, availableNodes, pairLabel, err := r.classifyDedicatedNodes(t.Context(), hc, test.desiredSize)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(goalNodes).To(HaveLen(test.expectedGoalLen))
			g.Expect(availableNodes).To(HaveLen(test.expectedAvailLen))
			g.Expect(pairLabel).To(Equal(test.expectedPairLabel))
		})
	}
}

func TestEnsurePairConfigMap(t *testing.T) {
	tests := []struct {
		name        string
		existing    []client.Object
		pairLabel   string
		hcNamespace string
		hcName      string
		expectError bool
	}{
		{
			name:        "When no configmap exists, it should create one",
			existing:    nil,
			pairLabel:   "pair-1",
			hcNamespace: "ns",
			hcName:      "hc1",
		},
		{
			name: "When configmap exists for the same cluster, it should succeed",
			existing: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: placeholderNamespace,
						Name:      "pair-1",
						Labels:    map[string]string{pairLabelKey: "pair-1"},
					},
					Data: map[string]string{
						clusterNamespaceKey: "ns",
						clusterNameKey:      "hc1",
					},
				},
			},
			pairLabel:   "pair-1",
			hcNamespace: "ns",
			hcName:      "hc1",
		},
		{
			name: "When configmap exists for a different cluster, it should return conflict error",
			existing: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: placeholderNamespace,
						Name:      "pair-1",
						Labels:    map[string]string{pairLabelKey: "pair-1"},
					},
					Data: map[string]string{
						clusterNamespaceKey: "other-ns",
						clusterNameKey:      "other-hc",
					},
				},
			},
			pairLabel:   "pair-1",
			hcNamespace: "ns",
			hcName:      "hc1",
			expectError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(test.existing...).Build()
			r := &DedicatedServingComponentSchedulerAndSizer{Client: c}
			err := r.ensurePairConfigMap(t.Context(), test.pairLabel, test.hcNamespace, test.hcName)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("conflict"))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				cm := &corev1.ConfigMap{}
				err = c.Get(t.Context(), types.NamespacedName{Namespace: placeholderNamespace, Name: test.pairLabel}, cm)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm.Data[clusterNamespaceKey]).To(Equal(test.hcNamespace))
				g.Expect(cm.Data[clusterNameKey]).To(Equal(test.hcName))
			}
		})
	}
}

func TestPairLabelFromConfigMaps(t *testing.T) {
	tests := []struct {
		name      string
		existing  []client.Object
		namespace string
		hcName    string
		expected  string
	}{
		{
			name:      "When no configmaps exist, it should return empty string",
			existing:  nil,
			namespace: "ns",
			hcName:    "hc1",
			expected:  "",
		},
		{
			name: "When a matching configmap exists, it should return the pair label",
			existing: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: placeholderNamespace,
						Name:      "pair-1",
						Labels:    map[string]string{pairLabelKey: "pair-1"},
					},
					Data: map[string]string{
						clusterNamespaceKey: "ns",
						clusterNameKey:      "hc1",
					},
				},
			},
			namespace: "ns",
			hcName:    "hc1",
			expected:  "pair-1",
		},
		{
			name: "When configmaps exist for other clusters only, it should return empty string",
			existing: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: placeholderNamespace,
						Name:      "pair-1",
						Labels:    map[string]string{pairLabelKey: "pair-1"},
					},
					Data: map[string]string{
						clusterNamespaceKey: "other-ns",
						clusterNameKey:      "other-hc",
					},
				},
			},
			namespace: "ns",
			hcName:    "hc1",
			expected:  "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(test.existing...).Build()
			r := &DedicatedServingComponentSchedulerAndSizer{Client: c}
			result, err := r.pairLabelFromConfigMaps(t.Context(), test.namespace, test.hcName)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(test.expected))
		})
	}
}

func TestDeletePairConfigMaps(t *testing.T) {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns",
			Name:      "hc1",
		},
	}

	tests := []struct {
		name              string
		existing          []client.Object
		expectedRemaining int
	}{
		{
			name:              "When there are no configmaps, it should succeed",
			existing:          nil,
			expectedRemaining: 0,
		},
		{
			name: "When configmaps match the cluster, they should be deleted",
			existing: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: placeholderNamespace,
						Name:      "pair-1",
						Labels:    map[string]string{pairLabelKey: "pair-1"},
					},
					Data: map[string]string{
						clusterNamespaceKey: "ns",
						clusterNameKey:      "hc1",
					},
				},
			},
			expectedRemaining: 0,
		},
		{
			name: "When configmaps belong to a different cluster, they should not be deleted",
			existing: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: placeholderNamespace,
						Name:      "pair-1",
						Labels:    map[string]string{pairLabelKey: "pair-1"},
					},
					Data: map[string]string{
						clusterNamespaceKey: "other-ns",
						clusterNameKey:      "other-hc",
					},
				},
			},
			expectedRemaining: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewWithT(t)
			c := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(test.existing...).Build()
			r := &DedicatedServingComponentSchedulerAndSizer{Client: c}
			err := r.deletePairConfigMaps(t.Context(), hc)
			g.Expect(err).ToNot(HaveOccurred())

			cmList := &corev1.ConfigMapList{}
			err = c.List(t.Context(), cmList, client.InNamespace(placeholderNamespace))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cmList.Items).To(HaveLen(test.expectedRemaining))
		})
	}
}
