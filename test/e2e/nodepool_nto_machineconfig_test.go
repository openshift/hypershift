//go:build e2e
// +build e2e

package e2e

import (
	"context"
	_ "embed"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	hugepagesTuned = `apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  name: hugepages
  namespace: openshift-cluster-node-tuning-operator
spec:
  profile:
  - data: |
      [main]
      summary=Boot time configuration for hugepages
      include=openshift-node
      [bootloader]
      cmdline_openshift_node_hugepages=hugepagesz=2M hugepages=4
    name: openshift-hugepages
  recommend:
  - priority: 20
    profile: openshift-hugepages
`

	hypershiftNodePoolNameLabel = "hypershift.openshift.io/nodePoolName" // HyperShift-enabled NTO adds this label to Tuned CRs bound to NodePools
	tuningConfigKey             = "tuning"
)

func TestNTOMachineConfigGetsRolledOut(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.BeforeApply = func(o crclient.Object) {
		nodePool, isNodepool := o.(*hyperv1.NodePool)
		if !isNodepool {
			return
		}
		nodePool.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{
			Strategy: hyperv1.UpgradeStrategyRollingUpdate,
			RollingUpdate: &hyperv1.RollingUpdate{
				MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(0)),
				MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(int(*nodePool.Spec.Replicas))),
			},
		}
	}

	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir)

	// Sanity check the cluster by waiting for the nodes to report ready
	t.Logf("Waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, testContext, client, hostedCluster)

	// Wait for Nodes to be Ready
	e2eutil.WaitForNReadyNodes(t, testContext, guestClient, clusterOpts.NodePoolReplicas, hostedCluster.Spec.Platform.Type)

	// Wait for the rollout to be complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, guestClient, hostedCluster, globalOpts.LatestReleaseImage)
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	tuningConfigConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hugepages-tuned-test",
			Namespace: hostedCluster.Namespace,
		},
		Data: map[string]string{tuningConfigKey: hugepagesTuned},
	}
	if err := client.Create(ctx, tuningConfigConfigMap); err != nil {
		t.Fatalf("failed to create configmap for custom Tuned object: %v", err)
	}

	nodePools := &hyperv1.NodePoolList{}
	if err := client.List(ctx, nodePools, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
		t.Fatalf("failed to list nodepools in namespace %s: %v", hostedCluster.Namespace, err)
	}

	var nodePool hyperv1.NodePool
	for _, nodePool = range nodePools.Items {
		if nodePool.Spec.ClusterName != hostedCluster.Name {
			continue
		}

		np := nodePool.DeepCopy()
		nodePool.Spec.TuningConfig = append(nodePool.Spec.TuningConfig, corev1.LocalObjectReference{Name: tuningConfigConfigMap.Name})
		if err := client.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
			t.Fatalf("failed to update nodepool %s after adding Tuned config: %v", nodePool.Name, err)
		}
	}

	ds := ntoMachineConfigUpdatedVerificationDS.DeepCopy()
	if err := guestClient.Create(ctx, ds); err != nil {
		t.Fatalf("failed to create %s DaemonSet in guestcluster: %v", ds.Name, err)
	}

	t.Logf("waiting for rollout of NodePools with NTO-generated config")
	err = wait.PollImmediateWithContext(ctx, 5*time.Second, 15*time.Minute, func(ctx context.Context) (bool, error) {
		if ctx.Err() != nil {
			return false, err
		}
		pods := &corev1.PodList{}
		if err := guestClient.List(ctx, pods, crclient.InNamespace(ds.Namespace), crclient.MatchingLabels(ds.Spec.Selector.MatchLabels)); err != nil {
			t.Logf("WARNING: failed to list pods, will retry: %v", err)
			return false, nil
		}
		nodes := &corev1.NodeList{}
		if err := guestClient.List(ctx, nodes); err != nil {
			t.Logf("WARNING: failed to list nodes, will retry: %v", err)
			return false, nil
		}
		if len(pods.Items) != len(nodes.Items) {
			return false, nil
		}

		for _, pod := range pods.Items {
			if !isPodReady(&pod) {
				return false, nil
			}
		}

		return true, nil
	})
	if err != nil {
		t.Fatalf("failed waiting for all pods in the NTO MachineConfig update verification DS to be ready: %v", err)
	}

	e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, testContext, client, guestClient, hostedCluster.Namespace)
	e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
	e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, ctx, client, hostedCluster)
	e2eutil.EnsureHCPContainersHaveResourceRequests(t, ctx, client, hostedCluster)
	e2eutil.EnsureNoPodsWithTooHighPriority(t, ctx, client, hostedCluster)
}

