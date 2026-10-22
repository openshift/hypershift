//go:build e2ev2

package tests

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
	"github.com/google/uuid"
)

type nodePoolPredicate[Item any] func(Item) (bool, string, error)

func nodePoolCloudVersionApplicable(testCtx *internal.TestContext, minimum semver.Version) (bool, error) {
	version, err := healthReleaseVersion(testCtx)
	if err != nil {
		return false, err
	}
	return !version.LT(minimum), nil
}

type nodePoolPollOptions struct {
	interval time.Duration
	timeout  time.Duration
}

type nodePoolPollOption func(*nodePoolPollOptions)

func nodePoolPollInterval(interval time.Duration) nodePoolPollOption {
	return func(options *nodePoolPollOptions) { options.interval = interval }
}

func nodePoolPollTimeout(timeout time.Duration) nodePoolPollOption {
	return func(options *nodePoolPollOptions) { options.timeout = timeout }
}

func nodePoolEventuallyObject[Item crclient.Object](ctx context.Context, objective string, getter func(context.Context) (Item, error), predicates []nodePoolPredicate[Item], options ...nodePoolPollOption) {
	GinkgoHelper()
	settings := nodePoolPollOptions{interval: 3 * time.Second, timeout: 10 * time.Minute}
	for _, option := range options {
		option(&settings)
	}
	Eventually(func(g Gomega) {
		object, err := getter(ctx)
		g.Expect(err).NotTo(HaveOccurred(), objective)
		for _, predicate := range predicates {
			complete, reason, err := predicate(object)
			g.Expect(err).NotTo(HaveOccurred(), objective)
			g.Expect(complete).To(BeTrue(), "%s: %s", objective, reason)
		}
	}).WithContext(ctx).WithTimeout(settings.timeout).WithPolling(settings.interval).Should(Succeed(), objective)
}

func nodePoolEventuallyObjects[Item crclient.Object](ctx context.Context, objective string, getter func(context.Context) ([]Item, error), groupPredicates []nodePoolPredicate[[]Item], predicates []nodePoolPredicate[Item], options ...nodePoolPollOption) {
	GinkgoHelper()
	settings := nodePoolPollOptions{interval: 3 * time.Second, timeout: 10 * time.Minute}
	for _, option := range options {
		option(&settings)
	}
	Eventually(func(g Gomega) {
		objects, err := getter(ctx)
		g.Expect(err).NotTo(HaveOccurred(), objective)
		for _, predicate := range groupPredicates {
			complete, reason, err := predicate(objects)
			g.Expect(err).NotTo(HaveOccurred(), objective)
			g.Expect(complete).To(BeTrue(), "%s: %s", objective, reason)
		}
		if len(predicates) > 0 {
			g.Expect(objects).NotTo(BeEmpty(), objective)
		}
		for _, object := range objects {
			for _, predicate := range predicates {
				complete, reason, err := predicate(object)
				g.Expect(err).NotTo(HaveOccurred(), objective)
				g.Expect(complete).To(BeTrue(), "%s: %s", objective, reason)
			}
		}
	}).WithContext(ctx).WithTimeout(settings.timeout).WithPolling(settings.interval).Should(Succeed(), objective)
}

func nodePoolConditionPredicate[Item crclient.Object](expected v2util.Condition) nodePoolPredicate[Item] {
	return func(object Item) (bool, string, error) {
		conditions, err := v2util.Conditions(object)
		if err != nil {
			return false, "", err
		}
		for _, actual := range conditions {
			if actual.Type == expected.Type {
				return expected.Matches(actual), fmt.Sprintf("expected %s, got %s", expected, actual), nil
			}
		}
		return false, fmt.Sprintf("missing condition %s", expected.Type), nil
	}
}

