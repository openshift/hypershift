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
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultAvailabilityZone = "nova"
)

type OpenStackAZTest struct {
	DummyInfraSetup
	ctx                         context.Context
	managementClient            crclient.Client
	hostedCluster               *hyperv1.HostedCluster
	hostedControlPlaneNamespace string
}

func NewOpenStackAZTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) *OpenStackAZTest {
	return &OpenStackAZTest{
		ctx:                         ctx,
		hostedCluster:               hostedCluster,
		managementClient:            mgmtClient,
		hostedControlPlaneNamespace: manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name),
	}
}

func (o OpenStackAZTest) Setup(t *testing.T) {
	t.Log("Starting test OpenStackAZTest")

	if globalOpts.Platform != hyperv1.OpenStackPlatform {
		t.Skip("test only supported on platform OpenStack")
	}
}

func (o OpenStackAZTest) Run(t *testing.T, nodePool hyperv1.NodePool, _ []corev1.Node) {
	np := &hyperv1.NodePool{}
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
				diff := cmp.Diff(defaultAvailabilityZone, ptr.Deref(np.Spec.Platform.OpenStack, hyperv1.OpenStackNodePoolPlatform{}).AvailabilityZone)
				return diff == "", fmt.Sprintf("incorrect availability zone: %v", diff), nil
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
				want, got := defaultAvailabilityZone, *machine.Spec.FailureDomain
				return want == got, fmt.Sprintf("expected Machine to have failure domain %s, got %s", want, got), nil
			},
		},
	)
}

func (o OpenStackAZTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.hostedCluster.Name + "-" + "test-osp-az",
			Namespace: o.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &oneReplicas
	nodePool.Spec.Platform.OpenStack.AvailabilityZone = defaultAvailabilityZone
	return nodePool, nil
}

func (o OpenStackAZTest) SetupInfra(t *testing.T) error {
	return nil
}

func (o OpenStackAZTest) TeardownInfra(t *testing.T) error {
	return nil
}
