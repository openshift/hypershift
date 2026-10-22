//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/conditions"
	"github.com/openshift/hypershift/support/netutil"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
)

func waitForHealthControlPlaneVersion(tc *internal.TestContext, timeout, interval time.Duration) error {
	check := healthControlPlaneVersionCheck(tc)
	var lastError error
	err := wait.PollUntilContextTimeout(tc.Context, interval, timeout, true, func(ctx context.Context) (bool, error) {
		lastError = check(ctx)
		return lastError == nil, nil
	})
	return errors.Join(err, lastError)
}

func healthInitialClientReady(ctx context.Context, client crclient.Client, version semver.Version) error {
	if version.LT(semver.MustParse("4.15.0")) {
		return nil
	}
	return client.Create(ctx, &authenticationv1.SelfSubjectReview{})
}

func healthInitialAPIReady(tc *internal.TestContext) error {
	hostedCluster, err := tc.GetHostedCluster()
	if err != nil {
		return err
	}
	if !netutil.IsPublicHC(hostedCluster) {
		_, err := tc.GetHostedClusterRESTConfig(hostedCluster)
		return err
	}
	hostedClient, err := tc.GetHostedClusterClient(hostedCluster)
	if err != nil {
		return err
	}
	version, err := healthReleaseVersion(tc)
	if err != nil {
		return err
	}
	return healthInitialClientReady(tc.Context, hostedClient, version)
}

func healthInitialConfiguredWorkersReady(tc *internal.TestContext) error {
	hostedCluster, err := tc.GetHostedCluster()
	if err != nil {
		return err
	}
	pools := &hyperv1.NodePoolList{}
	if err := tc.MgmtClient.List(tc.Context, pools, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
		return err
	}
	nodes := &corev1.NodeList{}
	if netutil.IsPublicHC(hostedCluster) {
		hostedClient, err := tc.GetHostedClusterClient(hostedCluster)
		if err != nil {
			return err
		}
		if err := hostedClient.List(tc.Context, nodes); err != nil {
			return err
		}
	}
	return healthConfiguredNodePoolCounts(hostedCluster, pools.Items, nodes.Items, tc.ExpectedNodePools)
}

func healthInitialDataPlaneCompleted(hostedCluster *hyperv1.HostedCluster, expectedPools map[string]int32) error {
	for _, replicas := range expectedPools {
		if replicas <= 0 {
			continue
		}
		version := hostedCluster.Status.Version
		if version == nil || version.Desired.Image == "" || len(version.History) == 0 {
			return fmt.Errorf("configured workers require data plane desired image and history")
		}
		if version.History[0].Image != version.Desired.Image || version.History[0].State != configv1.CompletedUpdate {
			return fmt.Errorf("configured workers require latest completed data plane history matching desired image")
		}
		if !meta.IsStatusConditionTrue(hostedCluster.Status.Conditions, string(hyperv1.HostedClusterAvailable)) || !meta.IsStatusConditionPresentAndEqual(hostedCluster.Status.Conditions, string(hyperv1.HostedClusterProgressing), metav1.ConditionFalse) {
			return fmt.Errorf("configured workers require an Available and not Progressing hosted cluster")
		}
		break
	}
	return nil
}

func healthControlPlaneVersionCompleted(hostedCluster *hyperv1.HostedCluster, identity releaseImageIdentity) error {
	version := hostedCluster.Status.ControlPlaneVersion
	if !identity.Matches(version.Desired.Image) {
		return fmt.Errorf("control plane desired image must identify the requested release")
	}
	if len(version.History) == 0 || !identity.Matches(version.History[0].Image) || version.History[0].State != configv1.CompletedUpdate {
		return fmt.Errorf("latest completed control plane history must identify the requested release")
	}
	return nil
}

func healthControlPlaneVersionApplicable(tc *internal.TestContext) (bool, error) {
	version, err := healthReleaseVersion(tc)
	if err != nil {
		return false, fmt.Errorf("control plane version eligibility: %w", err)
	}
	return !version.LT(semver.MustParse("4.22.0")), nil
}

func healthReleaseVersion(tc *internal.TestContext) (semver.Version, error) {
	if failureConditionInitialReleaseImage() != "" || internal.GetEnvVarValue("E2E_CONDITION_DIAGNOSTIC_UPGRADE_TARGET") != "" {
		return failureConditionVersion(tc)
	}
	return tc.GetHostedClusterVersion()
}

func healthConditionInputs(tc *internal.TestContext) (semver.Version, bool, time.Duration, error) {
	after, err := healthAfterWorkers()
	if err != nil {
		return semver.Version{}, false, 0, err
	}
	hasWorkers := false
	if !after && tc.ExpectedNodePools != nil {
		if err := healthConfiguredNodePoolIntent(tc.ExpectedNodePools); err != nil {
			return semver.Version{}, false, 0, err
		}
		for _, replicas := range tc.ExpectedNodePools {
			hasWorkers = hasWorkers || replicas > 0
		}
	} else {
		hasWorkers, err = healthObservedWorkers(tc)
		if err != nil {
			return semver.Version{}, false, 0, err
		}
	}
	version, err := healthReleaseVersion(tc)
	if err != nil {
		return semver.Version{}, false, 0, fmt.Errorf("hosted cluster condition release: %w", err)
	}
	timeout := 10 * time.Minute
	if !after && !hasWorkers {
		timeout = 20 * time.Minute
	}
	return version, hasWorkers, timeout, nil
}

