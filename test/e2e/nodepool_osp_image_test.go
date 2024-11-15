//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	capiopenstackv1alpha1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1alpha1"
	capiopenstackv1beta1 "sigs.k8s.io/cluster-api-provider-openstack/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type OpenStackImageTest struct {
	DummyInfraSetup
	ctx                         context.Context
	managementClient            crclient.Client
	hostedCluster               *hyperv1.HostedCluster
	hostedControlPlaneNamespace string
}

func NewOpenStackImageTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) *OpenStackImageTest {
	return &OpenStackImageTest{
		ctx:                         ctx,
		hostedCluster:               hostedCluster,
		managementClient:            mgmtClient,
		hostedControlPlaneNamespace: manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name),
	}
}

func (o OpenStackImageTest) Setup(t *testing.T) {
	t.Log("Starting test OpenStackImageTest")

	if globalOpts.Platform != hyperv1.OpenStackPlatform {
		t.Skip("test only supported on platform OpenStack")
	}

	if globalOpts.ConfigurableClusterOptions.OpenStackNodeImageName == "" {
		t.Skip("OpenStack image name not provided, skipping test")
	}
}

func (o OpenStackImageTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
	np := &hyperv1.NodePool{}
	e2eutil.EventuallyObject(t, o.ctx, "NodePool to have image configured",
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := o.managementClient.Get(ctx, util.ObjectKey(&nodePool), np)
			return np, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			func(nodePool *hyperv1.NodePool) (done bool, reasons string, err error) {
				want, got := hyperv1.OpenStackPlatform, nodePool.Spec.Platform.Type
				return want == got, fmt.Sprintf("expected NodePool to have platform %s, got %s", want, got), nil
			},
			func(pool *hyperv1.NodePool) (done bool, reasons string, err error) {
				diff := cmp.Diff(globalOpts.ConfigurableClusterOptions.OpenStackNodeImageName, ptr.Deref(np.Spec.Platform.OpenStack, hyperv1.OpenStackNodePoolPlatform{}).ImageName)
				return diff == "", fmt.Sprintf("incorrect image name: %v", diff), nil
			},
		},
	)

	e2eutil.EventuallyObjects(t, o.ctx, "OpenStackServers to be created with the correct image",
		func(ctx context.Context) ([]*capiopenstackv1beta1.OpenStackMachine, error) {
			list := &capiopenstackv1beta1.OpenStackMachineList{}
			err := o.managementClient.List(ctx, list, crclient.InNamespace(o.hostedControlPlaneNamespace), crclient.MatchingLabels{capiv1.MachineDeploymentNameLabel: nodePool.Name})
			oms := make([]*capiopenstackv1beta1.OpenStackMachine, len(list.Items))
			for i := range list.Items {
				oms[i] = &list.Items[i]
			}
			return oms, err
		},
		[]e2eutil.Predicate[[]*capiopenstackv1beta1.OpenStackMachine]{
			func(machines []*capiopenstackv1beta1.OpenStackMachine) (done bool, reasons string, err error) {
				return len(machines) == int(*nodePool.Spec.Replicas), fmt.Sprintf("expected %d OpenStackMachines, got %d", *nodePool.Spec.Replicas, len(machines)), nil
			},
		},
		[]e2eutil.Predicate[*capiopenstackv1beta1.OpenStackMachine]{
			func(machine *capiopenstackv1beta1.OpenStackMachine) (done bool, reasons string, err error) {
				server := &capiopenstackv1alpha1.OpenStackServer{}
				err = o.managementClient.Get(o.ctx, crclient.ObjectKey{Name: machine.Name, Namespace: o.hostedControlPlaneNamespace}, server)
				if err != nil {
					return false, "", err
				}
				if server.Spec.Image.Filter != nil && *server.Spec.Image.Filter.Name != globalOpts.ConfigurableClusterOptions.OpenStackNodeImageName {
					return false, fmt.Sprintf("expected image name %s, got %s", globalOpts.ConfigurableClusterOptions.OpenStackNodeImageName, *server.Spec.Image.Filter.Name), nil
				}
				return true, "", nil
			},
		},
	)
}

func (o OpenStackImageTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.hostedCluster.Name + "-" + "test-osp-image",
			Namespace: o.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Platform.OpenStack.ImageName = globalOpts.ConfigurableClusterOptions.OpenStackNodeImageName
	return nodePool, nil
}

func (o OpenStackImageTest) SetupInfra(t *testing.T) error {
	return nil
}

func (o OpenStackImageTest) TeardownInfra(t *testing.T) error {
	return nil
}
