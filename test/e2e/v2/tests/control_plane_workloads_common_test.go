//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/kubernetes"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func controlPlaneCustomizablePod(pod corev1.Pod) bool {
	_, importer := pod.Labels["cdi.kubevirt.io"]
	return pod.Labels["kubevirt.io"] != "virt-launcher" && pod.Labels["app"] != "vmi-console-debug" && !importer
}

func controlPlaneGenerationApplicable(hostedCluster *hyperv1.HostedCluster, now time.Time) bool {
	return !hostedCluster.CreationTimestamp.IsZero() && now.Sub(hostedCluster.CreationTimestamp.Time) <= 4*time.Hour
}

func controlPlaneDeploymentGenerations(ctx context.Context, client crclient.Client, namespace string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	deployments := &appsv1.DeploymentList{}
	if err := client.List(ctx, deployments, crclient.InNamespace(namespace)); err != nil {
		return fmt.Errorf("list control plane deployments in %s: %w", namespace, err)
	}
	if len(deployments.Items) == 0 {
		return fmt.Errorf("no control plane deployments found in %s", namespace)
	}
	for _, deployment := range deployments.Items {
		if deployment.Generation > 10 {
			return fmt.Errorf("deployment %s/%s generation %d exceeds maximum 10", deployment.Namespace, deployment.Name, deployment.Generation)
		}
	}
	return nil
}

func controlPlaneTrustBundleProbe(ctx context.Context, probe func(context.Context) error) error {
	execContext, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	return probe(execContext)
}

func controlPlaneCustomizationMatches(pod corev1.Pod, hostedCluster *hyperv1.HostedCluster) bool {
	for key, value := range hostedCluster.Spec.Labels {
		if actual, exists := pod.Labels[key]; !exists || actual != value {
			return false
		}
	}
	for _, toleration := range hostedCluster.Spec.Tolerations {
		if !slices.ContainsFunc(pod.Spec.Tolerations, func(actual corev1.Toleration) bool {
			return apiequality.Semantic.DeepEqual(actual, toleration)
		}) {
			return false
		}
	}
	return true
}

func controlPlaneFilesystemExempt(pod corev1.Pod, container corev1.Container, temporaryMount bool) bool {
	component := pod.Labels["hypershift.openshift.io/control-plane-component"]
	if component == "etcd" || component == "featuregate-generator" || pod.Labels["kubevirt.io"] == "virt-launcher" {
		return true
	}
	exempt := sets.New(
		"azure-disk-csi-driver-controller", "azure-disk-csi-driver-operator", "azure-file-csi-driver-controller", "azure-file-csi-driver-operator",
		"aws-ebs-csi-driver-controller", "aws-ebs-csi-driver-operator", "openstack-cinder-csi-driver-controller", "openstack-manila-csi",
		"multus-admission-controller", "network-node-identity", "ovnkube-control-plane", "cloud-network-config-controller", "vmi-console-debug", "containerized-data-importer",
	)
	if !temporaryMount {
		exempt.Insert("openstack-cinder-csi-driver-operator", "manila-csi-driver-operator")
	} else {
		exempt.Insert("csi-snapshot-controller", "csi-snapshot-webhook")
		if pod.Labels["app"] == "packageserver" && container.Name == "packageserver" {
			return true
		}
	}
	return exempt.Has(pod.Labels["app"])
}

type controlPlaneCommonChecks struct {
	safeEviction, filesystem, termination, tokens, snapshotToken bool
}

func controlPlaneCommonReleaseChecks(tc *internal.TestContext) (controlPlaneCommonChecks, error) {
	version, err := healthReleaseVersion(tc)
	if err != nil {
		return controlPlaneCommonChecks{}, fmt.Errorf("originating release for common workload assertions: %w", err)
	}
	return controlPlaneCommonChecks{
		safeEviction:  !version.LT(semver.MustParse("4.20.0")),
		filesystem:    !version.LT(semver.MustParse("4.20.0")),
		termination:   !version.LT(semver.MustParse("4.19.0")),
		tokens:        !version.LT(semver.MustParse("4.16.0")),
		snapshotToken: version.LT(semver.MustParse("4.18.0")),
	}, nil
}

