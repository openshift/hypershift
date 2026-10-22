//go:build e2ev2

package tests

import (
	"fmt"
	"slices"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func controlPlanePodStaticHealth(pod corev1.Pod) error {
	if !strings.HasPrefix(pod.Name, "multus-admission-controller") && pod.Spec.Priority != nil && *pod.Spec.Priority > 100002000 {
		return fmt.Errorf("pod %s priority %d exceeds etcd priority", pod.Name, *pod.Spec.Priority)
	}
	for _, container := range slices.Concat(pod.Spec.Containers, pod.Spec.InitContainers) {
		if container.ImagePullPolicy != corev1.PullIfNotPresent {
			return fmt.Errorf("pod %s container %s must use IfNotPresent", pod.Name, container.Name)
		}
	}
	if strings.Contains(pod.Name, "-catalog-rollout-") {
		return nil
	}
	for _, container := range pod.Spec.Containers {
		for _, resourceName := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
			if _, exists := container.Resources.Requests[resourceName]; !exists {
				return fmt.Errorf("pod %s container %s lacks %s request", pod.Name, container.Name, resourceName)
			}
		}
	}
	return nil
}

func validateControlPlaneNamespaceHealth(tc *internal.TestContext) error {
	if tc == nil || tc.MgmtClient == nil || tc.ControlPlaneNamespace == "" {
		return fmt.Errorf("namespace health requires a management client and control plane namespace")
	}
	hostedCluster, err := tc.GetHostedCluster()
	if err != nil {
		return fmt.Errorf("reading hosted cluster for namespace health: %w", err)
	}
	pods := &corev1.PodList{}
	if err := tc.MgmtClient.List(tc.Context, pods, crclient.InNamespace(tc.ControlPlaneNamespace)); err != nil {
		return fmt.Errorf("listing control plane pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return fmt.Errorf("no control plane pods in namespace %s", tc.ControlPlaneNamespace)
	}
	for _, pod := range pods.Items {
		if err := controlPlanePodStaticHealth(pod); err != nil {
			return err
		}
		if err := controlPlanePodCrashHealth(tc, hostedCluster, pod); err != nil {
			return err
		}
	}
	return nil
}

func controlPlanePodCrashTolerance(hostedCluster *hyperv1.HostedCluster, pod corev1.Pod) int32 {
	return crashTolerationForComponent(securityPodComponent(pod), defaultCrashTolerationForCluster(hostedCluster))
}

func controlPlanePodCrashHealth(tc *internal.TestContext, hostedCluster *hyperv1.HostedCluster, pod corev1.Pod) error {
	tolerance := controlPlanePodCrashTolerance(hostedCluster, pod)
	for _, status := range pod.Status.ContainerStatuses {
		if status.RestartCount <= tolerance {
			continue
		}
		if strings.HasPrefix(pod.Name, "kube-controller-manager-") && isCertificateTriggeredRestart(tc.Context, tc.MgmtClient, &pod) {
			continue
		}
		restConfig, err := v2util.GetConfig()
		if err != nil {
			return fmt.Errorf("loading management config for crash inspection: %w", err)
		}
		clientset, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return fmt.Errorf("creating management client for crash inspection: %w", err)
		}
		if !isLeaderElectionFailure(tc.Context, clientset, &pod, status.Name) {
			return fmt.Errorf("pod %s container %s restarted %d times, allowed %d", pod.Name, status.Name, status.RestartCount, tolerance)
		}
	}
	return nil
}
