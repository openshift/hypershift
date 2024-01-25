//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

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
	g := NewWithT(t)

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

	ppConfigMapLabels := map[string]string{
		controllerGeneratedPPConfig: "true",
	}
	g.Eventually(func(gg Gomega) {
		cms := &corev1.ConfigMapList{}
		err := mc.managementClient.List(ctx, cms, crclient.InNamespace(controlPlaneNamespace), crclient.MatchingLabels(ppConfigMapLabels))
		gg.Expect(err).ToNot(HaveOccurred(), "unable to find configmaps in namespace %q with label %q: %v", controlPlaneNamespace, controllerGeneratedPPConfig, err)

		//Looking for the matching configmap for this nodepool in the list
		cmName := ppConfigMapNamePrefix + nodePool.Name
		t.Logf("Looking for Configmap %s ...", cmName)
		ppCMs := []corev1.ConfigMap{}
		for _, cm := range cms.Items {
			if cm.Name == cmName {
				ppCMs = append(ppCMs, cm)
			}
		}
		gg.Expect(ppCMs).To(HaveLen(1), "failed to find performance profile configmap for nodepool %q", nodePool.Name)

		cm := ppCMs[0]
		t.Logf("Configmap %s/%s found. Checking Labels and Annotations...", cm.Namespace, cm.Name)
		//Checking basic label
		gg.Expect(controllerGeneratedPPConfig).To(BeKeyOf(cm.Labels), "Unable to find %q label in PerformanceProfile ConfigMap %q", controllerGeneratedPPConfig, cm.Name)

		gg.Expect(nodePoolNameLabel).To(BeKeyOf(cm.Labels), "Unable to find %q label in PerformanceProfile ConfigMap %q", nodePoolNameLabel, cm.Name)
		gg.Expect(cm.Labels[nodePoolNameLabel]).To(Equal(nodePool.Name))

		gg.Expect(nodePoolNsNameAnnotation).To(BeKeyOf(cm.Annotations), "Unable to find %q annotation in PerformanceProfile ConfigMap %q", nodePoolNsNameAnnotation, cm.Name)
		gg.Expect(cm.Annotations[nodePoolNsNameAnnotation]).To(Equal(nodePool.Namespace + "/" + nodePool.Name))
	}).Within(1 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
	t.Log("... configmap found with proper metadata.")

	t.Log("Deleting configmap reference from nodepool ...")
	baseNP := nodePool.DeepCopy()
	nodePool.Spec = np.Spec
	if err := mc.managementClient.Patch(ctx, &nodePool, crclient.MergeFrom(baseNP)); err != nil {
		t.Fatalf("failed to update nodepool %s after removing PerformanceProfile config: %v", nodePool.Name, err)
	}

	g.Eventually(func(gg Gomega) {
		cms := &corev1.ConfigMapList{}
		err := mc.managementClient.List(ctx, cms, crclient.InNamespace(controlPlaneNamespace), crclient.MatchingLabels(ppConfigMapLabels))
		gg.Expect(err).ToNot(HaveOccurred(), "unable to find configmaps in namespace %q with label %q: %v", controlPlaneNamespace, controllerGeneratedPPConfig, err)

		//Looking for the matching configmap for this nodepool in the list
		cmName := ppConfigMapNamePrefix + nodePool.Name
		t.Logf("Looking for Configmap %s ...", cmName)
		ppCMs := []corev1.ConfigMap{}
		for _, cm := range cms.Items {
			if cm.Name == cmName {
				ppCMs = append(ppCMs, cm)
			}
		}
		gg.Expect(ppCMs).To(BeEmpty(), "Performance Profile ConfigMap '%s/%s' for nodepool %q found. It should be deleted", controlPlaneNamespace, cmName, nodePool.Name)
	}).Within(1 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
	t.Log("... configmap reference deleted")

	t.Log("Ending NTO PerformanceProfile test: OK")
}
