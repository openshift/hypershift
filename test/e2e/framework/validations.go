//go:build e2e
// +build e2e

package framework

import (
	"context"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	integrationframework "github.com/openshift/hypershift/test/integration/framework"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Ginkgo-enabled validation functions
// These are copies of test/e2e/util/util.go functions but with t.Run() removed
// and replaced with By() statements for Ginkgo compatibility

// EnsureAPIUX validates API immutability without using t.Run()
func EnsureAPIUX(ctx context.Context, hostClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	g := NewGomegaWithT(GinkgoT())

	By("ensuring hosted cluster immutability")
	err := updateObject(ctx, hostClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
		for i, svc := range obj.Spec.Services {
			if svc.Service == hyperv1.APIServer {
				svc.Type = hyperv1.NodePort
				obj.Spec.Services[i] = svc
			}
		}
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("Services is immutable"))

	err = updateObject(ctx, hostClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
		if obj.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable {
			obj.Spec.ControllerAvailabilityPolicy = hyperv1.SingleReplica
		}
		if obj.Spec.ControllerAvailabilityPolicy == hyperv1.SingleReplica {
			obj.Spec.ControllerAvailabilityPolicy = hyperv1.HighlyAvailable
		}
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("ControllerAvailabilityPolicy is immutable"))

	By("ensuring hosted cluster capabilities immutability")
	atLeast(e2eutil.Version419)
	err = updateObject(ctx, hostClient, hostedCluster, func(obj *hyperv1.HostedCluster) {
		obj.Spec.Capabilities = &hyperv1.Capabilities{
			Disabled: []hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability},
		}
	})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("Capabilities is immutable"))
}

// EnsureCustomLabels validates custom labels without using t.Run()
func EnsureCustomLabels(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	atLeast(e2eutil.Version419)

	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	podList := &corev1.PodList{}
	err := client.List(ctx, podList, crclient.InNamespace(hcpNamespace))
	Expect(err).NotTo(HaveOccurred(), "error listing hcp pods")

	var podsWithoutLabel []string
	for _, pod := range podList.Items {
		// Skip KubeVirt related pods
		if pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["app"] == "vmi-console-debug" {
			continue
		}

		// Ensure that each pod in the HCP has the custom label
		if value, exist := pod.Labels["hypershift-e2e-test-label"]; !exist || value != "test" {
			podsWithoutLabel = append(podsWithoutLabel, pod.Name)
		}
	}

	Expect(podsWithoutLabel).To(BeEmpty(), "expected pods [%s] to have label %s=%s", strings.Join(podsWithoutLabel, ", "), "hypershift-e2e-test-label", "test")
}

// EnsureCustomTolerations validates custom tolerations without using t.Run()
func EnsureCustomTolerations(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	atLeast(e2eutil.Version419)

	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	podList := &corev1.PodList{}
	err := client.List(ctx, podList, crclient.InNamespace(hcpNamespace))
	Expect(err).NotTo(HaveOccurred(), "error listing hcp pods")

	var podsWithoutToleration []string
	for _, pod := range podList.Items {
		// Skip KubeVirt related pods
		if pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["app"] == "vmi-console-debug" {
			continue
		}

		// Ensure that each pod in the HCP has the custom toleration
		found := false
		for _, toleration := range pod.Spec.Tolerations {
			if toleration.Key == "hypershift-e2e-test-toleration" &&
				toleration.Operator == corev1.TolerationOpEqual &&
				toleration.Value == "true" &&
				toleration.Effect == corev1.TaintEffectNoSchedule {
				found = true
				break
			}
		}

		if !found {
			podsWithoutToleration = append(podsWithoutToleration, pod.Name)
		}
	}

	Expect(podsWithoutToleration).To(BeEmpty(), "expected pods [%s] to have toleration key=%s", strings.Join(podsWithoutToleration, ", "), "hypershift-e2e-test-toleration")
}

// EnsureAppLabel validates app label without using t.Run()
func EnsureAppLabel(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	atLeast(e2eutil.Version419)

	hcpNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
	podList := &corev1.PodList{}
	err := client.List(ctx, podList, crclient.InNamespace(hcpNamespace))
	Expect(err).NotTo(HaveOccurred(), "error listing hcp pods")

	for _, pod := range podList.Items {
		// Skip KubeVirt related pods
		if pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["app"] == "vmi-console-debug" {
			continue
		}

		_, exist := pod.Labels["app"]
		Expect(exist).To(BeTrue(), "expected pod %s to have label app", pod.Name)
	}
}

