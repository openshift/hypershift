//go:build e2e
// +build e2e

package e2e

import (
	"context"
	_ "embed"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
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
)

type NTOMachineConfigRolloutTest struct {
	DummyInfraSetup
	ctx        context.Context
	mgmtClient crclient.Client

	hostedCluster       *hyperv1.HostedCluster
	hostedClusterClient crclient.Client

	inplace bool
}

func NewNTOMachineConfigRolloutTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hcClient crclient.Client, inplace bool) *NTOMachineConfigRolloutTest {
	return &NTOMachineConfigRolloutTest{
		ctx:                 ctx,
		hostedCluster:       hostedCluster,
		hostedClusterClient: hcClient,
		mgmtClient:          mgmtClient,
		inplace:             inplace,
	}
}

func (mc *NTOMachineConfigRolloutTest) Setup(t *testing.T) {
	t.Log("Starting test NTOMachineConfigRolloutTest")

	if globalOpts.Platform == hyperv1.KubevirtPlatform {
		t.Skip("test is being skipped for KubeVirt platform until https://issues.redhat.com/browse/CNV-38196 is addressed")
	}

	if globalOpts.Platform == hyperv1.OpenStackPlatform {
		t.Skip("test is being skipped for OpenStack platform until https://issues.redhat.com/browse/OSASINFRA-3566 is addressed")
	}
}

func (mc *NTOMachineConfigRolloutTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: v1.ObjectMeta{
			Name:      mc.hostedCluster.Name + "-" + "test-ntomachineconfig-replace",
			Namespace: mc.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &twoReplicas
	nodePool.Spec.Management.Replace = &hyperv1.ReplaceUpgrade{
		Strategy: hyperv1.UpgradeStrategyRollingUpdate,
		RollingUpdate: &hyperv1.RollingUpdate{
			MaxUnavailable: func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(0)),
			MaxSurge:       func(v intstr.IntOrString) *intstr.IntOrString { return &v }(intstr.FromInt(int(twoReplicas))),
		},
	}

	return nodePool, nil
}

func (mc *NTOMachineConfigRolloutTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	ctx := mc.ctx

	tuningConfigConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hugepages-tuned-test",
			Namespace: mc.hostedCluster.Namespace,
		},
		Data: map[string]string{tuningConfigKey: hugepagesTuned},
	}
	if err := mc.mgmtClient.Create(ctx, tuningConfigConfigMap); err != nil {
		if !errors.IsAlreadyExists(err) {
			t.Fatalf("failed to create configmap for custom Tuned object: %v", err)
		}
	}

	np := nodePool.DeepCopy()
	nodePool.Spec.TuningConfig = append(nodePool.Spec.TuningConfig, corev1.LocalObjectReference{Name: tuningConfigConfigMap.Name})
	if err := mc.mgmtClient.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
		t.Fatalf("failed to update nodepool %s after adding Tuned config: %v", nodePool.Name, err)
	}

	// DS Customization
	ds := ntoMachineConfigUpdatedVerificationDS.DeepCopy()
	dsName := nodePool.Name
	e2eutil.CorrelateDaemonSet(ds, &nodePool, dsName)

	if err := mc.hostedClusterClient.Create(ctx, ds); err != nil {
		if !errors.IsAlreadyExists(err) {
			t.Fatalf("failed to create %s DaemonSet in guestcluster: %v", ds.Name, err)
		}
	}

	e2eutil.WaitForNodePoolConfigUpdateComplete(t, ctx, mc.mgmtClient, &nodePool)
	eventuallyDaemonSetRollsOut(t, ctx, mc.hostedClusterClient, len(nodes), np, ds)
	e2eutil.WaitForReadyNodesByNodePool(t, ctx, mc.hostedClusterClient, &nodePool, mc.hostedCluster.Spec.Platform.Type)
	e2eutil.EnsureNoCrashingPods(t, ctx, mc.mgmtClient, mc.hostedCluster)
	e2eutil.EnsureAllContainersHavePullPolicyIfNotPresent(t, ctx, mc.mgmtClient, mc.hostedCluster)
	e2eutil.EnsureHCPContainersHaveResourceRequests(t, ctx, mc.mgmtClient, mc.hostedCluster)
	e2eutil.EnsureNoPodsWithTooHighPriority(t, ctx, mc.mgmtClient, mc.hostedCluster)

}

type NTOMachineConfigInPlaceRolloutTestManifest struct {
	hostedCluster *hyperv1.HostedCluster
}

func NewNTOMachineConfigInPlaceRolloutTestManifest(hostedCluster *hyperv1.HostedCluster) *NTOMachineConfigInPlaceRolloutTestManifest {
	return &NTOMachineConfigInPlaceRolloutTestManifest{
		hostedCluster: hostedCluster,
	}
}

func (mc *NTOMachineConfigInPlaceRolloutTestManifest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: v1.ObjectMeta{
			Name:      mc.hostedCluster.Name + "-" + "test-ntomachineconfig-inplace",
			Namespace: mc.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &twoReplicas
	nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeInPlace

	return nodePool, nil
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
