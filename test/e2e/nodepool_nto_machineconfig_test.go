//go:build e2e
// +build e2e

package e2e

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
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

func testNTOMachineConfigGetsRolledOut(parentCtx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts core.CreateOptions) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		ctx, cancel := context.WithCancel(parentCtx)
		originalNP := hyperv1.NodePool{}
		defer func() {
			t.Log("Test: NTO MachineConfig Replace finished")
			cancel()
		}()

		// List NodePools (should exists only one)
		nodePools := &hyperv1.NodePoolList{}
		err := mgmtClient.List(ctx, nodePools, &crclient.ListOptions{
			Namespace: hostedCluster.Namespace,
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepools")
		for _, nodePool := range nodePools.Items {
			if !strings.Contains(nodePool.Name, "-test-") {
				originalNP = nodePool
			}
		}
		g.Expect(originalNP.Name).NotTo(BeEmpty())
		g.Expect(originalNP.Name).NotTo(ContainSubstring("test"))
		awsNPInfo := originalNP.Spec.Platform.AWS

		// Define a new Nodepool
		nodePool := &hyperv1.NodePool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "NodePool",
				APIVersion: hyperv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostedCluster.Name + "-" + "test-ntomachineconfig-replace",
				Namespace: hostedCluster.Namespace,
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
				ClusterName: hostedCluster.Name,
				Replicas:    &twoReplicas,
				Release: hyperv1.Release{
					Image: hostedCluster.Spec.Release.Image,
				},
				Platform: hyperv1.NodePoolPlatform{
					Type: hostedCluster.Spec.Platform.Type,
					AWS:  awsNPInfo,
				},
			},
		}
		// Create NodePool for current test
		err = mgmtClient.Create(ctx, nodePool)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create nodePool %s with Autorepair function: %v", nodePool.Name, err)
			}
			err = nodePoolRecreate(t, ctx, nodePool, mgmtClient)
			g.Expect(err).NotTo(HaveOccurred(), "failed to Create the NodePool")
		}
		defer nodePoolScaleDownToZero(ctx, mgmtClient, *nodePool, t)

		numNodes := twoReplicas

		t.Logf("Waiting for Nodes %d\n", numNodes)
		nodes := e2eutil.WaitForNReadyNodesByNodePool(t, ctx, hostedClusterClient, numNodes, hostedCluster.Spec.Platform.Type, nodePool.Name)
		t.Logf("Desired replicas available for nodePool: %v", nodePool.Name)

		// Wait for the rollout to be reported complete
		t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)

		e2eutil.WaitForImageRollout(t, ctx, mgmtClient, hostedCluster, globalOpts.LatestReleaseImage)

		tuningConfigConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hugepages-tuned-test",
				Namespace: hostedCluster.Namespace,
			},
			Data: map[string]string{tuningConfigKey: hugepagesTuned},
		}
		if err := mgmtClient.Create(ctx, tuningConfigConfigMap); err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create configmap for custom Tuned object: %v", err)
			}
		}

		np := nodePool.DeepCopy()
		nodePool.Spec.TuningConfig = append(nodePool.Spec.TuningConfig, corev1.LocalObjectReference{Name: tuningConfigConfigMap.Name})
		if err := mgmtClient.Patch(ctx, nodePool, crclient.MergeFrom(np)); err != nil {
			t.Fatalf("failed to update nodepool %s after adding Tuned config: %v", nodePool.Name, err)
		}

		// DS Customization
		ds := ntoMachineConfigUpdatedVerificationDS.DeepCopy()
		dsName := ds.Name + "-replace"

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

		if err := hostedClusterClient.Create(ctx, ds); err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create %s DaemonSet in guestcluster: %v", ds.Name, err)
			}
		}

		t.Logf("waiting for rollout of NodePools with NTO-generated config")
		err = wait.PollImmediateWithContext(ctx, 10*time.Second, 15*time.Minute, func(ctx context.Context) (bool, error) {
			if ctx.Err() != nil {
				return false, err
			}
			pods := &corev1.PodList{}
			if err := hostedClusterClient.List(ctx, pods, crclient.InNamespace(ds.Namespace), crclient.MatchingLabels(ds.Spec.Selector.MatchLabels)); err != nil {
				t.Logf("WARNING: failed to list pods, will retry: %v", err)
				return false, nil
			}

			if len(pods.Items) != len(nodes) {
				return false, nil
			}

			for _, pod := range pods.Items {
				if !isPodReady(&pod) {
					return false, nil
				}
			}

			return true, nil
		})

		g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed waiting for all pods in the NTO MachineConfig update verification DS to be ready: %v", err))
		g.Expect(nodePool.Status.Replicas).To(BeEquivalentTo(len(nodes)))
		e2eutil.EnsureNoCrashingPods(t, ctx, mgmtClient, hostedCluster)
		e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, ctx, mgmtClient, hostedCluster)
		e2eutil.EnsureHCPContainersHaveResourceRequests(t, ctx, mgmtClient, hostedCluster)
		e2eutil.EnsureNoPodsWithTooHighPriority(t, ctx, mgmtClient, hostedCluster)
	}
}

