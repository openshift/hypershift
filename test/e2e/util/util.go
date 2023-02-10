package util

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/google/go-cmp/cmp"
	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	routev1 "github.com/openshift/api/route/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/util"
	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	promconfig "github.com/prometheus/common/config"
	prommodel "github.com/prometheus/common/model"
	"go.uber.org/zap/zaptest"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// DeleteNamespace deletes and finalizes the given namespace, logging any failures
// along the way.
func DeleteNamespace(t *testing.T, ctx context.Context, client crclient.Client, namespace string) error {
	t.Logf("Deleting namespace: %s", namespace)
	err := wait.PollImmediateWithContext(ctx, 10*time.Second, 20*time.Minute, func(ctx context.Context) (done bool, err error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		if err := client.Delete(ctx, ns, &crclient.DeleteOptions{}); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			t.Logf("Failed to delete namespace: %s, will retry: %v", namespace, err)
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("failed to delete namespace: %w", err)
	}

	t.Logf("Waiting for namespace to be finalized. Namespace: %s", namespace)
	err = wait.PollImmediateWithContext(ctx, 10*time.Second, 20*time.Minute, func(ctx context.Context) (done bool, err error) {
		ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(ns), ns); err != nil {
			if errors.IsNotFound(err) {
				return true, nil
			}
			t.Logf("Failed to get namespace: %s. %v", namespace, err)
			return false, nil
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("namespace still exists after deletion timeout: %v", err)
	}
	t.Logf("Deleted namespace: %s", namespace)
	return nil
}

func WaitForGuestKubeConfig(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) ([]byte, error) {
	start := time.Now()
	t.Logf("Waiting for hostedcluster kubeconfig to be published. Namespace: %s, name: %s", hostedCluster.Namespace, hostedCluster.Name)
	var guestKubeConfigSecret corev1.Secret
	err := wait.PollImmediateWithContext(ctx, 1*time.Second, 30*time.Minute, func(ctx context.Context) (done bool, err error) {
		if err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster); err != nil {
			return false, nil
		}
		if hostedCluster.Status.KubeConfig == nil {
			return false, nil
		}
		key := crclient.ObjectKey{
			Namespace: hostedCluster.Namespace,
			Name:      hostedCluster.Status.KubeConfig.Name,
		}
		if err := client.Get(ctx, key, &guestKubeConfigSecret); err != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("kubeconfig didn't become available: %w", err)
	}
	t.Logf("Found kubeconfig for cluster in %s. Namespace: %s, name: %s", time.Since(start).Round(time.Second), hostedCluster.Namespace, hostedCluster.Name)

	// TODO: this key should probably be published or an API constant
	data, hasData := guestKubeConfigSecret.Data["kubeconfig"]
	if !hasData {
		return nil, fmt.Errorf("kubeconfig secret is missing kubeconfig key")
	}
	return data, nil
}

func WaitForGuestClient(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) crclient.Client {
	g := NewWithT(t)
	start := time.Now()

	guestKubeConfigSecretData, err := WaitForGuestKubeConfig(t, ctx, client, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "couldn't get kubeconfig")

	guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
	g.Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")

	t.Logf("Waiting for a successful connection to the guest apiserver")
	var guestClient crclient.Client
	waitForGuestClientCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	// SOA TTL is 60s. If DNS lookup fails on the api-* name, it is unlikely to succeed in less than 60s.
	err = wait.PollImmediateWithContext(waitForGuestClientCtx, 35*time.Second, 30*time.Minute, func(ctx context.Context) (done bool, err error) {
		kubeClient, err := crclient.New(guestConfig, crclient.Options{Scheme: scheme})
		if err != nil {
			t.Logf("attempt to connect failed: %s", err)
			return false, nil
		}
		guestClient = kubeClient
		return true, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to establish a connection to the guest apiserver")

	t.Logf("Successfully connected to the guest apiserver in %s", time.Since(start).Round(time.Second))
	return guestClient
}

func WaitForNReadyNodes(t *testing.T, ctx context.Context, client crclient.Client, n int32, platform hyperv1.PlatformType) []corev1.Node {
	g := NewWithT(t)
	start := time.Now()

	// waitTimeout for nodes to become Ready
	waitTimeout := 30 * time.Minute
	switch platform {
	case hyperv1.PowerVSPlatform:
		waitTimeout = 60 * time.Minute
	}

	t.Logf("Waiting for nodes to become ready. Want: %v", n)
	nodes := &corev1.NodeList{}
	readyNodeCount := 0
	err := wait.PollImmediateWithContext(ctx, 5*time.Second, waitTimeout, func(ctx context.Context) (done bool, err error) {
		err = client.List(ctx, nodes)
		if err != nil {
			return false, nil
		}
		if len(nodes.Items) == 0 {
			return false, nil
		}
		var readyNodes []string
		for _, node := range nodes.Items {
			for _, cond := range node.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					readyNodes = append(readyNodes, node.Name)
				}
			}
		}
		if len(readyNodes) != int(n) {
			readyNodeCount = len(readyNodes)
			return false, nil
		}
		t.Logf("All nodes are ready. Count: %v", len(nodes.Items))

		return true, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed to ensure guest nodes became ready, ready: (%d/%d): ", readyNodeCount, n))

	t.Logf("All nodes for nodepool appear to be ready in %s. Count: %v", time.Since(start).Round(time.Second), n)

	return nodes.Items
}

func WaitForNReadyNodesByNodePool(t *testing.T, ctx context.Context, client crclient.Client, n int32, platform hyperv1.PlatformType, nodePoolName string) []corev1.Node {
	g := NewWithT(t)
	start := time.Now()

	// waitTimeout for nodes to become Ready
	waitTimeout := 30 * time.Minute
	switch platform {
	case hyperv1.PowerVSPlatform:
		waitTimeout = 60 * time.Minute
	}

	t.Logf("Waiting for nodes to become ready by NodePool. NodePool: %s Want: %v", nodePoolName, n)
	nodesFromNodePool := []corev1.Node{}
	err := wait.PollImmediateWithContext(ctx, 5*time.Second, waitTimeout, func(ctx context.Context) (done bool, err error) {
		nodes := &corev1.NodeList{}
		err = client.List(ctx, nodes)
		if err != nil {
			return false, nil
		}
		if len(nodes.Items) == 0 {
			return false, nil
		}
		for _, node := range nodes.Items {
			if node.Labels["hypershift.openshift.io/nodePool"] == nodePoolName {
				for _, cond := range node.Status.Conditions {
					if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
						nodesFromNodePool = append(nodesFromNodePool, node)
					}
				}
			}
		}
		if len(nodesFromNodePool) != int(n) {
			nodesFromNodePool = nil
			return false, nil
		}
		t.Logf("All nodes are ready. Count: %v", len(nodesFromNodePool))

		return true, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed to ensure guest nodes became ready, ready: (%d/%d): ", len(nodesFromNodePool), n))
	t.Logf("All nodes for NodePool %s appear to be ready in %s. Count: %v", nodePoolName, time.Since(start).Round(time.Second), n)

	return nodesFromNodePool
}

func WaitForImageRollout(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, image string) {
	start := time.Now()
	g := NewWithT(t)

	var rolloutIncompleteReason string
	t.Logf("Waiting for hostedcluster to rollout image. Namespace: %s, name: %s, image: %s", hostedCluster.Namespace, hostedCluster.Name, image)
	err := wait.PollImmediateWithContext(ctx, 10*time.Second, 30*time.Minute, func(ctx context.Context) (done bool, err error) {
		latest := hostedCluster.DeepCopy()
		err = client.Get(ctx, crclient.ObjectKeyFromObject(latest), latest)
		if err != nil {
			t.Errorf("Failed to get hostedcluster: %v", err)
			return false, nil
		}

		available := meta.FindStatusCondition(latest.Status.Conditions, string(hyperv1.HostedClusterAvailable))
		progressing := meta.FindStatusCondition(latest.Status.Conditions, string(hyperv1.HostedClusterProgressing))
		switch {
		case available == nil:
			rolloutIncompleteReason = fmt.Sprintf("status.conditions[type==%s] does not exist", hyperv1.HostedClusterAvailable)
		case available.Status != metav1.ConditionTrue:
			rolloutIncompleteReason = fmt.Sprintf("status.conditions[type==%s] %q (expected %q): %s %s: %s", available.Type, available.Status, metav1.ConditionTrue, available.LastTransitionTime, available.Reason, available.Message)
		case progressing == nil:
			rolloutIncompleteReason = fmt.Sprintf("status.conditions[type==%s] does not exist", hyperv1.HostedClusterProgressing)
		case progressing.Status != metav1.ConditionFalse:
			rolloutIncompleteReason = fmt.Sprintf("status.conditions[type==%s] %q (expected %q): %s %s: %s", progressing.Type, progressing.Status, metav1.ConditionFalse, progressing.LastTransitionTime, progressing.Reason, progressing.Message)
		case latest.Status.Version == nil:
			rolloutIncompleteReason = "nil status.version"
		case latest.Status.Version.Desired.Image != image:
			rolloutIncompleteReason = fmt.Sprintf("status.version.desired.image is %q, but we want %q", latest.Status.Version.Desired.Image, image)
		case len(latest.Status.Version.History) == 0:
			rolloutIncompleteReason = "status.version.history has no entries"
		case latest.Status.Version.History[0].Image != latest.Status.Version.Desired.Image:
			rolloutIncompleteReason = fmt.Sprintf("status.version.history[0].image is %q, but we want %q", latest.Status.Version.History[0].Image, latest.Status.Version.Desired.Image)
		case latest.Status.Version.History[0].State != configv1.CompletedUpdate:
			rolloutIncompleteReason = fmt.Sprintf("status.version.history[0].state is %q, but we want %q", latest.Status.Version.History[0].State, configv1.CompletedUpdate)
		default:
			rolloutIncompleteReason = ""
		}

		if rolloutIncompleteReason != "" {
			t.Logf("Waiting for hostedcluster rollout. Image: %s: %s", image, rolloutIncompleteReason)
			return false, nil
		}
		return true, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed waiting for hostedcluster image rollout: %s", rolloutIncompleteReason))

	t.Logf("Observed hostedcluster to have successfully rolled out image in %s. Namespace: %s, name: %s, image: %s", time.Since(start).Round(time.Second), hostedCluster.Namespace, hostedCluster.Name, image)
}

func WaitForConditionsOnHostedControlPlane(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, image string) {
	g := NewWithT(t)
	start := time.Now()
	conditions := []hyperv1.ConditionType{
		hyperv1.HostedControlPlaneAvailable,
		hyperv1.EtcdAvailable,
		hyperv1.KubeAPIServerAvailable,
		hyperv1.InfrastructureReady,
		hyperv1.ValidHostedControlPlaneConfiguration,
	}
	var rolloutIncompleteReasons []string
	namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

	t.Logf("Waiting for hostedcontrolplane to rollout image. Namespace: %s, name: %s, image: %s", namespace, hostedCluster.Name, image)
	err := wait.PollImmediateWithContext(ctx, 10*time.Second, 30*time.Minute, func(ctx context.Context) (done bool, err error) {
		cp := &hyperv1.HostedControlPlane{}
		err = client.Get(ctx, types.NamespacedName{Namespace: namespace, Name: hostedCluster.Name}, cp)
		if err != nil {
			t.Logf("Failed to get hostedcontrolplane: %v", err)
			return false, nil
		}

		rolloutIncompleteReasons = make([]string, 0, len(conditions))
		for _, conditionType := range conditions {
			condition := meta.FindStatusCondition(cp.Status.Conditions, string(conditionType))
			switch {
			case condition == nil:
				rolloutIncompleteReasons = append(rolloutIncompleteReasons, fmt.Sprintf("status.conditions[type==%s] does not exist", conditionType))
			case condition.Status != metav1.ConditionTrue:
				rolloutIncompleteReasons = append(rolloutIncompleteReasons, fmt.Sprintf("status.conditions[type==%s] %q (expected %q): %s %s: %s", condition.Type, condition.Status, metav1.ConditionTrue, condition.LastTransitionTime, condition.Reason, condition.Message))
			}
		}

		if len(rolloutIncompleteReasons) > 0 {
			t.Logf("Waiting for hostedcontrolplane rollout. Image: %s\n%s", image, strings.Join(rolloutIncompleteReasons, "\n"))
			return false, nil
		}
		return true, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed waiting for hostedcontrolplane image rollout\n%s", strings.Join(rolloutIncompleteReasons, "\n")))

	t.Logf("Observed hostedcontrolplane to have successfully rolled out image in %s. Namespace: %s, name: %s, image: %s", time.Since(start).Round(time.Second), namespace, hostedCluster.Name, image)
}

// WaitForNodePoolVersion blocks until the NodePool status indicates the given
// version. If the context is closed before the version is observed, the given
// test will get an error.
func WaitForNodePoolVersion(t *testing.T, ctx context.Context, client crclient.Client, nodePool *hyperv1.NodePool, version string) {
	g := NewWithT(t)
	start := time.Now()

	t.Logf("Waiting for nodepool %s/%s to report version %s (currently %s)", nodePool.Namespace, nodePool.Name, version, nodePool.Status.Version)
	// TestInPlaceUpgradeNodePool must update nodes in the pool squentially and it takes about 5m per node
	// TestInPlaceUpgradeNodePool currently uses a single nodepool with 2 replicas so 20m should be enough time (2x expected)
	err := wait.PollImmediateWithContext(ctx, 10*time.Second, 20*time.Minute, func(ctx context.Context) (done bool, err error) {
		latest := nodePool.DeepCopy()
		err = client.Get(ctx, crclient.ObjectKeyFromObject(nodePool), latest)
		if err != nil {
			t.Logf("Failed to get nodepool: %v", err)
			return false, nil
		}
		return latest.Status.Version == version, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed waiting for nodepool version")

	t.Logf("Observed nodepool %s/%s to report version %s in %s", nodePool.Namespace, nodePool.Name, version, time.Since(start).Round(time.Second))
}

func EnsureNoCrashingPods(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureNoCrashingPods", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

		var podList corev1.PodList
		if err := client.List(ctx, &podList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list pods in namespace %s: %v", namespace, err)
		}
		for _, pod := range podList.Items {
			// TODO: Remove this when https://issues.redhat.com/browse/OCPBUGS-6953 is resolved
			if strings.HasPrefix(pod.Name, "ovnkube-master-") {
				continue
			}

			// TODO: Figure out why Route kind does not exist when ingress-operator first starts
			if strings.HasPrefix(pod.Name, "ingress-operator-") {
				continue
			}

			// TODO: Figure out why default-http backend health check is failing and triggering liveness probe to restart
			if strings.HasPrefix(pod.Name, "router-") {
				continue
			}

			// TODO: Autoscaler is restarting because it times out accessing the kube apiserver for leader election.
			// Investigate a fix.
			if strings.HasPrefix(pod.Name, "cluster-autoscaler") {
				continue
			}

			// TODO: Machine approver started restarting in the UpgradeControlPlane test with the following error:
			// F1122 15:17:01.880727       1 main.go:144] Can't create clients: failed to create client: Unauthorized
			// Investigate a fix.
			if strings.HasPrefix(pod.Name, "machine-approver") {
				continue
			}

			// TODO: 4.11 and later, FBC based catalogs can in excess of 150s to start
			// https://github.com/openshift/hypershift/pull/1746
			// https://github.com/operator-framework/operator-lifecycle-manager/pull/2791
			// Investigate a fix.
			if strings.Contains(pod.Name, "-catalog") {
				continue
			}

			for _, containerStatus := range pod.Status.ContainerStatuses {
				if containerStatus.RestartCount > 0 {
					t.Errorf("Container %s in pod %s has a restartCount > 0 (%d)", containerStatus.Name, pod.Name, containerStatus.RestartCount)
				}
			}
		}
	})
}

func NoticePreemptionOrFailedScheduling(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("NoticePreemptionOrFailedScheduling", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

		var eventList corev1.EventList
		if err := client.List(ctx, &eventList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list events in namespace %s: %v", namespace, err)
		}
		for _, event := range eventList.Items {
			if event.Reason == "FailedScheduling" || event.Reason == "Preempted" {
				// "error: " is to trigger prow syntax highlight in prow
				t.Logf("error: non-fatal, observed FailedScheduling or Preempted event: %s", event.Message)
			}
		}
	})
}

func EnsureNoPodsWithTooHighPriority(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	// Priority of the etcd priority class, nothing should ever exceed this.
	const maxAllowedPriority = 100002000
	t.Run("EnsureNoPodsWithTooHighPriority", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

		var podList corev1.PodList
		if err := client.List(ctx, &podList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list pods in namespace %s: %v", namespace, err)
		}
		for _, pod := range podList.Items {
			// Bandaid until this is fixed in the CNO
			if strings.HasPrefix(pod.Name, "multus-admission-controller") {
				continue
			}
			if pod.Spec.Priority != nil && *pod.Spec.Priority > maxAllowedPriority {
				t.Errorf("pod %s with priorityClassName %s has a priority of %d with exceeds the max allowed of %d", pod.Name, pod.Spec.PriorityClassName, *pod.Spec.Priority, maxAllowedPriority)
			}
		}
	})
}

func EnsureAllContainersHavePullPolicyIfNotPresent(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureAllContainersHavePullPolicyIfNotPresent", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name

		var podList corev1.PodList
		if err := client.List(ctx, &podList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list pods in namespace %s: %v", namespace, err)
		}
		for _, pod := range podList.Items {
			for _, initContainer := range pod.Spec.InitContainers {
				if initContainer.ImagePullPolicy != corev1.PullIfNotPresent {
					t.Errorf("container %s in pod %s has doesn't have imagePullPolicy %s but %s", initContainer.Name, pod.Name, corev1.PullIfNotPresent, initContainer.ImagePullPolicy)
				}
			}
			for _, container := range pod.Spec.Containers {
				if container.ImagePullPolicy != corev1.PullIfNotPresent {
					t.Errorf("container %s in pod %s has doesn't have imagePullPolicy %s but %s", container.Name, pod.Name, corev1.PullIfNotPresent, container.ImagePullPolicy)
				}
			}
		}
	})
}

func EnsureNodeCountMatchesNodePoolReplicas(t *testing.T, ctx context.Context, hostClient, guestClient crclient.Client, nodePoolNamespace string) {
	t.Run("EnsureNodeCountMatchesNodePoolReplicas", func(t *testing.T) {
		var nodePoolList hyperv1.NodePoolList
		if err := hostClient.List(ctx, &nodePoolList, &crclient.ListOptions{Namespace: nodePoolNamespace}); err != nil {
			t.Fatalf("failed to list nodepools: %v", err)
		}
		replicas := 0
		for _, nodePool := range nodePoolList.Items {
			replicas = replicas + int(*nodePool.Spec.Replicas)
		}

		var nodes corev1.NodeList
		if err := guestClient.List(ctx, &nodes); err != nil {
			t.Fatalf("failed to list nodes in guest cluster: %v", err)
		}

		if replicas != len(nodes.Items) {
			t.Errorf("nodepool replicas %d does not match number of nodes in cluster %d", replicas, len(nodes.Items))
		}
	})
}

func EnsureMachineDeploymentGeneration(t *testing.T, ctx context.Context, hostClient crclient.Client, hostedCluster *hyperv1.HostedCluster, expectedGeneration int64) {
	t.Run("EnsureMachineDeploymentGeneration", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name
		var machineDeploymentList capiv1.MachineDeploymentList
		if err := hostClient.List(ctx, &machineDeploymentList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list machinedeployments: %v", err)
		}
		for _, machineDeployment := range machineDeploymentList.Items {
			if machineDeployment.Generation != expectedGeneration {
				t.Errorf("machineDeployment %s does not have expected generation %d but %d", crclient.ObjectKeyFromObject(&machineDeployment), expectedGeneration, machineDeployment.Generation)
			}
		}
	})
}

func EnsureAPIBudget(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureAPIBudget", func(t *testing.T) {

		// Get hypershift-operator token
		operatorServiceAccount := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "operator",
				Namespace: "hypershift",
			},
		}
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(operatorServiceAccount), operatorServiceAccount); err != nil {
			t.Fatalf("failed to get hypershift operator service account: %v", err)
		}
		var secretName string
		for _, secret := range operatorServiceAccount.Secrets {
			if strings.HasPrefix(secret.Name, "operator-token-") {
				secretName = secret.Name
				break
			}
		}

		token, err := getPrometheusToken(ctx, secretName, client)
		if err != nil {
			t.Fatalf("can't get token for Prometheus; %v", err)
		}

		// Get thanos-querier endpoint
		promRoute := &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "thanos-querier",
				Namespace: "openshift-monitoring",
			},
		}
		if err = client.Get(ctx, crclient.ObjectKeyFromObject(promRoute), promRoute); err != nil {
			t.Skip("unable to get prometheus route, skipping")
		}
		if len(promRoute.Status.Ingress) == 0 {
			t.Skip("unable to get prometheus ingress, skipping")
		}
		promEndpoint := fmt.Sprintf("https://%s", promRoute.Status.Ingress[0].Host)

		// Create prometheus client
		cfg := promconfig.HTTPClientConfig{
			Authorization: &promconfig.Authorization{
				Type:        "Bearer",
				Credentials: promconfig.Secret(token),
			},
			TLSConfig: promconfig.TLSConfig{
				InsecureSkipVerify: true,
			},
		}
		rt, err := promconfig.NewRoundTripperFromConfig(cfg, "e2e-budget-checker")
		if err != nil {
			t.Fatalf("failed to get create round tripper: %v", err)
		}
		promClient, err := promapi.NewClient(promapi.Config{
			Address:      promEndpoint,
			RoundTripper: rt,
		})
		if err != nil {
			t.Fatalf("failed to get create prometheus client: %v", err)
		}
		v1api := promv1.NewAPI(promClient)

		// Compare metrics against budgets
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name
		clusterAgeMinutes := int32(time.Since(hostedCluster.CreationTimestamp.Time).Round(time.Minute).Minutes())
		budgets := []struct {
			name   string
			query  string
			budget float64
		}{
			{
				name:   "control-plane-operator read",
				query:  fmt.Sprintf(`sum by (pod) (max_over_time(hypershift:controlplane:component_api_requests_total{app="control-plane-operator", method="GET", namespace=~"%s"}[%dm]))`, namespace, clusterAgeMinutes),
				budget: 3000,
			},
			{
				name:   "control-plane-operator mutate",
				query:  fmt.Sprintf(`sum by (pod) (max_over_time(hypershift:controlplane:component_api_requests_total{app="control-plane-operator", method!="GET", namespace=~"%s"}[%dm]))`, namespace, clusterAgeMinutes),
				budget: 3000,
			},
			{
				name:   "control-plane-operator no 404 deletes",
				query:  fmt.Sprintf(`sum by (pod) (max_over_time(hypershift:controlplane:component_api_requests_total{app="control-plane-operator", method="DELETE", code="404", namespace=~"%s"}[%dm]))`, namespace, clusterAgeMinutes),
				budget: 50,
			},
			//{
			//	name:   "ignition-server p90 payload generation time",
			//	query:  fmt.Sprintf(`sum by (namespace) (max_over_time(hypershift:controlplane:ign_payload_generation_seconds_p90{namespace="%s"}[%dm]))`, namespace, clusterAgeMinutes),
			//	budget: 45,
			//},
			// hypershift-operator budget can not be per HC so metric will be
			// significantly under budget for all but the last test(s) to complete on
			// a particular test cluster These budgets will also need to scale up with
			// additional tests that create HostedClusters
			{
				name:   "hypershift-operator read",
				query:  `sum(hypershift:operator:component_api_requests_total{method="GET"})`,
				budget: 5000,
			},
			{
				name:   "hypershift-operator mutate",
				query:  `sum(hypershift:operator:component_api_requests_total{method!="GET"})`,
				budget: 20000,
			},
			{
				name:   "hypershift-operator no 404 deletes",
				query:  `sum(hypershift:operator:component_api_requests_total{method="DELETE", code="404"})`,
				budget: 50,
			},
		}

		for _, budget := range budgets {
			t.Run(budget.name, func(t *testing.T) {
				result, _, err := v1api.Query(ctx, budget.query, time.Now())
				if err != nil {
					t.Fatalf("failed to query prometheus: %v", err)
				}
				vector, ok := result.(prommodel.Vector)
				if !ok {
					t.Fatal("expected vector result")
				}
				if len(vector) == 0 {
					if budget.budget <= 50 {
						t.Log("no samples returned for query with small budget, skipping check")
					} else {
						t.Errorf("no samples returned for query with large budget, failed check")
					}
				}
				for _, sample := range vector {
					podMsg := ""
					if podName, ok := sample.Metric["pod"]; ok {
						podMsg = fmt.Sprintf("pod %s ", podName)
					}
					if float64(sample.Value) > budget.budget {
						t.Errorf("%sover budget: budget: %.0f, actual: %.0f", podMsg, budget.budget, sample.Value)
					} else {
						t.Logf("%swithin budget: budget: %.0f, actual: %.0f", podMsg, budget.budget, sample.Value)
					}
				}
			})
		}
	})
}