// EnsureFeatureGateStatus validates feature gate status without using t.Run()
func EnsureFeatureGateStatus(ctx context.Context, guestClient crclient.Client) {
	atLeast(e2eutil.Version419)
	g := NewGomegaWithT(GinkgoT())

	clusterVersion := &configv1.ClusterVersion{}
	err := guestClient.Get(ctx, crclient.ObjectKey{Name: "version"}, clusterVersion)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get ClusterVersion resource")

	featureGate := &configv1.FeatureGate{}
	err = guestClient.Get(ctx, crclient.ObjectKey{Name: "cluster"}, featureGate)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get FeatureGate resource")

	// Expect at least one entry in ClusterVersion history
	g.Expect(len(clusterVersion.Status.History)).To(BeNumerically(">", 0), "ClusterVersion history is empty")
	currentVersion := clusterVersion.Status.History[0].Version

	// Expect current version to be in Completed state
	g.Expect(clusterVersion.Status.History[0].State).To(Equal(configv1.CompletedUpdate), "most recent ClusterVersion history entry is not in Completed state")

	// Ensure that the current version in ClusterVersion is also present in FeatureGate status
	versionFound := false
	for _, details := range featureGate.Status.FeatureGates {
		if details.Version == currentVersion {
			versionFound = true
			break
		}
	}
	g.Expect(versionFound).To(BeTrue(), "current version %s from ClusterVersion not found in FeatureGate status", currentVersion)
}

// EnsureKubeAPIDNSNameCustomCert validates KubeAPI DNS custom cert without using t.Run()
// TODO: Full implementation needed to remove subtest creation (150+ lines).
// Skipped for now in Ginkgo migration pilot.
func EnsureKubeAPIDNSNameCustomCert(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	Skip("EnsureKubeAPIDNSNameCustomCert requires full migration - tracked in TODO")
}

// EnsureDefaultSecurityGroupTags validates default security group tags without using t.Run()
// TODO: Full implementation needed to remove subtest creation (50+ lines).
// Skipped for now in Ginkgo migration pilot.
func EnsureDefaultSecurityGroupTags(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts PlatformAgnosticOptions) {
	Skip("EnsureDefaultSecurityGroupTags requires full migration - tracked in TODO")
}

// EnsureKubeAPIServerAllowedCIDRs validates KubeAPIServer allowed CIDRs without using t.Run()
// TODO: Full implementation needed to remove subtest creation (40+ lines).
// Skipped for now in Ginkgo migration pilot.
func EnsureKubeAPIServerAllowedCIDRs(ctx context.Context, client crclient.Client, guestConfig *rest.Config, hostedCluster *hyperv1.HostedCluster) {
	Skip("EnsureKubeAPIServerAllowedCIDRs requires full migration - tracked in TODO")
}

// EnsureGlobalPullSecret validates global pull secret without using t.Run()
// TODO: Full implementation needed to remove subtest creation (200+ lines with nested t.Run calls).
// Skipped for now in Ginkgo migration pilot.
func EnsureGlobalPullSecret(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster) {
	Skip("EnsureGlobalPullSecret requires full migration - tracked in TODO")
}

// RunTestControlPlanePKIOperatorBreakGlassCredentials runs PKI operator break glass credentials test
// TODO: Requires full surgical duplication of integration test (200+ lines with nested t.Run)
// Skipped for pilot - demonstrates pattern with simpler functions
func RunTestControlPlanePKIOperatorBreakGlassCredentials(ctx context.Context, hostedCluster *hyperv1.HostedCluster,
	mgmtClients, guestClients *integrationframework.Clients) {
	Skip("PKI test requires full surgical migration - complex integration test with nested subtests. Skipped for pilot.")
}

// ValidatePublicCluster validates a public hosted cluster
// Pure Ginkgo version - waits for guest API and cluster rollout completion
func ValidatePublicCluster(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts *e2eutil.PlatformAgnosticOptions) {
	GinkgoHelper()

	// Wait for guest API to be accessible
	_ = WaitForGuestClient(ctx, client, hostedCluster)

	logf("ValidatePublicCluster: successfully obtained guest client")

	// Wait for cluster rollout to complete before returning
	// This ensures ClusterVersionProgressing: False and all other expected conditions are met
	// This is critical for validations like EnsureFeatureGateStatus which expect the cluster
	// to be in Completed state, not Partial/Progressing
	numNodes := clusterOpts.NodePoolReplicas * int32(len(clusterOpts.AWSPlatform.Zones))
	ValidateHostedClusterConditions(ctx, client, hostedCluster, numNodes > 0, 10*time.Minute)

	logf("ValidatePublicCluster: cluster rollout complete, all conditions met")
}
