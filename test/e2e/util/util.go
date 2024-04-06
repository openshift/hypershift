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
	routev1client "github.com/openshift/client-go/route/clientset/versioned"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	hcmetrics "github.com/openshift/hypershift/hypershift-operator/controllers/hostedcluster/metrics"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/conditions"
	suppconfig "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	support "github.com/openshift/hypershift/support/util"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"go.uber.org/zap/zaptest"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/kubernetes"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/pointer"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func UpdateObject[T crclient.Object](t *testing.T, ctx context.Context, client crclient.Client, original T, mutate func(obj T)) error {
	return wait.PollImmediateWithContext(ctx, time.Second, time.Minute*1, func(ctx context.Context) (done bool, err error) {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(original), original); err != nil {
			t.Logf("failed to retrieve object %s, will retry: %v", original.GetName(), err)
			return false, nil
		}

		obj := original.DeepCopyObject().(T)
		mutate(obj)

		if err := client.Patch(ctx, obj, crclient.MergeFrom(original)); err != nil {
			t.Logf("failed to patch object %s, will retry: %v", original.GetName(), err)
			if errors.IsConflict(err) {
				return false, nil
			}
			return false, err
		}

		return true, nil
	})
}

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
	// Mulham: increased timeout from 5m to 15m as guest kubeconfig/API server takes longer to report available after switching from private to public
	waitForGuestClientCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
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

func GetGuestKubeconfigHost(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) (string, error) {
	guestKubeConfigSecretData, err := WaitForGuestKubeConfig(t, ctx, client, hostedCluster)
	if err != nil {
		return "", fmt.Errorf("couldn't get guest kubeconfig: %v", err)
	}

	guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
	if err != nil {
		return "", fmt.Errorf("couldn't load guest kubeconfig: %v", err)
	}

	host := guestConfig.Host
	if len(host) == 0 {
		return "", fmt.Errorf("guest kubeconfig host is empty")
	}
	return host, nil
}