func TestNTOMachineConfigAppliedInPlace(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "failed to get k8s client")

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.BeforeApply = func(o crclient.Object) {
		nodePool, isNodepool := o.(*hyperv1.NodePool)
		if !isNodepool {
			return
		}
		nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace
	}

	hostedCluster := e2eutil.CreateCluster(t, ctx, client, &clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir)

	// Sanity check the cluster by waiting for the nodes to report ready
	t.Logf("Waiting for guest client to become available")
	guestClient := e2eutil.WaitForGuestClient(t, testContext, client, hostedCluster)

	// Wait for Nodes to be Ready
	e2eutil.WaitForNReadyNodes(t, testContext, guestClient, clusterOpts.NodePoolReplicas, hostedCluster.Spec.Platform.Type)

	// Wait for the rollout to be complete
	t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
	e2eutil.WaitForImageRollout(t, testContext, client, guestClient, hostedCluster, globalOpts.LatestReleaseImage)
	err = client.Get(testContext, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get hostedcluster")

	tuningConfigConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hugepages-tuned-test",
			Namespace: hostedCluster.Namespace,
		},
		Data: map[string]string{tuningConfigKey: hugepagesTuned},
	}
	if err := client.Create(ctx, tuningConfigConfigMap); err != nil {
		t.Fatalf("failed to create configmap for custom Tuned object: %v", err)
	}

	nodePools := &hyperv1.NodePoolList{}
	if err := client.List(ctx, nodePools, crclient.InNamespace(hostedCluster.Namespace)); err != nil {
		t.Fatalf("failed to list nodepools in namespace %s: %v", hostedCluster.Namespace, err)
	}

	var nodePool hyperv1.NodePool
	for _, nodePool = range nodePools.Items {
		if nodePool.Spec.ClusterName != hostedCluster.Name {
			continue
		}

		np := nodePool.DeepCopy()
		nodePool.Spec.TuningConfig = append(nodePool.Spec.TuningConfig, corev1.LocalObjectReference{Name: tuningConfigConfigMap.Name})
		if err := client.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
			t.Fatalf("failed to update nodepool %s after adding Tuned config: %v", nodePool.Name, err)
		}
	}

	ds := ntoMachineConfigUpdatedVerificationDS.DeepCopy()
	if err := guestClient.Create(ctx, ds); err != nil {
		t.Fatalf("failed to create %s DaemonSet in guestcluster: %v", ds.Name, err)
	}

	t.Logf("waiting for NodePools in-place update with NTO-generated MachineConfig")
	err = wait.PollImmediateWithContext(ctx, 5*time.Second, 15*time.Minute, func(ctx context.Context) (bool, error) {
		if ctx.Err() != nil {
			return false, err
		}
		pods := &corev1.PodList{}
		if err := guestClient.List(ctx, pods, crclient.InNamespace(ds.Namespace), crclient.MatchingLabels(ds.Spec.Selector.MatchLabels)); err != nil {
			t.Logf("WARNING: failed to list pods, will retry: %v", err)
			return false, nil
		}
		nodes := &corev1.NodeList{}
		if err := guestClient.List(ctx, nodes); err != nil {
			t.Logf("WARNING: failed to list nodes, will retry: %v", err)
			return false, nil
		}
		if len(pods.Items) != len(nodes.Items) {
			return false, nil
		}

		for _, pod := range pods.Items {
			if !isPodReady(&pod) {
				return false, nil
			}
		}

		return true, nil
	})
	if err != nil {
		t.Fatalf("failed waiting for all pods in the NTO MachineConfig update verification DS to be ready: %v", err)
	}

	e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, testContext, client, guestClient, hostedCluster.Namespace)
	e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
	e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, ctx, client, hostedCluster)
	e2eutil.EnsureHCPContainersHaveResourceRequests(t, ctx, client, hostedCluster)
	e2eutil.EnsureNoPodsWithTooHighPriority(t, ctx, client, hostedCluster)
}

//go:embed nodepool_nto_machineconfig_verification_ds.yaml
var ntoMachineConfigUpdatedVerificationDSRaw []byte

var ntoMachineConfigUpdatedVerificationDS = func() *appsv1.DaemonSet {
	ds := &appsv1.DaemonSet{}
	if err := yaml.Unmarshal(ntoMachineConfigUpdatedVerificationDSRaw, &ds); err != nil {
		panic(err)
	}
	return ds
}()