func controlPlanePodFilesystem(pod corev1.Pod) error {
	for _, container := range pod.Spec.Containers {
		if !controlPlaneFilesystemExempt(pod, container, false) {
			if container.SecurityContext == nil || container.SecurityContext.ReadOnlyRootFilesystem == nil || !*container.SecurityContext.ReadOnlyRootFilesystem {
				return fmt.Errorf("pod %s container %s must have a read-only root filesystem", pod.Name, container.Name)
			}
		}
		if !controlPlaneFilesystemExempt(pod, container, true) && !slices.ContainsFunc(container.VolumeMounts, func(mount corev1.VolumeMount) bool { return mount.MountPath == "/tmp" }) {
			return fmt.Errorf("pod %s container %s must mount /tmp", pod.Name, container.Name)
		}
	}
	return nil
}

func controlPlanePodResources(pod corev1.Pod, platform hyperv1.PlatformType, checks controlPlaneCommonChecks) error {
	if !strings.HasPrefix(pod.Name, "multus-admission-controller") && pod.Spec.Priority != nil && *pod.Spec.Priority > 100002000 {
		return fmt.Errorf("pod %s priority %d exceeds maximum 100002000", pod.Name, *pod.Spec.Priority)
	}
	if !strings.Contains(pod.Name, "-catalog-rollout-") {
		for _, container := range pod.Spec.Containers {
			for _, resource := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
				if _, present := container.Resources.Requests[resource]; !present {
					return fmt.Errorf("pod %s container %s must request %s", pod.Name, container.Name, resource)
				}
			}
		}
	}
	if checks.termination && controlPlaneCustomizablePod(pod) && !strings.HasPrefix(pod.Name, "cloud-network-config-controller") && !strings.HasPrefix(pod.Name, "network-node-identity") && !strings.HasPrefix(pod.Name, "ovnkube-control-plane") {
		for _, container := range slices.Concat(pod.Spec.Containers, pod.Spec.InitContainers) {
			if container.TerminationMessagePolicy != corev1.TerminationMessageFallbackToLogsOnError {
				return fmt.Errorf("pod %s container %s must use termination policy %s", pod.Name, container.Name, corev1.TerminationMessageFallbackToLogsOnError)
			}
		}
	}
	if platform == hyperv1.AWSPlatform && checks.tokens {
		allowedTokens := securityManagementKASComponents()
		allowedTokens.Insert("aws-ebs-csi-driver-controller", "packageserver", "csi-snapshot-controller", "shared-resource-csi-driver-operator")
		if checks.snapshotToken {
			allowedTokens.Insert("csi-snapshot-webhook")
		}
		allowed := false
		for component := range allowedTokens {
			allowed = allowed || strings.HasPrefix(pod.Name, component)
		}
		if !allowed {
			for _, volume := range pod.Spec.Volumes {
				if strings.HasPrefix(volume.Name, "kube-api-access-") {
					return fmt.Errorf("pod %s must not mount unnecessary service account token volume %s", pod.Name, volume.Name)
				}
			}
		}
	}
	return nil
}

