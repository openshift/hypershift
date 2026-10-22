//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func autoscalingCapacity(native corev1.ResourceList, annotations map[string]string, architecture string) error {
	if len(native) > 0 {
		if native.Cpu().Sign() <= 0 || native.Memory().Sign() <= 0 {
			return fmt.Errorf("native capacity must include positive CPU and memory, got %v", native)
		}
		return nil
	}
	for _, annotation := range []string{"machine.openshift.io/vCPU", "machine.openshift.io/memoryMb"} {
		quantity, err := strconv.ParseInt(annotations[annotation], 10, 64)
		if err != nil || quantity <= 0 {
			return fmt.Errorf("capacity annotation %s must be positive, got %q", annotation, annotations[annotation])
		}
	}
	for _, label := range strings.Split(annotations["capacity.cluster-autoscaler.kubernetes.io/labels"], ",") {
		key, value, found := strings.Cut(strings.TrimSpace(label), "=")
		if found && key == corev1.LabelArchStable && strings.TrimSpace(value) != "" && (architecture == "" || value == architecture) {
			return nil
		}
	}
	return fmt.Errorf("capacity labels do not contain expected architecture %q", architecture)
}

func waitForAutoscalingNodes(ctx context.Context, client crclient.Client, count int, selector crclient.MatchingLabels) []corev1.Node {
	GinkgoHelper()
	nodes := &corev1.NodeList{}
	Eventually(func(g Gomega) {
		g.Expect(client.List(ctx, nodes, selector)).To(Succeed())
		g.Expect(nodes.Items).To(HaveLen(count))
		for _, node := range nodes.Items {
			g.Expect(node.DeletionTimestamp).To(BeNil())
			g.Expect(node.Status.Conditions).To(ContainElement(And(HaveField("Type", corev1.NodeReady), HaveField("Status", corev1.ConditionTrue))))
		}
	}).WithContext(ctx).WithTimeout(30 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	return nodes.Items
}

func waitForAutoscalerSettings(ctx context.Context, client crclient.Client, namespace string, required []string) {
	GinkgoHelper()
	Eventually(func(g Gomega) {
		deployment := &appsv1.Deployment{}
		g.Expect(client.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: "cluster-autoscaler"}, deployment)).To(Succeed())
		g.Expect(autoscalerSettingsReady(deployment, required)).To(Succeed())
	}).WithContext(ctx).WithTimeout(5 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
}

func autoscalerSettingsReady(deployment *appsv1.Deployment, required []string) error {
	if deployment == nil || len(deployment.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("autoscaler deployment must have a pod template container")
	}
	desired := ptr.Deref(deployment.Spec.Replicas, 1)
	if desired <= 0 {
		return fmt.Errorf("autoscaler must have a positive desired replica count, got %d", desired)
	}
	if deployment.Status.ObservedGeneration < deployment.Generation {
		return fmt.Errorf("autoscaler generation %d has not been observed: %d", deployment.Generation, deployment.Status.ObservedGeneration)
	}
	status := deployment.Status
	if status.Replicas != desired || status.UpdatedReplicas != desired || status.ReadyReplicas != desired || status.AvailableReplicas != desired {
		return fmt.Errorf("autoscaler rollout incomplete: desired=%d total=%d updated=%d ready=%d available=%d", desired, status.Replicas, status.UpdatedReplicas, status.ReadyReplicas, status.AvailableReplicas)
	}
	return autoscalerSettingsMatch(deployment.Spec.Template.Spec.Containers[0].Args, required)
}

func autoscalerSettingsMatch(arguments, required []string) error {
	for _, argument := range required {
		if !slices.Contains(arguments, argument) {
			return fmt.Errorf("autoscaler is missing exact argument %q", argument)
		}
	}
	return nil
}

func configureAutoscalerScaleDown(tc *internal.TestContext, hc *hyperv1.HostedCluster) {
	GinkgoHelper()
	applicable, err := autoscalerScaleDownConfigurationApplicable(tc)
	Expect(err).NotTo(HaveOccurred(), "resolve originating release for autoscaler scale-down configuration")
	if !applicable {
		return
	}
	original := hc.Spec.Autoscaling.DeepCopy()
	Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), hc); err != nil {
			return err
		}
		hc.Spec.Autoscaling.Scaling = hyperv1.ScaleUpAndScaleDown
		hc.Spec.Autoscaling.ScaleDown = &hyperv1.ScaleDownConfig{
			DelayAfterAddSeconds: ptr.To[int32](30), UnneededDurationSeconds: ptr.To[int32](60), UtilizationThresholdPercent: ptr.To[int32](50),
		}
		return tc.MgmtClient.Update(tc.Context, hc)
	})).To(Succeed())
	DeferCleanup(func() {
		Expect(retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current := &hyperv1.HostedCluster{}
			if err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKeyFromObject(hc), current); err != nil {
				if apierrors.IsNotFound(err) {
					return nil
				}
				return err
			}
			current.Spec.Autoscaling = *original
			return tc.MgmtClient.Update(tc.Context, current)
		})).To(Succeed(), "cleanup: restore autoscaler configuration")
	})
}

