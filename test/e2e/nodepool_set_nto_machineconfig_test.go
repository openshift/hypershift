//go:build e2e
// +build e2e

package e2e

import (
	"context"
	_ "embed"
	"strings"
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
		originalNP := hyperv1.NodePool{}
		var count int32 = 0
		err := mgmtClient.List(ctx, nodePools, &crclient.ListOptions{
			Namespace: guestCluster.Namespace,
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepools")
		for _, nodePool := range nodePools.Items {
			if !strings.Contains(nodePool.Name, "-test-") {
				originalNP = nodePool
			}
		}
		g.Expect(originalNP.Name).NotTo(ContainSubstring("test"))
		awsNPInfo := originalNP.Spec.Platform.AWS

		// Define a new Nodepool
		nodePool := &hyperv1.NodePool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "NodePool",
				APIVersion: hyperv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      guestCluster.Name + "-" + "test-nto-mc-rolling-update",
				Namespace: guestCluster.Namespace,
			},
			Spec: hyperv1.NodePoolSpec{
				Management: hyperv1.NodePoolManagement{
					UpgradeType: hyperv1.UpgradeTypeReplace,
					AutoRepair:  true,
					Replace: &hyperv1.ReplaceUpgrade{
						Strategy: hyperv1.UpgradeStrategyRollingUpdate,
						RollingUpdate: &hyperv1.RollingUpdate{
							MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(0)),
							MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(int(twoReplicas))),
						},
					},
				},
				ClusterName: guestCluster.Name,
				Replicas:    &twoReplicas,
				Release: hyperv1.Release{
					Image: guestCluster.Spec.Release.Image,
				},
				Platform: hyperv1.NodePoolPlatform{
					Type: guestCluster.Spec.Platform.Type,
					AWS:  awsNPInfo,
				},
			},
		}

		// Create NodePool for current test
		err = mgmtClient.Create(ctx, nodePool)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create nodePool %s with Rolling Upgrade Strategy: %v", nodePool.Name, err)
			}

			// Update NodePool
			existantNodePool := &hyperv1.NodePool{}
			// grab the existant nodepool and store it in another variable
			err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), existantNodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepool")
			err = mgmtClient.Delete(ctx, existantNodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed to Delete the existant NodePool")
			t.Logf("waiting for NodePools in-place update with NTO-generated MachineConfig")
			err = wait.PollImmediateWithContext(ctx, 10*time.Second, 15*time.Minute, func(ctx context.Context) (bool, error) {
				if ctx.Err() != nil {
					return false, err
				}
				err = mgmtClient.Create(ctx, nodePool)
				if errors.IsAlreadyExists(err) {
					t.Logf("WARNING: NodePool still there, will retry")
					return false, nil
				}
				return true, nil
			})
			t.Logf("Nodepool Recreated")
			g.Expect(err).NotTo(HaveOccurred(), "failed to Create the NodePool")
		}

		// Wait for Nodes to be Ready
		numNodes := int32(globalOpts.configurableClusterOptions.NodePoolReplicas * len(clusterOpts.AWSPlatform.Zones))
		e2eutil.WaitForNReadyNodes(t, testContext, guestClient, numNodes, guestCluster.Spec.Platform.Type, nodePool.Name)

		tuningConfigConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hugepages-tuned-test",
				Namespace: guestCluster.Namespace,
			},
			Data: map[string]string{tuningConfigKey: hugepagesTuned},
		}

		err = mgmtClient.Create(ctx, tuningConfigConfigMap)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create configmap for custom Tuned object: %v", err)
			}
		}

		// Adding TuningConfig into NodePool
		err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
		g.Expect(err).NotTo(HaveOccurred(), "failed getting nodepool to append TuningConfig")
		np := nodePool.DeepCopy()
		nodePool.Spec.TuningConfig = append(nodePool.Spec.TuningConfig, corev1.LocalObjectReference{Name: tuningConfigConfigMap.Name})
		if err := mgmtClient.Patch(ctx, nodePool, crclient.MergeFrom(np)); err != nil {
			t.Fatalf("failed to update nodepool %s after adding TuningConfig: %v", nodePool.Name, err)
		}

		ds := ntoMachineConfigUpdatedVerificationDS.DeepCopy()
		err = guestClient.Create(ctx, ds)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create %s DaemonSet in guestcluster: %v", ds.Name, err)
			}
		}

		t.Logf("waiting for NodePools in-place update with NTO-generated MachineConfig")
		err = wait.PollImmediateWithContext(ctx, 20*time.Second, 15*time.Minute, func(ctx context.Context) (bool, error) {
			count = 0
			if ctx.Err() != nil {
				return false, err
			}
			pods := &corev1.PodList{}
			if err := guestClient.List(ctx, pods, crclient.InNamespace(ds.Namespace), crclient.MatchingLabels(ds.Spec.Selector.MatchLabels)); err != nil {
				t.Logf("WARNING: failed to list pods, will retry: %v", err)
				return false, nil
			}

			if int32(len(pods.Items)) <= numNodes {
				t.Logf("Replicas still not the same as pods: %v vs %v", len(pods.Items), numNodes)
				return false, nil
			}

			// We could have more than 1 Nodepool and the DS it's propagated to every node BUT, not in each one
			// the MC will be applied, only in 1 NodePool
			for _, pod := range pods.Items {
				if isPodReady(&pod) {
					t.Logf("Pod Ready: %v", pod.Name)
					count += 1
				}
			}

			if count < *nodePool.Spec.Replicas {
				t.Logf("Not enough pods ready: %d/%d", count, *nodePool.Spec.Replicas)
				return false, nil
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

		err = scaleDownTestNodePool(ctx, mgmtClient, nodePool)
		g.Expect(err).NotTo(HaveOccurred(), "failed Scalling down NodePool after test finished")
	}
}

func testSetNodePoolNTOMachineConfigAppliedInPlace(parentCtx context.Context, mgmtClient crclient.Client, guestCluster *hyperv1.HostedCluster, guestClient crclient.Client, clusterOpts core.CreateOptions) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		defer cancel()

		originalNP := hyperv1.NodePool{}
		nodePools := &hyperv1.NodePoolList{}
		err := mgmtClient.List(ctx, nodePools, &crclient.ListOptions{
			Namespace: guestCluster.Namespace,
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepools")
		for _, nodePool := range nodePools.Items {
			if !strings.Contains(nodePool.Name, "-test-") {
				originalNP = nodePool
			}
		}
		g.Expect(originalNP.Name).NotTo(ContainSubstring("test"))
		awsNPInfo := originalNP.Spec.Platform.AWS

		// Define a new Nodepool
		nodePool := &hyperv1.NodePool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "NodePool",
				APIVersion: hyperv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      guestCluster.Name + "-" + "test-nto-mc-update-inplace",
				Namespace: guestCluster.Namespace,
			},
			Spec: hyperv1.NodePoolSpec{
				Management: hyperv1.NodePoolManagement{
					UpgradeType: hyperv1.UpgradeTypeInPlace,
					AutoRepair:  true,
				},
				ClusterName: guestCluster.Name,
				Replicas:    &twoReplicas,
				Release: hyperv1.Release{
					Image: guestCluster.Spec.Release.Image,
				},
				Platform: hyperv1.NodePoolPlatform{
					Type: guestCluster.Spec.Platform.Type,
					AWS:  awsNPInfo,
				},
			},
		}

		// Create NodePool for current test
		err = mgmtClient.Create(ctx, nodePool)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create nodePool %s with Rolling Upgrade Strategy: %v", nodePool.Name, err)
			}

			// Update NodePool
			existantNodePool := &hyperv1.NodePool{}
			// grab the existant nodepool and store it in another variable
			err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), existantNodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepool")
			err = mgmtClient.Delete(ctx, existantNodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed to Delete the existant NodePool")
			t.Logf("waiting for NodePools in-place update with NTO-generated MachineConfig")
			err = wait.PollImmediateWithContext(ctx, 10*time.Second, 15*time.Minute, func(ctx context.Context) (bool, error) {
				if ctx.Err() != nil {
					return false, err
				}
				err = mgmtClient.Create(ctx, nodePool)
				if errors.IsAlreadyExists(err) {
					t.Logf("WARNING: NodePool still there, will retry")
					return false, nil
				}
				return true, nil
			})
			t.Logf("Nodepool Recreated")
			g.Expect(err).NotTo(HaveOccurred(), "failed to Create the NodePool")
		}

		// Wait for Nodes to be Ready
		numNodes := int32(globalOpts.configurableClusterOptions.NodePoolReplicas * len(clusterOpts.AWSPlatform.Zones))
		e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, guestCluster.Spec.Platform.Type, nodePool.Name)

		tuningConfigConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hugepages-tuned-test",
				Namespace: guestCluster.Namespace,
			},
			Data: map[string]string{tuningConfigKey: hugepagesTuned},
		}

		err = mgmtClient.Create(ctx, tuningConfigConfigMap)
		if !errors.IsAlreadyExists(err) {
			t.Fatalf("failed to create configmap for custom Tuned object: %v", err)
		}
		t.Logf("======= Finished Create TuningConfigMap")

		err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
		var count int32 = 0
		g.Expect(err).NotTo(HaveOccurred(), "failed to Get test NodePool")
		np := nodePool.DeepCopy()
		nodePool.Spec.TuningConfig = append(nodePool.Spec.TuningConfig, corev1.LocalObjectReference{Name: tuningConfigConfigMap.Name})
		if err := mgmtClient.Patch(ctx, nodePool, crclient.MergeFrom(np)); err != nil {
			t.Fatalf("failed to update nodepool %s after adding TuningConfig: %v", nodePool.Name, err)
		}

		t.Logf("======= Finished Added TuningConfig to NodePool Spec")

		ds := ntoMachineConfigUpdatedVerificationDS.DeepCopy()
		err = guestClient.Create(ctx, ds)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create %s DaemonSet in guestcluster: %v", ds.Name, err)
			}
		}

		e2eutil.WaitForNReadyNodes(t, ctx, guestClient, numNodes, guestCluster.Spec.Platform.Type, nodePool.Name)

		t.Logf("waiting for NodePools in-place update with NTO-generated MachineConfig")
		err = wait.PollImmediateWithContext(ctx, 20*time.Second, 15*time.Minute, func(ctx context.Context) (bool, error) {
			count = 0
			if ctx.Err() != nil {
				return false, err
			}
			pods := &corev1.PodList{}
			if err := guestClient.List(ctx, pods, crclient.InNamespace(ds.Namespace), crclient.MatchingLabels(ds.Spec.Selector.MatchLabels)); err != nil {
				t.Logf("WARNING: failed to list pods, will retry: %v", err)
				return false, nil
			}

			if int32(len(pods.Items)) <= numNodes {
				t.Logf("Replicas still not the same as pods: %v vs %v", len(pods.Items), numNodes)
				return false, nil
			}

			// We could have more than 1 Nodepool and the DS it's propagated to every node BUT, not in each one
			// the MC will be applied, only in 1 NodePool
			for _, pod := range pods.Items {
				if isPodReady(&pod) {
					t.Logf("Pod Ready: %v", pod.Name)
					count += 1
				}
			}

			if count < *nodePool.Spec.Replicas {
				t.Logf("Not enough pods ready: %d/%d", count, *nodePool.Spec.Replicas)
				return false, nil
			}
			return true, nil
		})
		if err != nil {
			t.Fatalf("failed waiting for all pods in the NTO MachineConfig update verification DS to be ready: %v", err)
		}

		t.Logf("======= Finished waiting all pods in NTO MachineConfig")

		e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, ctx, mgmtClient, guestClient, guestCluster.Namespace)
		e2eutil.EnsureNoCrashingPods(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureHCPContainersHaveResourceRequests(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureNoPodsWithTooHighPriority(t, ctx, mgmtClient, guestCluster)

		err = scaleDownTestNodePool(ctx, mgmtClient, nodePool)
		g.Expect(err).NotTo(HaveOccurred(), "failed Scalling down NodePool after test finished")
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

func scaleDownTestNodePool(ctx context.Context, mgmtClient crclient.Client, nodePool *hyperv1.NodePool) error {
	// Test Finished. Scalling down the NodePool to avoid waste resources
	err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
	if err != nil {
		return err
	}
	np := nodePool.DeepCopy()
	nodePool.Spec.Replicas = &zeroReplicas
	if err := mgmtClient.Patch(ctx, nodePool, crclient.MergeFrom(np)); err != nil {
		return err
	}

	return nil
}
