//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"k8s.io/utils/ptr"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	kvinfra "github.com/openshift/hypershift/kubevirtexternalinfra"
)

type KubeVirtCacheTest struct {
	DummyInfraSetup
	ctx           context.Context
	client        crclient.Client
	hostedCluster *hyperv1.HostedCluster
}

func NewKubeVirtCacheTest(ctx context.Context, cl crclient.Client, hc *hyperv1.HostedCluster) *KubeVirtCacheTest {
	return &KubeVirtCacheTest{
		ctx:           ctx,
		client:        cl,
		hostedCluster: hc,
	}
}

func (k KubeVirtCacheTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.KubevirtPlatform {
		t.Skip("test only supported on platform KubeVirt")
	}

	t.Log("Starting test KubeVirtCacheTest")
}

func (k KubeVirtCacheTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
	g := NewWithT(t)

	np := &hyperv1.NodePool{}
	e2eutil.EventuallyObject(
		t, k.ctx, fmt.Sprintf("waiting for NodePool %s/%s to have a cache data volume", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := k.client.Get(k.ctx, util.ObjectKey(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(pool *hyperv1.NodePool) (done bool, reasons string, err error) {
				if np.Status.Platform != nil && np.Status.Platform.KubeVirt != nil && np.Status.Platform.KubeVirt.CacheName != "" {
					return true, "", nil
				}
				return false, "no cache data volume set", nil
			},
		},
	)

	localInfraNS := manifests.HostedControlPlaneNamespace(k.hostedCluster.Namespace, k.hostedCluster.Name)
	var guestNamespace string
	if np.Status.Platform.KubeVirt.Credentials != nil &&
		len(np.Status.Platform.KubeVirt.Credentials.InfraNamespace) > 0 {
		guestNamespace = np.Status.Platform.KubeVirt.Credentials.InfraNamespace
		g.Expect(np.Status.Platform.KubeVirt.Credentials.InfraKubeConfigSecret).ToNot(BeNil())
		g.Expect(np.Status.Platform.KubeVirt.Credentials.InfraKubeConfigSecret.Key).Should(Equal("kubeconfig"))
	} else {
		guestNamespace = localInfraNS
	}

	cm := kvinfra.NewKubevirtInfraClientMap()
	infraClient, err := cm.DiscoverKubevirtClusterClient(k.ctx, k.client, k.hostedCluster.Spec.InfraID, np.Status.Platform.KubeVirt.Credentials, localInfraNS, np.GetNamespace())
	g.Expect(err).ShouldNot(HaveOccurred())

	dv := &v1beta1.DataVolume{}
	g.Expect(
		infraClient.GetInfraClient().Get(k.ctx, crclient.ObjectKey{Namespace: guestNamespace, Name: np.Status.Platform.KubeVirt.CacheName}, dv),
	).To(Succeed())

	g.Expect(dv.Status.Phase).Should(Equal(v1beta1.Succeeded))
}

func (k KubeVirtCacheTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      k.hostedCluster.Name + "-" + "test-kv-cache-root-volume",
			Namespace: k.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	if nodePool.Spec.Platform.Kubevirt != nil &&
		nodePool.Spec.Platform.Kubevirt.RootVolume != nil {
		nodePool.Spec.Platform.Kubevirt.RootVolume.CacheStrategy = &hyperv1.KubevirtCachingStrategy{
			Type: hyperv1.KubevirtCachingStrategyPVC,
		}
	}

	nodePool.Spec.Replicas = ptr.To[int32](1)

	return nodePool, nil
}
