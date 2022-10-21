//go:build e2e
// +build e2e

package e2e

import (
	"context"
	_ "embed"
	"encoding/json"
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
		if !errors.IsAlreadyExists(err) {
			t.Fatalf("failed to create configmap for custom machineconfig: %v", err)
		}

		for _, nodePool := range nodePools.Items {
			if nodePool.Spec.ClusterName != guestCluster.Name {
				continue
			}
			np := nodePool.DeepCopy()
			nodePool.Spec.Config = append(nodePool.Spec.Config, corev1.LocalObjectReference{Name: machineConfigConfigMap.Name})
			if err := mgmtClient.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
				t.Fatalf("failed to update nodePool %s after adding machineconfig: %v", nodePool.Name, err)
			}
		}

		ds := machineConfigUpdatedVerificationDS.DeepCopy()
		if err := guestClient.Create(ctx, ds); err != nil {
			t.Fatalf("failed to create %s DaemonSet in guestcluster: %v", ds.Name, err)
		}

		t.Logf("waiting for rollout of updated nodePools")
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
			t.Fatalf("failed waiting for all pods in the machine config update verification DS to be ready: %v", err)
		}

		e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, testContext, mgmtClient, guestClient, guestCluster.Namespace)
		e2eutil.EnsureNoCrashingPods(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureHCPContainersHaveResourceRequests(t, ctx, mgmtClient, guestCluster)
		e2eutil.EnsureNoPodsWithTooHighPriority(t, ctx, mgmtClient, guestCluster)
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
