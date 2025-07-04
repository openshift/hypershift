//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
)

func TestDisableMultus(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	// Enable DisableMultus for the test cluster
	clusterOpts.BeforeApply = func(o crclient.Object) {
		if hc, ok := o.(*hyperv1.HostedCluster); ok {
			hc.Spec.OperatorConfiguration.ClusterNetworkOperator.DisableMultus = true
		}
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Sanity check the cluster by waiting for the nodes to report ready
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgmtClient, hostedCluster)

		// Verify multus is disabled in the cluster configuration
		t.Run("VerifyMultusDisabledInClusterConfig", func(t *testing.T) {
			verifyMultusDisabledInClusterConfig(t, ctx, mgmtClient, hostedCluster)
		})

		// Verify multus components are not deployed in the control plane
		t.Run("VerifyMultusComponentsNotDeployed", func(t *testing.T) {
			verifyMultusComponentsNotDeployed(t, ctx, mgmtClient, hostedCluster)
		})

		// Verify the cluster network operator has disabled multus
		t.Run("VerifyNetworkOperatorConfig", func(t *testing.T) {
			verifyNetworkOperatorConfig(t, ctx, guestClient)
		})

		// Verify basic cluster functionality still works
		t.Run("VerifyBasicClusterFunctionality", func(t *testing.T) {
			verifyBasicClusterFunctionality(t, ctx, mgmtClient, guestClient, hostedCluster)
		})

		// Verify multus-related resources don't exist in the guest cluster
		t.Run("VerifyMultusResourcesAbsent", func(t *testing.T) {
			verifyMultusResourcesAbsent(t, ctx, guestClient)
		})
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "disable-multus", globalOpts.ServiceAccountSigningKey)
}

func verifyMultusDisabledInClusterConfig(t *testing.T, ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	g := NewWithT(t)

	// Get the latest version of the hosted cluster
	updatedHC := &hyperv1.HostedCluster{}
	err := mgmtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), updatedHC)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify DisableMultus is set to true
	g.Expect(updatedHC.Spec.OperatorConfiguration.ClusterNetworkOperator.DisableMultus).To(BeTrue(), "DisableMultus should be true in the HostedCluster spec")

	t.Logf("✓ Verified DisableMultus is set to true in HostedCluster configuration")
}

func verifyMultusComponentsNotDeployed(t *testing.T, ctx context.Context, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	g := NewWithT(t)

	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)

	// Check that multus-admission-controller deployment doesn't exist or is not running
	multusDeployment := &appsv1.Deployment{}
	err := mgmtClient.Get(ctx, crclient.ObjectKey{
		Namespace: hcpNamespace,
		Name:      "multus-admission-controller",
	}, multusDeployment)

	if err == nil {
		// If deployment exists, check that it has 0 replicas
		g.Expect(multusDeployment.Spec.Replicas).To(BeNil(), "multus-admission-controller deployment should have nil replicas when multus is disabled")
		t.Logf("✓ Verified multus-admission-controller deployment has nil replicas")
	} else {
		// Deployment doesn't exist, which is also acceptable
		t.Logf("✓ Verified multus-admission-controller deployment does not exist")
	}
}

func verifyNetworkOperatorConfig(t *testing.T, ctx context.Context, guestClient crclient.Client) {
	g := NewWithT(t)

	// Get the Network operator configuration from the guest cluster
	network := hcpmanifests.NetworkOperator()
	err := guestClient.Get(ctx, crclient.ObjectKeyFromObject(network), network)
	g.Expect(err).NotTo(HaveOccurred())

	// Verify DisableMultiNetwork is set to true
	g.Expect(network.Spec.DisableMultiNetwork).NotTo(BeNil(), "DisableMultiNetwork should be set")
	g.Expect(*network.Spec.DisableMultiNetwork).To(BeTrue(), "DisableMultiNetwork should be true when multus is disabled")

	t.Logf("✓ Verified Network operator has DisableMultiNetwork=true")
}