func EnsureAllRoutesUseHCPRouter(t *testing.T, ctx context.Context, hostClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureAllRoutesUseHCPRouter", func(t *testing.T) {
		for _, svc := range hostedCluster.Spec.Services {
			if svc.Service == hyperv1.APIServer && svc.Type != hyperv1.Route {
				t.Skip("skipping test because APIServer is not exposed through a route")
			}
		}
		var routes routev1.RouteList
		if err := hostClient.List(ctx, &routes, crclient.InNamespace(manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name)); err != nil {
			t.Fatalf("failed to list routes: %v", err)
		}
		for _, route := range routes.Items {
			original := route.DeepCopy()
			util.AddHCPRouteLabel(&route)
			if diff := cmp.Diff(route.GetLabels(), original.GetLabels()); diff != "" {
				t.Errorf("route %s is missing the label to use the per-HCP router: %s", route.Name, diff)
			}
		}
	})
}

func getPrometheusToken(ctx context.Context, secretName string, client crclient.Client) ([]byte, error) {
	if secretName == "" {
		return createPrometheusToken(ctx)
	} else {
		return getTokenFromSecret(ctx, secretName, client)
	}
}

func createPrometheusToken(ctx context.Context) ([]byte, error) {
	cli, err := createK8sClient()
	if err != nil {
		return nil, err
	}

	tokenReq, err := cli.CoreV1().ServiceAccounts("openshift-monitoring").CreateToken(
		ctx,
		"prometheus-k8s",
		&authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				// Avoid specifying any audiences so that the token will be
				// issued for the default audience of the issuer.
			},
		},
		metav1.CreateOptions{},
	)

	if err != nil {
		return nil, fmt.Errorf("failed to create token; %w", err)
	}

	return []byte(tokenReq.Status.Token), nil
}

