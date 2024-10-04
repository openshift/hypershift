package util

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/openshift/hypershift/hypershift-operator/controllers/scheduler"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	supportutil "github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func SetupReqServingClusterNodePools(ctx context.Context, t *testing.T, kubeconfigPath, mgmtHCNamespace, mgmtHCName string) []*hyperv1.NodePool {
	g := NewWithT(t)
	mgmtParentClient := kubeClient(t, kubeconfigPath)
	nodePoolList := &hyperv1.NodePoolList{}
	err := mgmtParentClient.List(ctx, nodePoolList, client.InNamespace(mgmtHCNamespace))
	g.Expect(err).ToNot(HaveOccurred(), "cannot list nodepools in management parent cluster namespace "+mgmtHCNamespace)

	// filter  management cluster nodepools
	var mgmtNodePools []hyperv1.NodePool
	for _, nodePool := range nodePoolList.Items {
		if nodePool.Spec.ClusterName == mgmtHCName && nodePool.Spec.Platform.AWS != nil {
			mgmtNodePools = append(mgmtNodePools, nodePool)
		}
	}
	g.Expect(len(mgmtNodePools) >= 3).To(BeTrue(), "we need at least 3 AWS management cluster nodepools in different zones")

	var nodePoolA, nodePoolB, nodePoolC *hyperv1.NodePool
	nodePoolA = &mgmtNodePools[0]
	for i := range mgmtNodePools[1:] {
		nodePool := &mgmtNodePools[1+i]
		if nodePool.Spec.Platform.AWS.Subnet.ID != nodePoolA.Spec.Platform.AWS.Subnet.ID {
			if nodePoolB == nil {
				nodePoolB = nodePool
			}
			if nodePoolB != nil && nodePool.Spec.Platform.AWS.Subnet.ID != nodePoolB.Spec.Platform.AWS.Subnet.ID {
				nodePoolC = nodePool
				break
			}
		}
	}
	g.Expect(nodePoolB).ToNot(BeNil(), "did not find 2 nodepools with different subnets in parent")
	g.Expect(nodePoolC).ToNot(BeNil(), "did not find 3 nodepools with different subnets in parent")

	// Prepare and create nodepools for request serving components
	reqServingNodePoolA := nodePoolA.DeepCopy()
	reqServingNodePoolB := nodePoolB.DeepCopy()
	nonReqServingNodePoolA := nodePoolA.DeepCopy()
	nonReqServingNodePoolB := nodePoolB.DeepCopy()
	nonReqServingNodePoolC := nodePoolC.DeepCopy()

	prepareNodePool := func(np *hyperv1.NodePool) {
		np.ObjectMeta = metav1.ObjectMeta{
			Name:      SimpleNameGenerator.GenerateName(fmt.Sprintf("%s-reqserving-", mgmtHCName)),
			Namespace: mgmtHCNamespace,
		}
		np.Status = hyperv1.NodePoolStatus{}
		np.Spec.Replicas = ptr.To[int32](1)
		np.Spec.AutoScaling = nil
		np.Spec.NodeLabels = map[string]string{
			hyperv1.RequestServingComponentLabel:      "true",
			scheduler.OSDFleetManagerPairedNodesLabel: "true",
			"hypershift.openshift.io/control-plane":   "true",
		}
		np.Spec.Taints = []hyperv1.Taint{
			{
				Key:    hyperv1.RequestServingComponentLabel,
				Value:  "true",
				Effect: corev1.TaintEffectNoSchedule,
			},
		}
	}

	prepareNonReqServingNodePool := func(np *hyperv1.NodePool) {
		np.ObjectMeta = metav1.ObjectMeta{
			Name:      SimpleNameGenerator.GenerateName(fmt.Sprintf("%s-non-reqserving-", mgmtHCName)),
			Namespace: mgmtHCNamespace,
		}
		np.Status = hyperv1.NodePoolStatus{}
		np.Spec.Replicas = ptr.To[int32](1)
		np.Spec.AutoScaling = nil
		np.Spec.NodeLabels = map[string]string{
			"hypershift.openshift.io/control-plane": "true",
		}
	}

	var result []*hyperv1.NodePool
	for _, np := range []*hyperv1.NodePool{reqServingNodePoolA, reqServingNodePoolB} {
		prepareNodePool(np)
		err := mgmtParentClient.Create(ctx, np)
		g.Expect(err).ToNot(HaveOccurred(), "failed to create request serving management nodepool")
		t.Logf("Created request serving nodepool %s/%s", np.Namespace, np.Name)
		result = append(result, np)
	}

	for _, np := range []*hyperv1.NodePool{nonReqServingNodePoolA, nonReqServingNodePoolB, nonReqServingNodePoolC} {
		prepareNonReqServingNodePool(np)
		err := mgmtParentClient.Create(ctx, np)
		g.Expect(err).ToNot(HaveOccurred(), "failed to create non request serving management nodepool")
		t.Logf("Created non request serving nodepool %s/%s", np.Namespace, np.Name)
		result = append(result, np)
	}

	// Wait for nodes to become available for each nodepool
	mgmtClient, err := GetClient()
	g.Expect(err).ToNot(HaveOccurred(), "failed to get management cluster client")
	for _, np := range []*hyperv1.NodePool{reqServingNodePoolA, reqServingNodePoolB, nonReqServingNodePoolA, nonReqServingNodePoolB, nonReqServingNodePoolC} {
		_ = WaitForReadyNodesByNodePool(t, ctx, mgmtClient, np, hyperv1.AWSPlatform)
	}

	return result
}

func TearDownNodePools(ctx context.Context, t *testing.T, kubeconfigPath string, nodePools []*hyperv1.NodePool) {
	g := NewWithT(t)
	mgmtParentClient := kubeClient(t, kubeconfigPath)
	var errs []error
	for _, np := range nodePools {
		t.Logf("Tearing down custom nodepool %s/%s", np.Namespace, np.Name)
		_, err := supportutil.DeleteIfNeeded(ctx, mgmtParentClient, np)
		if err != nil {
			errs = append(errs, err)
		}
	}
	g.Expect(errors.NewAggregate(errs)).ToNot(HaveOccurred())
}

func kubeClient(t *testing.T, kubeconfigPath string) client.Client {
	g := NewWithT(t)
	kubeconfigBytes, err := os.ReadFile(kubeconfigPath)
	g.Expect(err).ToNot(HaveOccurred(), "cannot read kubeconfig: "+kubeconfigPath)
	mgmtParentRESTConfig, err := clientcmd.RESTConfigFromKubeConfig(kubeconfigBytes)
	g.Expect(err).ToNot(HaveOccurred(), "cannot create REST config from kubeconfig")

	kubeClient, err := client.New(mgmtParentRESTConfig, client.Options{Scheme: hyperapi.Scheme})
	g.Expect(err).ToNot(HaveOccurred(), "cannot get client")
	return kubeClient
}