func WaitForGuestKubeconfigHostUpdate(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, oldHost string) {
	g := NewWithT(t)
	waitTimeout := 30 * time.Minute
	pollingInterval := 15 * time.Second

	t.Logf("Waiting for guest kubeconfig host update")
	var newHost string
	var getHostError error
	err := wait.PollUntilContextTimeout(ctx, pollingInterval, waitTimeout, true, func(ctx context.Context) (done bool, err error) {
		newHost, getHostError = GetGuestKubeconfigHost(t, ctx, client, hostedCluster)
		if getHostError != nil {
			t.Logf("failed to get guest kubeconfig host: %v", getHostError)
			return false, nil
		}
		if newHost == oldHost {
			t.Logf("guest kubeconfig host is not yet updated, keep polling")
			return false, nil
		}
		return true, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to wait for guest kubeconfig host update")
	t.Logf("Guest kubeconfig host switched from %s to %s", oldHost, newHost)
}

func WaitForNReadyNodes(t *testing.T, ctx context.Context, client crclient.Client, n int32, platform hyperv1.PlatformType) []corev1.Node {
	g := NewWithT(t)
	start := time.Now()

	// waitTimeout for nodes to become Ready
	waitTimeout := 30 * time.Minute
	switch platform {
	case hyperv1.PowerVSPlatform:
		waitTimeout = 60 * time.Minute
	case hyperv1.KubevirtPlatform:
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

		var readyNodes []string
		for _, node := range nodes.Items {
			for _, cond := range node.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					readyNodes = append(readyNodes, node.Name)
					g.Expect(node.Labels[hyperv1.NodePoolLabel]).NotTo(BeEmpty())
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
	case hyperv1.KubevirtPlatform:
		waitTimeout = 45 * time.Minute
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
	namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

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
	// TestInPlaceUpgradeNodePool must update nodes in the pool sequentially and it takes about 5m per node
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

func WaitForNodePoolDesiredNodes(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	g := NewWithT(t)

	waitTimeout := 30 * time.Minute
	err := wait.PollImmediateWithContext(ctx, 5*time.Second, waitTimeout, func(ctx context.Context) (done bool, err error) {
		var nodePoolList hyperv1.NodePoolList
		if err := client.List(ctx, &nodePoolList, &crclient.ListOptions{Namespace: hostedCluster.Namespace}); err != nil {
			t.Fatalf("failed to list nodepools: %v", err)
		}
		for _, nodePool := range nodePoolList.Items {
			if *nodePool.Spec.Replicas != nodePool.Status.Replicas {
				t.Logf("Waiting. NodePool %q wants %v replicas but has %v", nodePool.Name, *nodePool.Spec.Replicas, nodePool.Status.Replicas)
				return false, nil
			}
		}
		return true, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to ensure all NodePools' nodes ready")
}

func EnsureNoCrashingPods(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureNoCrashingPods", func(t *testing.T) {

		var crashToleration int32

		switch hostedCluster.Spec.Platform.Type {
		case hyperv1.KubevirtPlatform:
			kvPlatform := hostedCluster.Spec.Platform.Kubevirt
			// External infra can be slow at times due to the nested nature
			// of how external infra is tested within a kubevirt hcp running
			// within baremetal ocp. Occasionally pods will fail with
			// "Error: context deadline exceeded" reported by the kubelet. This
			// seems to be an infra issue with etcd latency within the external
			// infra test environment. Tolerating a single restart for random
			// components helps.
			//
			// This toleration is not used for the default local HCP KubeVirt,
			// only external infra
			if kvPlatform != nil && kvPlatform.Credentials != nil {
				crashToleration = 1
			}
		default:
			crashToleration = 0
		}

		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

		var podList corev1.PodList
		if err := client.List(ctx, &podList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list pods in namespace %s: %v", namespace, err)
		}
		for _, pod := range podList.Items {
			// TODO: Figure out why Route kind does not exist when ingress-operator first starts
			if strings.HasPrefix(pod.Name, "ingress-operator-") {
				continue
			}
			// Restart built into OLM by design by
			// https://github.com/openshift/operator-framework-olm/commit/1cf358424a0cbe353428eab9a16051c6cabbd002
			if strings.HasPrefix(pod.Name, "olm-operator-") {
				continue
			}

			if strings.HasPrefix(pod.Name, "catalog-operator-") {
				continue
			}
			if strings.Contains(pod.Name, "-catalog") {
				continue
			}

			for _, containerStatus := range pod.Status.ContainerStatuses {
				if containerStatus.RestartCount > crashToleration {
					t.Errorf("Container %s in pod %s has a restartCount > 0 (%d)", containerStatus.Name, pod.Name, containerStatus.RestartCount)
				}
			}
		}
	})
}

func NoticePreemptionOrFailedScheduling(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("NoticePreemptionOrFailedScheduling", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

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
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

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
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

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
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
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

func EnsureAllRoutesUseHCPRouter(t *testing.T, ctx context.Context, hostClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureAllRoutesUseHCPRouter", func(t *testing.T) {
		for _, svc := range hostedCluster.Spec.Services {
			if svc.Service == hyperv1.APIServer && svc.Type != hyperv1.Route {
				t.Skip("skipping test because APIServer is not exposed through a route")
			}
		}
		var routes routev1.RouteList
		if err := hostClient.List(ctx, &routes, crclient.InNamespace(manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name))); err != nil {
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

func EnsureNetworkPolicies(t *testing.T, ctx context.Context, c crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureNetworkPolicies", func(t *testing.T) {
		if hostedCluster.Spec.Platform.Type != hyperv1.AWSPlatform {
			t.Skip()
		}

		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		t.Run("EnsureComponentsHaveNeedManagementKASAccessLabel", func(t *testing.T) {
			// Check for all components expected to have NeedManagementKASAccessLabel.
			want := []string{
				"cluster-network-operator",
				"ignition-server",
				"cluster-storage-operator",
				"csi-snapshot-controller-operator",
				"machine-approver",
				"cluster-autoscaler",
				"cluster-node-tuning-operator",
				"capi-provider-controller-manager",
				"cluster-api",
				"etcd", // For etcd-defrag leader elections
				"control-plane-operator",
				"control-plane-pki-operator",
				"hosted-cluster-config-operator",
				"cloud-controller-manager",
				"olm-collect-profiles",
				"aws-ebs-csi-driver-operator",
			}

			g := NewWithT(t)
			err := checkPodsHaveLabel(ctx, c, want, hcpNamespace, client.MatchingLabels{suppconfig.NeedManagementKASAccessLabel: "true"})
			g.Expect(err).ToNot(HaveOccurred())
		})

		t.Run("EnsureLimitedEgressTrafficToManagementKAS", func(t *testing.T) {
			g := NewWithT(t)

			kubernetesEndpoint := &corev1.Endpoints{ObjectMeta: metav1.ObjectMeta{Name: "kubernetes", Namespace: "default"}}
			err := c.Get(ctx, client.ObjectKeyFromObject(kubernetesEndpoint), kubernetesEndpoint)
			g.Expect(err).ToNot(HaveOccurred())

			kasAddress := ""
			for _, subset := range kubernetesEndpoint.Subsets {
				if len(subset.Addresses) > 0 && len(subset.Ports) > 0 {
					kasAddress = fmt.Sprintf("https://%s:%v", subset.Addresses[0].IP, subset.Ports[0].Port)
					break
				}
			}
			t.Logf("Connecting to kubernetes endpoint on: %s", kasAddress)

			command := []string{
				"curl",
				"--connect-timeout",
				"2",
				"-Iks",
				// Default KAS advertised address.
				kasAddress,
			}

			// Validate cluster-version-operator is not allowed to access management KAS.
			_, err = RunCommandInPod(ctx, c, "cluster-version-operator", hcpNamespace, command, "cluster-version-operator")
			g.Expect(err).To(HaveOccurred())

			// Validate private router is not allowed to access management KAS.
			if hostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform {
				if hostedCluster.Spec.Platform.AWS.EndpointAccess != hyperv1.Private {
					// TODO (alberto): Run also in private case. Today it results in a flake:
					// === CONT  TestCreateClusterPrivate/EnsureHostedCluster/EnsureNetworkPolicies/EnsureLimitedEgressTrafficToManagementKAS
					//    util.go:851: private router pod was unexpectedly allowed to reach the management KAS. stdOut: . stdErr: Internal error occurred: error executing command in container: container is not created or running
					// Should be solve with https://issues.redhat.com/browse/HOSTEDCP-1200
					_, err := RunCommandInPod(ctx, c, "private-router", hcpNamespace, command, "private-router")
					g.Expect(err).To(HaveOccurred())
				}
			}

			// Validate cluster api is allowed to access management KAS.
			stdOut, err := RunCommandInPod(ctx, c, "cluster-api", hcpNamespace, command, "manager")
			// Expect curl return a 403 from the KAS.
			if !strings.Contains(stdOut, "HTTP/2 403") || err != nil {
				t.Errorf("cluster api pod was unexpectedly not allowed to reach the management KAS. stdOut: %s. stdErr: %s", stdOut, err.Error())
			}
		})
	})
}

func getComponentName(pod *corev1.Pod) string {
	if pod.Labels["app"] != "" {
		return pod.Labels["app"]
	}

	if pod.Labels["name"] != "" {
		return pod.Labels["name"]
	}

	if strings.HasPrefix(pod.Labels["job-name"], "olm-collect-profiles") {
		return "olm-collect-profiles"
	}

	return ""
}

func checkPodsHaveLabel(ctx context.Context, c crclient.Client, allowedComponents []string, namespace string, labels map[string]string) error {
	// Get all Pods with wanted label.
	podList := &corev1.PodList{}
	err := c.List(ctx, podList, client.InNamespace(namespace), client.MatchingLabels(labels))
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	// Get the component name for each labelled pod and ensure it exists in the components slice
	for _, pod := range podList.Items {
		if pod.Labels[suppconfig.NeedManagementKASAccessLabel] == "" {
			continue
		}
		componentName := getComponentName(&pod)
		if componentName == "" {
			return fmt.Errorf("unable to determine component name for pod that has NeedManagementKASAccessLabel: %s", pod.Name)
		}
		allowed := false
		for _, component := range allowedComponents {
			if component == componentName {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("NeedManagementKASAccessLabel label is not allowed on component: %s", componentName)
		}
	}

	return nil
}

func RunCommandInPod(ctx context.Context, c crclient.Client, component, namespace string, command []string, containerName string) (string, error) {
	podList := &corev1.PodList{}
	if err := c.List(ctx, podList,
		client.InNamespace(namespace),
		client.MatchingLabels{"app": component}); err != nil {
		return "", fmt.Errorf("failed to list Pods: %w", err)
	}
	if len(podList.Items) < 1 {
		return "", fmt.Errorf("pods for component %q not found", component)
	}

	restConfig, err := GetConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get restConfig; %w", err)
	}

	stdOut := new(bytes.Buffer)
	podExecuter := PodExecOptions{
		StreamOptions: StreamOptions{
			IOStreams: genericclioptions.IOStreams{
				Out:    stdOut,
				ErrOut: os.Stderr,
			},
		},
		Command:       command,
		Namespace:     namespace,
		PodName:       podList.Items[0].Name,
		Config:        restConfig,
		ContainerName: containerName,
	}

	err = podExecuter.Run()
	return stdOut.String(), err
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
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
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

		hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
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

func CorrelateDaemonSet(ds *appsv1.DaemonSet, nodePool *hyperv1.NodePool, dsName string) {

	for _, c := range ds.Spec.Template.Spec.Containers {
		if c.Name == ds.Name {
			c.Name = dsName
		}
	}

	ds.Name = dsName
	ds.ObjectMeta.Labels = make(map[string]string)
	ds.ObjectMeta.Labels["hypershift.openshift.io/nodePool"] = nodePool.Name

	ds.Spec.Selector.MatchLabels["name"] = dsName
	ds.Spec.Selector.MatchLabels["hypershift.openshift.io/nodePool"] = nodePool.Name

	ds.Spec.Template.ObjectMeta.Labels["name"] = dsName
	ds.Spec.Template.ObjectMeta.Labels["hypershift.openshift.io/nodePool"] = nodePool.Name

	// Set NodeSelector for the DS
	ds.Spec.Template.Spec.NodeSelector = make(map[string]string)
	ds.Spec.Template.Spec.NodeSelector["hypershift.openshift.io/nodePool"] = nodePool.Name

}

func NewPrometheusClient(ctx context.Context) (prometheusv1.API, error) {
	config, err := GetConfig()
	if err != nil {
		return nil, err
	}

	kubeClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}
	routeClient, err := routev1client.NewForConfig(config)
	if err != nil {
		panic(err)
	}
	prometheusClient, err := newPrometheusClient(ctx, kubeClient, routeClient)
	if err != nil {
		panic(err)
	}
	return prometheusClient, nil
}

// PrometheusResponse is used to contain prometheus query results
type PrometheusResponse struct {
	Data prometheusResponseData `json:"data"`
}

type prometheusResponseData struct {
	Result model.Vector `json:"result"`
}

func RunQueryAtTime(ctx context.Context, log logr.Logger, prometheusClient prometheusv1.API, query string, evaluationTime time.Time) (*PrometheusResponse, error) {
	result, warnings, err := prometheusClient.Query(ctx, query, evaluationTime)
	if err != nil {
		return nil, err
	}
	if len(warnings) > 0 {
		log.Info(fmt.Sprintf("#### warnings \n\t%v\n", strings.Join(warnings, "\n\t")))
	}
	if result.Type() != model.ValVector {
		return nil, fmt.Errorf("result type is not the vector: %v", result.Type())
	}
	return &PrometheusResponse{
		Data: prometheusResponseData{
			Result: result.(model.Vector),
		},
	}, nil
}

func EnsurePodsWithEmptyDirPVsHaveSafeToEvictAnnotations(t *testing.T, ctx context.Context, hostClient crclient.Client, hcpNs string) {
	t.Run("EnsurePodsWithEmptyDirPVsHaveSafeToEvictAnnotations", func(t *testing.T) {
		g := NewWithT(t)

		auditedAppList := map[string]string{
			"cloud-controller-manager":         "app",
			"cloud-credential-operator":        "app",
			"aws-ebs-csi-driver-controller":    "app",
			"capi-provider-controller-manager": "app",
			"cloud-network-config-controller":  "app",
			"cluster-network-operator":         "app",
			"cluster-version-operator":         "app",
			"control-plane-operator":           "app",
			"ignition-server":                  "app",
			"ingress-operator":                 "app",
			"kube-apiserver":                   "app",
			"kube-controller-manager":          "app",
			"kube-scheduler":                   "app",
			"multus-admission-controller":      "app",
			"oauth-openshift":                  "app",
			"openshift-apiserver":              "app",
			"openshift-oauth-apiserver":        "app",
			"packageserver":                    "app",
			"ovnkube-master":                   "app",
			"kubevirt-csi-driver":              "app",
			"cluster-image-registry-operator":  "name",
			"virt-launcher":                    "kubevirt.io",
		}

		hcpPods := &corev1.PodList{}
		if err := hostClient.List(ctx, hcpPods, &client.ListOptions{
			Namespace: hcpNs,
		}); err != nil {
			t.Fatalf("cannot list hostedControlPlane pods: %v", err)
		}

		// Check if the pod's volumes are hostPath or emptyDir, if so, the deployment
		// should annotate the pods with the safe-to-evict-local-volumes, with all the volumes
		// involved as an string  and separated by comma, to satisfy CA operator contract.
		// more info here: https://github.com/kubernetes/autoscaler/blob/master/cluster-autoscaler/FAQ.md#what-types-of-pods-can-prevent-ca-from-removing-a-node
		for _, pod := range hcpPods.Items {
			var labelKey, labelValue string
			// Go through our audited list looking for the label that matches the pod Labels
			// Get the key and value and delete the entry from the audited list.
			for appName, prefix := range auditedAppList {
				if pod.Labels[prefix] == appName {
					labelKey = prefix
					labelValue = appName
					delete(auditedAppList, prefix)
					break
				}
			}

			if labelKey == "" || labelValue == "" {
				// if the Key/Value are empty we asume that the pod is not in the auditedList,
				// if that's the case the annotation should not exists in that pod.
				// Then continue to the next pod
				g.Expect(pod.Annotations[suppconfig.PodSafeToEvictLocalVolumesKey]).To(BeEmpty(), "the pod  %s is not in the audited list for safe-eviction and should not contain the safe-to-evict-local-volume annotation", pod.Name)
				continue
			}

			annotationValue := pod.ObjectMeta.Annotations[suppconfig.PodSafeToEvictLocalVolumesKey]
			for _, volume := range pod.Spec.Volumes {
				// Check the pod's volumes, if they are emptyDir or hostPath,
				// they should include that volume in the annotation
				if volume.EmptyDir != nil || volume.HostPath != nil {
					g.Expect(strings.Contains(annotationValue, volume.Name)).To(BeTrue(), "pod with name %s do not have the right volumes set in the safe-to-evict-local-volume annotation: \nCurrent: %s, Expected to be included in: %s", pod.Name, volume.Name, annotationValue)
				}
			}
		}
	})
}

func EnsureGuestWebhooksValidated(t *testing.T, ctx context.Context, guestClient crclient.Client) {
	t.Run("EnsureGuestWebhooksValidated", func(t *testing.T) {
		guestWebhookConf := &admissionregistrationv1.ValidatingWebhookConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-webhook",
				Namespace: "default",
				Annotations: map[string]string{
					"service.beta.openshift.io/inject-cabundle": "true",
				},
			},
		}

		sideEffectsNone := admissionregistrationv1.SideEffectClassNone
		guestWebhookConf.Webhooks = []admissionregistrationv1.ValidatingWebhook{{
			AdmissionReviewVersions: []string{"v1"},
			Name:                    "etcd-client.example.com",
			ClientConfig: admissionregistrationv1.WebhookClientConfig{
				URL: pointer.String("https://etcd-client:2379"),
			},
			Rules: []admissionregistrationv1.RuleWithOperations{{
				Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
				Rule: admissionregistrationv1.Rule{
					APIGroups:   []string{""},
					APIVersions: []string{"v1"},
					Resources:   []string{"pods"},
				},
			}},
			SideEffects: &sideEffectsNone,
		}}

		if err := guestClient.Create(ctx, guestWebhookConf); err != nil {
			t.Errorf("failed to create webhook: %v", err)
		}

		err := wait.PollImmediateWithContext(ctx, 5*time.Second, 1*time.Minute, func(ctx context.Context) (done bool, err error) {
			webhook := &admissionregistrationv1.ValidatingWebhookConfiguration{}
			if err := guestClient.Get(ctx, client.ObjectKeyFromObject(guestWebhookConf), webhook); err != nil && errors.IsNotFound(err) {
				// webhook has been deleted
				return true, nil
			}

			return false, nil
		})

		if err != nil {
			t.Errorf("failed to ensure guest webhooks validated, violating webhook %s was not deleted: %v", guestWebhookConf.Name, err)
		}

	})
}

const (
	// Metrics
	// TODO (jparrill): We need to separate the metrics.go from the main pkg in the hypershift-operator.
	//     Delete these references when it's done and import it from there
	HypershiftOperatorInfoName = "hypershift_operator_info"
)

// Verifies that the given metrics are defined for the given hosted cluster if areMetricsExpectedToBePresent is set to true.
// Verifies that the given metrics are not defined otherwise.
func ValidateMetrics(t *testing.T, ctx context.Context, hc *hyperv1.HostedCluster, metricsNames []string, areMetricsExpectedToBePresent bool) {
	t.Run("ValidateMetricsAreExposed", func(t *testing.T) {
		// TODO (alberto) this test should pass in None.
		// https://gcsweb-ci.apps.ci.l2s4.p1.openshiftapps.com/gcs/origin-ci-test/pr-logs/pull/openshift_hypershift/2459/pull-ci-openshift-hypershift-main-e2e-aws/1650438383060652032/artifacts/e2e-aws/run-e2e/artifacts/TestNoneCreateCluster_PreTeardownClusterDump/
		// https://storage.googleapis.com/origin-ci-test/pr-logs/pull/openshift_hypershift/2459/pull-ci-openshift-hypershift-main-e2e-aws/1650438383060652032/build-log.txt
		// https://prow.ci.openshift.org/view/gs/origin-ci-test/pr-logs/pull/openshift_hypershift/2459/pull-ci-openshift-hypershift-main-e2e-aws/1650438383060652032
		if hc.Spec.Platform.Type == hyperv1.NonePlatform {
			t.Skip()
		}

		g := NewWithT(t)

		prometheusClient, err := NewPrometheusClient(ctx)
		g.Expect(err).ToNot(HaveOccurred())

		// Polling to prevent races with prometheus scrape interval.
		err = wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
			for _, metricName := range metricsNames {
				// Query fo HC specific metrics by hc.name.
				query := fmt.Sprintf("%v{name=\"%s\"}", metricName, hc.Name)
				if metricName == HypershiftOperatorInfoName {
					// Query HO info metric
					query = HypershiftOperatorInfoName
				}
				if strings.HasPrefix(metricName, "hypershift_nodepools") {
					query = fmt.Sprintf("%v{cluster_name=\"%s\"}", metricName, hc.Name)
				}
				// upgrade metric is only available for TestUpgradeControlPlane
				if metricName == hcmetrics.UpgradingDurationMetricName && !strings.HasPrefix("TestUpgradeControlPlane", t.Name()) {
					continue
				}

				result, err := RunQueryAtTime(ctx, NewLogr(t), prometheusClient, query, time.Now())
				if err != nil {
					return false, err
				}

				if areMetricsExpectedToBePresent {
					if len(result.Data.Result) < 1 {
						t.Logf("Metric not found: %q", metricName)
						return false, nil
					}
					for _, series := range result.Data.Result {
						t.Logf("Time series found: %v", series.String())
					}
				} else {
					if len(result.Data.Result) > 0 {
						t.Logf("Metric found: %q", metricName)
						return false, nil
					}
				}
			}
			return true, nil
		})
		if err != nil {
			t.Errorf("Failed to validate all metrics")
		}
	})
}

func ValidatePublicCluster(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts *core.CreateOptions) {
	g := NewWithT(t)

	// Sanity check the cluster by waiting for the nodes to report ready
	t.Logf("Waiting for guest client to become available")
	guestClient := WaitForGuestClient(t, ctx, client, hostedCluster)

	// Wait for Nodes to be Ready
	numNodes := clusterOpts.NodePoolReplicas * int32(len(clusterOpts.AWSPlatform.Zones))
	WaitForNReadyNodes(t, ctx, guestClient, numNodes, hostedCluster.Spec.Platform.Type)

	// rollout will not complete if there are no wroker nodes.
	if numNodes > 0 {
		// Wait for the rollout to be complete
		t.Logf("Waiting for cluster rollout. Image: %s", clusterOpts.ReleaseImage)
		WaitForImageRollout(t, ctx, client, hostedCluster, clusterOpts.ReleaseImage)
	}

	err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	serviceStrategy := util.ServicePublishingStrategyByTypeByHC(hostedCluster, hyperv1.APIServer)
	g.Expect(serviceStrategy).ToNot(BeNil())
	if serviceStrategy.Type == hyperv1.Route && serviceStrategy.Route != nil && serviceStrategy.Route.Hostname != "" {
		g.Expect(hostedCluster.Status.ControlPlaneEndpoint.Host).To(Equal(serviceStrategy.Route.Hostname))
	} else {
		// sanity check
		g.Expect(hostedCluster.Status.ControlPlaneEndpoint.Host).ToNot(ContainSubstring("hypershift.local"))
	}

	validateHostedClusterConditions(t, ctx, client, hostedCluster, numNodes > 0)

	EnsureNodeCountMatchesNodePoolReplicas(t, ctx, client, guestClient, hostedCluster.Namespace)
	EnsureNoCrashingPods(t, ctx, client, hostedCluster)
	EnsureGuestWebhooksValidated(t, ctx, guestClient)

	if numNodes > 0 {
		EnsureNodeCommunication(t, ctx, client, hostedCluster)
	}
}

func ValidatePrivateCluster(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts *core.CreateOptions) {
	g := NewWithT(t)

	_, err := WaitForGuestKubeConfig(t, ctx, client, hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "couldn't get kubeconfig")

	// Ensure NodePools have all Nodes ready.
	WaitForNodePoolDesiredNodes(t, ctx, client, hostedCluster)

	numNodes := clusterOpts.NodePoolReplicas * int32(len(clusterOpts.AWSPlatform.Zones))
	// rollout will not complete if there are no worker nodes.
	if numNodes > 0 {
		// Wait for the rollout to be complete
		t.Logf("Waiting for cluster rollout. Image: %s", clusterOpts.ReleaseImage)
		WaitForImageRollout(t, ctx, client, hostedCluster, clusterOpts.ReleaseImage)
	}

	err = client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	serviceStrategy := util.ServicePublishingStrategyByTypeByHC(hostedCluster, hyperv1.APIServer)
	g.Expect(serviceStrategy).ToNot(BeNil())
	if serviceStrategy.Route != nil && serviceStrategy.Route.Hostname != "" {
		g.Expect(hostedCluster.Status.ControlPlaneEndpoint.Host).To(Equal(serviceStrategy.Route.Hostname))
	} else {
		// sanity check
		g.Expect(hostedCluster.Status.ControlPlaneEndpoint.Host).ToNot(ContainSubstring("hypershift.local"))
	}

	validateHostedClusterConditions(t, ctx, client, hostedCluster, numNodes > 0)

	EnsureNoCrashingPods(t, ctx, client, hostedCluster)
}

func validateHostedClusterConditions(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, hasWorkerNodes bool) {
	expectedConditions := conditions.ExpectedHCConditions()

	switch hostedCluster.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		if hostedCluster.Spec.SecretEncryption == nil || hostedCluster.Spec.SecretEncryption.KMS == nil || hostedCluster.Spec.SecretEncryption.KMS.AWS == nil {
			// AWS KMS is not configured
			expectedConditions[hyperv1.ValidAWSKMSConfig] = metav1.ConditionUnknown
		}
	case hyperv1.AzurePlatform:
		if hostedCluster.Spec.SecretEncryption == nil || hostedCluster.Spec.SecretEncryption.KMS == nil || hostedCluster.Spec.SecretEncryption.KMS.Azure == nil {
			// Azure KMS is not configured
			expectedConditions[hyperv1.ValidAzureKMSConfig] = metav1.ConditionUnknown
		}
	}

	kasExternalHostname := support.ServiceExternalDNSHostnameByHC(hostedCluster, hyperv1.APIServer)
	if kasExternalHostname == "" {
		// ExternalDNS is not configured
		expectedConditions[hyperv1.ExternalDNSReachable] = metav1.ConditionUnknown
	} else {
		expectedConditions[hyperv1.ExternalDNSReachable] = metav1.ConditionTrue
	}
	if !hasWorkerNodes {
		expectedConditions[hyperv1.ClusterVersionAvailable] = metav1.ConditionFalse
		expectedConditions[hyperv1.ClusterVersionSucceeding] = metav1.ConditionFalse
		expectedConditions[hyperv1.ClusterVersionProgressing] = metav1.ConditionTrue
	}

	if hostedCluster.Spec.Platform.Type == hyperv1.KubevirtPlatform &&
		hostedCluster.Spec.Networking.NetworkType == hyperv1.OVNKubernetes {
		if hostedCluster.Annotations[hyperv1.ManagementPlatformAnnotation] == string(hyperv1.AWSPlatform) {
			// AWS platform supports Jumbo frames
			expectedConditions[hyperv1.ValidKubeVirtInfraNetworkMTU] = metav1.ConditionTrue
		} else if hostedCluster.Annotations[hyperv1.ManagementPlatformAnnotation] == string(hyperv1.AzurePlatform) {
			// Azure platform doesn't support Jumbo frames
			expectedConditions[hyperv1.ValidKubeVirtInfraNetworkMTU] = metav1.ConditionFalse
		}
	}

	t.Logf("validating status for hostedcluster %s/%s", hostedCluster.Namespace, hostedCluster.Name)
	start := time.Now()
	previousResourceVersion := ""
	previousConditions := map[string]metav1.Condition{}
	err := wait.PollImmediateWithContext(ctx, 10*time.Second, 10*time.Minute, func(ctx context.Context) (bool, error) {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster); err != nil {
			t.Logf("Failed to get hostedcluster: %v", err)
			return false, nil
		}

		if hostedCluster.ResourceVersion == previousResourceVersion {
			// nothing's changed since the last time we checked
			return false, nil
		}
		previousResourceVersion = hostedCluster.ResourceVersion

		currentConditions := map[string]metav1.Condition{}
		conditionsValid := true
		for i, condition := range hostedCluster.Status.Conditions {
			if condition.Type == string(hyperv1.ClusterVersionUpgradeable) {
				// ClusterVersionUpgradeable condition status is not always guranteed to be true, skip.
				t.Logf("unchecked condition: %s", formatCondition(condition))
				continue
			}

			expectedStatus, known := expectedConditions[hyperv1.ConditionType(condition.Type)]
			if !known {
				return false, fmt.Errorf("unknown condition %s", condition.Type)
			}

			conditionsValid = conditionsValid && (condition.Status == expectedStatus)

			currentConditions[condition.Type] = hostedCluster.Status.Conditions[i]
			if conditionsIdentical(currentConditions[condition.Type], previousConditions[condition.Type]) {
				// no need to spam anything, we already said it when we processed this last time
				continue
			}
			prefix := ""
			if condition.Status != expectedStatus {
				prefix = "in"
			}
			msg := fmt.Sprintf("%scorrect condition: wanted %s=%s, got %s", prefix, condition.Type, expectedStatus, formatCondition(condition))
			t.Log(msg)
		}
		previousConditions = currentConditions

		return conditionsValid, nil
	})
	duration := time.Since(start).Round(time.Second)

	if err != nil {
		t.Fatalf("Failed to validate HostedCluster conditions in %s: %v", duration, err)
	}
	t.Logf("Successfully validated all expected HostedCluster conditions in %s", duration)
}