func healthAfterWorkers() (bool, error) {
	phase := internal.GetEnvVarValue("E2E_HEALTH_WORKER_PHASE")
	switch phase {
	case "":
		return false, nil
	case "after":
		return true, nil
	default:
		return false, fmt.Errorf("unsupported E2E_HEALTH_WORKER_PHASE %q: expected empty or after", phase)
	}
}

func healthObservedWorkers(tc *internal.TestContext) (bool, error) {
	if err := tc.Context.Err(); err != nil {
		return false, err
	}
	hostedCluster, err := tc.GetHostedCluster()
	if err != nil {
		return false, err
	}
	if netutil.IsPublicHC(hostedCluster) {
		hostedClient, err := tc.GetHostedClusterClient(hostedCluster)
		if err != nil {
			return false, err
		}
		nodes := &corev1.NodeList{}
		if err := hostedClient.List(tc.Context, nodes); err != nil {
			return false, err
		}
		return healthHasWorkers(hostedCluster, nil, nodes.Items), nil
	}
	pools := &hyperv1.NodePoolList{}
	if err := tc.MgmtClient.List(tc.Context, pools, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
		return false, err
	}
	return healthHasWorkers(hostedCluster, pools.Items, nil), nil
}

func healthConditionsCheck(tc *internal.TestContext, version semver.Version, hasWorkers bool) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		hostedCluster := &hyperv1.HostedCluster{}
		if err := tc.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}, hostedCluster); err != nil {
			return fmt.Errorf("read hosted cluster for condition health: %w", err)
		}
		return failureConditionMismatch(hostedCluster, hasWorkers, version, tc.ConditionDiagnosticUpgradeTarget != "", false)
	}
}

func healthControlPlaneVersionCheck(tc *internal.TestContext) func(context.Context) error {
	latched := false
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		hostedCluster := &hyperv1.HostedCluster{}
		if err := tc.MgmtClient.Get(ctx, crclient.ObjectKey{Namespace: tc.ClusterNamespace, Name: tc.ClusterName}, hostedCluster); err != nil {
			return fmt.Errorf("read hosted cluster for control plane version: %w", err)
		}
		if !latched {
			ready, err := failureControlPlaneVersionReady(ctx, tc, hostedCluster)
			if err != nil {
				return err
			}
			if !ready {
				return nil
			}
			latched = true
		}
		identity, err := hostedReleaseImageIdentity(ctx, tc.MgmtClient, hostedCluster, hostedCluster.Spec.Release.Image)
		if err != nil {
			return err
		}
		return healthControlPlaneVersionCompleted(hostedCluster, identity)
	}
}

func healthControlPlaneVersionTimeout(expectedPools map[string]int32) (time.Duration, error) {
	after, err := healthAfterWorkers()
	if err != nil {
		return 0, err
	}
	if after {
		return 10 * time.Minute, nil
	}
	if expectedPools == nil {
		return 10 * time.Minute, nil
	}
	if err := healthConfiguredNodePoolIntent(expectedPools); err != nil {
		return 0, err
	}
	for _, replicas := range expectedPools {
		if replicas > 0 {
			return 10 * time.Minute, nil
		}
	}
	return 20 * time.Minute, nil
}

func healthNodePoolCounts(hostedCluster *hyperv1.HostedCluster, pools []hyperv1.NodePool, nodes []corev1.Node) error {
	public := netutil.IsPublicHC(hostedCluster)
	expectedTotal := int32(0)
	for _, pool := range pools {
		if pool.Spec.ClusterName != hostedCluster.Name {
			continue
		}
		expected := pool.Status.Replicas
		if pool.Spec.Replicas != nil {
			expected = *pool.Spec.Replicas
			if pool.Status.Replicas != expected {
				return fmt.Errorf("NodePool %s reports %d replicas, expected %d", pool.Name, pool.Status.Replicas, expected)
			}
		} else if pool.Spec.AutoScaling != nil {
			if expected > pool.Spec.AutoScaling.Max || pool.Spec.AutoScaling.Min != nil && expected < *pool.Spec.AutoScaling.Min {
				return fmt.Errorf("NodePool %s replicas %d outside autoscaling bounds", pool.Name, expected)
			}
		} else {
			return fmt.Errorf("NodePool %s has neither replicas nor autoscaling", pool.Name)
		}
		expectedTotal += expected
		if public {
			actual := int32(0)
			for _, node := range nodes {
				if node.Labels[hyperv1.NodePoolLabel] == pool.Name {
					actual++
				}
			}
			if actual != expected {
				return fmt.Errorf("NodePool %s has %d nodes, expected %d", pool.Name, actual, expected)
			}
		}
	}
	if !public {
		return nil
	}
	if hostedCluster.Spec.AutoNode.Provisioner.Name != hyperv1.ProvisionerKarpenter && len(nodes) != int(expectedTotal) {
		return fmt.Errorf("hosted cluster has %d nodes, expected %d from NodePools", len(nodes), expectedTotal)
	}
	for _, node := range nodes {
		ready := false
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				ready = true
			}
		}
		if !ready {
			return fmt.Errorf("node %s is not Ready", node.Name)
		}
	}
	return nil
}