func autoscalerScaleDownConfigurationApplicable(tc *internal.TestContext) (bool, error) {
	return nativeOriginAtLeast(tc, semver.MustParse("4.18.0"))
}

func AutoscalingPauseTest(getTestCtx internal.TestContextGetter) {
	It("should not decrement paused MachineDeployment replicas and should scale down after unpausing", Label("nodepool-autoscaling-pause"), func() {
		tc := getTestCtx()
		skipIfNativeOriginBelow(tc, semver.MustParse("4.18.0"))
		hc, err := tc.GetHostedCluster()
		Expect(err).NotTo(HaveOccurred())
		client, err := tc.GetHostedClusterClient(hc)
		Expect(err).NotTo(HaveOccurred())
		ctx := tc.Context
		configureAutoscalerScaleDown(tc, hc)
		template := getDefaultNodePool(ctx, tc.MgmtClient, hc)
		Expect(template).NotTo(BeNil())
		nodeLabels := map[string]string{autoscalingTestLabelKey: "pause"}
		pool := buildAutoscalingNodePool(template, 1, 3, nodeLabels)
		Expect(tc.MgmtClient.Create(ctx, pool)).To(Succeed())
		DeferCleanup(func() { cleanupNodePool(ctx, tc.MgmtClient, pool) })
		waitForAutoscalerSettings(ctx, tc.MgmtClient, tc.ControlPlaneNamespace, []string{
			"--scale-down-enabled=true", "--scale-down-delay-after-add=30s", "--scale-down-unneeded-time=60s",
		})
		selector := crclient.MatchingLabels{hyperv1.NodePoolLabel: pool.Name}
		nodes := waitForAutoscalingNodes(ctx, client, 1, selector)
		memory := nodes[0].Status.Allocatable.Memory().Value()
		Expect(memory).To(BeNumerically(">", 0))
		workload := newAutoscalingWorkload(3, *resource.NewQuantity(memory/2, resource.BinarySI), nodeLabels)
		Expect(client.Create(ctx, workload)).To(Succeed())
		DeferCleanup(func() { cleanupWorkload(ctx, client, workload) })
		waitForAutoscalingNodes(ctx, client, 3, selector)
		setPaused := func(paused *string) error {
			return retry.RetryOnConflict(retry.DefaultRetry, func() error {
				if err := tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(pool), pool); err != nil {
					return err
				}
				pool.Spec.PausedUntil = paused
				return tc.MgmtClient.Update(ctx, pool)
			})
		}
		Expect(setPaused(ptr.To("true"))).To(Succeed())
		DeferCleanup(func() {
			err := setPaused(nil)
			Expect(err == nil || apierrors.IsNotFound(err)).To(BeTrue(), "cleanup: unpause NodePool: %v", err)
		})
		deployment := &capiv1.MachineDeployment{}
		Eventually(func(g Gomega) {
			g.Expect(tc.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: tc.ControlPlaneNamespace, Name: pool.Name}, deployment)).To(Succeed())
			g.Expect(deployment.Annotations[capiv1.PausedAnnotation]).To(Equal("true"))
			g.Expect(deployment.Spec.Replicas).To(Equal(ptr.To[int32](3)))
		}).WithContext(ctx).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		before := ptr.Deref(deployment.Spec.Replicas, 0)
		cleanupWorkload(ctx, client, workload)
		Consistently(func(g Gomega) {
			g.Expect(tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
			g.Expect(deployment.Annotations[capiv1.PausedAnnotation]).To(Equal("true"))
			g.Expect(deployment.Spec.Replicas).To(Equal(ptr.To(before)))
		}).WithContext(ctx).WithTimeout(3 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		Expect(setPaused(nil)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(tc.MgmtClient.Get(ctx, crclient.ObjectKeyFromObject(deployment), deployment)).To(Succeed())
			g.Expect(deployment.Annotations[capiv1.PausedAnnotation]).NotTo(Equal("true"))
		}).WithContext(ctx).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
		waitForAutoscalingNodes(ctx, client, 1, selector)
	})
}
