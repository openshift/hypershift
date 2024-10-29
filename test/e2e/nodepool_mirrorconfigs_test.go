//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/google/go-cmp/cmp"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool"
	"github.com/openshift/hypershift/support/util"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

const (
	kubeletConfig1 = `
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: set-max-pods
spec:
  kubeletConfig:
    maxPods: 100
`
)

const (
	configKey              = "config"
	configManagedNamespace = "openshift-config-managed"
)

type MirrorConfigsTest struct {
	DummyInfraSetup
	ctx                 context.Context
	managementClient    crclient.Client
	hostedClusterClient crclient.Client
	hostedCluster       *hyperv1.HostedCluster
}

func NewMirrorConfigsTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster, hcClient crclient.Client) *MirrorConfigsTest {
	return &MirrorConfigsTest{
		ctx:                 ctx,
		hostedCluster:       hostedCluster,
		hostedClusterClient: hcClient,
		managementClient:    mgmtClient,
	}
}

func (mc *MirrorConfigsTest) Setup(t *testing.T) {
	t.Log("Starting test MirrorConfigsTest")

	if globalOpts.Platform == hyperv1.OpenStackPlatform {
		t.Skip("test is being skipped for OpenStack platform until https://issues.redhat.com/browse/OSASINFRA-3566 is addressed")
	}
}

func (mc *MirrorConfigsTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      mc.hostedCluster.Name + "-" + "test-mirrorconfigs",
			Namespace: mc.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)
	nodePool.Spec.Replicas = &oneReplicas

	return nodePool, nil
}

func (mc *MirrorConfigsTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	t.Log("Entering MirrorConfigs test")
	ctx := mc.ctx

	KubeletConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kc-test",
			Namespace: nodePool.Namespace,
		},
		Data: map[string]string{configKey: kubeletConfig1},
	}
	if err := mc.managementClient.Create(ctx, KubeletConfigMap); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			t.Fatalf("failed to create configmap for KubeletConfig object: %v", err)
		}
	}

	defer func() {
		if err := mc.managementClient.Delete(ctx, KubeletConfigMap); err != nil {
			t.Logf("failed to delete configmap for KubeletConfigMap object: %v", err)
		}
		t.Log("Exiting MirrorConfigs test: OK")
	}()

	np := nodePool.DeepCopy()
	nodePool.Spec.Config = append(nodePool.Spec.Config, corev1.LocalObjectReference{Name: KubeletConfigMap.Name})
	if err := mc.managementClient.Patch(ctx, &nodePool, crclient.MergeFrom(np)); err != nil {
		t.Fatalf("failed to update nodepool %s after adding KubeletConfig config: %v", nodePool.Name, err)
	}

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(mc.hostedCluster.Namespace, mc.hostedCluster.Name)
	t.Logf("Hosted control plane namespace is %s", controlPlaneNamespace)

	e2eutil.EventuallyObjects(t, ctx, "kubeletConfig should be mirrored and present in the hosted cluster",
		func(ctx context.Context) ([]*corev1.ConfigMap, error) {
			list := &corev1.ConfigMapList{}
			err := mc.hostedClusterClient.List(ctx, list, crclient.InNamespace(configManagedNamespace),
				crclient.MatchingLabels(map[string]string{
					nodepool.KubeletConfigConfigMapLabel: "true",
					hyperv1.NodePoolLabel:                nodePool.Name,
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
				return want == got, fmt.Sprintf("expected %d kubelet config ConfigMaps, got %d", want, got), nil
			},
		},
		[]e2eutil.Predicate[*corev1.ConfigMap]{
			func(configMap *corev1.ConfigMap) (done bool, reasons string, err error) {
				if want, got := util.ShortenName(KubeletConfigMap.Name, nodePool.Name, nodepool.QualifiedNameMaxLength), configMap.Name; want != got {
					return false, fmt.Sprintf("expected kubelet config ConfigMap name to be '%s', got '%s'", want, got), nil
				}
				return true, fmt.Sprintf("kubelet config ConfigMap name is as expected"), nil
			},
			func(configMap *corev1.ConfigMap) (done bool, reasons string, err error) {
				if diff := cmp.Diff(map[string]string{
					nodepool.KubeletConfigConfigMapLabel: configMap.Labels[nodepool.KubeletConfigConfigMapLabel],
					hyperv1.NodePoolLabel:                configMap.Labels[hyperv1.NodePoolLabel],
					nodepool.NTOMirroredConfigLabel:      configMap.Labels[nodepool.NTOMirroredConfigLabel],
				}, map[string]string{
					nodepool.KubeletConfigConfigMapLabel: "true",
					hyperv1.NodePoolLabel:                nodePool.Name,
					nodepool.NTOMirroredConfigLabel:      "true",
				}); diff != "" {
					return false, fmt.Sprintf("incorrect labels: %v", diff), nil
				}
				return true, "labels are correct", nil
			},
		},
	)

	t.Log("Deleting KubeletConfig configmap reference from nodepool ...")
	baseNP := nodePool.DeepCopy()
	nodePool.Spec = np.Spec
	if err := mc.managementClient.Patch(ctx, &nodePool, crclient.MergeFrom(baseNP)); err != nil {
		t.Fatalf("failed to update nodepool %s after removing KubeletConfig configmap: %v", nodePool.Name, err)
	}
	e2eutil.EventuallyObjects(t, ctx, "KubeletConfig configmap to be deleted",
		func(ctx context.Context) ([]*corev1.ConfigMap, error) {
			list := &corev1.ConfigMapList{}
			err := mc.hostedClusterClient.List(ctx, list, crclient.InNamespace(configManagedNamespace), crclient.MatchingLabels(map[string]string{
				nodepool.KubeletConfigConfigMapLabel: "true",
				hyperv1.NodePoolLabel:                nodePool.Name,
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
				return want == got, fmt.Sprintf("expected %d KubeletConfig configmap, got %d", want, got), nil
			},
		}, nil,
	)
}
