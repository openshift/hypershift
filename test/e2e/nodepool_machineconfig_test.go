//go:build e2e
// +build e2e

package e2e

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	utilpointer "k8s.io/utils/pointer"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/yaml"
)

type NodePoolMachineconfigRolloutTest struct {
	ctx        context.Context
	mgmtClient crclient.Client

	hostedCluster       *hyperv1.HostedCluster
	hostedClusterClient crclient.Client
	clusterOpts         core.CreateOptions
}

func NewNodePoolMachineconfigRolloutTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hcClient crclient.Client, clusterOpts core.CreateOptions) *NodePoolMachineconfigRolloutTest {
	return &NodePoolMachineconfigRolloutTest{
		ctx:                 ctx,
		hostedCluster:       hostedCluster,
		hostedClusterClient: hcClient,
		clusterOpts:         clusterOpts,
		mgmtClient:          mgmtClient,
	}
}

func (mc *NodePoolMachineconfigRolloutTest) Setup(t *testing.T) {
}

func (mc *NodePoolMachineconfigRolloutTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: v1.ObjectMeta{
			Name:      mc.hostedCluster.Name + "-" + "test-machineconfig",
			Namespace: mc.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{
		Strategy: hyperv1.UpgradeStrategyRollingUpdate,
		RollingUpdate: &hyperv1.RollingUpdate{
			MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(0)),
			MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(int(oneReplicas))),
		},
	}

	return nodePool, nil
}

func (mc *NodePoolMachineconfigRolloutTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)

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
			Namespace: mc.hostedCluster.Namespace,
		},
		Data: map[string]string{"config": string(serializedMachineConfig)},
	}

	ctx := mc.ctx
	if err := mc.mgmtClient.Create(ctx, machineConfigConfigMap); err != nil {
		t.Fatalf("failed to create configmap for custom machineconfig: %v", err)
	}

	np := nodePool.DeepCopy()
	nodePool.Spec.Config = append(nodePool.Spec.Config, corev1.LocalObjectReference{Name: machineConfigConfigMap.Name})
	if err := mc.mgmtClient.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
		t.Fatalf("failed to update nodepool %s after adding machineconfig: %v", nodePool.Name, err)
	}

	// DS Customization
	ds := machineConfigUpdatedVerificationDS.DeepCopy()
	dsName := ds.Name + "-replace"
	e2eutil.CorrelateDaemonSet(ds, &nodePool, dsName)

	if err := mc.hostedClusterClient.Create(ctx, ds); err != nil {
		t.Fatalf("failed to create %s DaemonSet in guestcluster: %v", ds.Name, err)
	}

	t.Logf("waiting for rollout of updated nodepools")
	err = wait.PollImmediateWithContext(ctx, 10*time.Second, 15*time.Minute, func(ctx context.Context) (bool, error) {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		pods := &corev1.PodList{}
		if err := mc.hostedClusterClient.List(ctx, pods, crclient.InNamespace(ds.Namespace), crclient.MatchingLabels(ds.Spec.Selector.MatchLabels)); err != nil {
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

	e2eutil.EnsureNoCrashingPods(t, ctx, mc.mgmtClient, mc.hostedCluster)
	e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, ctx, mc.mgmtClient, mc.hostedCluster)
	e2eutil.EnsureHCPContainersHaveResourceRequests(t, ctx, mc.mgmtClient, mc.hostedCluster)
	e2eutil.EnsureNoPodsWithTooHighPriority(t, ctx, mc.mgmtClient, mc.hostedCluster)
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
