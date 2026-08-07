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
	"net"
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
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type controlPlaneTLSComponent struct {
	name                string
	port                string
	podAppLabel         string // target pod (TLS listener)
	execPodAppLabel     string // pod to exec into; defaults to podAppLabel
	execContainerNames  []string
	connectViaPodIP     bool // when true, probe targetPod.Status.PodIP instead of localhost
	configMapName       string // PKI operator stores TLS settings in a ConfigMap
	deploymentName      string // aws-pod-identity-webhook stores TLS settings in deployment command flags
	deploymentContainer string
	awsOnly             bool
}

var (
	pkiOperatorTLSComponent = controlPlaneTLSComponent{
		name:              "control-plane-pki-operator",
		port:              "8443",
		podAppLabel:       "control-plane-pki-operator",
		execContainerNames: []string{"control-plane-pki-operator"},
		configMapName:     "control-plane-pki-operator-config",
	}

	awsPodIdentityWebhookTLSComponent = controlPlaneTLSComponent{
		name:                "aws-pod-identity-webhook",
		port:                "4443",
		podAppLabel:         "kube-apiserver",
		execPodAppLabel:     "control-plane-pki-operator",
		execContainerNames:  []string{"control-plane-pki-operator"},
		connectViaPodIP:     true,
		deploymentName:      "kube-apiserver",
		deploymentContainer: "aws-pod-identity-webhook",
		awsOnly:             true,
	}
)

type tlsVersionExpectation struct {
	opensslFlag      string
	expectedProtocol string
	rejectConnection bool
}

func endpointAppliesToHostedCluster(component controlPlaneTLSComponent, hostedCluster *hyperv1.HostedCluster) bool {
	return !component.awsOnly || hostedCluster.Spec.Platform.Type == hyperv1.AWSPlatform
}

func skipIfComponentNotApplicable(component controlPlaneTLSComponent, hostedCluster *hyperv1.HostedCluster) {
	if !endpointAppliesToHostedCluster(component, hostedCluster) {
		Skip(fmt.Sprintf("%s TLS test is only for AWS platform", component.name))
	}
}

func requireDefaultOrIntermediateTLSProfile(hostedCluster *hyperv1.HostedCluster) {
	hasProfile := hostedCluster.Spec.Configuration != nil &&
		hostedCluster.Spec.Configuration.APIServer != nil &&
		hostedCluster.Spec.Configuration.APIServer.TLSSecurityProfile != nil

	isDefaultOrIntermediate := !hasProfile || hostedClusterHasTLSProfileType(hostedCluster, configv1.TLSProfileIntermediateType)
	if !isDefaultOrIntermediate {
		Skip("HostedCluster does not have default or Intermediate TLS profile")
	}
}

func containerHasTLSMinVersionFlag(container corev1.Container, minTLSVersion string) bool {
	expected := fmt.Sprintf("--tls-min-version=%s", minTLSVersion)
	for _, arg := range container.Command {
		if arg == expected {
			return true
		}
	}
	for _, arg := range container.Args {
		if arg == expected {
			return true
		}
	}
	return false
}

func getFirstRunningPod(ctx context.Context, mgmtClient crclient.Client, namespace, appLabel string) (*corev1.Pod, error) {
	podList := &corev1.PodList{}
	if err := mgmtClient.List(ctx, podList,
		crclient.InNamespace(namespace),
		crclient.MatchingLabels{"app": appLabel},
	); err != nil {
		return nil, err
	}
	if len(podList.Items) == 0 {
		return nil, fmt.Errorf("no pods found with app=%s in namespace %s", appLabel, namespace)
	}

	for i := range podList.Items {
		if podList.Items[i].Status.Phase == corev1.PodRunning {
			return &podList.Items[i], nil
		}
	}
	return nil, fmt.Errorf("no running pod found with app=%s in namespace %s", appLabel, namespace)
}