func controlPlanePodSafeEviction(pod corev1.Pod) error {
	component := pod.Labels["hypershift.openshift.io/control-plane-component"]
	if component == "etcd" || component == "featuregate-generator" {
		return nil
	}
	audited := sets.New("etcd", "cloud-controller-manager", "cloud-credential-operator", "aws-ebs-csi-driver-controller", "capi-provider-controller-manager", "cloud-network-config-controller", "cluster-network-operator", "cluster-version-operator", "control-plane-operator", "ignition-server", "ingress-operator", "kube-apiserver", "kube-controller-manager", "kube-scheduler", "multus-admission-controller", "oauth-openshift", "openshift-apiserver", "openshift-oauth-apiserver", "packageserver", "ovnkube-master", "kubevirt-csi-driver", "azure-disk-csi-driver-controller", "azure-file-csi-driver-controller", "certified-operators-catalog", "community-operators-catalog", "redhat-operators-catalog", "redhat-marketplace-catalog", "openstack-cinder-csi-driver-controller", "openstack-manila-csi", "karpenter", "karpenter-operator", "aws-node-termination-handler")
	annotation := strings.Split(pod.Annotations["cluster-autoscaler.kubernetes.io/safe-to-evict-local-volumes"], ",")
	if !audited.Has(pod.Labels["app"]) && pod.Labels["name"] != "cluster-image-registry-operator" && pod.Labels["kubevirt.io"] != "virt-launcher" {
		for _, volumeName := range annotation {
			if volumeName != "" && volumeName != "tmp-dir" {
				return fmt.Errorf("unaudited pod %s may only annotate tmp-dir", pod.Name)
			}
		}
		if !slices.Contains(annotation, "tmp-dir") {
			return nil
		}
	}
	for _, volume := range pod.Spec.Volumes {
		if (volume.EmptyDir != nil && volume.EmptyDir.Medium != corev1.StorageMediumMemory || volume.HostPath != nil) && !slices.Contains(annotation, volume.Name) {
			return fmt.Errorf("pod %s local volume %s must be annotated safe to evict", pod.Name, volume.Name)
		}
	}
	return nil
}

func ControlPlaneCommonAssertionsTest(getTestCtx internal.TestContextGetter) {
	Context("Namespace-wide common assertions", func() {
		controlPlaneCustomizationAssertions(getTestCtx)
		controlPlaneHealthAssertions(getTestCtx)
		controlPlaneResourceAssertions(getTestCtx)
		controlPlaneSchedulingAssertions(getTestCtx)
		controlPlaneEvictionAssertions(getTestCtx)
	})
}