func getTokenFromSecret(ctx context.Context, secretName string, client crclient.Client) ([]byte, error) {
	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: "hypershift",
		},
	}
	if err := client.Get(ctx, crclient.ObjectKeyFromObject(tokenSecret), tokenSecret); err != nil {
		return nil, fmt.Errorf("failed to get hypershift operator token secret: %w", err)
	}
	token, ok := tokenSecret.Data["token"]
	if !ok {
		return nil, fmt.Errorf("token secret did not contain a token value")
	}
	return token, nil
}

func EnsureHCPContainersHaveResourceRequests(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureHCPContainersHaveResourceRequests", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name
		var podList corev1.PodList
		if err := client.List(ctx, &podList, &crclient.ListOptions{Namespace: namespace}); err != nil {
			t.Fatalf("failed to list pods: %v", err)
		}
		for _, pod := range podList.Items {
			if strings.Contains(pod.Name, "-catalog-rollout-") {
				continue
			}
			for _, container := range pod.Spec.Containers {
				if container.Resources.Requests == nil {
					t.Errorf("container %s in pod %s has no resource requests", container.Name, pod.Name)
					continue
				}
				if _, ok := container.Resources.Requests[corev1.ResourceCPU]; !ok {
					t.Errorf("container %s in pod %s has no CPU resource request", container.Name, pod.Name)
				}
				if _, ok := container.Resources.Requests[corev1.ResourceMemory]; !ok {
					t.Errorf("container %s in pod %s has no memory resource request", container.Name, pod.Name)
				}
			}
		}
	})
}