func resolveTLSProbeContainers(pod *corev1.Pod, candidates []string) []string {
	defined := make(map[string]struct{}, len(pod.Spec.Containers))
	for _, container := range pod.Spec.Containers {
		defined[container.Name] = struct{}{}
	}

	running := make(map[string]struct{}, len(pod.Status.ContainerStatuses))
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Running != nil {
			running[cs.Name] = struct{}{}
		}
	}

	var resolved []string
	for _, candidate := range candidates {
		if _, ok := defined[candidate]; !ok {
			continue
		}
		if _, ok := running[candidate]; !ok {
			continue
		}
		resolved = append(resolved, candidate)
	}
	return resolved
}

func runOpenSSLClient(
	ctx context.Context,
	mgmtKubeClient *kubernetes.Clientset,
	mgmtRestConfig *rest.Config,
	namespace, podName, containerName, connectHost, port, tlsFlag string,
) (string, error) {
	return v2util.RunCommandInPod(ctx, mgmtKubeClient, mgmtRestConfig,
		namespace, podName, containerName,
		"sh", "-c",
		fmt.Sprintf("timeout 5 openssl s_client -connect %s -%s 2>&1 || true",
			net.JoinHostPort(connectHost, port), tlsFlag))
}

// waitForAppPodRestart waits until a ready pod with app=<appLabel> exists whose UID
// differs from previousUID. This avoids TLS probes against a stale pod that has not
// yet rolled with the updated TLS configuration.
func waitForAppPodRestart(
	ctx context.Context,
	mgmtClient crclient.Client,
	namespace, appLabel, previousUID string,
) string {
	var newPodUID string
	Eventually(func(g Gomega) {
		podList := &corev1.PodList{}
		g.Expect(mgmtClient.List(ctx, podList,
			crclient.InNamespace(namespace),
			crclient.MatchingLabels{"app": appLabel},
		)).To(Succeed(), "failed to list %s pods", appLabel)
		g.Expect(podList.Items).NotTo(BeEmpty(), "expected at least one %s pod", appLabel)

		var readyPod *corev1.Pod
		for i := range podList.Items {
			pod := &podList.Items[i]
			if pod.Status.Phase != corev1.PodRunning {
				continue
			}
			ready := false
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					ready = true
					break
				}
			}
			if !ready {
				continue
			}
			allContainersReady := true
			for _, cs := range pod.Status.ContainerStatuses {
				if !cs.Ready {
					allContainersReady = false
					break
				}
			}
			if !allContainersReady {
				continue
			}
			readyPod = pod
			break
		}
		g.Expect(readyPod).NotTo(BeNil(), "expected a ready running %s pod", appLabel)

		newPodUID = string(readyPod.UID)
		g.Expect(newPodUID).NotTo(Equal(previousUID),
			"%s pod UID should have changed after TLS config mutation (still %s)", appLabel, previousUID)
	}, 2*time.Minute, 5*time.Second).Should(Succeed(),
		"%s pod should restart and become ready after TLS config mutation", appLabel)
	return newPodUID
}

func expectTLSConnectionResult(g Gomega, result string, expectation tlsVersionExpectation) {
	lowerResult := strings.ToLower(result)
	if expectation.rejectConnection {
		g.Expect(lowerResult).To(Or(
			ContainSubstring("cipher is (none)"),
			ContainSubstring("alert protocol version"),
			ContainSubstring("wrong version number"),
		), "TLS connection should be rejected, got: %s", result)
		return
	}
	g.Expect(expectation.expectedProtocol).NotTo(BeEmpty(),
		"expectedProtocol must be non-empty when rejectConnection is false")
	g.Expect(lowerResult).To(ContainSubstring(expectation.expectedProtocol),
		"should confirm %s was used, got: %s", expectation.expectedProtocol, result)
}