func healthConfiguredNodePoolIntent(expectedPools map[string]int32) error {
	if expectedPools == nil {
		return fmt.Errorf("configured worker validation requires independent initial NodePool creation intent")
	}
	for name, replicas := range expectedPools {
		if name == "" || replicas < 0 {
			return fmt.Errorf("initial NodePool intent requires nonempty names and nonnegative replicas")
		}
	}
	return nil
}

func healthConfiguredNodePoolCounts(hostedCluster *hyperv1.HostedCluster, pools []hyperv1.NodePool, nodes []corev1.Node, expectedPools map[string]int32) error {
	if err := healthConfiguredNodePoolIntent(expectedPools); err != nil {
		return err
	}
	expectedTotal := int64(0)
	for name, replicas := range expectedPools {
		expectedTotal += int64(replicas)
		found := false
		for _, pool := range pools {
			if pool.Name != name {
				continue
			}
			if pool.Spec.ClusterName != hostedCluster.Name {
				return fmt.Errorf("configured NodePool %s belongs to cluster %s, expected %s", name, pool.Spec.ClusterName, hostedCluster.Name)
			}
			if pool.Spec.Replicas == nil || *pool.Spec.Replicas != replicas {
				return fmt.Errorf("configured NodePool %s must retain initial replica intent %d at initial root validation", name, replicas)
			}
			found = true
		}
		if !found {
			return fmt.Errorf("configured NodePool %s is missing", name)
		}
	}
	for _, pool := range pools {
		if pool.Spec.ClusterName != hostedCluster.Name {
			continue
		}
		if _, expected := expectedPools[pool.Name]; !expected {
			return fmt.Errorf("unexpected NodePool %s belongs to cluster %s", pool.Name, hostedCluster.Name)
		}
	}
	if netutil.IsPublicHC(hostedCluster) && int64(len(nodes)) != expectedTotal {
		return fmt.Errorf("initial hosted cluster has %d nodes, expected %d from configured NodePool intent", len(nodes), expectedTotal)
	}
	return healthNodePoolCounts(hostedCluster, pools, nodes)
}

func healthExpectedConditions(hostedCluster *hyperv1.HostedCluster, hasWorkers bool, version semver.Version) map[hyperv1.ConditionType]metav1.ConditionStatus {
	expected := conditions.ExpectedHCConditions(hostedCluster)
	delete(expected, hyperv1.KubeVirtNodesLiveMigratable)
	if !hasWorkers {
		expected[hyperv1.ClusterVersionAvailable] = metav1.ConditionFalse
		expected[hyperv1.ClusterVersionSucceeding] = metav1.ConditionFalse
		expected[hyperv1.ClusterVersionProgressing] = metav1.ConditionTrue
		expected[hyperv1.DataPlaneConnectionAvailable] = metav1.ConditionUnknown
		expected[hyperv1.ControlPlaneConnectionAvailable] = metav1.ConditionUnknown
		delete(expected, hyperv1.ValidKubeVirtInfraNetworkMTU)
		delete(expected, hyperv1.ValidKubeVirtInfraNetworkPolicyRBAC)
	}
	if version.LT(semver.MustParse("4.15.0")) {
		delete(expected, hyperv1.ValidKubeVirtInfraNetworkMTU)
	}
	if version.LT(semver.MustParse("4.21.0")) {
		delete(expected, hyperv1.DataPlaneConnectionAvailable)
	}
	if version.LT(semver.MustParse("4.22.0")) {
		delete(expected, hyperv1.ControlPlaneConnectionAvailable)
		delete(expected, hyperv1.ValidKubeVirtInfraNetworkPolicyRBAC)
	}
	if version.LT(semver.MustParse("4.23.0")) {
		delete(expected, hyperv1.ConfigOperatorReconciliationSucceeded)
	}
	return expected
}

func healthHasWorkers(hostedCluster *hyperv1.HostedCluster, nodePools []hyperv1.NodePool, nodes []corev1.Node) bool {
	if netutil.IsPublicHC(hostedCluster) {
		return len(nodes) > 0
	}
	for _, pool := range nodePools {
		if pool.Spec.ClusterName != hostedCluster.Name {
			continue
		}
		if pool.Spec.Replicas != nil && *pool.Spec.Replicas > 0 || pool.Spec.AutoScaling != nil && pool.Spec.AutoScaling.Max > 0 {
			return true
		}
	}
	return false
}