func nodePoolOSImageStreamPredicate(expected string) nodePoolPredicate[*hyperv1.NodePool] {
	return func(pool *hyperv1.NodePool) (bool, string, error) {
		return pool.Status.OSImageStream.Name == expected, fmt.Sprintf("expected stream %q, got %q", expected, pool.Status.OSImageStream.Name), nil
	}
}

func nodePoolUpdateObject[Item crclient.Object](ctx context.Context, client crclient.Client, object Item, mutate func(Item)) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := client.Get(ctx, crclient.ObjectKeyFromObject(object), object); err != nil {
			return err
		}
		original := object.DeepCopyObject().(Item)
		mutate(object)
		return client.Patch(ctx, object, crclient.MergeFromWithOptions(original, crclient.MergeFromWithOptimisticLock{}))
	})
}

func nodePoolEventuallyNotFound(ctx context.Context, client crclient.Client, object crclient.Object, options ...nodePoolPollOption) {
	GinkgoHelper()
	settings := nodePoolPollOptions{interval: 3 * time.Second, timeout: 10 * time.Minute}
	for _, option := range options {
		option(&settings)
	}
	Eventually(func() bool {
		return apierrors.IsNotFound(client.Get(ctx, crclient.ObjectKeyFromObject(object), object))
	}).WithContext(ctx).WithTimeout(settings.timeout).WithPolling(settings.interval).Should(BeTrue(), "expected %T %s to be deleted", object, crclient.ObjectKeyFromObject(object))
}

