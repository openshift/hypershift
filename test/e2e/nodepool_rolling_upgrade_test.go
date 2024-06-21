//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type RollingUpgradeTest struct {
	DummyInfraSetup
	ctx        context.Context
	mgmtClient crclient.Client

	hostedCluster *hyperv1.HostedCluster
}

func NewRollingUpgradeTest(ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) *RollingUpgradeTest {
	return &RollingUpgradeTest{
		ctx:           ctx,
		mgmtClient:    mgmtClient,
		hostedCluster: hostedCluster,
	}
}

func (k *RollingUpgradeTest) Setup(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
}

func (k *RollingUpgradeTest) BuildNodePoolManifest(defaultNodepool hyperv1.NodePool) (*hyperv1.NodePool, error) {
	nodePool := &hyperv1.NodePool{
		ObjectMeta: v1.ObjectMeta{
			Name:      k.hostedCluster.Name + "-" + "test-rolling-upgrade",
			Namespace: k.hostedCluster.Namespace,
		},
	}
	defaultNodepool.Spec.DeepCopyInto(&nodePool.Spec)

	nodePool.Spec.Replicas = &twoReplicas
	nodePool.Spec.Platform.AWS.InstanceType = "m5.large"
	nodePool.Spec.Management.UpgradeType = hyperv1.UpgradeTypeReplace

	return nodePool, nil
}

func (k *RollingUpgradeTest) Run(t *testing.T, nodePool hyperv1.NodePool, nodes []corev1.Node) {
	g := NewWithT(t)

	instanceType := "m5.xlarge"
	// change instance type to trigger a rolling upgrade
	err := e2eutil.UpdateObject(t, k.ctx, k.mgmtClient, &nodePool, func(obj *hyperv1.NodePool) {
		obj.Spec.Platform.AWS.InstanceType = instanceType
	})
	g.Expect(err).ToNot(HaveOccurred())

	e2eutil.EventuallyObject(t, k.ctx, fmt.Sprintf("NodePool %s/%s to start the rolling upgrade", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := k.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
			return &nodePool, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			e2eutil.ConditionPredicate[*hyperv1.NodePool](e2eutil.Condition{
				Type: hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType,
			}),
		},
		e2eutil.WithTimeout(2*time.Minute),
	)

	e2eutil.EventuallyObject(t, k.ctx, fmt.Sprintf("NodePool %s/%s to finish the rolling upgrade", nodePool.Namespace, nodePool.Name),
		func(ctx context.Context) (*hyperv1.NodePool, error) {
			err := k.mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(&nodePool), &nodePool)
			return &nodePool, err
		},
		[]e2eutil.Predicate[*hyperv1.NodePool]{
			e2eutil.ConditionMissingPredicate[*hyperv1.NodePool](e2eutil.Condition{
				Type: hyperv1.NodePoolUpdatingPlatformMachineTemplateConditionType,
			}),
		},
		e2eutil.WithTimeout(30*time.Minute),
	)

	// check all aws machines have the new instance type
	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(k.hostedCluster.Namespace, k.hostedCluster.Name)
	awsMachines := &capiaws.AWSMachineList{}
	err = k.mgmtClient.List(k.ctx, awsMachines, crclient.InNamespace(controlPlaneNamespace), crclient.MatchingLabels{capiv1.MachineDeploymentNameLabel: nodePool.Name})
	g.Expect(err).ToNot(HaveOccurred(), "failed to list aws machines")

	for _, machine := range awsMachines.Items {
		g.Expect(machine.Spec.InstanceType).To(Equal(instanceType))
	}
}
