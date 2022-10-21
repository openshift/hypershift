//go:build e2e
// +build e2e

package e2e

import (
	"context"
	_ "embed"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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

func testSetNodePoolNTOMachineConfigGetsRolledout(parentCtx context.Context, mgmtClient crclient.Client, guestCluster *hyperv1.HostedCluster, guestClient crclient.Client, clusterOpts core.CreateOptions) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		defer cancel()

		nodePools := &hyperv1.NodePoolList{}
		err := mgmtClient.List(ctx, nodePools, &crclient.ListOptions{
			Namespace: guestCluster.Namespace,
		})

		for _, nodePool := range nodePools.Items {
			err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
			t.Logf("Replacing the Upgrade Strategy to RollingUpdate")
			original := nodePool.DeepCopy()
			nodePool.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{
				Strategy: hyperv1.UpgradeStrategyRollingUpdate,
				RollingUpdate: &hyperv1.RollingUpdate{
					MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(0)),
					MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(int(*nodePool.Spec.Replicas))),
				},
			}
			err = mgmtClient.Patch(ctx, &nodePool, crclient.MergeFrom(original))
			g.Expect(err).NotTo(HaveOccurred(), "failed update NodePool replicas")
		}
		g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePools")

		// Wait for Nodes to be Ready
		numNodes := int32(globalOpts.configurableClusterOptions.NodePoolReplicas * len(clusterOpts.AWSPlatform.Zones))
		e2eutil.WaitForNReadyNodes(t, testContext, guestClient, numNodes, guestCluster.Spec.Platform.Type)

		tunedConfigConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hugepages-tuned-test",
				Namespace: guestCluster.Namespace,
			},
			Data: map[string]string{tuningConfigKey: hugepagesTuned},
		}

		err = mgmtClient.Create(ctx, tunedConfigConfigMap)
		if !errors.IsAlreadyExists(err) {
			t.Fatalf("failed to create configmap for custom Tuned object: %v", err)
		}

		for _, nodePool := range nodePools.Items {
			if nodePool.Spec.ClusterName != guestCluster.Name {
				continue
			}

			np := nodePool.DeepCopy()
			nodePool.Spec.TunedConfig = append(nodePool.Spec.TunedConfig, corev1.LocalObjectReference{Name: tunedConfigConfigMap.Name})
			if err := mgmtClient.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
				t.Fatalf("failed to update nodepool %s after adding Tuned config: %v", nodePool.Name, err)
			}
		}

		ds := ntoMachineConfigUpdatedVerificationDS.DeepCopy()
		err = guestClient.Create(ctx, ds)
		if !errors.IsAlreadyExists(err) {
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

		e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, ctx, mgmtClient, guestClient, guestCluster.Namespace)
		e2eutil.EnsureNoCrashingPods(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureHCPContainersHaveResourceRequests(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureNoPodsWithTooHighPriority(t, ctx, mgmtClient, guestCluster)
	}
}

func testSetNodePoolNTOMachineConfigAppliedInPlace(parentCtx context.Context, mgmtClient crclient.Client, guestCluster *hyperv1.HostedCluster, guestClient crclient.Client, clusterOpts core.CreateOptions) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		defer cancel()

		nodePools := &hyperv1.NodePoolList{}
		err := mgmtClient.List(ctx, nodePools, &crclient.ListOptions{
			Namespace: guestCluster.Namespace,
		})

		for _, nodePool := range nodePools.Items {
			err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePool")
			t.Logf("Replacing the Upgrade Strategy to InPlace")
			original := nodePool.DeepCopy()
			nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace
			err = mgmtClient.Patch(ctx, &nodePool, crclient.MergeFrom(original))
			g.Expect(err).NotTo(HaveOccurred(), "failed update NodePool replicas")
		}
		g.Expect(err).NotTo(HaveOccurred(), "failed to get NodePools")

		// Wait for Nodes to be Ready
		numNodes := int32(globalOpts.configurableClusterOptions.NodePoolReplicas * len(clusterOpts.AWSPlatform.Zones))
		e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, guestCluster.Spec.Platform.Type)

		tunedConfigConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hugepages-tuned-test",
				Namespace: guestCluster.Namespace,
			},
			Data: map[string]string{tunedConfigKey: hugepagesTuned},
		}

		err = mgmtClient.Create(ctx, tunedConfigConfigMap)
		if !errors.IsAlreadyExists(err) {
			t.Fatalf("failed to create configmap for custom Tuned object: %v", err)
		}

		for _, nodePool := range nodePools.Items {
			if nodePool.Spec.ClusterName != guestCluster.Name {
				continue
			}

			np := nodePool.DeepCopy()
			nodePool.Spec.TunedConfig = append(nodePool.Spec.TunedConfig, corev1.LocalObjectReference{Name: tunedConfigConfigMap.Name})
			if err := mgmtClient.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
				t.Fatalf("failed to update nodepool %s after adding Tuned config: %v", nodePool.Name, err)
			}
		}

		ds := ntoMachineConfigUpdatedVerificationDS.DeepCopy()
		err = guestClient.Create(ctx, ds)
		if !errors.IsAlreadyExists(err) {
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

		e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, ctx, mgmtClient, guestClient, guestCluster.Namespace)
		e2eutil.EnsureNoCrashingPods(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureHCPContainersHaveResourceRequests(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureNoPodsWithTooHighPriority(t, ctx, mgmtClient, guestCluster)

	}
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