func nodePoolWaitForReadyNodes(ctx context.Context, client crclient.Client, pool *hyperv1.NodePool, platform hyperv1.PlatformType) []corev1.Node {
	GinkgoHelper()
	var nodes []corev1.Node
	Eventually(func(g Gomega) {
		list := &corev1.NodeList{}
		g.Expect(client.List(ctx, list, crclient.MatchingLabels{hyperv1.NodePoolLabel: pool.Name})).To(Succeed())
		g.Expect(pool.Spec.Replicas).NotTo(BeNil())
		g.Expect(*pool.Spec.Replicas).To(BeNumerically(">", 0))
		g.Expect(list.Items).To(HaveLen(int(*pool.Spec.Replicas)))
		for _, node := range list.Items {
			g.Expect(node.DeletionTimestamp).To(BeNil())
			g.Expect(awsNodeHealthy(&node)).To(Succeed())
			g.Expect(awsNodeMetadataMatches(&node, pool)).To(Succeed())
		}
		nodes = list.Items
	}).WithContext(ctx).WithTimeout(nodePoolUpgradeTimeout(platform) + 10*time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	return nodes
}

func nodePoolWaitForConfigUpdate(ctx context.Context, client crclient.Client, pool *hyperv1.NodePool, platform hyperv1.PlatformType) {
	GinkgoHelper()
	getter := func(ctx context.Context) (*hyperv1.NodePool, error) {
		current := &hyperv1.NodePool{}
		err := client.Get(ctx, crclient.ObjectKeyFromObject(pool), current)
		return current, err
	}
	nodePoolEventuallyObject(ctx, "NodePool config Updating=True followed by False", getter,
		[]nodePoolPredicate[*hyperv1.NodePool]{nodePoolUpdatingTransitionPredicate(hyperv1.NodePoolUpdatingConfigConditionType)},
		nodePoolPollTimeout(nodePoolUpgradeTimeout(platform)+5*time.Minute), nodePoolPollInterval(15*time.Second))
}

func nodePoolUpdatingTransitionPredicate(conditionType string) nodePoolPredicate[*hyperv1.NodePool] {
	observedUpdating := false
	return func(pool *hyperv1.NodePool) (bool, string, error) {
		condition := awsPoolCondition(pool, conditionType)
		if condition == nil {
			return false, fmt.Sprintf("missing condition %s", conditionType), nil
		}
		if condition.Status == corev1.ConditionTrue {
			observedUpdating = true
			return false, "observed Updating=True; waiting for completion", nil
		}
		if !observedUpdating {
			return false, "must observe Updating=True before completion", nil
		}
		return condition.Status == corev1.ConditionFalse, fmt.Sprintf("expected Updating=False after True, got %s", condition.Status), nil
	}
}

func cleanupNativeNodePool(ctx context.Context, client crclient.Client, pool *hyperv1.NodePool) {
	GinkgoHelper()
	err := client.Delete(ctx, pool)
	if !apierrors.IsNotFound(err) {
		Expect(err).NotTo(HaveOccurred(), "cleanup: delete NodePool %s", pool.Name)
	}
	nodePoolEventuallyNotFound(ctx, client, pool, nodePoolPollTimeout(nodePoolUpgradeTimeout(pool.Spec.Platform.Type)), nodePoolPollInterval(15*time.Second))
}

func nodePoolUniqueName(prefix string) string {
	if len(prefix) > 54 {
		prefix = prefix[:54]
	}
	prefix = strings.Trim(prefix, "-")
	if prefix == "" {
		prefix = "nodepool"
	}
	return prefix + "-" + uuid.NewString()[:8]
}

func cleanupNodePoolObject(ctx context.Context, client crclient.Client, object crclient.Object) {
	GinkgoHelper()
	err := client.Delete(ctx, object)
	if !apierrors.IsNotFound(err) {
		Expect(err).NotTo(HaveOccurred(), "cleanup: delete %T %s", object, crclient.ObjectKeyFromObject(object))
	}
}

func nodePoolTemplate(ctx context.Context, client crclient.Client, hc *hyperv1.HostedCluster) *hyperv1.NodePool {
	GinkgoHelper()
	pools := &hyperv1.NodePoolList{}
	Expect(client.List(ctx, pools, crclient.InNamespace(hc.Namespace))).To(Succeed())
	var fallback *hyperv1.NodePool
	for index := range pools.Items {
		pool := &pools.Items[index]
		if pool.Spec.ClusterName != hc.Name || pool.DeletionTimestamp != nil {
			continue
		}
		if pool.Status.Replicas > 0 {
			return pool
		}
		if fallback == nil {
			fallback = pool
		}
	}
	Expect(fallback).NotTo(BeNil(), "expected a template NodePool for %s/%s, including a zero-worker template", hc.Namespace, hc.Name)
	return fallback
}

func nodePoolWaitForInitialState(testCtx *internal.TestContext, hc *hyperv1.HostedCluster, client crclient.Client, pool *hyperv1.NodePool) []corev1.Node {
	GinkgoHelper()
	expectAWSNodePoolProvisioningConditions(testCtx, pool)
	nodes := nodePoolWaitForReadyNodes(testCtx.Context, client, pool, hc.Spec.Platform.Type)
	expectAWSNodePoolSteadyState(testCtx, pool)
	waitForRollout, err := nodePoolLegacyRolloutRequired(testCtx)
	Expect(err).NotTo(HaveOccurred(), "resolve release for the legacy NodePool rollout wait")
	if waitForRollout {
		ExpectHostedClusterUpgradeToComplete(testCtx.Context, testCtx.MgmtClient, hc, hc.Spec.Release.Image)
	}
	return nodes
}

func nodePoolLegacyRolloutRequired(testCtx *internal.TestContext) (bool, error) {
	version, err := healthReleaseVersion(testCtx)
	if err != nil {
		return false, err
	}
	return version.LT(semver.MustParse("4.17.0")), nil
}

func latestNodePoolMachineSet(machineSets []capiv1.MachineSet) (string, error) {
	var latest string
	var maximum int64 = -1
	for _, machineSet := range machineSets {
		revision, err := strconv.ParseInt(machineSet.Annotations[capiv1.RevisionAnnotation], 10, 64)
		if err == nil && revision > maximum {
			maximum = revision
			latest = machineSet.Name
		}
	}
	if latest == "" {
		return "", fmt.Errorf("no MachineSet has a valid revision")
	}
	return latest, nil
}
