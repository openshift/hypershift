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

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/yaml"
)

type NodePoolMachineconfigRolloutTest struct {
	DummyInfraSetup
	ctx        context.Context
	mgmtClient crclient.Client

	hostedCluster       *hyperv1.HostedCluster
	hostedClusterClient crclient.Client
	clusterOpts         e2eutil.PlatformAgnosticOptions
}

func NewNodePoolMachineconfigRolloutTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hcClient crclient.Client, clusterOpts e2eutil.PlatformAgnosticOptions) *NodePoolMachineconfigRolloutTest {
	return &NodePoolMachineconfigRolloutTest{
		ctx:                 ctx,
		hostedCluster:       hostedCluster,
		hostedClusterClient: hcClient,
		clusterOpts:         clusterOpts,
		mgmtClient:          mgmtClient,
	}
}

func (mc *NodePoolMachineconfigRolloutTest) Setup(t *testing.T) {
	if globalOpts.Platform == hyperv1.KubevirtPlatform {
		t.Skip("test is being skipped for KubeVirt platform until https://issues.redhat.com/browse/CNV-38196 is addressed")
	}
	t.Log("Starting test NodePoolMachineconfigRolloutTest")
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
	// MachineConfig Actions
	ignitionConfig := ignitionapi.Config{
		Ignition: ignitionapi.Ignition{
			Version: "3.2.0",
		},
		Storage: ignitionapi.Storage{
			Files: []ignitionapi.File{{
				Node:          ignitionapi.Node{Path: "/etc/custom-config"},
				FileEmbedded1: ignitionapi.FileEmbedded1{Contents: ignitionapi.Resource{Source: ptr.To("data:,content%0A")}},
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

	e2eutil.WaitForNodePoolConfigUpdateComplete(t, ctx, mc.mgmtClient, &nodePool)
	eventuallyDaemonSetRollsOut(t, ctx, mc.hostedClusterClient, len(nodes), np, ds)
	e2eutil.WaitForReadyNodesByNodePool(t, ctx, mc.hostedClusterClient, &nodePool, mc.hostedCluster.Spec.Platform.Type)
	e2eutil.EnsureNodeCountMatchesNodePoolReplicas(t, ctx, mc.mgmtClient, mc.hostedClusterClient, mc.hostedCluster.Spec.Platform.Type, mc.hostedCluster.Namespace)
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

func eventuallyDaemonSetRollsOut(t *testing.T, ctx context.Context, client crclient.Client, expectedCount int, np *hyperv1.NodePool, ds *appsv1.DaemonSet) {
	timeout := 15 * time.Minute
	if np.Spec.Platform.Type == hyperv1.KubevirtPlatform {
		timeout = 25 * time.Minute
	}

	e2eutil.EventuallyObjects(t, ctx, fmt.Sprintf("all pods in the DaemonSet %s/%s to be ready", ds.Namespace, ds.Name),
		func(ctx context.Context) ([]*corev1.Pod, error) {
			list := &corev1.PodList{}
			err := client.List(ctx, list, crclient.InNamespace(ds.Namespace), crclient.MatchingLabels(ds.Spec.Selector.MatchLabels))
			readyPods := []*corev1.Pod{}
			for i := range list.Items {
				pod := &list.Items[i]
				for _, condition := range pod.Status.Conditions {
					if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
						readyPods = append(readyPods, pod)
						break
					}
				}
			}
			return readyPods, err
		},
		[]e2eutil.Predicate[[]*corev1.Pod]{
			func(readyPods []*corev1.Pod) (done bool, reasons string, err error) {
				want, got := expectedCount, len(readyPods)
				return want == got, fmt.Sprintf("expected %d Pods, got %d", want, got), nil
			},
		}, nil, e2eutil.WithTimeout(timeout),
	)
}