func testNTOMachineConfigAppliedInPlace(parentCtx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts core.CreateOptions) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		ctx, cancel := context.WithCancel(parentCtx)
		originalNP := hyperv1.NodePool{}
		defer func() {
			t.Log("Test: NTO MachineConfig InPlace finished")
			cancel()
		}()

		// List NodePools (should exists only one)
		nodePools := &hyperv1.NodePoolList{}
		err := mgmtClient.List(ctx, nodePools, &crclient.ListOptions{
			Namespace: hostedCluster.Namespace,
		})
		g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepools")
		for _, nodePool := range nodePools.Items {
			if !strings.Contains(nodePool.Name, "-test-") {
				originalNP = nodePool
			}
		}
		g.Expect(originalNP.Name).NotTo(BeEmpty())
		g.Expect(originalNP.Name).NotTo(ContainSubstring("test"))
		awsNPInfo := originalNP.Spec.Platform.AWS

		// Define a new Nodepool
		nodePool := &hyperv1.NodePool{
			TypeMeta: metav1.TypeMeta{
				Kind:       "NodePool",
				APIVersion: hyperv1.GroupVersion.String(),
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      hostedCluster.Name + "-" + "test-ntomachineconfig-inplace",
				Namespace: hostedCluster.Namespace,
			},
			Spec: hyperv1.NodePoolSpec{
				Management: hyperv1.NodePoolManagement{
					UpgradeType: hyperv1.UpgradeTypeInPlace,
					AutoRepair:  true,
				},
				ClusterName: hostedCluster.Name,
				Replicas:    &twoReplicas,
				Release: hyperv1.Release{
					Image: hostedCluster.Spec.Release.Image,
				},
				Platform: hyperv1.NodePoolPlatform{
					Type: hostedCluster.Spec.Platform.Type,
					AWS:  awsNPInfo,
				},
			},
		}
		// Create NodePool for current test
		err = mgmtClient.Create(ctx, nodePool)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create nodePool %s with Autorepair function: %v", nodePool.Name, err)
			}
			err = nodePoolRecreate(t, ctx, nodePool, mgmtClient)
			g.Expect(err).NotTo(HaveOccurred(), "failed to Create the NodePool")
		}
		defer nodePoolScaleDownToZero(ctx, mgmtClient, *nodePool, t)

		numNodes := twoReplicas

		t.Logf("Waiting for Nodes %d\n", numNodes)
		nodes := e2eutil.WaitForNReadyNodesByNodePool(t, ctx, hostedClusterClient, numNodes, hostedCluster.Spec.Platform.Type, nodePool.Name)
		t.Logf("Desired replicas available for nodePool: %v", nodePool.Name)

		// Wait for the rollout to be reported complete
		t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
		e2eutil.WaitForImageRollout(t, ctx, mgmtClient, hostedCluster, globalOpts.LatestReleaseImage)

		tuningConfigConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hugepages-tuned-test-inplace",
				Namespace: hostedCluster.Namespace,
			},
			Data: map[string]string{tuningConfigKey: hugepagesTuned},
		}

		if err := mgmtClient.Create(ctx, tuningConfigConfigMap); err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create configmap for custom Tuned object: %v", err)
			}
		}

		np := nodePool.DeepCopy()
		nodePool.Spec.TuningConfig = append(nodePool.Spec.TuningConfig, corev1.LocalObjectReference{Name: tuningConfigConfigMap.Name})
		if err := mgmtClient.Patch(ctx, nodePool, crclient.MergeFrom(np)); err != nil {
			t.Fatalf("failed to update nodepool %s after adding Tuned config: %v", nodePool.Name, err)
		}

		// DS Customization
		ds := ntoMachineConfigUpdatedVerificationDS.DeepCopy()
		dsName := ds.Name + "-inplace"

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

		if err := hostedClusterClient.Create(ctx, ds); err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create %s DaemonSet in guestcluster: %v", ds.Name, err)
			}
		}

		t.Logf("waiting for NodePools in-place update with NTO-generated MachineConfig")
		err = wait.PollImmediateWithContext(ctx, 5*time.Second, 15*time.Minute, func(ctx context.Context) (bool, error) {
			if ctx.Err() != nil {
				return false, err
			}
			pods := &corev1.PodList{}
			if err := hostedClusterClient.List(ctx, pods, crclient.InNamespace(ds.Namespace), crclient.MatchingLabels(ds.Spec.Selector.MatchLabels)); err != nil {
				t.Logf("WARNING: failed to list pods, will retry: %v", err)
				return false, nil
			}

			if len(pods.Items) != len(nodes) {
				return false, nil
			}

			for _, pod := range pods.Items {
				if !isPodReady(&pod) {
					return false, nil
				}
			}

			return true, nil
		})

		g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed waiting for all pods in the NTO MachineConfig update verification DS to be ready: %v", err))
		g.Expect(nodePool.Status.Replicas).To(BeEquivalentTo(len(nodes)))
		e2eutil.EnsureNoCrashingPods(t, ctx, mgmtClient, hostedCluster)
		e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, ctx, mgmtClient, hostedCluster)
		e2eutil.EnsureHCPContainersHaveResourceRequests(t, ctx, mgmtClient, hostedCluster)
		e2eutil.EnsureNoPodsWithTooHighPriority(t, ctx, mgmtClient, hostedCluster)
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
