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

const (
	// For now we hardcode the network name, but we should make it configurable
	// and maybe use Gophercloud to create a network with a dynamic name.
	additionalNetworkName = "hcp-nodepool-multinet-e2e"
)

type OpenStackMultinetTest struct {
	DummyInfraSetup
	ctx                         context.Context
	managementClient            crclient.Client
	hostedCluster               *hyperv1.HostedCluster
	hostedControlPlaneNamespace string
}

func NewOpenStackMultinetTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) *OpenStackMultinetTest {
	return &OpenStackMultinetTest{
		ctx:                         ctx,
		hostedCluster:               hostedCluster,
		managementClient:            mgmtClient,
		hostedControlPlaneNamespace: manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name),
	}
}

func (o OpenStackMultinetTest) Setup(t *testing.T) {
	t.Log("Starting test OpenStackMultinetTest")

	if globalOpts.Platform != hyperv1.OpenStackPlatform {
		t.Skip("test only supported on platform OpenStack")
	}
}

func (o OpenStackMultinetTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
	np := &hyperv1.NodePool{}
	e2eutil.EventuallyObject(t, o.ctx, "NodePool to have additional networks configured",
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
				diff := cmp.Diff([]hyperv1.PortSpec{
					{
						Network: &hyperv1.NetworkParam{
							Filter: &hyperv1.NetworkFilter{
								Name: additionalNetworkName,
							},
						},
					},
				}, ptr.Deref(np.Spec.Platform.OpenStack, hyperv1.OpenStackNodePoolPlatform{}).AdditionalPorts)
				return diff == "", fmt.Sprintf("incorrect additional networks: %v", diff), nil
			},
		},
	)

	e2eutil.EventuallyObjects(t, o.ctx, "OpenStackServers to be created with the correct number of ports",
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
				// The number of ports for a machine is 1 + the number of additional ports in the nodepool.
				if len(server.Status.Resources.Ports) != len(nodePool.Spec.Platform.OpenStack.AdditionalPorts)+1 {
					return false, fmt.Sprintf("expected %d ports, got %d", len(nodePool.Spec.Platform.OpenStack.AdditionalPorts)+1, len(server.Status.Resources.Ports)), nil
				}
				return true, "", nil
			},
		},
	)
}

func (o OpenStackMultinetTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.hostedCluster.Name + "-" + "test-osp-multinet",
			Namespace: o.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Platform.OpenStack.AdditionalPorts = []hyperv1.PortSpec{
		{
			Network: &hyperv1.NetworkParam{
				Filter: &hyperv1.NetworkFilter{
					Name: additionalNetworkName,
				},
			},
		},
	}

	return nodePool, nil
}

func (o OpenStackMultinetTest) SetupInfra(t *testing.T) error {
	return nil
}

func (o OpenStackMultinetTest) TeardownInfra(t *testing.T) error {
	return nil
}
