package dump

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/pkg/manifests"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	kubeclient "k8s.io/client-go/kubernetes"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	// clusterAutoscalerSelector matches the cluster-autoscaler pods HyperShift
	// deploys in the hosted control plane namespace.
	clusterAutoscalerSelector = "app=cluster-autoscaler"
	// clusterAutoscalerStatusConfigMap is published by the autoscaler into the
	// data plane. It records, per node, whether the node is a scale-down
	// candidate and why it is not.
	clusterAutoscalerStatusConfigMap = "cluster-autoscaler-status"
	// criticalPodPriority is the lowest priority of the built-in
	// system-cluster-critical and system-node-critical priority classes.
	criticalPodPriority = int32(2000000000)
)

// DumpScaleDownDiagnostics collects the artifacts needed to explain why a NodePool
// did not converge to its expected node count: the cluster autoscaler's own logs and
// status, the inputs it bases scale-down decisions on (per node pod placement,
// requests, owners and priorities, PodDisruptionBudgets) and the replica state of the
// NodePools and MachineDeployments driving it.
//
// It is meant to run while the HostedCluster is still up, before teardown deletes the
// hosted control plane namespace.
func DumpScaleDownDiagnostics(ctx context.Context, artifactDir string, hc *hyperv1.HostedCluster, managementClient crclient.Client, managementKubeClient kubeclient.Interface, guestClient crclient.Client) error {
	if hc == nil {
		return fmt.Errorf("a HostedCluster is required to collect scale-down diagnostics")
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", artifactDir, err)
	}

	hcpNamespace := manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)
	var errs []error

	// The autoscaler logs. This is the only place that states why a node was not
	// selected for scale-down.
	if managementKubeClient != nil {
		errs = append(errs, dumpClusterAutoscalerLogs(ctx, managementKubeClient, hcpNamespace, artifactDir))
	}

	if guestClient != nil {
		errs = append(errs, dumpGuestScaleDownInputs(ctx, guestClient, artifactDir))
	}

	if managementClient != nil {
		errs = append(errs, dumpManagementScaleDownObjects(ctx, managementClient, hc, hcpNamespace, artifactDir))
	}

	return utilerrors.NewAggregate(errs)
}

func dumpClusterAutoscalerLogs(ctx context.Context, kubeClient kubeclient.Interface, hcpNamespace, artifactDir string) error {
	logsDir := filepath.Join(artifactDir, "cluster-autoscaler")
	if err := os.MkdirAll(logsDir, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", logsDir, err)
	}

	var errs []error
	pods, err := kubeClient.CoreV1().Pods(hcpNamespace).List(ctx, metav1.ListOptions{LabelSelector: clusterAutoscalerSelector})
	if err != nil {
		return fmt.Errorf("failed to list cluster-autoscaler pods in %s: %w", hcpNamespace, err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no cluster-autoscaler pods found in %s", hcpNamespace)
	}

	objects := make([]crclient.Object, 0, len(pods.Items))
	for i := range pods.Items {
		pod := &pods.Items[i]
		objects = append(objects, pod)
		if err := dumpPodContainerLogs(ctx, kubeClient, pod, logsDir); err != nil {
			errs = append(errs, err)
		}
	}

	if err := writeYAMLObjects(filepath.Join(artifactDir, "cluster-autoscaler-pods.yaml"), objects); err != nil {
		errs = append(errs, err)
	}

	deployment, err := kubeClient.AppsV1().Deployments(hcpNamespace).Get(ctx, "cluster-autoscaler", metav1.GetOptions{})
	if err != nil {
		errs = append(errs, fmt.Errorf("failed to get the cluster-autoscaler deployment in %s: %w", hcpNamespace, err))
	} else if err := writeYAMLObjects(filepath.Join(artifactDir, "cluster-autoscaler-deployment.yaml"), []crclient.Object{deployment}); err != nil {
		errs = append(errs, err)
	}
	return utilerrors.NewAggregate(errs)
}

// dumpPodContainerLogs writes the current and previous logs of every container of the
// given pod. Missing previous logs are expected and not reported.
func dumpPodContainerLogs(ctx context.Context, kubeClient kubeclient.Interface, pod *corev1.Pod, dir string) error {
	containers := make([]corev1.Container, 0, len(pod.Spec.InitContainers)+len(pod.Spec.Containers))
	containers = append(containers, pod.Spec.InitContainers...)
	containers = append(containers, pod.Spec.Containers...)

	var errs []error
	for _, container := range containers {
		for _, previous := range []bool{false, true} {
			logs, err := kubeClient.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{
				Container: container.Name,
				Previous:  previous,
			}).DoRaw(ctx)
			if err != nil {
				// A container without a previous instance is the common case and the
				// API reports it as a client error; anything else is unexpected.
				if previous && isMissingPreviousLogsError(err) {
					continue
				}
				errs = append(errs, fmt.Errorf("failed to get logs of %s/%s container %s: %w", pod.Namespace, pod.Name, container.Name, err))
				continue
			}
			name := fmt.Sprintf("%s-%s.log", pod.Name, container.Name)
			if previous {
				name = fmt.Sprintf("%s-%s-previous.log", pod.Name, container.Name)
			}
			if err := os.WriteFile(filepath.Join(dir, name), logs, 0o644); err != nil {
				errs = append(errs, fmt.Errorf("failed to write %s: %w", name, err))
			}
		}
	}
	return utilerrors.NewAggregate(errs)
}

