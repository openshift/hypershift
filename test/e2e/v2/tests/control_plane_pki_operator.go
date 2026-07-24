//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package tests

import (
	"fmt"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/internal"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	pkiOperatorConfigMapName = "control-plane-pki-operator-config"
	pkiOperatorAppLabel      = "control-plane-pki-operator"
	pkiOperatorMetricsPort   = "8443"
)

// hostedClusterHasTLSProfileType returns true if the HostedCluster has the specified TLS profile type.
func hostedClusterHasTLSProfileType(hc *hyperv1.HostedCluster, profileType configv1.TLSProfileType) bool {
	return hc.Spec.Configuration != nil &&
		hc.Spec.Configuration.APIServer != nil &&
		hc.Spec.Configuration.APIServer.TLSSecurityProfile != nil &&
		hc.Spec.Configuration.APIServer.TLSSecurityProfile.Type == profileType
}

func RegisterControlPlanePKIOperatorTests(getTestCtx internal.TestContextGetter) {
	VerifyPKIOperatorTLSConfigTest(getTestCtx)
}

// VerifyPKIOperatorTLSConfigTest validates that when TLS security profile changes are applied
// to the HostedCluster, the control-plane-pki-operator config reflects the correct minTLSVersion
// and that the control-plane-pki-operator's HTTPS endpoint enforces those TLS versions correctly.
func VerifyPKIOperatorTLSConfigTest(getTestCtx internal.TestContextGetter) {
	When("PKI operator TLS configuration is modified", Ordered, Serial, Label("Lifecycle"), func() {
		var tc *internal.TestContext
		var originalTLSProfile *configv1.TLSSecurityProfile
		var mgmtRestConfig *rest.Config
		var mgmtKubeClient *kubernetes.Clientset
		var podUIDBeforeMutation string

		BeforeAll(func() {
			tc = getTestCtx()

			// Capture original TLS security profile from HostedCluster
			hostedCluster := tc.GetHostedCluster()
			if hostedCluster.Spec.Configuration != nil &&
				hostedCluster.Spec.Configuration.APIServer != nil &&
				hostedCluster.Spec.Configuration.APIServer.TLSSecurityProfile != nil {
				originalTLSProfile = hostedCluster.Spec.Configuration.APIServer.TLSSecurityProfile.DeepCopy()
			}

			// Setup management cluster REST config and kubernetes client for pod exec
			var err error
			mgmtRestConfig, err = e2eutil.GetConfig()
			Expect(err).NotTo(HaveOccurred(), "failed to get management cluster REST config")
			mgmtKubeClient, err = kubernetes.NewForConfig(mgmtRestConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create management cluster kubernetes client")
		})

		It("should have control-plane-pki-operator-config ConfigMap with TLS configuration", func() {
			// Check in management cluster's control plane namespace, not hosted cluster
			mgmtClient := tc.MgmtClient
			cm := &corev1.ConfigMap{}
			err := mgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      pkiOperatorConfigMapName,
			}, cm)

			Expect(err).NotTo(HaveOccurred(), "failed to get PKI operator ConfigMap %s/%s",
				tc.ControlPlaneNamespace, pkiOperatorConfigMapName)

			Expect(cm.Data).NotTo(BeNil(), "ConfigMap data should not be nil")
			Expect(cm.Data).To(HaveKey("config.yaml"), "ConfigMap should have config.yaml key")
			Expect(cm.Data["config.yaml"]).NotTo(BeEmpty(), "config.yaml should not be empty")
		})

		It("should have minTLSVersion set to VersionTLS12 with default/intermediate profile", func() {
			// Check HostedCluster TLS profile configuration
			hostedCluster := tc.GetHostedCluster()

			// Only run for nil (default) or explicit Intermediate profile
			hasProfile := hostedCluster.Spec.Configuration != nil &&
				hostedCluster.Spec.Configuration.APIServer != nil &&
				hostedCluster.Spec.Configuration.APIServer.TLSSecurityProfile != nil

			isDefaultOrIntermediate := !hasProfile || hostedClusterHasTLSProfileType(hostedCluster, configv1.TLSProfileIntermediateType)

			if !isDefaultOrIntermediate {
				Skip("HostedCluster does not have default or Intermediate TLS profile - skipping TLS 1.2 test")
			}

			// Check ConfigMap in management cluster control plane namespace
			mgmtClient := tc.MgmtClient
			cm := &corev1.ConfigMap{}
			err := mgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      pkiOperatorConfigMapName,
			}, cm)
			Expect(err).NotTo(HaveOccurred(), "failed to get PKI operator ConfigMap %s/%s",
				tc.ControlPlaneNamespace, pkiOperatorConfigMapName)

			Expect(cm.Data["config.yaml"]).To(ContainSubstring("minTLSVersion: VersionTLS12"),
				"PKI operator config should have minTLSVersion: VersionTLS12 for intermediate profile")
		})

		It("should accept both TLS 1.2 and TLS 1.3 connections with intermediate profile", func() {
			// Check HostedCluster TLS profile configuration
			hostedCluster := tc.GetHostedCluster()

			// Only run for nil (default) or explicit Intermediate profile
			hasProfile := hostedCluster.Spec.Configuration != nil &&
				hostedCluster.Spec.Configuration.APIServer != nil &&
				hostedCluster.Spec.Configuration.APIServer.TLSSecurityProfile != nil

			isDefaultOrIntermediate := !hasProfile || hostedClusterHasTLSProfileType(hostedCluster, configv1.TLSProfileIntermediateType)

			if !isDefaultOrIntermediate {
				Skip("HostedCluster does not have default or Intermediate TLS profile - skipping TLS 1.2 connectivity test")
			}

			// Get control-plane-pki-operator pod from management cluster
			mgmtClient := tc.MgmtClient
			pkiPodList := &corev1.PodList{}
			Expect(mgmtClient.List(tc.Context, pkiPodList,
				crclient.InNamespace(tc.ControlPlaneNamespace),
				crclient.MatchingLabels{"app": pkiOperatorAppLabel},
			)).To(Succeed(), "failed to list control-plane-pki-operator pods")

			Expect(pkiPodList.Items).NotTo(BeEmpty(),
				"expected at least one control-plane-pki-operator pod in namespace %s", tc.ControlPlaneNamespace)

			pkiPod := &pkiPodList.Items[0]
			Expect(pkiPod.Status.Phase).To(Equal(corev1.PodRunning),
				"control-plane-pki-operator pod should be running")

			GinkgoWriter.Printf("Testing TLS connections to PKI operator pod %s on port %s\n", pkiPod.Name, pkiOperatorMetricsPort)

			// Test TLS 1.2 connection using openssl s_client from within the PKI operator pod
			tls12Result, err := v2util.RunCommandInPod(tc.Context, mgmtKubeClient, mgmtRestConfig,
				tc.ControlPlaneNamespace, pkiPod.Name, "control-plane-pki-operator",
				"sh", "-c",
				fmt.Sprintf("timeout 5 openssl s_client -connect localhost:%s -tls1_2 2>&1 || true", pkiOperatorMetricsPort))
			Expect(err).NotTo(HaveOccurred(), "failed to exec into PKI operator pod for TLS 1.2 test")
			Expect(strings.ToLower(tls12Result)).To(ContainSubstring("tlsv1.2"),
				"should confirm TLS 1.2 was used for PKI operator port %s", pkiOperatorMetricsPort)

			// Test TLS 1.3 connection using openssl s_client
			tls13Result, err := v2util.RunCommandInPod(tc.Context, mgmtKubeClient, mgmtRestConfig,
				tc.ControlPlaneNamespace, pkiPod.Name, "control-plane-pki-operator",
				"sh", "-c",
				fmt.Sprintf("timeout 5 openssl s_client -connect localhost:%s -tls1_3 2>&1 || true", pkiOperatorMetricsPort))
			Expect(err).NotTo(HaveOccurred(), "failed to exec into PKI operator pod for TLS 1.3 test")
			Expect(strings.ToLower(tls13Result)).To(ContainSubstring("tlsv1.3"),
				"should confirm TLS 1.3 was used for PKI operator port %s", pkiOperatorMetricsPort)
		})

		It("should update to minTLSVersion VersionTLS13 when TLS profile changed to Modern", func() {
			// Get the HostedCluster from management cluster and update its TLS profile
			mgmtClient := tc.MgmtClient

			// Capture current pod UID before mutation
			pkiPodList := &corev1.PodList{}
			Expect(mgmtClient.List(tc.Context, pkiPodList,
				crclient.InNamespace(tc.ControlPlaneNamespace),
				crclient.MatchingLabels{"app": pkiOperatorAppLabel},
			)).To(Succeed(), "failed to list control-plane-pki-operator pods")
			Expect(pkiPodList.Items).NotTo(BeEmpty(), "expected at least one control-plane-pki-operator pod before mutation")
			podUIDBeforeMutation = string(pkiPodList.Items[0].UID)
			GinkgoWriter.Printf("Captured pod UID before mutation: %s\n", podUIDBeforeMutation)

			// Update to Modern TLS profile with retry on conflict
			Eventually(func(g Gomega) {
				hostedCluster := &hyperv1.HostedCluster{}
				err := mgmtClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: tc.ClusterNamespace,
					Name:      tc.ClusterName,
				}, hostedCluster)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

				// Initialize Configuration if needed
				if hostedCluster.Spec.Configuration == nil {
					hostedCluster.Spec.Configuration = &hyperv1.ClusterConfiguration{}
				}
				if hostedCluster.Spec.Configuration.APIServer == nil {
					hostedCluster.Spec.Configuration.APIServer = &configv1.APIServerSpec{}
				}

				// Update to Modern TLS profile in the HostedCluster CR
				hostedCluster.Spec.Configuration.APIServer.TLSSecurityProfile = &configv1.TLSSecurityProfile{
					Type:   configv1.TLSProfileModernType,
					Modern: &configv1.ModernTLSProfile{},
				}
				err = mgmtClient.Update(tc.Context, hostedCluster)
				if apierrors.IsConflict(err) {
					return
				}
				g.Expect(err).NotTo(HaveOccurred(), "failed to update HostedCluster TLS profile to Modern")
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "failed to update HostedCluster to Modern profile")

			GinkgoWriter.Printf("Updated HostedCluster to Modern TLS profile, waiting for changes to propagate\n")

			// Wait for ConfigMap in management cluster to reflect the Modern profile with TLS 1.3
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				err := mgmtClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: tc.ControlPlaneNamespace,
					Name:      pkiOperatorConfigMapName,
				}, cm)

				if apierrors.IsNotFound(err) {
					// ConfigMap doesn't exist yet, retry
					return
				}
				g.Expect(err).NotTo(HaveOccurred(), "failed to get ConfigMap")

				configYAML := cm.Data["config.yaml"]
				g.Expect(configYAML).To(ContainSubstring("minTLSVersion: VersionTLS13"),
					"PKI operator config should have minTLSVersion: VersionTLS13 for modern profile")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("should accept TLS 1.3 but reject TLS 1.2 with Modern profile", func() {
			// Verify HostedCluster has Modern profile (fetch fresh, not cached)
			mgmtClient := tc.MgmtClient
			hostedCluster := &hyperv1.HostedCluster{}
			err := mgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ClusterNamespace,
				Name:      tc.ClusterName,
			}, hostedCluster)
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

			if !hostedClusterHasTLSProfileType(hostedCluster, configv1.TLSProfileModernType) {
				Fail("HostedCluster does not have Modern TLS profile - previous ordered test should have set it")
			}

			// Wait for PKI operator pod to restart and pick up the new TLS config
			// Must verify it's a new pod (different UID) to avoid testing old config
			var newPodUID string
			Eventually(func(g Gomega) {
				pkiPodList := &corev1.PodList{}
				g.Expect(mgmtClient.List(tc.Context, pkiPodList,
					crclient.InNamespace(tc.ControlPlaneNamespace),
					crclient.MatchingLabels{"app": pkiOperatorAppLabel},
				)).To(Succeed(), "failed to list control-plane-pki-operator pods")

				g.Expect(pkiPodList.Items).NotTo(BeEmpty(),
					"expected at least one control-plane-pki-operator pod")

				pkiPod := &pkiPodList.Items[0]
				newPodUID = string(pkiPod.UID)

				// Verify this is a new pod (different UID from before mutation)
				g.Expect(newPodUID).NotTo(Equal(podUIDBeforeMutation),
					"pod UID should have changed after TLS config mutation")

				g.Expect(pkiPod.Status.Phase).To(Equal(corev1.PodRunning),
					"control-plane-pki-operator pod should be running")

				// Check Ready condition
				readyFound := false
				for _, cond := range pkiPod.Status.Conditions {
					if cond.Type == corev1.PodReady {
						readyFound = true
						g.Expect(cond.Status).To(Equal(corev1.ConditionTrue),
							"control-plane-pki-operator pod should be ready")
					}
				}
				g.Expect(readyFound).To(BeTrue(), "PodReady condition not found on pod %s", pkiPod.Name)

				// Ensure all containers are ready
				for _, containerStatus := range pkiPod.Status.ContainerStatuses {
					g.Expect(containerStatus.Ready).To(BeTrue(),
						"container %s should be ready in pod %s", containerStatus.Name, pkiPod.Name)
				}
			}, 2*time.Minute, 5*time.Second).Should(Succeed(), "PKI operator pod should be running and ready")

			GinkgoWriter.Printf("New pod UID after mutation to Modern profile: %s\n", newPodUID)
			// Update the tracked UID for the next mutation
			podUIDBeforeMutation = newPodUID

			// Test TLS 1.3 connection should succeed using openssl
			// Wrap in Eventually since pod might still be restarting even after Ready
			var tls13Result string
			Eventually(func(g Gomega) {
				// Re-fetch pod list to get current pod
				pkiPodList := &corev1.PodList{}
				g.Expect(mgmtClient.List(tc.Context, pkiPodList,
					crclient.InNamespace(tc.ControlPlaneNamespace),
					crclient.MatchingLabels{"app": pkiOperatorAppLabel},
				)).To(Succeed(), "failed to list control-plane-pki-operator pods")
				g.Expect(pkiPodList.Items).NotTo(BeEmpty(),
					"expected at least one control-plane-pki-operator pod")
				pkiPod := &pkiPodList.Items[0]

				GinkgoWriter.Printf("Testing TLS 1.3 connection to PKI operator pod %s with Modern profile\n", pkiPod.Name)
				var err error
				tls13Result, err = v2util.RunCommandInPod(tc.Context, mgmtKubeClient, mgmtRestConfig,
					tc.ControlPlaneNamespace, pkiPod.Name, "control-plane-pki-operator",
					"sh", "-c",
					fmt.Sprintf("timeout 5 openssl s_client -connect localhost:%s -tls1_3 2>&1 || true", pkiOperatorMetricsPort))
				g.Expect(err).NotTo(HaveOccurred(), "failed to exec into PKI operator pod for TLS 1.3 test")
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "TLS 1.3 connection test should succeed")

			Expect(strings.ToLower(tls13Result)).To(ContainSubstring("tlsv1.3"),
				"should confirm TLS 1.3 was used for PKI operator with Modern profile")

			// Test TLS 1.2 connection should fail using openssl
			// Wrap in Eventually since pod might still be restarting
			var tls12Result string
			Eventually(func(g Gomega) {
				// Re-fetch pod list to get current pod
				pkiPodList := &corev1.PodList{}
				g.Expect(mgmtClient.List(tc.Context, pkiPodList,
					crclient.InNamespace(tc.ControlPlaneNamespace),
					crclient.MatchingLabels{"app": pkiOperatorAppLabel},
				)).To(Succeed(), "failed to list control-plane-pki-operator pods")
				g.Expect(pkiPodList.Items).NotTo(BeEmpty(),
					"expected at least one control-plane-pki-operator pod")
				pkiPod := &pkiPodList.Items[0]

				GinkgoWriter.Printf("Testing TLS 1.2 connection to PKI operator pod %s with Modern profile\n", pkiPod.Name)
				var err error
				tls12Result, err = v2util.RunCommandInPod(tc.Context, mgmtKubeClient, mgmtRestConfig,
					tc.ControlPlaneNamespace, pkiPod.Name, "control-plane-pki-operator",
					"sh", "-c",
					fmt.Sprintf("timeout 5 openssl s_client -connect localhost:%s -tls1_2 2>&1 || true", pkiOperatorMetricsPort))
				g.Expect(err).NotTo(HaveOccurred(), "failed to exec into PKI operator pod for TLS 1.2 test")
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "TLS 1.2 connection test should succeed (even though it will be rejected)")

			// TLS 1.2 should be rejected - check that no cipher was negotiated
			lowerResult := strings.ToLower(tls12Result)
			Expect(lowerResult).To(ContainSubstring("cipher is (none)"),
				"TLS 1.2 connection should be rejected with modern profile, got: %s", tls12Result)
		})

		It("should downgrade to minTLSVersion VersionTLS12 when Modern TLS profile is removed", func() {
			// Get the HostedCluster from management cluster and update its TLS profile
			mgmtClient := tc.MgmtClient

			// First verify it currently has Modern profile (fetch fresh, not cached)
			hostedCluster := &hyperv1.HostedCluster{}
			err := mgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ClusterNamespace,
				Name:      tc.ClusterName,
			}, hostedCluster)
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

			if !hostedClusterHasTLSProfileType(hostedCluster, configv1.TLSProfileModernType) {
				Fail("HostedCluster does not have Modern TLS profile - previous ordered tests should have set it")
			}

			// Remove Modern TLS profile (downgrade to default/Intermediate) with retry on conflict
			Eventually(func(g Gomega) {
				hostedCluster := &hyperv1.HostedCluster{}
				err := mgmtClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: tc.ClusterNamespace,
					Name:      tc.ClusterName,
				}, hostedCluster)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

				// Remove TLS profile to downgrade to default (Intermediate)
				hostedCluster.Spec.Configuration.APIServer.TLSSecurityProfile = nil

				err = mgmtClient.Update(tc.Context, hostedCluster)
				if apierrors.IsConflict(err) {
					return
				}
				g.Expect(err).NotTo(HaveOccurred(), "failed to remove TLS profile to downgrade to Intermediate")
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "failed to downgrade HostedCluster to Intermediate profile")

			GinkgoWriter.Printf("Removed Modern TLS profile from HostedCluster (downgraded to default/Intermediate), waiting for changes to propagate\n")

			// Wait for ConfigMap in management cluster to reflect the Intermediate profile with TLS 1.2
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				err := mgmtClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: tc.ControlPlaneNamespace,
					Name:      pkiOperatorConfigMapName,
				}, cm)

				if apierrors.IsNotFound(err) {
					// ConfigMap doesn't exist yet, retry
					return
				}
				g.Expect(err).NotTo(HaveOccurred(), "failed to get ConfigMap")

				configYAML := cm.Data["config.yaml"]
				g.Expect(configYAML).To(ContainSubstring("minTLSVersion: VersionTLS12"),
					"PKI operator config should have minTLSVersion: VersionTLS12 for intermediate profile")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("should accept both TLS 1.2 and TLS 1.3 connections after downgrade to Intermediate profile", func() {
			// Verify HostedCluster does not have Modern profile (fetch fresh, not cached)
			mgmtClient := tc.MgmtClient
			hostedCluster := &hyperv1.HostedCluster{}
			err := mgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ClusterNamespace,
				Name:      tc.ClusterName,
			}, hostedCluster)
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")

			// Check that TLS profile is nil (default/Intermediate) or explicitly not Modern
			if hostedClusterHasTLSProfileType(hostedCluster, configv1.TLSProfileModernType) {
				Fail("HostedCluster still has Modern TLS profile - previous ordered test should have downgraded it")
			}

			// Wait for PKI operator pod to restart and pick up the new TLS config
			// Must verify it's a new pod (different UID) to avoid testing old config
			var newPodUID string
			Eventually(func(g Gomega) {
				pkiPodList := &corev1.PodList{}
				g.Expect(mgmtClient.List(tc.Context, pkiPodList,
					crclient.InNamespace(tc.ControlPlaneNamespace),
					crclient.MatchingLabels{"app": pkiOperatorAppLabel},
				)).To(Succeed(), "failed to list control-plane-pki-operator pods")

				g.Expect(pkiPodList.Items).NotTo(BeEmpty(),
					"expected at least one control-plane-pki-operator pod")

				pkiPod := &pkiPodList.Items[0]
				newPodUID = string(pkiPod.UID)

				// Verify this is a new pod (different UID from before mutation)
				g.Expect(newPodUID).NotTo(Equal(podUIDBeforeMutation),
					"pod UID should have changed after TLS config mutation")

				g.Expect(pkiPod.Status.Phase).To(Equal(corev1.PodRunning),
					"control-plane-pki-operator pod should be running")

				// Check Ready condition
				readyFound := false
				for _, cond := range pkiPod.Status.Conditions {
					if cond.Type == corev1.PodReady {
						readyFound = true
						g.Expect(cond.Status).To(Equal(corev1.ConditionTrue),
							"control-plane-pki-operator pod should be ready")
					}
				}
				g.Expect(readyFound).To(BeTrue(), "PodReady condition not found on pod %s", pkiPod.Name)

				// Ensure all containers are ready
				for _, containerStatus := range pkiPod.Status.ContainerStatuses {
					g.Expect(containerStatus.Ready).To(BeTrue(),
						"container %s should be ready in pod %s", containerStatus.Name, pkiPod.Name)
				}
			}, 2*time.Minute, 5*time.Second).Should(Succeed(), "PKI operator pod should be running and ready")

			GinkgoWriter.Printf("New pod UID after downgrade to Intermediate profile: %s\n", newPodUID)

			// Test TLS 1.2 connection should succeed using openssl
			// Wrap in Eventually since pod might still be restarting even after Ready
			var tls12Result string
			Eventually(func(g Gomega) {
				// Re-fetch pod list to get current pod
				pkiPodList := &corev1.PodList{}
				g.Expect(mgmtClient.List(tc.Context, pkiPodList,
					crclient.InNamespace(tc.ControlPlaneNamespace),
					crclient.MatchingLabels{"app": pkiOperatorAppLabel},
				)).To(Succeed(), "failed to list control-plane-pki-operator pods")
				g.Expect(pkiPodList.Items).NotTo(BeEmpty(),
					"expected at least one control-plane-pki-operator pod")
				pkiPod := &pkiPodList.Items[0]

				GinkgoWriter.Printf("Testing TLS 1.2 connection to PKI operator pod %s after downgrade to Intermediate profile\n", pkiPod.Name)
				var err error
				tls12Result, err = v2util.RunCommandInPod(tc.Context, mgmtKubeClient, mgmtRestConfig,
					tc.ControlPlaneNamespace, pkiPod.Name, "control-plane-pki-operator",
					"sh", "-c",
					fmt.Sprintf("timeout 5 openssl s_client -connect localhost:%s -tls1_2 2>&1 || true", pkiOperatorMetricsPort))
				g.Expect(err).NotTo(HaveOccurred(), "failed to exec into PKI operator pod for TLS 1.2 test")
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "TLS 1.2 connection test should succeed")

			Expect(strings.ToLower(tls12Result)).To(ContainSubstring("tlsv1.2"),
				"should confirm TLS 1.2 was accepted for PKI operator after downgrade to Intermediate profile")

			// Test TLS 1.3 connection should also succeed using openssl
			// Wrap in Eventually since pod might still be restarting
			var tls13Result string
			Eventually(func(g Gomega) {
				// Re-fetch pod list to get current pod
				pkiPodList := &corev1.PodList{}
				g.Expect(mgmtClient.List(tc.Context, pkiPodList,
					crclient.InNamespace(tc.ControlPlaneNamespace),
					crclient.MatchingLabels{"app": pkiOperatorAppLabel},
				)).To(Succeed(), "failed to list control-plane-pki-operator pods")
				g.Expect(pkiPodList.Items).NotTo(BeEmpty(),
					"expected at least one control-plane-pki-operator pod")
				pkiPod := &pkiPodList.Items[0]

				GinkgoWriter.Printf("Testing TLS 1.3 connection to PKI operator pod %s after downgrade to Intermediate profile\n", pkiPod.Name)
				var err error
				tls13Result, err = v2util.RunCommandInPod(tc.Context, mgmtKubeClient, mgmtRestConfig,
					tc.ControlPlaneNamespace, pkiPod.Name, "control-plane-pki-operator",
					"sh", "-c",
					fmt.Sprintf("timeout 5 openssl s_client -connect localhost:%s -tls1_3 2>&1 || true", pkiOperatorMetricsPort))
				g.Expect(err).NotTo(HaveOccurred(), "failed to exec into PKI operator pod for TLS 1.3 test")
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "TLS 1.3 connection test should succeed")
			Expect(strings.ToLower(tls13Result)).To(ContainSubstring("tlsv1.3"),
				"should confirm TLS 1.3 was accepted for PKI operator with Intermediate profile")
		})

		AfterAll(func() {
			if tc == nil {
				return
			}
			GinkgoWriter.Printf("Restoring original TLS security profile\n")

			// Restore TLS profile with retry on conflict
			Eventually(func(g Gomega) {
				hostedCluster := &hyperv1.HostedCluster{}
				err := tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{
					Namespace: tc.ClusterNamespace,
					Name:      tc.ClusterName,
				}, hostedCluster)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster for cleanup")

				if hostedCluster.Spec.Configuration == nil {
					hostedCluster.Spec.Configuration = &hyperv1.ClusterConfiguration{}
				}
				if hostedCluster.Spec.Configuration.APIServer == nil {
					hostedCluster.Spec.Configuration.APIServer = &configv1.APIServerSpec{}
				}
				hostedCluster.Spec.Configuration.APIServer.TLSSecurityProfile = originalTLSProfile

				err = tc.MgmtClient.Update(tc.Context, hostedCluster)
				if apierrors.IsConflict(err) {
					return
				}
				g.Expect(err).NotTo(HaveOccurred(), "failed to restore original TLS profile")
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "failed to restore original TLS profile")
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:PKIOperator] Control Plane PKI Operator", Label("control-plane-pki-operator"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterControlPlanePKIOperatorTests(func() *internal.TestContext { return testCtx })
})