func expectComponentMinTLSVersion(
	g Gomega,
	ctx context.Context,
	mgmtClient crclient.Client,
	namespace string,
	component controlPlaneTLSComponent,
	minTLSVersion string,
) {
	switch {
	case component.configMapName != "":
		cm := &corev1.ConfigMap{}
		err := mgmtClient.Get(ctx, crclient.ObjectKey{
			Namespace: namespace,
			Name:      component.configMapName,
		}, cm)
		g.Expect(err).NotTo(HaveOccurred(), "failed to get %s ConfigMap", component.configMapName)
		g.Expect(cm.Data["config.yaml"]).To(ContainSubstring(fmt.Sprintf("minTLSVersion: %s", minTLSVersion)),
			"%s config should have minTLSVersion: %s", component.name, minTLSVersion)
	case component.deploymentName != "":
		deployment := &appsv1.Deployment{}
		g.Expect(mgmtClient.Get(ctx, crclient.ObjectKey{
			Namespace: namespace,
			Name:      component.deploymentName,
		}, deployment)).To(Succeed(), "failed to get %s deployment", component.deploymentName)

		containerFound := false
		for i := range deployment.Spec.Template.Spec.Containers {
			if deployment.Spec.Template.Spec.Containers[i].Name == component.deploymentContainer {
				containerFound = true
				container := deployment.Spec.Template.Spec.Containers[i]
				g.Expect(containerHasTLSMinVersionFlag(container, minTLSVersion)).To(BeTrue(),
					"%s should have min TLS version %s", component.name, minTLSVersion)
				return
			}
		}
		g.Expect(containerFound).To(BeTrue(), "%s container should exist in %s deployment", component.deploymentContainer, component.deploymentName)
	default:
		g.Expect(component.name).To(BeEmpty(), "component %s has no TLS config source configured", component.name)
	}
}