func EnsureSecretEncryptedUsingKMS(t *testing.T, ctx context.Context, hostedCluster *hyperv1.HostedCluster, guestClient crclient.Client) {
	t.Run("EnsureSecretEncryptedUsingKMS", func(t *testing.T) {
		// create secret in guest cluster
		testSecret := corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "default",
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"myKey": []byte("myData"),
			},
		}
		if err := guestClient.Create(ctx, &testSecret); err != nil {
			t.Errorf("failed to create a secret in guest cluster; %v", err)
		}

		restConfig, err := GetConfig()
		if err != nil {
			t.Errorf("failed to get restConfig; %v", err)
		}

		secretEtcdKey := fmt.Sprintf("/kubernetes.io/secrets/%s/%s", testSecret.Namespace, testSecret.Name)
		command := []string{
			"/usr/bin/etcdctl",
			"--endpoints=localhost:2379",
			"--cacert=/etc/etcd/tls/etcd-ca/ca.crt",
			"--cert=/etc/etcd/tls/client/etcd-client.crt",
			"--key=/etc/etcd/tls/client/etcd-client.key",
			"get",
			secretEtcdKey,
		}

		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name).Name
		out := new(bytes.Buffer)

		podExecuter := PodExecOptions{
			StreamOptions: StreamOptions{
				IOStreams: genericclioptions.IOStreams{
					Out:    out,
					ErrOut: os.Stderr,
				},
			},
			Command:       command,
			Namespace:     hcpNamespace,
			PodName:       "etcd-0",
			ContainerName: "etcd",
			Config:        restConfig,
		}

		if err := podExecuter.Run(); err != nil {
			t.Errorf("failed to execute etcdctl command; %v", err)
		}

		if !strings.Contains(out.String(), "k8s:enc:kms:") {
			t.Errorf("secret is not encrypted using kms")
		}
	})
}

