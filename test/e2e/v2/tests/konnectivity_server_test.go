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
	"context"
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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// hostedClusterHasTLSProfileType returns true if the HostedCluster has the specified TLS profile type.
func hostedClusterHasTLSProfileType(hc *hyperv1.HostedCluster, profileType configv1.TLSProfileType) bool {
	return hc.Spec.Configuration != nil &&
		hc.Spec.Configuration.APIServer != nil &&
		hc.Spec.Configuration.APIServer.TLSSecurityProfile != nil &&
		hc.Spec.Configuration.APIServer.TLSSecurityProfile.Type == profileType
}

// getTLSMinVersionFromArgs extracts the TLS min version value from container args.
// It handles both formats: "--tls-min-version=VALUE" and "--tls-min-version" "VALUE"
func getTLSMinVersionFromArgs(args []string) string {
	for i, arg := range args {
		// Check for "--tls-min-version=VALUE" format
		if strings.HasPrefix(arg, "--tls-min-version=") {
			return strings.TrimPrefix(arg, "--tls-min-version=")
		}
		// Check for "--tls-min-version" "VALUE" format
		if arg == "--tls-min-version" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// getKubeAPIServerDeployment retrieves the kube-apiserver deployment from the control plane namespace.
func getKubeAPIServerDeployment(ctx context.Context, client crclient.Client, namespace string) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{}
	err := client.Get(ctx, crclient.ObjectKey{
		Namespace: namespace,
		Name:      "kube-apiserver",
	}, deployment)
	return deployment, err
}

// waitForKubeAPIServerRollout waits for the kube-apiserver deployment to complete rollout.
// This ensures all replicas have the latest pod template spec.
func waitForKubeAPIServerRollout(ctx context.Context, client crclient.Client, namespace string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 15*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		deployment, err := getKubeAPIServerDeployment(ctx, client, namespace)
		if err != nil {
			return false, err
		}

		// Check if rollout is complete
		// All replicas must be updated, available, and ready
		if deployment.Status.UpdatedReplicas == *deployment.Spec.Replicas &&
			deployment.Status.Replicas == *deployment.Spec.Replicas &&
			deployment.Status.AvailableReplicas == *deployment.Spec.Replicas &&
			deployment.Status.ObservedGeneration >= deployment.Generation {
			return true, nil
		}
		return false, nil
	})
}

// getReadyKubeAPIServerPod finds and returns a ready kube-apiserver pod.
// This should be called after waiting for rollout to complete.
func getReadyKubeAPIServerPod(ctx context.Context, client crclient.Client, namespace string) (*corev1.Pod, error) {
	kasPodList := &corev1.PodList{}
	err := client.List(ctx, kasPodList,
		crclient.InNamespace(namespace),
		crclient.MatchingLabels{"app": "kube-apiserver"},
	)
	if err != nil {
		return nil, err
	}

	if len(kasPodList.Items) == 0 {
		return nil, fmt.Errorf("no kube-apiserver pods found")
	}

	// Find a ready pod
	for i := range kasPodList.Items {
		if kasPodList.Items[i].Status.Phase == corev1.PodRunning {
			for _, cond := range kasPodList.Items[i].Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					// Verify all containers are ready
					// Skip pods with no container statuses to prevent vacuous pass
					if len(kasPodList.Items[i].Status.ContainerStatuses) == 0 {
						continue
					}

					allReady := true
					for _, cs := range kasPodList.Items[i].Status.ContainerStatuses {
						if !cs.Ready {
							allReady = false
							break
						}
					}
					if allReady {
						return &kasPodList.Items[i], nil
					}
				}
			}
		}
	}

	return nil, fmt.Errorf("no ready kube-apiserver pod found")
}

func RegisterKonnectivityServerTests(getTestCtx internal.TestContextGetter) {
	VerifyKonnectivityServerTLSConfigTest(getTestCtx)
}