// isMissingPreviousLogsError reports whether err is the API rejecting a previous log
// request because the container has no previous instance, which is expected.
func isMissingPreviousLogsError(err error) bool {
	if apierrors.IsBadRequest(err) || apierrors.IsNotFound(err) {
		return true
	}
	return strings.Contains(err.Error(), "previous terminated container")
}

// dumpGuestScaleDownInputs captures the data plane state the autoscaler reads when
// deciding whether a node can be removed.
func dumpGuestScaleDownInputs(ctx context.Context, guestClient crclient.Client, artifactDir string) error {
	var errs []error

	nodes := &corev1.NodeList{}
	nodesAvailable := true
	if err := guestClient.List(ctx, nodes); err != nil {
		nodesAvailable = false
		errs = append(errs, fmt.Errorf("failed to list nodes: %w", err))
	} else {
		nodeObjects := make([]crclient.Object, 0, len(nodes.Items))
		for i := range nodes.Items {
			nodeObjects = append(nodeObjects, &nodes.Items[i])
		}
		if err := writeYAMLObjects(filepath.Join(artifactDir, "nodes.yaml"), nodeObjects); err != nil {
			errs = append(errs, err)
		}
	}

	pods := &corev1.PodList{}
	podsAvailable := true
	if err := guestClient.List(ctx, pods); err != nil {
		podsAvailable = false
		errs = append(errs, fmt.Errorf("failed to list pods: %w", err))
	} else {
		podObjects := make([]crclient.Object, 0, len(pods.Items))
		for i := range pods.Items {
			podObjects = append(podObjects, &pods.Items[i])
		}
		if err := writeYAMLObjects(filepath.Join(artifactDir, "pods.yaml"), podObjects); err != nil {
			errs = append(errs, err)
		}
	}

	// The placement report needs both lists; the remaining artifacts do not.
	if nodesAvailable && podsAvailable {
		placement := renderPodPlacement(nodes.Items, pods.Items)
		if err := os.WriteFile(filepath.Join(artifactDir, "pods-by-node.txt"), []byte(placement), 0o644); err != nil {
			errs = append(errs, fmt.Errorf("failed to write pods-by-node.txt: %w", err))
		}
	}

	status := &corev1.ConfigMap{}
	err := guestClient.Get(ctx, crclient.ObjectKey{Namespace: "kube-system", Name: clusterAutoscalerStatusConfigMap}, status)
	switch {
	case err == nil:
		if err := writeYAMLObjects(filepath.Join(artifactDir, "cluster-autoscaler-status.yaml"), []crclient.Object{status}); err != nil {
			errs = append(errs, err)
		}
	case apierrors.IsNotFound(err):
		// Not an error: the ConfigMap is written by the autoscaler into the data
		// plane and may be absent while the autoscaler is not leader.
	default:
		errs = append(errs, fmt.Errorf("failed to get %s/%s: %w", clusterAutoscalerStatusConfigMap, clusterAutoscalerStatusConfigMap, err))
	}

	pdbs := &policyv1.PodDisruptionBudgetList{}
	if err := guestClient.List(ctx, pdbs); err != nil {
		errs = append(errs, fmt.Errorf("failed to list pod disruption budgets: %w", err))
	} else {
		pdbObjects := make([]crclient.Object, 0, len(pdbs.Items))
		for i := range pdbs.Items {
			pdbObjects = append(pdbObjects, &pdbs.Items[i])
		}
		if err := writeYAMLObjects(filepath.Join(artifactDir, "poddisruptionbudgets.yaml"), pdbObjects); err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

// dumpManagementScaleDownObjects captures the replica and condition state of the
// resources that drive the node count.
func dumpManagementScaleDownObjects(ctx context.Context, managementClient crclient.Client, hc *hyperv1.HostedCluster, hcpNamespace, artifactDir string) error {
	var errs []error

	nodePools := &hyperv1.NodePoolList{}
	if err := managementClient.List(ctx, nodePools, crclient.InNamespace(hc.Namespace)); err != nil {
		errs = append(errs, fmt.Errorf("failed to list NodePools: %w", err))
	} else {
		objects := make([]crclient.Object, 0, len(nodePools.Items))
		for i := range nodePools.Items {
			objects = append(objects, &nodePools.Items[i])
		}
		if err := writeYAMLObjects(filepath.Join(artifactDir, "nodepools.yaml"), objects); err != nil {
			errs = append(errs, err)
		}
	}

	machineDeployments := &capiv1.MachineDeploymentList{}
	if err := managementClient.List(ctx, machineDeployments, crclient.InNamespace(hcpNamespace)); err != nil {
		errs = append(errs, fmt.Errorf("failed to list MachineDeployments: %w", err))
	} else {
		objects := make([]crclient.Object, 0, len(machineDeployments.Items))
		for i := range machineDeployments.Items {
			objects = append(objects, &machineDeployments.Items[i])
		}
		if err := writeYAMLObjects(filepath.Join(artifactDir, "machinedeployments.yaml"), objects); err != nil {
			errs = append(errs, err)
		}
	}

	machineSets := &capiv1.MachineSetList{}
	if err := managementClient.List(ctx, machineSets, crclient.InNamespace(hcpNamespace)); err != nil {
		errs = append(errs, fmt.Errorf("failed to list MachineSets: %w", err))
	} else {
		objects := make([]crclient.Object, 0, len(machineSets.Items))
		for i := range machineSets.Items {
			objects = append(objects, &machineSets.Items[i])
		}
		if err := writeYAMLObjects(filepath.Join(artifactDir, "machinesets.yaml"), objects); err != nil {
			errs = append(errs, err)
		}
	}

	machines := &capiv1.MachineList{}
	if err := managementClient.List(ctx, machines, crclient.InNamespace(hcpNamespace)); err != nil {
		errs = append(errs, fmt.Errorf("failed to list Machines: %w", err))
	} else {
		objects := make([]crclient.Object, 0, len(machines.Items))
		for i := range machines.Items {
			objects = append(objects, &machines.Items[i])
		}
		if err := writeYAMLObjects(filepath.Join(artifactDir, "machines.yaml"), objects); err != nil {
			errs = append(errs, err)
		}
	}

	return utilerrors.NewAggregate(errs)
}

func writeYAMLObjects(path string, objects []crclient.Object) error {
	var buf bytes.Buffer
	for _, object := range objects {
		content, err := yaml.Marshal(object)
		if err != nil {
			return fmt.Errorf("failed to marshal %s: %w", object.GetName(), err)
		}
		buf.WriteString("---\n")
		buf.Write(content)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// renderPodPlacement renders the node and pod state that determines whether the
// cluster autoscaler considers a node a scale-down candidate: per node requests
// against allocatable, the pods that are not DaemonSet or mirror pods, the pods in
// kube-system (which block scale-down unless a PodDisruptionBudget allows eviction)
// and the pods that are not scheduled anywhere.
func renderPodPlacement(nodes []corev1.Node, pods []corev1.Pod) string {
	podsByNode := map[string][]corev1.Pod{}
	var pending []corev1.Pod
	for _, pod := range pods {
		if pod.Spec.NodeName == "" {
			if pod.Status.Phase != corev1.PodSucceeded {
				pending = append(pending, pod)
			}
			continue
		}
		podsByNode[pod.Spec.NodeName] = append(podsByNode[pod.Spec.NodeName], pod)
	}

	sortedNodes := make([]corev1.Node, len(nodes))
	copy(sortedNodes, nodes)
	sort.Slice(sortedNodes, func(i, j int) bool { return sortedNodes[i].Name < sortedNodes[j].Name })

	var out strings.Builder
	for i := range sortedNodes {
		node := &sortedNodes[i]
		nodePods := podsByNode[node.Name]
		sort.Slice(nodePods, func(i, j int) bool {
			return nodePods[i].Namespace+"/"+nodePods[i].Name < nodePods[j].Namespace+"/"+nodePods[j].Name
		})

		allocatableCPU := node.Status.Allocatable.Cpu()
		allocatableMemory := node.Status.Allocatable.Memory()
		requestedCPU := resource.Quantity{}
		requestedMemory := resource.Quantity{}
		nonDaemonSetPods := 0
		kubeSystemBlockingPods := 0

		for j := range nodePods {
			pod := &nodePods[j]
			cpu, memory := podRequests(pod)
			requestedCPU.Add(cpu)
			requestedMemory.Add(memory)

			daemonSet := isDaemonSetPod(pod)
			mirror := isMirrorPod(pod)
			if !daemonSet && !mirror {
				nonDaemonSetPods++
				if pod.Namespace == "kube-system" {
					kubeSystemBlockingPods++
				}
			}
		}

		fmt.Fprintf(&out, "node %s unschedulable=%t ready=%s\n", node.Name, node.Spec.Unschedulable, nodeReady(node))
		fmt.Fprintf(&out, "  allocatable cpu=%s memory=%s\n", allocatableCPU.String(), allocatableMemory.String())
		fmt.Fprintf(&out, "  requested cpu=%s (%s) memory=%s (%s) pods=%d nonDaemonSetPods=%d kubeSystemBlockingPods=%d\n",
			requestedCPU.String(), percentage(&requestedCPU, allocatableCPU),
			requestedMemory.String(), percentage(&requestedMemory, allocatableMemory),
			len(nodePods), nonDaemonSetPods, kubeSystemBlockingPods)
		for j := range nodePods {
			fmt.Fprintf(&out, "  %s\n", renderPod(&nodePods[j]))
		}
		out.WriteString("\n")
	}

	fmt.Fprintf(&out, "pending (not scheduled) pods: %d\n", len(pending))
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].Namespace+"/"+pending[i].Name < pending[j].Namespace+"/"+pending[j].Name
	})
	for j := range pending {
		fmt.Fprintf(&out, "  %s\n", renderPod(&pending[j]))
	}

	return out.String()
}

func renderPod(pod *corev1.Pod) string {
	cpu, memory := podRequests(pod)
	owner := "none"
	for _, reference := range pod.OwnerReferences {
		owner = reference.Kind + "/" + reference.Name
		break
	}
	priority := int32(0)
	if pod.Spec.Priority != nil {
		priority = *pod.Spec.Priority
	}
	return fmt.Sprintf("%s/%s phase=%s ds=%t mirror=%t owner=%s priority=%d critical=%t requests=cpu=%s,memory=%s",
		pod.Namespace, pod.Name, pod.Status.Phase, isDaemonSetPod(pod), isMirrorPod(pod), owner, priority, priority >= criticalPodPriority,
		cpu.String(), memory.String())
}

// podRequests approximates the node resources reserved for a pod: the sum of the
// application container requests.
func podRequests(pod *corev1.Pod) (resource.Quantity, resource.Quantity) {
	cpu := resource.Quantity{}
	memory := resource.Quantity{}
	for _, container := range pod.Spec.Containers {
		cpu.Add(*container.Resources.Requests.Cpu())
		memory.Add(*container.Resources.Requests.Memory())
	}
	return cpu, memory
}

func isDaemonSetPod(pod *corev1.Pod) bool {
	for _, reference := range pod.OwnerReferences {
		if reference.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

func isMirrorPod(pod *corev1.Pod) bool {
	_, ok := pod.Annotations[corev1.MirrorPodAnnotationKey]
	return ok
}

func nodeReady(node *corev1.Node) string {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return string(condition.Status)
		}
	}
	return "Unknown"
}

func percentage(requested, allocatable *resource.Quantity) string {
	if allocatable == nil || allocatable.IsZero() {
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", float64(requested.MilliValue())/float64(allocatable.MilliValue())*100)
}