func verifyComponentTLSConnectivity(
	ctx context.Context,
	tc *internal.TestContext,
	mgmtClient crclient.Client,
	mgmtKubeClient *kubernetes.Clientset,
	mgmtRestConfig *rest.Config,
	component controlPlaneTLSComponent,
	expectations []tlsVersionExpectation,
) {
	for _, expectation := range expectations {
		expectation := expectation
		Eventually(func(g Gomega) {
			targetPod, err := getFirstRunningPod(ctx, mgmtClient, tc.ControlPlaneNamespace, component.podAppLabel)
			g.Expect(err).NotTo(HaveOccurred(), "failed to get target pod for %s", component.name)

			execPodLabel := component.podAppLabel
			if component.execPodAppLabel != "" {
				execPodLabel = component.execPodAppLabel
			}

			execPod := targetPod
			if execPodLabel != component.podAppLabel {
				execPod, err = getFirstRunningPod(ctx, mgmtClient, tc.ControlPlaneNamespace, execPodLabel)
				g.Expect(err).NotTo(HaveOccurred(), "failed to get exec pod for %s", component.name)
			}

			connectHost := "localhost"
			if component.connectViaPodIP {
				g.Expect(targetPod.Status.PodIP).NotTo(BeEmpty(),
					"target pod %s should have a PodIP for %s TLS probe", targetPod.Name, component.name)
				connectHost = targetPod.Status.PodIP
			}

			freshExecPod := &corev1.Pod{}
			g.Expect(mgmtClient.Get(ctx, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      execPod.Name,
			}, freshExecPod)).To(Succeed(), "failed to refresh exec pod %s for %s", execPod.Name, component.name)

			containerNames := resolveTLSProbeContainers(freshExecPod, component.execContainerNames)
			g.Expect(containerNames).NotTo(BeEmpty(),
				"expected at least one TLS probe container in pod %s for %s", freshExecPod.Name, component.name)

			var execErr error
			for _, containerName := range containerNames {
				result, err := runOpenSSLClient(ctx, mgmtKubeClient, mgmtRestConfig,
					tc.ControlPlaneNamespace, freshExecPod.Name, containerName,
					connectHost, component.port, expectation.opensslFlag)
				if err != nil && strings.Contains(err.Error(), "container not found") {
					execErr = err
					continue
				}
				g.Expect(err).NotTo(HaveOccurred(), "failed openssl test for %s via pod %s container %s",
					component.name, freshExecPod.Name, containerName)
				expectTLSConnectionResult(g, result, expectation)
				return
			}
			g.Expect(execErr).NotTo(HaveOccurred(), "failed openssl test for %s using containers %v",
				component.name, containerNames)
		}, 1*time.Minute, 5*time.Second).Should(Succeed(), "TLS connectivity test should complete for %s", component.name)
	}
}

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
	When("control plane TLS configuration is modified", Ordered, Serial, Label("lifecycle"), func() {
		var tc *internal.TestContext
		var originalTLSProfile *configv1.TLSSecurityProfile
		var mgmtRestConfig *rest.Config
		var mgmtKubeClient *kubernetes.Clientset
		var pkiPodUIDBeforeMutation string
		var kasPodUIDBeforeMutation string

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

		It("control-plane-pki-operator should have control-plane-pki-operator-config ConfigMap with TLS configuration", func() {
			// Check in management cluster's control plane namespace, not hosted cluster
			mgmtClient := tc.MgmtClient
			cm := &corev1.ConfigMap{}
			err := mgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      pkiOperatorTLSComponent.configMapName,
			}, cm)

			Expect(err).NotTo(HaveOccurred(), "failed to get PKI operator ConfigMap %s/%s",
				tc.ControlPlaneNamespace, pkiOperatorTLSComponent.configMapName)

			Expect(cm.Data).NotTo(BeNil(), "ConfigMap data should not be nil")
			Expect(cm.Data).To(HaveKey("config.yaml"), "ConfigMap should have config.yaml key")
			Expect(cm.Data["config.yaml"]).NotTo(BeEmpty(), "config.yaml should not be empty")
		})

		It("control-plane-pki-operator should have minTLSVersion set to VersionTLS12 with default/intermediate profile", func() {
			hostedCluster := tc.GetHostedCluster()
			requireDefaultOrIntermediateTLSProfile(hostedCluster)

			mgmtClient := tc.MgmtClient
			cm := &corev1.ConfigMap{}
			err := mgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ControlPlaneNamespace,
				Name:      pkiOperatorTLSComponent.configMapName,
			}, cm)
			Expect(err).NotTo(HaveOccurred(), "failed to get PKI operator ConfigMap %s/%s",
				tc.ControlPlaneNamespace, pkiOperatorTLSComponent.configMapName)

			Expect(cm.Data["config.yaml"]).To(ContainSubstring("minTLSVersion: VersionTLS12"),
				"PKI operator config should have minTLSVersion: VersionTLS12 for intermediate profile")
		})

		It("aws-pod-identity-webhook should have --tls-min-version set to VersionTLS12 with default/intermediate profile", func() {
			hostedCluster := tc.GetHostedCluster()
			skipIfComponentNotApplicable(awsPodIdentityWebhookTLSComponent, hostedCluster)
			requireDefaultOrIntermediateTLSProfile(hostedCluster)

			Eventually(func(g Gomega) {
				expectComponentMinTLSVersion(g, tc.Context, tc.MgmtClient, tc.ControlPlaneNamespace,
					awsPodIdentityWebhookTLSComponent, "VersionTLS12")
			}, 1*time.Minute, 5*time.Second).Should(Succeed())
		})

		It("control-plane-pki-operator should accept both TLS 1.2 and TLS 1.3 connections with intermediate profile", func() {
			hostedCluster := tc.GetHostedCluster()
			requireDefaultOrIntermediateTLSProfile(hostedCluster)

			verifyComponentTLSConnectivity(tc.Context, tc, tc.MgmtClient, mgmtKubeClient, mgmtRestConfig,
				pkiOperatorTLSComponent, []tlsVersionExpectation{
					{opensslFlag: "tls1_2", expectedProtocol: "tlsv1.2"},
					{opensslFlag: "tls1_3", expectedProtocol: "tlsv1.3"},
				})
		})

		It("aws-pod-identity-webhook should accept both TLS 1.2 and TLS 1.3 connections with intermediate profile", func() {
			hostedCluster := tc.GetHostedCluster()
			skipIfComponentNotApplicable(awsPodIdentityWebhookTLSComponent, hostedCluster)
			requireDefaultOrIntermediateTLSProfile(hostedCluster)

			verifyComponentTLSConnectivity(tc.Context, tc, tc.MgmtClient, mgmtKubeClient, mgmtRestConfig,
				awsPodIdentityWebhookTLSComponent, []tlsVersionExpectation{
					{opensslFlag: "tls1_2", expectedProtocol: "tlsv1.2"},
					{opensslFlag: "tls1_3", expectedProtocol: "tlsv1.3"},
				})
		})

		It("should update HostedCluster TLS profile to Modern", func() {
			// Get the HostedCluster from management cluster and update its TLS profile
			mgmtClient := tc.MgmtClient

			// Capture current pod UIDs before mutation so connectivity tests can wait for restarts.
			pkiPod, err := getFirstRunningPod(tc.Context, mgmtClient, tc.ControlPlaneNamespace, pkiOperatorTLSComponent.podAppLabel)
			Expect(err).NotTo(HaveOccurred(), "failed to get control-plane-pki-operator pod before mutation")
			pkiPodUIDBeforeMutation = string(pkiPod.UID)
			GinkgoWriter.Printf("Captured PKI operator pod UID before mutation: %s\n", pkiPodUIDBeforeMutation)

			kasPod, err := getFirstRunningPod(tc.Context, mgmtClient, tc.ControlPlaneNamespace, awsPodIdentityWebhookTLSComponent.podAppLabel)
			Expect(err).NotTo(HaveOccurred(), "failed to get kube-apiserver pod before mutation")
			kasPodUIDBeforeMutation = string(kasPod.UID)
			GinkgoWriter.Printf("Captured kube-apiserver pod UID before mutation: %s\n", kasPodUIDBeforeMutation)

			// Update to Modern TLS profile; Eventually retries on conflict after re-get.
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
				g.Expect(err).NotTo(HaveOccurred(), "failed to update HostedCluster TLS profile to Modern")
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "failed to update HostedCluster to Modern profile")

			GinkgoWriter.Printf("Updated HostedCluster to Modern TLS profile, waiting for changes to propagate\n")
		})

		It("control-plane-pki-operator should propagate minTLSVersion VersionTLS13 with Modern profile", func() {
			mgmtClient := tc.MgmtClient
			hostedCluster := &hyperv1.HostedCluster{}
			Expect(mgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ClusterNamespace,
				Name:      tc.ClusterName,
			}, hostedCluster)).To(Succeed(), "failed to get HostedCluster")

			if !hostedClusterHasTLSProfileType(hostedCluster, configv1.TLSProfileModernType) {
				Fail("HostedCluster does not have Modern TLS profile - previous ordered test should have set it")
			}

			Eventually(func(g Gomega) {
				expectComponentMinTLSVersion(g, tc.Context, mgmtClient, tc.ControlPlaneNamespace,
					pkiOperatorTLSComponent, "VersionTLS13")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("aws-pod-identity-webhook should propagate --tls-min-version VersionTLS13 with Modern profile", func() {
			mgmtClient := tc.MgmtClient
			hostedCluster := &hyperv1.HostedCluster{}
			Expect(mgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ClusterNamespace,
				Name:      tc.ClusterName,
			}, hostedCluster)).To(Succeed(), "failed to get HostedCluster")
			skipIfComponentNotApplicable(awsPodIdentityWebhookTLSComponent, hostedCluster)

			if !hostedClusterHasTLSProfileType(hostedCluster, configv1.TLSProfileModernType) {
				Fail("HostedCluster does not have Modern TLS profile - previous ordered test should have set it")
			}

			Eventually(func(g Gomega) {
				expectComponentMinTLSVersion(g, tc.Context, mgmtClient, tc.ControlPlaneNamespace,
					awsPodIdentityWebhookTLSComponent, "VersionTLS13")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("control-plane-pki-operator should accept TLS 1.3 but reject TLS 1.2 with Modern profile", func() {
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
			pkiPodUIDBeforeMutation = waitForAppPodRestart(tc.Context, mgmtClient, tc.ControlPlaneNamespace,
				pkiOperatorTLSComponent.podAppLabel, pkiPodUIDBeforeMutation)
			GinkgoWriter.Printf("New PKI operator pod UID after mutation to Modern profile: %s\n", pkiPodUIDBeforeMutation)

			verifyComponentTLSConnectivity(tc.Context, tc, mgmtClient, mgmtKubeClient, mgmtRestConfig,
				pkiOperatorTLSComponent, []tlsVersionExpectation{
					{opensslFlag: "tls1_3", expectedProtocol: "tlsv1.3"},
					{opensslFlag: "tls1_2", rejectConnection: true},
				})
		})

		It("aws-pod-identity-webhook should accept TLS 1.3 but reject TLS 1.2 with Modern profile", func() {
			mgmtClient := tc.MgmtClient
			hostedCluster := &hyperv1.HostedCluster{}
			err := mgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ClusterNamespace,
				Name:      tc.ClusterName,
			}, hostedCluster)
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")
			skipIfComponentNotApplicable(awsPodIdentityWebhookTLSComponent, hostedCluster)

			if !hostedClusterHasTLSProfileType(hostedCluster, configv1.TLSProfileModernType) {
				Fail("HostedCluster does not have Modern TLS profile - previous ordered test should have set it")
			}

			// Wait for kube-apiserver (webhook sidecar) to restart with the new TLS config.
			// Both Intermediate and Modern accept TLS 1.3, so probing a stale pod can pass vacuously.
			kasPodUIDBeforeMutation = waitForAppPodRestart(tc.Context, mgmtClient, tc.ControlPlaneNamespace,
				awsPodIdentityWebhookTLSComponent.podAppLabel, kasPodUIDBeforeMutation)
			GinkgoWriter.Printf("New kube-apiserver pod UID after mutation to Modern profile: %s\n", kasPodUIDBeforeMutation)

			verifyComponentTLSConnectivity(tc.Context, tc, mgmtClient, mgmtKubeClient, mgmtRestConfig,
				awsPodIdentityWebhookTLSComponent, []tlsVersionExpectation{
					{opensslFlag: "tls1_3", expectedProtocol: "tlsv1.3"},
					{opensslFlag: "tls1_2", rejectConnection: true},
				})
		})

		It("should downgrade HostedCluster TLS profile to default/intermediate", func() {
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

			// Remove Modern TLS profile (downgrade to default/Intermediate); Eventually retries on conflict.
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
				g.Expect(err).NotTo(HaveOccurred(), "failed to remove TLS profile to downgrade to Intermediate")
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "failed to downgrade HostedCluster to Intermediate profile")

			GinkgoWriter.Printf("Removed Modern TLS profile from HostedCluster (downgraded to default/Intermediate), waiting for changes to propagate\n")
		})

		It("control-plane-pki-operator should propagate minTLSVersion VersionTLS12 after downgrade", func() {
			mgmtClient := tc.MgmtClient
			hostedCluster := &hyperv1.HostedCluster{}
			Expect(mgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ClusterNamespace,
				Name:      tc.ClusterName,
			}, hostedCluster)).To(Succeed(), "failed to get HostedCluster")

			if hostedClusterHasTLSProfileType(hostedCluster, configv1.TLSProfileModernType) {
				Fail("HostedCluster still has Modern TLS profile - previous ordered test should have downgraded it")
			}

			Eventually(func(g Gomega) {
				expectComponentMinTLSVersion(g, tc.Context, mgmtClient, tc.ControlPlaneNamespace,
					pkiOperatorTLSComponent, "VersionTLS12")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("aws-pod-identity-webhook should propagate --tls-min-version VersionTLS12 after downgrade", func() {
			mgmtClient := tc.MgmtClient
			hostedCluster := &hyperv1.HostedCluster{}
			Expect(mgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ClusterNamespace,
				Name:      tc.ClusterName,
			}, hostedCluster)).To(Succeed(), "failed to get HostedCluster")
			skipIfComponentNotApplicable(awsPodIdentityWebhookTLSComponent, hostedCluster)

			if hostedClusterHasTLSProfileType(hostedCluster, configv1.TLSProfileModernType) {
				Fail("HostedCluster still has Modern TLS profile - previous ordered test should have downgraded it")
			}

			Eventually(func(g Gomega) {
				expectComponentMinTLSVersion(g, tc.Context, mgmtClient, tc.ControlPlaneNamespace,
					awsPodIdentityWebhookTLSComponent, "VersionTLS12")
			}, 5*time.Minute, 10*time.Second).Should(Succeed())
		})

		It("control-plane-pki-operator should accept both TLS 1.2 and TLS 1.3 connections after downgrade to Intermediate profile", func() {
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

			// Wait for PKI operator pod to restart and pick up the downgraded TLS config
			pkiPodUIDBeforeMutation = waitForAppPodRestart(tc.Context, mgmtClient, tc.ControlPlaneNamespace,
				pkiOperatorTLSComponent.podAppLabel, pkiPodUIDBeforeMutation)
			GinkgoWriter.Printf("New PKI operator pod UID after downgrade to Intermediate profile: %s\n", pkiPodUIDBeforeMutation)

			verifyComponentTLSConnectivity(tc.Context, tc, mgmtClient, mgmtKubeClient, mgmtRestConfig,
				pkiOperatorTLSComponent, []tlsVersionExpectation{
					{opensslFlag: "tls1_2", expectedProtocol: "tlsv1.2"},
					{opensslFlag: "tls1_3", expectedProtocol: "tlsv1.3"},
				})
		})

		It("aws-pod-identity-webhook should accept both TLS 1.2 and TLS 1.3 connections after downgrade to Intermediate profile", func() {
			mgmtClient := tc.MgmtClient
			hostedCluster := &hyperv1.HostedCluster{}
			err := mgmtClient.Get(tc.Context, crclient.ObjectKey{
				Namespace: tc.ClusterNamespace,
				Name:      tc.ClusterName,
			}, hostedCluster)
			Expect(err).NotTo(HaveOccurred(), "failed to get HostedCluster")
			skipIfComponentNotApplicable(awsPodIdentityWebhookTLSComponent, hostedCluster)

			if hostedClusterHasTLSProfileType(hostedCluster, configv1.TLSProfileModernType) {
				Fail("HostedCluster still has Modern TLS profile - previous ordered test should have downgraded it")
			}

			// Wait for kube-apiserver (webhook sidecar) to restart with the downgraded TLS config.
			kasPodUIDBeforeMutation = waitForAppPodRestart(tc.Context, mgmtClient, tc.ControlPlaneNamespace,
				awsPodIdentityWebhookTLSComponent.podAppLabel, kasPodUIDBeforeMutation)
			GinkgoWriter.Printf("New kube-apiserver pod UID after downgrade to Intermediate profile: %s\n", kasPodUIDBeforeMutation)

			verifyComponentTLSConnectivity(tc.Context, tc, mgmtClient, mgmtKubeClient, mgmtRestConfig,
				awsPodIdentityWebhookTLSComponent, []tlsVersionExpectation{
					{opensslFlag: "tls1_2", expectedProtocol: "tlsv1.2"},
					{opensslFlag: "tls1_3", expectedProtocol: "tlsv1.3"},
				})
		})

		AfterAll(func() {
			if tc == nil {
				return
			}
			GinkgoWriter.Printf("Restoring original TLS security profile\n")

			// Restore TLS profile; Eventually retries on conflict after re-get.
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
				g.Expect(err).NotTo(HaveOccurred(), "failed to restore original TLS profile")
			}, 1*time.Minute, 5*time.Second).Should(Succeed(), "failed to restore original TLS profile")
		})
	})
}

var _ = Describe("[sig-hypershift][Jira:Hypershift][Feature:ControlPlaneTLS] Control Plane TLS Operator", Label("control-plane-pki-operator", "aws-pod-identity-webhook"), func() {
	var testCtx *internal.TestContext

	BeforeEach(func() {
		testCtx = internal.GetTestContext()
		Expect(testCtx).NotTo(BeNil(), "test context should be set up in BeforeSuite")

		testCtx.ValidateHostedCluster()
	})

	RegisterControlPlanePKIOperatorTests(func() *internal.TestContext { return testCtx })
})