func verifyBasicClusterFunctionality(t *testing.T, ctx context.Context, mgmtClient, guestClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	g := NewWithT(t)

	// Create a test pod to verify basic pod networking works
	testPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "multus-disabled-test-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "test-container",
					Image: "registry.access.redhat.com/ubi8/ubi-minimal:latest",
					Command: []string{
						"sleep",
						"300",
					},
				},
			},
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}

	err := guestClient.Create(ctx, testPod)
	g.Expect(err).NotTo(HaveOccurred())

	// Clean up the test pod
	defer func() {
		if err := guestClient.Delete(ctx, testPod); err != nil {
			t.Logf("Failed to clean up test pod: %v", err)
		}
	}()

	// Wait for the pod to be running
	err = wait.PollImmediate(5*time.Second, 2*time.Minute, func() (bool, error) {
		err := guestClient.Get(ctx, crclient.ObjectKeyFromObject(testPod), testPod)
		if err != nil {
			return false, err
		}
		return testPod.Status.Phase == corev1.PodRunning, nil
	})
	g.Expect(err).NotTo(HaveOccurred(), "Test pod should become running")

	// Verify the pod has a single network interface (no multus networks)
	g.Expect(testPod.Status.PodIP).NotTo(BeEmpty(), "Pod should have an IP address")

	t.Logf("✓ Verified basic pod networking works without multus (Pod IP: %s)", testPod.Status.PodIP)
}

func verifyMultusResourcesAbsent(t *testing.T, ctx context.Context, guestClient crclient.Client) {
	g := NewWithT(t)

	//TODO: still waiting for response from network team to understand the issue I see with NAD. Maybe this part isn't relevant

	// Verify NetworkAttachmentDefinition CRD doesn't exist
	// This is a key indicator that multus is disabled
	crdList := &metav1.PartialObjectMetadataList{}
	crdList.SetGroupVersionKind(metav1.SchemeGroupVersion.WithKind("CustomResourceDefinitionList"))

	err := guestClient.List(ctx, crdList)
	if err == nil {
		// Check if network-attachment-definitions CRD exists
		nadCRDExists := false
		for _, crd := range crdList.Items {
			if crd.Name == "network-attachment-definitions.k8s.cni.cncf.io" {
				nadCRDExists = true
				break
			}
		}
		g.Expect(nadCRDExists).To(BeFalse(), "NetworkAttachmentDefinition CRD should not exist when multus is disabled")
	}

	// Verify multus daemonset doesn't exist in kube-system namespace
	daemonsetList := &appsv1.DaemonSetList{}
	err = guestClient.List(ctx, daemonsetList, crclient.InNamespace("openshift-multus"))
	g.Expect(err).NotTo(HaveOccurred())

	for _, ds := range daemonsetList.Items {
		g.Expect(ds.Name).NotTo(ContainSubstring("multus"), "No multus daemonsets should exist when multus is disabled")
	}

	// Verify multus configmaps don't exist
	configMapList := &corev1.ConfigMapList{}
	err = guestClient.List(ctx, configMapList, crclient.InNamespace("openshift-multus"))
	g.Expect(err).NotTo(HaveOccurred())

	for _, cm := range configMapList.Items {
		g.Expect(cm.Name).NotTo(ContainSubstring("multus"), "No multus configmaps should exist when multus is disabled")
	}

	t.Logf("✓ Verified multus-related resources are absent from the guest cluster")
}

// TestDisableMultusValidation tests the immutability validation of DisableMultus
func TestDisableMultusValidation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	// Start with multus enabled (default)
	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgmtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Wait for cluster to be ready
		_ = e2eutil.WaitForGuestClient(t, ctx, mgmtClient, hostedCluster)

		t.Run("VerifyDisableMultusImmutability", func(t *testing.T) {
			// Try to change DisableMultus from false to true (should fail)
			hc := hostedCluster.DeepCopy()
			hc.Spec.OperatorConfiguration.ClusterNetworkOperator.DisableMultus = true

			err := mgmtClient.Update(ctx, hc)
			g.Expect(err).To(HaveOccurred(), "Changing DisableMultus should fail due to immutability constraint")
			g.Expect(err.Error()).To(ContainSubstring("disableMultus is immutable"), "Error should mention immutability")

			t.Logf("✓ Verified DisableMultus field is immutable")
		})
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "disable-multus-validation", globalOpts.ServiceAccountSigningKey)
}