func formatCondition(condition metav1.Condition) string {
	msg := fmt.Sprintf("%s=%s", condition.Type, condition.Status)
	if condition.Reason != "" {
		msg += ": " + condition.Reason
	}
	if condition.Message != "" {
		msg += "(" + condition.Message + ")"
	}
	return msg
}

func conditionsIdentical(a, b metav1.Condition) bool {
	return a.Type == b.Type && a.Status == b.Status && a.Reason == b.Reason && a.Message == b.Message
}

func EnsureHCPPodsAffinitiesAndTolerations(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	t.Run("EnsureHCPPodsAffinitiesAndTolerations", func(t *testing.T) {
		namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
		awsEbsCsiDriverOperatorPodSubstring := "aws-ebs-csi-driver-operator"
		controlPlaneLabelTolerationKey := "hypershift.openshift.io/control-plane"
		clusterNodeSchedulingAffinityWeight := 100
		controlPlaneNodeSchedulingAffinityWeight := clusterNodeSchedulingAffinityWeight / 2
		colocationLabelKey := "hypershift.openshift.io/hosted-control-plane"

		g := NewGomegaWithT(t)

		var podList corev1.PodList
		if err := client.List(ctx, &podList, crclient.InNamespace(namespace)); err != nil {
			t.Fatalf("failed to list pods in namespace %s: %v", namespace, err)
		}

		namespacedName := types.NamespacedName{
			Namespace: namespace,
			Name:      hostedCluster.Name,
		}

		var hcp hyperv1.HostedControlPlane
		if err := client.Get(ctx, namespacedName, &hcp); err != nil {
			t.Fatalf("failed to get hostedcontrolplane: %v", err)
		}

		expected := suppconfig.DeploymentConfig{
			Scheduling: suppconfig.Scheduling{
				Tolerations: []corev1.Toleration{
					{
						Key:      controlPlaneLabelTolerationKey,
						Operator: corev1.TolerationOpEqual,
						Value:    "true",
						Effect:   corev1.TaintEffectNoSchedule,
					},
					{
						Key:      hyperv1.HostedClusterLabel,
						Operator: corev1.TolerationOpEqual,
						Value:    hcp.Namespace,
						Effect:   corev1.TaintEffectNoSchedule,
					},
				},
				Affinity: &corev1.Affinity{
					NodeAffinity: &corev1.NodeAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
							{
								Weight: int32(controlPlaneNodeSchedulingAffinityWeight),
								Preference: corev1.NodeSelectorTerm{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      controlPlaneLabelTolerationKey,
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{"true"},
										},
									},
								},
							},
							{
								Weight: int32(clusterNodeSchedulingAffinityWeight),
								Preference: corev1.NodeSelectorTerm{
									MatchExpressions: []corev1.NodeSelectorRequirement{
										{
											Key:      hyperv1.HostedClusterLabel,
											Operator: corev1.NodeSelectorOpIn,
											Values:   []string{hcp.Namespace},
										},
									},
								},
							},
						},
					},
					PodAffinity: &corev1.PodAffinity{
						PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
							{
								Weight: 100,
								PodAffinityTerm: corev1.PodAffinityTerm{
									LabelSelector: &metav1.LabelSelector{
										MatchLabels: map[string]string{
											colocationLabelKey: hcp.Namespace,
										},
									},
									TopologyKey: corev1.LabelHostname,
								},
							},
						},
					},
				},
			},
		}

		awsEbsCsiDriverOperatorTolerations := []corev1.Toleration{
			{
				Key:      controlPlaneLabelTolerationKey,
				Operator: corev1.TolerationOpExists,
			},
			{
				Key:      hyperv1.HostedClusterLabel,
				Operator: corev1.TolerationOpEqual,
				Value:    hcp.Namespace,
			},
		}

		for _, pod := range podList.Items {
			t.Logf("checking pod %s/%s for tolerations and node affinities", pod.Namespace, pod.Name)
			// Skip KubeVirt VM worker node related pods
			if pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["app"] == "vmi-console-debug" {
				continue
			}

			// aws-ebs-csi-driver-operator tolerations are set through CSO and are different from the ones in the DC
			if strings.Contains(pod.Name, awsEbsCsiDriverOperatorPodSubstring) {
				g.Expect(pod.Spec.Tolerations).To(ContainElements(awsEbsCsiDriverOperatorTolerations))
			} else {
				g.Expect(pod.Spec.Tolerations).To(ContainElements(expected.Scheduling.Tolerations))
			}

			g.Expect(pod.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(ContainElements(expected.Scheduling.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution))
			g.Expect(pod.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(ContainElements(expected.Scheduling.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution))

		}
	})
}