func controlPlaneCustomizationAssertions(getTestCtx internal.TestContextGetter) {
	It("should propagate configured custom labels and tolerations to every customizable pod", func() {
		tc := getTestCtx()
		skipIfNativeOriginBelow(tc, semver.MustParse("4.19.0"))
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		pods := &corev1.PodList{}
		Expect(tc.MgmtClient.List(tc.Context, pods, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
		Expect(pods.Items).NotTo(BeEmpty())
		for _, pod := range pods.Items {
			if !controlPlaneCustomizablePod(pod) {
				continue
			}
			Expect(controlPlaneCustomizationMatches(pod, hc)).To(BeTrue(), "pod %s must propagate HC spec.labels and spec.tolerations", pod.Name)
		}
	})

	It("should have a nonempty app label on every customizable pod", func() {
		tc := getTestCtx()
		skipIfNativeOriginBelow(tc, semver.MustParse("4.19.0"))
		pods := &corev1.PodList{}
		Expect(tc.MgmtClient.List(tc.Context, pods, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
		Expect(pods.Items).NotTo(BeEmpty())
		for _, pod := range pods.Items {
			if controlPlaneCustomizablePod(pod) {
				Expect(pod.Labels["app"]).NotTo(BeEmpty(), "pod %s", pod.Name)
			}
		}
	})

	It("should apply IfNotPresent to ordinary and init containers throughout the namespace", func() {
		tc := getTestCtx()
		tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
		pods := &corev1.PodList{}
		Expect(tc.MgmtClient.List(tc.Context, pods, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
		Expect(pods.Items).NotTo(BeEmpty())
		for _, pod := range pods.Items {
			for _, container := range slices.Concat(pod.Spec.Containers, pod.Spec.InitContainers) {
				Expect(container.ImagePullPolicy).To(Equal(corev1.PullIfNotPresent), "pod %s container %s", pod.Name, container.Name)
			}
		}
	})

	It("should enforce precisely audited root filesystem and tmp mount exemptions", func() {
		tc := getTestCtx()
		tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
		checks, err := controlPlaneCommonReleaseChecks(tc)
		Expect(err).NotTo(HaveOccurred())
		if !checks.filesystem {
			Skip("read-only filesystem and tmp mounts require originating release 4.20 or newer")
		}
		pods := &corev1.PodList{}
		Expect(tc.MgmtClient.List(tc.Context, pods, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
		Expect(pods.Items).NotTo(BeEmpty())
		for _, pod := range pods.Items {
			Expect(controlPlanePodFilesystem(pod)).To(Succeed())
		}
	})

	It("should mount the OpenShift API server combined trust bundle", func() {
		tc := getTestCtx()
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		pods := &corev1.PodList{}
		Expect(tc.MgmtClient.List(tc.Context, pods, crclient.InNamespace(tc.ControlPlaneNamespace), crclient.MatchingLabels{"app": "openshift-apiserver"})).To(Succeed())
		Expect(pods.Items).NotTo(BeEmpty())
		restConfig, err := v2util.GetConfig()
		Expect(err).NotTo(HaveOccurred())
		clientset, err := kubernetes.NewForConfig(restConfig)
		Expect(err).NotTo(HaveOccurred())
		for _, pod := range pods.Items {
			if hc.Spec.AdditionalTrustBundle != nil && hc.Spec.AdditionalTrustBundle.Name != "" {
				Expect(pod.Spec.Volumes).To(ContainElement(HaveField("Name", "additional-trust-bundle")))
			}
			if hc.Spec.Configuration != nil && hc.Spec.Configuration.Proxy != nil && hc.Spec.Configuration.Proxy.TrustedCA.Name != "" {
				Expect(pod.Spec.Volumes).To(ContainElement(HaveField("Name", "proxy-additional-trust-bundle")))
			}
			Eventually(func() error {
				return controlPlaneTrustBundleProbe(tc.Context, func(execContext context.Context) error {
					_, err := v2util.RunCommandInPod(execContext, clientset, restConfig, tc.ControlPlaneNamespace, pod.Name, "openshift-apiserver", "test", "-f", "/etc/pki/tls/certs/ca-bundle.crt")
					return err
				})
			}, 5*time.Minute, 30*time.Second).Should(Succeed())
		}
	})

}

func controlPlaneHealthAssertions(getTestCtx internal.TestContextGetter) {
	It("should apply crash tolerances to every AWS control plane pod", func() {
		tc := getTestCtx()
		tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
		hostedCluster, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		pods := &corev1.PodList{}
		Expect(tc.MgmtClient.List(tc.Context, pods, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
		Expect(pods.Items).NotTo(BeEmpty())
		for _, pod := range pods.Items {
			Expect(controlPlanePodCrashHealth(tc, hostedCluster, pod)).To(Succeed())
		}
	})

	It("should bound every AWS control plane deployment generation", func() {
		tc := getTestCtx()
		tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
		Expect(controlPlaneDeploymentGenerations(tc.Context, tc.MgmtClient, tc.ControlPlaneNamespace)).To(Succeed())
	})

	It("should report preemption and failed scheduling events without treating them as fatal", func() {
		tc := getTestCtx()
		events := &corev1.EventList{}
		Expect(tc.MgmtClient.List(tc.Context, events, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
		for _, event := range events.Items {
			if event.Reason == "Preempted" || event.Reason == "FailedScheduling" {
				GinkgoWriter.Printf("%s %s: %s\n", event.InvolvedObject.Name, event.Reason, event.Message)
			}
		}
	})

}

func controlPlaneResourceAssertions(getTestCtx internal.TestContextGetter) {
	It("should enforce namespace-wide resource requests, priority, termination policies and AWS token permissions", func() {
		tc := getTestCtx()
		tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
		checks, err := controlPlaneCommonReleaseChecks(tc)
		Expect(err).NotTo(HaveOccurred())
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		pods := &corev1.PodList{}
		Expect(tc.MgmtClient.List(tc.Context, pods, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
		Expect(pods.Items).NotTo(BeEmpty())
		for _, pod := range pods.Items {
			Expect(controlPlanePodResources(pod, hc.Spec.Platform.Type, checks)).To(Succeed())
		}
	})

}

func controlPlaneSchedulingAssertions(getTestCtx internal.TestContextGetter) {
	It("should match both AWS scheduling affinities and exact topology tolerations", func() {
		tc := getTestCtx()
		tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
		pods := &corev1.PodList{}
		Expect(tc.MgmtClient.List(tc.Context, pods, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
		Expect(pods.Items).NotTo(BeEmpty())
		for _, pod := range pods.Items {
			if !controlPlaneCustomizablePod(pod) || pod.Labels["name"] == "shared-resource-csi-driver-operator" {
				continue
			}
			if strings.Contains(pod.Name, "aws-ebs-csi-driver-operator") {
				Expect(pod.Spec.Tolerations).To(ContainElements(
					corev1.Toleration{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpExists},
					corev1.Toleration{Key: hyperv1.HostedClusterLabel, Operator: corev1.TolerationOpEqual, Value: tc.ControlPlaneNamespace},
				), "pod %s", pod.Name)
			} else {
				Expect(pod.Spec.Tolerations).To(ContainElements(
					corev1.Toleration{Key: "hypershift.openshift.io/control-plane", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
					corev1.Toleration{Key: hyperv1.HostedClusterLabel, Operator: corev1.TolerationOpEqual, Value: tc.ControlPlaneNamespace, Effect: corev1.TaintEffectNoSchedule},
				), "pod %s", pod.Name)
			}
			Expect(pod.Spec.Affinity).NotTo(BeNil(), "pod %s", pod.Name)
			Expect(pod.Spec.Affinity.NodeAffinity).NotTo(BeNil(), "pod %s", pod.Name)
			Expect(pod.Spec.Affinity.PodAffinity).NotTo(BeNil(), "pod %s", pod.Name)
			Expect(pod.Spec.Affinity.NodeAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(ContainElements(
				corev1.PreferredSchedulingTerm{Weight: 50, Preference: corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: "hypershift.openshift.io/control-plane", Operator: corev1.NodeSelectorOpIn, Values: []string{"true"}}}}},
				corev1.PreferredSchedulingTerm{Weight: 100, Preference: corev1.NodeSelectorTerm{MatchExpressions: []corev1.NodeSelectorRequirement{{Key: hyperv1.HostedClusterLabel, Operator: corev1.NodeSelectorOpIn, Values: []string{tc.ControlPlaneNamespace}}}}},
			), "pod %s", pod.Name)
			Expect(pod.Spec.Affinity.PodAffinity.PreferredDuringSchedulingIgnoredDuringExecution).To(ContainElement(corev1.WeightedPodAffinityTerm{Weight: 100, PodAffinityTerm: corev1.PodAffinityTerm{LabelSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"hypershift.openshift.io/hosted-control-plane": tc.ControlPlaneNamespace}}, TopologyKey: corev1.LabelHostname}}), "pod %s", pod.Name)
		}
	})

}

func controlPlaneEvictionAssertions(getTestCtx internal.TestContextGetter) {
	It("should only annotate audited local volumes as safe to evict", func() {
		tc := getTestCtx()
		tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
		checks, err := controlPlaneCommonReleaseChecks(tc)
		Expect(err).NotTo(HaveOccurred())
		if !checks.safeEviction {
			Skip("safe eviction annotations require originating release 4.20 or newer")
		}
		pods := &corev1.PodList{}
		Expect(tc.MgmtClient.List(tc.Context, pods, crclient.InNamespace(tc.ControlPlaneNamespace))).To(Succeed())
		Expect(pods.Items).NotTo(BeEmpty())
		for _, pod := range pods.Items {
			Expect(controlPlanePodSafeEviction(pod)).To(Succeed())
		}
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ControlPlaneWorkloads] AWS Control Plane Customization Inputs", Label("aws-control-plane-customization"), func() {
	It("should configure the baseline nonempty labels and tolerations", func() {
		tc := internal.GetTestContext()
		tc.SkipIfNotPlatform(hyperv1.AWSPlatform)
		skipIfNativeOriginBelow(tc, semver.MustParse("4.19.0"))
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		Expect(hc.Spec.Labels).To(HaveKeyWithValue("hypershift-e2e-test-label", "test"))
		Expect(hc.Spec.Tolerations).To(ContainElement(corev1.Toleration{Key: "hypershift-e2e-test-toleration", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule}))
	})
})