// WaitForNodePoolConditionsNotToBePresent blocks until the given conditions are
// not present in the NodePool.
func WaitForNodePoolConditionsNotToBePresent(t *testing.T, ctx context.Context, client crclient.Client, nodePool *hyperv1.NodePool, conditions ...string) {
	g := NewWithT(t)

	t.Logf("Waiting for nodepool %s conditions to not be present: %v", nodePool.Name, conditions)
	err := wait.PollImmediateWithContext(ctx, 10*time.Second, 20*time.Minute, func(ctx context.Context) (done bool, err error) {
		latest := nodePool.DeepCopy()
		err = client.Get(ctx, crclient.ObjectKeyFromObject(nodePool), latest)
		if err != nil {
			t.Logf("Failed to get nodepool: %v", err)
			return false, nil
		}

		var exists []hyperv1.NodePoolCondition
		for _, actual := range nodePool.Status.Conditions {
			for _, cond := range conditions {
				if actual.Type == cond {
					exists = append(exists, actual)
					break
				}
			}
		}
		if len(exists) == 0 {
			return true, nil
		}

		t.Logf("Waiting for nodepool conditions to not be present: %v", exists)
		return false, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed waiting for nodepool conditions")
}

func createK8sClient() (*k8s.Clientset, error) {
	config, err := GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes config: %w", err)
	}

	cli, err := k8s.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("unable to get kubernetes client: %w", err)
	}

	return cli, nil
}

func NewLogr(t *testing.T) logr.Logger {
	return zapr.NewLogger(zaptest.NewLogger(t))
}
