//go:build e2e
// +build e2e

package e2e

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
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

func testSetNodePoolMachineConfigGetsRolledout(parentCtx context.Context, mgmtClient crclient.Client, guestCluster *hyperv1.HostedCluster, guestClient crclient.Client, clusterOpts core.CreateOptions) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		ctx, cancel := context.WithCancel(parentCtx)
		originalNP := hyperv1.NodePool{}
		var count int32 = 0

		defer cancel()

		// List NodePools (should exists only one and without replicas)
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
				Name:      guestCluster.Name + "-" + "test-mc-rolling-update",
				Namespace: guestCluster.Namespace,
			},
			Spec: hyperv1.NodePoolSpec{
				Management: hyperv1.NodePoolManagement{
					Replace: &hyperv1.ReplaceUpgrade{
						Strategy: hyperv1.UpgradeStrategyRollingUpdate,
						RollingUpdate: &hyperv1.RollingUpdate{
							MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(0)),
							MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(int(twoReplicas))),
						},
					},
					AutoRepair:  true,
					UpgradeType: hyperv1.UpgradeTypeReplace,
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
				t.Fatalf("failed to create nodePool %s: %v", nodePool.Name, err)
			}

			// Update NodePool
			err = mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(nodePool), nodePool)
			g.Expect(err).NotTo(HaveOccurred(), "failed getting existant nodepool")
			np := nodePool.DeepCopy()
			nodePool.Spec.Replicas = &twoReplicas
			nodePool.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{
				Strategy: hyperv1.UpgradeStrategyRollingUpdate,
				RollingUpdate: &hyperv1.RollingUpdate{
					MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(0)),
					MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(int(*nodePool.Spec.Replicas))),
				},
			}
			if err := mgmtClient.Patch(ctx, nodePool, crclient.MergeFrom(np)); err != nil {
				t.Fatalf("failed to update nodepool %s: %v", nodePool.Name, err)
			}
			g.Expect(err).NotTo(HaveOccurred(), "failed to Update existant NodePool")
		}

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
				Namespace: guestCluster.Namespace,
			},
			Data: map[string]string{"config": string(serializedMachineConfig)},
		}

		err = mgmtClient.Create(ctx, machineConfigConfigMap)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create configmap for custom machineconfig: %v", err)
			}
		}

		numNodes := int32(globalOpts.configurableClusterOptions.NodePoolReplicas * len(clusterOpts.AWSPlatform.Zones))
		np := nodePool.DeepCopy()
		nodePool.Spec.Config = append(nodePool.Spec.Config, corev1.LocalObjectReference{Name: machineConfigConfigMap.Name})
		if err := mgmtClient.Patch(ctx, nodePool, crclient.MergeFrom(np)); err != nil {
			t.Fatalf("failed to update nodePool %s after adding machineconfig: %v", nodePool.Name, err)
		}

		ds := machineConfigUpdatedVerificationDS.DeepCopy()
		err = guestClient.Create(ctx, ds)
		if err != nil {
			if !errors.IsAlreadyExists(err) {
				t.Fatalf("failed to create %s DaemonSet in guestcluster: %v", ds.Name, err)
			}
		}

		t.Logf("waiting for NodePools in-place update with generated MachineConfig")
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
			t.Fatalf("failed waiting for all pods in the machine config update verification DS to be ready: %v", err)
		}

		e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, testContext, mgmtClient, guestClient, guestCluster.Namespace)
		e2eutil.EnsureNoCrashingPods(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureHCPContainersHaveResourceRequests(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureNoPodsWithTooHighPriority(t, ctx, mgmtClient, guestCluster)

		// Scale Down current test nodepool
		err = scaleDownTestNodePool(ctx, mgmtClient, nodePool)
		g.Expect(err).NotTo(HaveOccurred(), "failed Scalling down NodePool after test finished")
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
