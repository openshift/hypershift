//go:build e2e
// +build e2e

package e2e

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	hyperapi "github.com/openshift/hypershift/support/api"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	mcfgv1 "github.com/openshift/hypershift/thirdparty/machineconfigoperator/pkg/apis/machineconfiguration.openshift.io/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	utilpointer "k8s.io/utils/pointer"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/yaml"
)

func testNodepoolMachineconfigGetsRolledout(parentCtx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hostedClusterClient crclient.Client, clusterOpts core.CreateOptions) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		ctx, cancel := context.WithCancel(parentCtx)
		originalNP := hyperv1.NodePool{}
		defer func() {
			t.Log("Test: NodePool MachineConfig finished")
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
				Name:      hostedCluster.Name + "-" + "test-machineconfig",
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
							MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(int(oneReplicas))),
						},
					},
				},
				ClusterName: hostedCluster.Name,
				Replicas:    &oneReplicas,
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

		numNodes := oneReplicas
		t.Logf("Waiting for Nodes %d\n", numNodes)
		nodes := e2eutil.WaitForNReadyNodesByNodePool(t, ctx, hostedClusterClient, numNodes, hostedCluster.Spec.Platform.Type, nodePool.Name)
		t.Logf("Desired replicas available for nodePool: %v", nodePool.Name)

		// Wait for the rollout to be complete
		t.Logf("Waiting for cluster rollout. Image: %s", globalOpts.LatestReleaseImage)
		e2eutil.WaitForImageRollout(t, ctx, mgmtClient, hostedCluster, globalOpts.LatestReleaseImage)

		// MachineConfig Actions
		ignitionConfig := ignitionapi.Config{
			Ignition: ignitionapi.Ignition{
				Version: "3.2.0",
			},
			Storage: ignitionapi.Storage{
				Files: []ignitionapi.File{{
					Node:          ignitionapi.Node{Path: "/etc/custom-config"},
					FileEmbedded1: ignitionapi.FileEmbedded1{Contents: ignitionapi.Resource{Source: utilpointer.String("data:,content%0A")}},
				}},
			},
		}
		serializedIgnitionConfig, err := json.Marshal(ignitionConfig)
		if err != nil {
			t.Fatalf("failed to serialize ignition config: %v", err)
		}
		machineConfig := &mcfgv1.MachineConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:   "custom",
				Labels: map[string]string{"machineconfiguration.openshift.io/role": "worker"},
			},
			Spec: mcfgv1.MachineConfigSpec{Config: runtime.RawExtension{Raw: serializedIgnitionConfig}},
		}
		gvk, err := apiutil.GVKForObject(machineConfig, hyperapi.Scheme)
		if err != nil {
			t.Fatalf("failed to get typeinfo for %T from scheme: %v", machineConfig, err)
		}
		machineConfig.SetGroupVersionKind(gvk)
		serializedMachineConfig, err := yaml.Marshal(machineConfig)
		if err != nil {
			t.Fatalf("failed to serialize machineConfig: %v", err)
		}
		machineConfigConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "custom-machine-config",
				Namespace: hostedCluster.Namespace,
			},
			Data: map[string]string{"config": string(serializedMachineConfig)},
		}
		if err := mgmtClient.Create(ctx, machineConfigConfigMap); err != nil {
			t.Fatalf("failed to create configmap for custom machineconfig: %v", err)
		}

		np := nodePool.DeepCopy()
		nodePool.Spec.Config = append(nodePool.Spec.Config, corev1.LocalObjectReference{Name: machineConfigConfigMap.Name})
		if err := mgmtClient.Patch(ctx, nodePool, crclient.MergeFrom(np)); err != nil {
			t.Fatalf("failed to update nodepool %s after adding machineconfig: %v", nodePool.Name, err)
		}

		// DS Customization
		ds := machineConfigUpdatedVerificationDS.DeepCopy()
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
			t.Fatalf("failed to create %s DaemonSet in guestcluster: %v", ds.Name, err)
		}

		t.Logf("waiting for rollout of updated nodepools")
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
		g.Expect(err).NotTo(HaveOccurred(), fmt.Sprintf("failed waiting for all pods in the MachineConfig update verification DS to be ready: %v", err))
		g.Expect(nodePool.Status.Replicas).To(BeEquivalentTo(len(nodes)))
		e2eutil.EnsureNoCrashingPods(t, ctx, mgmtClient, hostedCluster)
		e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, ctx, mgmtClient, hostedCluster)
		e2eutil.EnsureHCPContainersHaveResourceRequests(t, ctx, mgmtClient, hostedCluster)
		e2eutil.EnsureNoPodsWithTooHighPriority(t, ctx, mgmtClient, hostedCluster)
	}
}

//go:embed nodepool_machineconfig_verification_ds.yaml
var machineConfigUpdatedVerificationDSRaw []byte

var machineConfigUpdatedVerificationDS = func() *appsv1.DaemonSet {
	ds := &appsv1.DaemonSet{}
	if err := yaml.Unmarshal(machineConfigUpdatedVerificationDSRaw, &ds); err != nil {
		panic(err)
	}
	return ds
}()

func isPodReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}

	return false
}