// VerifyKonnectivityServerTLSConfigTest validates that when TLS security profile changes are applied
// to the HostedCluster, the konnectivity-server container reflects the correct --tls-min-version flag
// and that the konnectivity-server's HTTPS endpoint enforces those TLS versions correctly.
func VerifyKonnectivityServerTLSConfigTest(getTestCtx internal.TestContextGetter) {
	When("konnectivity-server TLS configuration is modified", Ordered, Label("Lifecycle"), func() {
		var tc *internal.TestContext
		var originalConfiguration *hyperv1.ClusterConfiguration
		var mgmtRestConfig *rest.Config
		var mgmtKubeClient *kubernetes.Clientset

		BeforeAll(func() {
			tc = getTestCtx()

			// Capture original Configuration from HostedCluster to restore complete state later
			hostedCluster := tc.GetHostedCluster()
			if hostedCluster.Spec.Configuration != nil {
				originalConfiguration = hostedCluster.Spec.Configuration.DeepCopy()
			}

			// Setup management cluster REST config and kubernetes client for pod exec
			var err error
			mgmtRestConfig, err = e2eutil.GetConfig()
			Expect(err).NotTo(HaveOccurred(), "failed to get management cluster REST config")
			mgmtKubeClient, err = kubernetes.NewForConfig(mgmtRestConfig)
			Expect(err).NotTo(HaveOccurred(), "failed to create management cluster kubernetes client")
		})

		It("should have konnectivity-server container with --tls-min-version flag", func() {
			// Get kube-apiserver deployment to check pod template spec (source of truth)
			mgmtClient := tc.MgmtClient
			deployment, err := getKubeAPIServerDeployment(tc.Context, mgmtClient, tc.ControlPlaneNamespace)
			Expect(err).NotTo(HaveOccurred(), "failed to get kube-apiserver deployment")

			// Find konnectivity-server container in pod template
			var konnectivityContainer *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == "konnectivity-server" {
					konnectivityContainer = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}

			Expect(konnectivityContainer).NotTo(BeNil(), "konnectivity-server container should exist in kube-apiserver deployment")

			// Verify --tls-min-version flag exists in container args
			// It can be either "--tls-min-version=VALUE" or "--tls-min-version" followed by "VALUE"
			hasTLSMinVersion := false
			for i, arg := range konnectivityContainer.Args {
				if strings.HasPrefix(arg, "--tls-min-version=") || arg == "--tls-min-version" {
					hasTLSMinVersion = true
					break
				}
				// Also check if this is the value following --tls-min-version flag
				if i > 0 && konnectivityContainer.Args[i-1] == "--tls-min-version" {
					hasTLSMinVersion = true
					break
				}
			}
			Expect(hasTLSMinVersion).To(BeTrue(), "konnectivity-server container should have --tls-min-version flag")
		})

		It("should have --tls-min-version=VersionTLS12 with default/intermediate profile", func() {
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

			// Get kube-apiserver deployment to check pod template spec
			mgmtClient := tc.MgmtClient
			deployment, err := getKubeAPIServerDeployment(tc.Context, mgmtClient, tc.ControlPlaneNamespace)
			Expect(err).NotTo(HaveOccurred(), "failed to get kube-apiserver deployment")

			// Find konnectivity-server container
			var konnectivityContainer *corev1.Container
			for i := range deployment.Spec.Template.Spec.Containers {
				if deployment.Spec.Template.Spec.Containers[i].Name == "konnectivity-server" {
					konnectivityContainer = &deployment.Spec.Template.Spec.Containers[i]
					break
				}
			}

			Expect(konnectivityContainer).NotTo(BeNil(), "konnectivity-server container should exist")

			// Verify --tls-min-version=VersionTLS12 in args
			tlsMinVersion := getTLSMinVersionFromArgs(konnectivityContainer.Args)
			Expect(tlsMinVersion).To(Equal("VersionTLS12"),
				"konnectivity-server should have --tls-min-version=VersionTLS12 for intermediate profile")
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

			// Wait for rollout to complete so all pods have the latest config
			mgmtClient := tc.MgmtClient
			GinkgoWriter.Printf("Waiting for kube-apiserver rollout to complete\n")
			err := waitForKubeAPIServerRollout(tc.Context, mgmtClient, tc.ControlPlaneNamespace, 10*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "kube-apiserver rollout should complete")

			// Get a ready kube-apiserver pod for connectivity testing
			kasPod, err := getReadyKubeAPIServerPod(tc.Context, mgmtClient, tc.ControlPlaneNamespace)
			Expect(err).NotTo(HaveOccurred(), "should find a ready kube-apiserver pod")

			// konnectivity-server listens on port 8090 for server connections
			konnectivityPort := "8090"
			GinkgoWriter.Printf("Testing TLS connections to konnectivity-server in pod %s on port %s\n", kasPod.Name, konnectivityPort)

			// Test TLS 1.2 connection using openssl s_client from within the kube-apiserver pod
			tls12Result, err := v2util.RunCommandInPod(tc.Context, mgmtKubeClient, mgmtRestConfig,
				tc.ControlPlaneNamespace, kasPod.Name, "konnectivity-server",
				"sh", "-c",
				fmt.Sprintf("timeout 2 openssl s_client -connect localhost:%s -tls1_2 2>&1 || true", konnectivityPort))
			Expect(err).NotTo(HaveOccurred(), "failed to exec into kube-apiserver pod for TLS 1.2 test")
			Expect(strings.ToLower(tls12Result)).To(ContainSubstring("tlsv1.2"),
				"should confirm TLS 1.2 was used for konnectivity-server port %s", konnectivityPort)

			// Test TLS 1.3 connection using openssl s_client
			tls13Result, err := v2util.RunCommandInPod(tc.Context, mgmtKubeClient, mgmtRestConfig,
				tc.ControlPlaneNamespace, kasPod.Name, "konnectivity-server",
				"sh", "-c",
				fmt.Sprintf("timeout 2 openssl s_client -connect localhost:%s -tls1_3 2>&1 || true", konnectivityPort))
			Expect(err).NotTo(HaveOccurred(), "failed to exec into kube-apiserver pod for TLS 1.3 test")
			Expect(strings.ToLower(tls13Result)).To(ContainSubstring("tlsv1.3"),
				"should confirm TLS 1.3 was used for konnectivity-server port %s", konnectivityPort)
		})

		It("should update to --tls-min-version=VersionTLS13 when TLS profile changed to Modern", func() {
			// Get the HostedCluster from management cluster and update its TLS profile
			mgmtClient := tc.MgmtClient

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
				g.Expect(apierrors.IsConflict(err)).To(BeFalse(), "conflict on update, retrying")
				g.Expect(err).NotTo(HaveOccurred(), "failed to update HostedCluster TLS profile to Modern")
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "failed to update HostedCluster to Modern profile")

			GinkgoWriter.Printf("Updated HostedCluster to Modern TLS profile, waiting for changes to propagate\n")

			// Wait for deployment to reflect the Modern profile with TLS 1.3
			Eventually(func(g Gomega) {
				deployment, err := getKubeAPIServerDeployment(tc.Context, mgmtClient, tc.ControlPlaneNamespace)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get kube-apiserver deployment")

				// Find konnectivity-server container in pod template
				var konnectivityContainer *corev1.Container
				for i := range deployment.Spec.Template.Spec.Containers {
					if deployment.Spec.Template.Spec.Containers[i].Name == "konnectivity-server" {
						konnectivityContainer = &deployment.Spec.Template.Spec.Containers[i]
						break
					}
				}

				g.Expect(konnectivityContainer).NotTo(BeNil(), "konnectivity-server container should exist")

				// Verify --tls-min-version=VersionTLS13 in args
				tlsMinVersion := getTLSMinVersionFromArgs(konnectivityContainer.Args)
				g.Expect(tlsMinVersion).To(Equal("VersionTLS13"),
					"konnectivity-server should have --tls-min-version=VersionTLS13 for modern profile")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			// Wait for rollout to complete so all pods have the latest config
			GinkgoWriter.Printf("Waiting for kube-apiserver rollout to complete after Modern profile update\n")
			err := waitForKubeAPIServerRollout(tc.Context, mgmtClient, tc.ControlPlaneNamespace, 10*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "kube-apiserver rollout should complete after Modern profile update")
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

			konnectivityPort := "8090"

			// Get a ready kube-apiserver pod for connectivity testing (rollout already completed in previous test)
			kasPod, err := getReadyKubeAPIServerPod(tc.Context, mgmtClient, tc.ControlPlaneNamespace)
			Expect(err).NotTo(HaveOccurred(), "should find a ready kube-apiserver pod")

			// Test TLS 1.3 connection should succeed using openssl
			GinkgoWriter.Printf("Testing TLS 1.3 connection to konnectivity-server in pod %s with Modern profile\n", kasPod.Name)
			tls13Result, err := v2util.RunCommandInPod(tc.Context, mgmtKubeClient, mgmtRestConfig,
				tc.ControlPlaneNamespace, kasPod.Name, "konnectivity-server",
				"sh", "-c",
				fmt.Sprintf("timeout 2 openssl s_client -connect localhost:%s -tls1_3 2>&1 || true", konnectivityPort))
			Expect(err).NotTo(HaveOccurred(), "failed to exec into kube-apiserver pod for TLS 1.3 test")

			Expect(strings.ToLower(tls13Result)).To(ContainSubstring("tlsv1.3"),
				"should confirm TLS 1.3 was used for konnectivity-server with Modern profile")

			// Test TLS 1.2 connection should fail using openssl
			GinkgoWriter.Printf("Testing TLS 1.2 connection to konnectivity-server in pod %s with Modern profile\n", kasPod.Name)
			tls12Result, err := v2util.RunCommandInPod(tc.Context, mgmtKubeClient, mgmtRestConfig,
				tc.ControlPlaneNamespace, kasPod.Name, "konnectivity-server",
				"sh", "-c",
				fmt.Sprintf("timeout 2 openssl s_client -connect localhost:%s -tls1_2 2>&1 || true", konnectivityPort))
			Expect(err).NotTo(HaveOccurred(), "failed to exec into kube-apiserver pod for TLS 1.2 test")

			// TLS 1.2 should be rejected - check that no cipher was negotiated
			lowerResult := strings.ToLower(tls12Result)
			Expect(lowerResult).To(ContainSubstring("cipher is (none)"),
				"TLS 1.2 connection should be rejected with modern profile, got: %s", tls12Result)
		})

		It("should downgrade to --tls-min-version=VersionTLS12 when Modern TLS profile is removed", func() {
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
				g.Expect(apierrors.IsConflict(err)).To(BeFalse(), "conflict on update, retrying")
				g.Expect(err).NotTo(HaveOccurred(), "failed to remove TLS profile to downgrade to Intermediate")
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "failed to downgrade HostedCluster to Intermediate profile")

			GinkgoWriter.Printf("Removed Modern TLS profile from HostedCluster (downgraded to default/Intermediate), waiting for changes to propagate\n")

			// Wait for deployment to reflect the Intermediate profile with TLS 1.2
			Eventually(func(g Gomega) {
				deployment, err := getKubeAPIServerDeployment(tc.Context, mgmtClient, tc.ControlPlaneNamespace)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get kube-apiserver deployment")

				// Find konnectivity-server container in pod template
				var konnectivityContainer *corev1.Container
				for i := range deployment.Spec.Template.Spec.Containers {
					if deployment.Spec.Template.Spec.Containers[i].Name == "konnectivity-server" {
						konnectivityContainer = &deployment.Spec.Template.Spec.Containers[i]
						break
					}
				}

				g.Expect(konnectivityContainer).NotTo(BeNil(), "konnectivity-server container should exist")

				// Verify --tls-min-version=VersionTLS12 in args
				tlsMinVersion := getTLSMinVersionFromArgs(konnectivityContainer.Args)
				g.Expect(tlsMinVersion).To(Equal("VersionTLS12"),
					"konnectivity-server should have --tls-min-version=VersionTLS12 for intermediate profile")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())

			// Wait for rollout to complete so all pods have the latest config
			GinkgoWriter.Printf("Waiting for kube-apiserver rollout to complete after downgrade to Intermediate profile\n")
			err = waitForKubeAPIServerRollout(tc.Context, mgmtClient, tc.ControlPlaneNamespace, 10*time.Minute)
			Expect(err).NotTo(HaveOccurred(), "kube-apiserver rollout should complete after downgrade to Intermediate profile")
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

			// Get a ready kube-apiserver pod for connectivity testing (rollout already completed in previous test)
			kasPod, err := getReadyKubeAPIServerPod(tc.Context, mgmtClient, tc.ControlPlaneNamespace)
			Expect(err).NotTo(HaveOccurred(), "should find a ready kube-apiserver pod")

			konnectivityPort := "8090"

			// Test TLS 1.2 connection should succeed using openssl
			GinkgoWriter.Printf("Testing TLS 1.2 connection to konnectivity-server in pod %s after downgrade to Intermediate profile\n", kasPod.Name)
			tls12Result, err := v2util.RunCommandInPod(tc.Context, mgmtKubeClient, mgmtRestConfig,
				tc.ControlPlaneNamespace, kasPod.Name, "konnectivity-server",
				"sh", "-c",
				fmt.Sprintf("timeout 2 openssl s_client -connect localhost:%s -tls1_2 2>&1 || true", konnectivityPort))
			Expect(err).NotTo(HaveOccurred(), "failed to exec into kube-apiserver pod for TLS 1.2 test")

			Expect(strings.ToLower(tls12Result)).To(ContainSubstring("tlsv1.2"),
				"should confirm TLS 1.2 was accepted for konnectivity-server after downgrade to Intermediate profile")

			// Test TLS 1.3 connection should also succeed using openssl
			GinkgoWriter.Printf("Testing TLS 1.3 connection to konnectivity-server in pod %s after downgrade to Intermediate profile\n", kasPod.Name)
			tls13Result, err := v2util.RunCommandInPod(tc.Context, mgmtKubeClient, mgmtRestConfig,
				tc.ControlPlaneNamespace, kasPod.Name, "konnectivity-server",
				"sh", "-c",
				fmt.Sprintf("timeout 2 openssl s_client -connect localhost:%s -tls1_3 2>&1 || true", konnectivityPort))
			Expect(err).NotTo(HaveOccurred(), "failed to exec into kube-apiserver pod for TLS 1.3 test")

			Expect(strings.ToLower(tls13Result)).To(ContainSubstring("tlsv1.3"),
				"should confirm TLS 1.3 was accepted for konnectivity-server with Intermediate profile")
		})

		AfterAll(func() {
			if tc == nil {
				return
			}
			GinkgoWriter.Printf("Restoring original Configuration\n")

			// Use Eventually with retry on conflict
			err := wait.PollUntilContextTimeout(tc.Context, 5*time.Second, 1*time.Minute, true, func(ctx context.Context) (bool, error) {
				hostedCluster := &hyperv1.HostedCluster{}
				err := tc.MgmtClient.Get(ctx, crclient.ObjectKey{
					Namespace: tc.ClusterNamespace,
					Name:      tc.ClusterName,
				}, hostedCluster)
				if err != nil {
					return false, err
				}

				// Restore the complete original Configuration shape
				if originalConfiguration == nil {
					hostedCluster.Spec.Configuration = nil
				} else {
					hostedCluster.Spec.Configuration = originalConfiguration.DeepCopy()
				}

				err = tc.MgmtClient.Update(ctx, hostedCluster)
				if apierrors.IsConflict(err) {
					return false, nil // Retry on conflict
				}
				if err != nil {
					return false, err
				}
				return true, nil
			})
			if err != nil {
				GinkgoWriter.Printf("Warning: failed to restore original Configuration: %v\n", err)
			}
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:KonnectivityServer] Konnectivity Server TLS Configuration", Label("konnectivity-server"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterKonnectivityServerTests(func() *internal.TestContext { return testCtx })
})
