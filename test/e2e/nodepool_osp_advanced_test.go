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

	// The default availability zone for OpenStack is "nova" but the AZ name
	// can be different depending on the OpenStack deployment so it can be
	// overridden by the e2e user.
	defaultAvailabilityZone = "nova"
)

type OpenStackAdvancedTest struct {
	DummyInfraSetup
	ctx                         context.Context
	managementClient            crclient.Client
	hostedCluster               *hyperv1.HostedCluster
	hostedControlPlaneNamespace string
}

func NewOpenStackAdvancedTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) *OpenStackAdvancedTest {
	return &OpenStackAdvancedTest{
		ctx:                         ctx,
		hostedCluster:               hostedCluster,
		managementClient:            mgmtClient,
		hostedControlPlaneNamespace: manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name),
	}
}

func (o OpenStackAdvancedTest) Setup(t *testing.T) {
	t.Log("Starting test OpenStackAdvancedTest")

	if globalOpts.Platform != hyperv1.OpenStackPlatform {
		t.Skip("test only supported on platform OpenStack")
	}

	// The features that are being tested here is only available in 4.19+
	if e2eutil.IsLessThan(e2eutil.Version419) {
		t.Skip("test only applicable for 4.19+")
	}
}

func (o OpenStackAdvancedTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
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
	e2eutil.EventuallyObject(t, o.ctx, "NodePool to have availability zone configured",
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
				diff := cmp.Diff(getAZName(), ptr.Deref(np.Spec.Platform.OpenStack, hyperv1.OpenStackNodePoolPlatform{}).AvailabilityZone)
				return diff == "", fmt.Sprintf("incorrect availability zone: %v", diff), nil
			},
		},
	)
	if globalOpts.ConfigurableClusterOptions.OpenStackNodeImageName != "" {
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
	}

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
	e2eutil.EventuallyObjects(t, o.ctx, "CAPI Machines to be created with the correct availability zone",
		func(ctx context.Context) ([]*capiv1.Machine, error) {
			list := &capiv1.MachineList{}
			err := o.managementClient.List(ctx, list, crclient.InNamespace(o.hostedControlPlaneNamespace), crclient.MatchingLabels{capiv1.MachineDeploymentNameLabel: nodePool.Name})
			oms := make([]*capiv1.Machine, len(list.Items))
			for i := range list.Items {
				oms[i] = &list.Items[i]
			}
			return oms, err
		},
		[]e2eutil.Predicate[[]*capiv1.Machine]{
			func(machines []*capiv1.Machine) (done bool, reasons string, err error) {
				return len(machines) == int(*nodePool.Spec.Replicas), fmt.Sprintf("expected %d Machines, got %d", *nodePool.Spec.Replicas, len(machines)), nil
			},
		},
		[]e2eutil.Predicate[*capiv1.Machine]{
			func(machine *capiv1.Machine) (done bool, reasons string, err error) {
				if machine.Spec.FailureDomain == nil {
					return false, "Machine does not have a failure domain", nil
				}
				want, got := getAZName(), *machine.Spec.FailureDomain
				return want == got, fmt.Sprintf("expected Machine to have failure domain %s, got %s", want, got), nil
			},
		},
	)
	if globalOpts.ConfigurableClusterOptions.OpenStackNodeImageName != "" {
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
}

func (o OpenStackAdvancedTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.hostedCluster.Name + "-" + "test-osp-advanced",
			Namespace: o.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Platform.OpenStack.AvailabilityZone = getAZName()
	nodePool.Spec.Platform.OpenStack.AdditionalPorts = []hyperv1.PortSpec{
		{
			Network: &hyperv1.NetworkParam{
				Filter: &hyperv1.NetworkFilter{
					Name: additionalNetworkName,
				},
			},
		},
	}
	if globalOpts.ConfigurableClusterOptions.OpenStackNodeImageName != "" {
		nodePool.Spec.Platform.OpenStack.ImageName = globalOpts.ConfigurableClusterOptions.OpenStackNodeImageName
	}

	return nodePool, nil
}

func getAZName() string {
	if globalOpts.ConfigurableClusterOptions.OpenStackNodeAvailabilityZone != "" {
		return globalOpts.ConfigurableClusterOptions.OpenStackNodeAvailabilityZone
	}
	return defaultAvailabilityZone
}

func (o OpenStackAdvancedTest) SetupInfra(t *testing.T) error {
	return nil
}

func (o OpenStackAdvancedTest) TeardownInfra(t *testing.T) error {
	return nil
}