func EnsureOnlyRequestServingPodsOnRequestServingNodes(t *testing.T, ctx context.Context, client crclient.Client) {
	g := NewWithT(t)

	var reqServingNodes corev1.NodeList
	if err := client.List(ctx, &reqServingNodes, crclient.MatchingLabels{hyperv1.RequestServingComponentLabel: "true"}); err != nil {
		t.Fatalf("failed to list requestServing nodes in guest cluster: %v", err)
	}

	for _, node := range reqServingNodes.Items {
		var podObj corev1.Pod
		var podList corev1.PodList

		if err := client.List(ctx, &podList, crclient.MatchingFields{podObj.Spec.NodeName: node.Name}); err != nil {
			t.Fatalf("failed to list pods for node: %v , error: %v", node.Name, err)
		}

		for _, pod := range podList.Items {
			g.Expect(pod.Labels).To(HaveKeyWithValue(hyperv1.RequestServingComponentLabel, "true"))
		}
	}
}

func EnsureAllReqServingPodsLandOnReqServingNodes(t *testing.T, ctx context.Context, client crclient.Client) {
	g := NewWithT(t)

	var reqServingPods corev1.PodList
	if err := client.List(ctx, &reqServingPods, crclient.MatchingLabels{hyperv1.RequestServingComponentLabel: "true"}); err != nil {
		t.Fatalf("failed to list requestServing pods in guest cluster: %v", err)
	}

	for _, pod := range reqServingPods.Items {
		var node corev1.Node
		node.Name = pod.Spec.NodeName

		if err := client.Get(ctx, crclient.ObjectKeyFromObject(&node), &node); err != nil {
			t.Fatalf("failed to get node: %v , error: %v", node.Name, err)
		}

		g.Expect(node.Labels).To(HaveKeyWithValue(hyperv1.RequestServingComponentLabel, "true"))
	}
}

func EnsureNoHCPPodsLandOnDefaultNode(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	g := NewWithT(t)

	namespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	var podList corev1.PodList
	if err := client.List(ctx, &podList, crclient.InNamespace(namespace)); err != nil {
		t.Fatalf("failed to list pods in namespace %s: %v", namespace, err)
	}

	var HCPNodes corev1.NodeList
	if err := client.List(ctx, &HCPNodes, crclient.MatchingLabels{"hypershift.openshift.io/control-plane": "true"}); err != nil {
		t.Fatalf("failed to list nodes with \"hypershift.openshift.io/control-plane\": \"true\" label: %v", err)
	}

	var hcpNodeNames []string
	for _, node := range HCPNodes.Items {
		hcpNodeNames = append(hcpNodeNames, node.Name)
	}

	for _, pod := range podList.Items {
		g.Expect(pod.Spec.NodeSelector).To(HaveKeyWithValue("hypershift.openshift.io/control-plane", "true"))
		g.Expect(hcpNodeNames).To(ContainElement(pod.Spec.NodeName))
	}
}
