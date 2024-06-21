//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	performanceProfile = `
apiVersion: performance.openshift.io/v2
kind: PerformanceProfile
metadata:
  name: perfprof-2
spec:
  cpu:
    isolated: "1"
    reserved: "0"
  numa:
    topologyPolicy: "single-numa-node"
  nodeSelector:
    node-role.kubernetes.io/worker-cnf: ""
`
	controllerGeneratedPPConfig = "hypershift.openshift.io/performanceprofile-config"
	ppConfigMapNamePrefix       = "perfprofile-"
)

type NTOPerformanceProfileTest struct {
	DummyInfraSetup
	ctx                 context.Context
	managementClient    crclient.Client
	hostedClusterClient crclient.Client
	hostedCluster       *hyperv1.HostedCluster
}

func NewNTOPerformanceProfileTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hcClient crclient.Client) *NTOPerformanceProfileTest {
	return &NTOPerformanceProfileTest{
		ctx:                 ctx,
		hostedCluster:       hostedCluster,
		hostedClusterClient: hcClient,
		managementClient:    mgmtClient,
	}
}

func (mc *NTOPerformanceProfileTest) Setup(t *testing.T) {
	t.Log("Starting test NTOPerformanceProfileTest")
}

func (mc *NTOPerformanceProfileTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mc.hostedCluster.Name + "-" + "test-ntoperformanceprofile",
			Namespace: mc.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)
	nodePool.Spec.Replicas = &oneReplicas

	return nodePool, nil
}

func (mc *NTOPerformanceProfileTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	t.Log("Entering NTO PerformanceProfile test")

	ctx := mc.ctx

	performanceProfileConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pp-test",
			Namespace: nodePool.Namespace,
		},
		Data: map[string]string{tuningConfigKey: performanceProfile},
	}
	if err := mc.managementClient.Create(ctx, performanceProfileConfigMap); err != nil {
		if !errors.IsAlreadyExists(err) {
			t.Fatalf("failed to create configmap for PerformanceProfile object: %v", err)
		}
	}

	defer func() {
		if err := mc.managementClient.Delete(ctx, performanceProfileConfigMap); err != nil {
			t.Logf("failed to delete configmap for PerformanceProfile object: %v", err)
		}
	}()

	np := nodePool.DeepCopy()
	nodePool.Spec.TuningConfig = append(nodePool.Spec.TuningConfig, corev1.LocalObjectReference{Name: performanceProfileConfigMap.Name})
	if err := mc.managementClient.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
		t.Fatalf("failed to update nodepool %s after adding PerformanceProfile config: %v", nodePool.Name, err)
	}

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(mc.hostedCluster.Namespace, mc.hostedCluster.Name)
	t.Logf("Hosted control plane namespace is %s", controlPlaneNamespace)

	e2eutil.EventuallyObjects(t, ctx, "performance profile ConfigMap to exist with correct labels and annotations",
		func(ctx context.Context) ([]*corev1.ConfigMap, error) {
			list := &corev1.ConfigMapList{}
			err := mc.managementClient.List(ctx, list, crclient.InNamespace(controlPlaneNamespace), crclient.MatchingLabels(map[string]string{
				controllerGeneratedPPConfig: "true",
			}))
			configMaps := make([]*corev1.ConfigMap, len(list.Items))
			for i := range list.Items {
				configMaps[i] = &list.Items[i]
			}
			return configMaps, err
		},
		[]e2eutil.Predicate[[]*corev1.ConfigMap]{
			func(configMaps []*corev1.ConfigMap) (done bool, reasons string, err error) {
				want, got := 1, len(configMaps)
				return want == got, fmt.Sprintf("expected %d performance profile ConfigMaps, got %d", want, got), nil
			},
		},
		[]e2eutil.Predicate[*corev1.ConfigMap]{
			func(configMap *corev1.ConfigMap) (done bool, reasons string, err error) {
				if diff := cmp.Diff(map[string]string{
					nodepool.PerformanceProfileConfigMapLabel: configMap.Labels[nodepool.PerformanceProfileConfigMapLabel],
					hyperv1.NodePoolLabel:                     configMap.Labels[hyperv1.NodePoolLabel],
				}, map[string]string{
					nodepool.PerformanceProfileConfigMapLabel: "true",
					hyperv1.NodePoolLabel:                     nodePool.Name,
				}); diff != "" {
					return false, fmt.Sprintf("incorrect labels: %v", diff), nil
				}
				if want, got := nodePool.Namespace+"/"+nodePool.Name, configMap.Annotations[hyperv1.NodePoolLabel]; want != got {
					return false, fmt.Sprintf("incorrect annotation %v: wanted %v, got %v", hyperv1.NodePoolLabel, want, got), nil
				}
				return true, "labels and annotations correct", nil
			},
		},
	)

	t.Log("Deleting configmap reference from nodepool ...")
	baseNP := nodePool.DeepCopy()
	nodePool.Spec = np.Spec
	if err := mc.managementClient.Patch(ctx, &nodePool, crclient.MergeFrom(baseNP)); err != nil {
		t.Fatalf("failed to update nodepool %s after removing PerformanceProfile config: %v", nodePool.Name, err)
	}

	e2eutil.EventuallyObjects(t, ctx, "performance profile ConfigMap to be deleted",
		func(ctx context.Context) ([]*corev1.ConfigMap, error) {
			list := &corev1.ConfigMapList{}
			err := mc.managementClient.List(ctx, list, crclient.InNamespace(controlPlaneNamespace), crclient.MatchingLabels(map[string]string{
				controllerGeneratedPPConfig: "true",
			}))
			configMaps := make([]*corev1.ConfigMap, len(list.Items))
			for i := range list.Items {
				configMaps[i] = &list.Items[i]
			}
			return configMaps, err
		},
		[]e2eutil.Predicate[[]*corev1.ConfigMap]{
			func(configMaps []*corev1.ConfigMap) (done bool, reasons string, err error) {
				want, got := 0, len(configMaps)
				return want == got, fmt.Sprintf("expected %d performance profile ConfigMaps, got %d", want, got), nil
			},
		}, nil,
	)
	t.Log("Ending NTO PerformanceProfile test: OK")
}
