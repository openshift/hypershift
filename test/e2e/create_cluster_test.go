//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/azureutil"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/integration"
	integrationframework "github.com/openshift/hypershift/test/integration/framework"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/clientcmd"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)


// TestCreateCluster implements a test that creates a cluster with the code under test
// vs upgrading to the code under test as TestUpgradeControlPlane does.
func TestCreateCluster(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	zones := strings.Split(globalOpts.ConfigurableClusterOptions.Zone.String(), ",")
	if len(zones) >= 3 {
		// CreateCluster also tests multi-zone workers work properly if a sufficient number of zones are configured
		t.Logf("Sufficient zones available for InfrastructureAvailabilityPolicy HighlyAvailable")
		clusterOpts.AWSPlatform.Zones = zones
		clusterOpts.InfrastructureAvailabilityPolicy = string(hyperv1.HighlyAvailable)
		clusterOpts.NodePoolReplicas = 1
	}
	if !e2eutil.IsLessThan(e2eutil.Version418) {
		clusterOpts.FeatureSet = string(configv1.TechPreviewNoUpgrade)
	}

	if globalOpts.Platform == hyperv1.AzurePlatform || globalOpts.Platform == hyperv1.AWSPlatform {
		// Configure Ingress Operator with custom endpointPublishingStrategy before cluster creation
		clusterOpts.BeforeApply = func(o crclient.Object) {
			switch hc := o.(type) {
			case *hyperv1.HostedCluster:
				if hc.Spec.OperatorConfiguration == nil {
					hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{}
				}
				if hc.Spec.OperatorConfiguration.IngressOperator == nil {
					hc.Spec.OperatorConfiguration.IngressOperator = &hyperv1.IngressOperatorSpec{}
				}
				// Set a custom endpoint publishing strategy (Internal LoadBalancer for testing)
				hc.Spec.OperatorConfiguration.IngressOperator.EndpointPublishingStrategy = &operatorv1.EndpointPublishingStrategy{
					Type: operatorv1.LoadBalancerServiceStrategyType,
					LoadBalancer: &operatorv1.LoadBalancerStrategy{
						Scope: operatorv1.InternalLoadBalancer,
					},
				}
			}
		}
	}

	clusterOpts.PodsLabels = map[string]string{
		"hypershift-e2e-test-label": "test",
	}
	clusterOpts.Tolerations = []string{"key=hypershift-e2e-test-toleration,operator=Equal,value=true,effect=NoSchedule"}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Sanity check the cluster by waiting for the nodes to report ready
		guestClient := e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

		t.Logf("fetching mgmt kubeconfig")
		mgmtCfg, err := e2eutil.GetConfig()
		g.Expect(err).NotTo(HaveOccurred(), "couldn't get mgmt kubeconfig")
		mgmtCfg.QPS = -1
		mgmtCfg.Burst = -1

		mgmtClients, err := integrationframework.NewClients(mgmtCfg)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't create mgmt clients")

		guestKubeConfigSecretData := e2eutil.WaitForGuestKubeConfig(t, ctx, mgtClient, hostedCluster)

		guestConfig, err := clientcmd.RESTConfigFromKubeConfig(guestKubeConfigSecretData)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't load guest kubeconfig")
		guestConfig.QPS = -1
		guestConfig.Burst = -1

		guestClients, err := integrationframework.NewClients(guestConfig)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't create guest clients")

		integration.RunTestControlPlanePKIOperatorBreakGlassCredentials(t, testContext, hostedCluster, mgmtClients, guestClients)
		e2eutil.EnsureAPIUX(t, ctx, mgtClient, hostedCluster)
		e2eutil.EnsureCustomLabels(t, ctx, mgtClient, hostedCluster)
		e2eutil.EnsureCustomTolerations(t, ctx, mgtClient, hostedCluster)
		e2eutil.EnsureAppLabel(t, ctx, mgtClient, hostedCluster)
		e2eutil.EnsureFeatureGateStatus(t, ctx, guestClient)
		e2eutil.EnsureCAPIFinalizers(t, ctx, mgtClient, hostedCluster)

		// ensure KAS DNS name is configured with a KAS Serving cert
		e2eutil.EnsureKubeAPIDNSNameCustomCert(t, ctx, mgtClient, hostedCluster, clusterOpts)
		e2eutil.EnsureDefaultSecurityGroupTags(t, ctx, mgtClient, hostedCluster, clusterOpts)

		if globalOpts.Platform == hyperv1.AzurePlatform {
			e2eutil.EnsureKubeAPIServerAllowedCIDRs(t, ctx, mgtClient, guestConfig, hostedCluster)
			e2eutil.EnsureAzureWorkloadIdentityWebhookMutation(t, ctx, guestClient)
		}

		e2eutil.EnsureGlobalPullSecret(t, ctx, mgtClient, hostedCluster)

		// Verify CPO override image if TEST_CPO_OVERRIDE=1 is set
		if os.Getenv("TEST_CPO_OVERRIDE") == "1" {
			controlPlaneNamespace := manifests.HostedControlPlaneNamespace(hostedCluster.Namespace, hostedCluster.Name)
			e2eutil.VerifyCPOOverrideImage(t, ctx, mgtClient, controlPlaneNamespace, clusterOpts.ReleaseImage, string(globalOpts.Platform))
		}

		if globalOpts.Platform == hyperv1.AzurePlatform || globalOpts.Platform == hyperv1.AWSPlatform {
			// ensure Ingress Operator configuration is properly applied
			e2eutil.EnsureIngressOperatorConfiguration(t, ctx, mgtClient, guestClient, hostedCluster)
		}
	}).WithAssetReader(content.ReadFile).
		Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "create-cluster", globalOpts.ServiceAccountSigningKey)
}

// TODO(alberto): rename this e2e to drop TestCreateCluster prefix after merging https://github.com/openshift/release/pull/66655
// Without the prefix, this e2e wouldn't run now.
func TestCreateClusterDefaultSecurityContextUID(t *testing.T) {
	t.Parallel()
	if globalOpts.Platform != hyperv1.AzurePlatform {
		t.Skip("test only supported on platform Azure")
	}
	if e2eutil.IsLessThan(e2eutil.Version420) {
		t.Skip("test only supported on version 4.20 and higher")
	}

	g := NewWithT(t)
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	labelSelector := labels.SelectorFromSet(labels.Set{
		"hypershift.openshift.io/hosted-control-plane": "true",
	})

	var namespaces []*corev1.Namespace
	client, err := e2eutil.GetClient()
	g.Expect(err).NotTo(HaveOccurred(), "couldn't get client")
	e2eutil.EventuallyObjects(t, ctx, "At least 3 Control Plane Namespaces",
		func(ctx context.Context) ([]*corev1.Namespace, error) {
			nsList := &corev1.NamespaceList{}
			err := client.List(ctx, nsList, &crclient.ListOptions{
				LabelSelector: labelSelector,
			})

			namespaces = make([]*corev1.Namespace, len(nsList.Items))
			for i := range nsList.Items {
				namespaces[i] = &nsList.Items[i]
			}
			return namespaces, err
		},
		[]e2eutil.Predicate[[]*corev1.Namespace]{
			func(namespaces []*corev1.Namespace) (done bool, reasons string, err error) {
				return len(namespaces) >= 3, fmt.Sprintf("expected at least 3 namespaces, got %v", len(namespaces)), nil
			},
		},
		nil,
		e2eutil.WithTimeout(30*time.Minute), e2eutil.WithInterval(4*time.Minute), e2eutil.WithDelayedStart(),
	)

	// Validate that each namespace has a unique SecurityContext UID.
	g.Expect(len(namespaces)).To(BeNumerically(">=", 3), "expected at least 3 namespaces, got %v", len(namespaces))
	uidMap := make(map[int64]bool, len(namespaces))
	for _, ns := range namespaces {
		uid, ok := ns.Annotations["hypershift.openshift.io/default-security-context-uid"]
		g.Expect(ok).To(BeTrue(), "namespace %s missing SCC UID annotation", ns.Name)

		expectedUID, err := strconv.ParseInt(uid, 10, 64)
		g.Expect(err).NotTo(HaveOccurred(), "couldn't parse SCC UID", ns.Name, uid)

		podList := &corev1.PodList{}
		err = client.List(ctx, podList, &crclient.ListOptions{Namespace: ns.Name})
		g.Expect(err).NotTo(HaveOccurred(), "couldn't list pods in namespace %s", ns.Name)

		g.Expect(uidMap[expectedUID]).To(BeFalse(), "namespace %s has duplicate SecurityContext UID %s", ns.Name, uid)
		uidMap[expectedUID] = true
	}

	for _, ns := range namespaces {
		uid := ns.Annotations["hypershift.openshift.io/default-security-context-uid"]
		t.Logf("Namespace %s has SecurityContext UID %s", ns.Name, uid)
	}
	t.Logf("Successfully validated that all %d control plane namespaces have unique SecurityContext UIDs", len(namespaces))
}

func TestCreateClusterRequestServingIsolation(t *testing.T) {
	if !globalOpts.RequestServingIsolation {
		t.Skip("Skipping request serving isolation test")
	}
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("Request serving isolation test requires the AWS platform")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	nodePools := e2eutil.SetupReqServingClusterNodePools(ctx, t, globalOpts.ManagementParentKubeconfig, globalOpts.ManagementClusterNamespace, globalOpts.ManagementClusterName)
	defer e2eutil.TearDownNodePools(ctx, t, globalOpts.ManagementParentKubeconfig, nodePools)

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	zones := strings.Split(globalOpts.ConfigurableClusterOptions.Zone.String(), ",")
	if len(zones) >= 3 {
		// CreateCluster also tests multi-zone workers work properly if a sufficient number of zones are configured
		t.Logf("Sufficient zones available for InfrastructureAvailabilityPolicy HighlyAvailable")
		clusterOpts.AWSPlatform.Zones = zones
		clusterOpts.InfrastructureAvailabilityPolicy = string(hyperv1.HighlyAvailable)
		clusterOpts.NodePoolReplicas = 1
		clusterOpts.NodeSelector = map[string]string{"hypershift.openshift.io/control-plane": "true"}
	}

	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.HighlyAvailable)
	clusterOpts.Annotations = append(clusterOpts.Annotations, fmt.Sprintf("%s=%s", hyperv1.TopologyAnnotation, hyperv1.DedicatedRequestServingComponentsTopology))

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		guestClient := e2eutil.WaitForGuestClient(t, testContext, mgtClient, hostedCluster)
		e2eutil.EnsurePSANotPrivileged(t, ctx, guestClient)
		e2eutil.EnsureAllReqServingPodsLandOnReqServingNodes(t, ctx, guestClient)
		e2eutil.EnsureOnlyRequestServingPodsOnRequestServingNodes(t, ctx, guestClient)
		e2eutil.EnsureNoHCPPodsLandOnDefaultNode(t, ctx, guestClient, hostedCluster)
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "request-serving-isolation", globalOpts.ServiceAccountSigningKey)
}

func TestCreateClusterCustomConfig(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform && globalOpts.Platform != hyperv1.AzurePlatform {
		t.Skip("test only supported on platform AWS and Azure")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	var (
		kmsKeyArn                      *string
		kmsKeyInfo                     *azureutil.AzureEncryptionKey
		kmsUserAssignedCredsSecretName string
		err                            error
	)

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	// Configure KMS settings for Azure platform (this test specifically tests KMS functionality)
	if globalOpts.Platform == hyperv1.AzurePlatform {
		clusterOpts.AzurePlatform.EncryptionKeyID = globalOpts.ConfigurableClusterOptions.AzureEncryptionKeyID
		clusterOpts.AzurePlatform.KMSUserAssignedCredsSecretName = globalOpts.ConfigurableClusterOptions.AzureKMSUserAssignedCredsSecretName
	}

	clusterOpts.BeforeApply = func(o crclient.Object) {
		switch hc := o.(type) {
		case *hyperv1.HostedCluster:
			hc.Spec.Configuration = &hyperv1.ClusterConfiguration{
				Image: &configv1.ImageSpec{
					RegistrySources: configv1.RegistrySources{
						BlockedRegistries: []string{"badregistry.io"},
					},
				},
			}

			// Disable Console only for versions >= 4.20 due to OCPBUGS-57129 — the HyperShift-specific deployment is missing the capability.openshift.io/name: Console annotation.
			// Additionally, due to OCPBUGS-58422, we currently allow disabling Ingress only if Console is also disabled, so Ingress is also disabled for versions >= 4.20.
			disabledCaps := []hyperv1.OptionalCapability{
				hyperv1.ImageRegistryCapability,
				hyperv1.OpenShiftSamplesCapability,
				hyperv1.InsightsCapability,
				hyperv1.NodeTuningCapability,
			}
			if e2eutil.IsGreaterThanOrEqualTo(e2eutil.Version420) {
				disabledCaps = append(disabledCaps, hyperv1.ConsoleCapability, hyperv1.IngressCapability)
			}

			hc.Spec.Capabilities = &hyperv1.Capabilities{
				Disabled: disabledCaps,
			}
		}
	}

	switch globalOpts.Platform {
	case hyperv1.AWSPlatform:
		// find kms key ARN using alias
		kmsKeyArn, err = e2eutil.GetKMSKeyArn(ctx, clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, clusterOpts.AWSPlatform.Region, globalOpts.ConfigurableClusterOptions.AWSKmsKeyAlias)
		if err != nil || kmsKeyArn == nil {
			t.Fatalf("failed to retrieve kms key arn: %v", err)
		}
		clusterOpts.AWSPlatform.EtcdKMSKeyARN = *kmsKeyArn
	case hyperv1.AzurePlatform:
		if globalOpts.ConfigurableClusterOptions.AzureEncryptionKeyID == "" {
			t.Fatal("azure encryption key id is required")
		}
		if globalOpts.ConfigurableClusterOptions.AzureKMSUserAssignedCredsSecretName == "" {
			t.Fatal("azure kms user assigned creds secret name is required")
		}

		kmsUserAssignedCredsSecretName = globalOpts.ConfigurableClusterOptions.AzureKMSUserAssignedCredsSecretName
		kmsKeyInfo, err = azureutil.GetAzureEncryptionKeyInfo(globalOpts.ConfigurableClusterOptions.AzureEncryptionKeyID)
		if err != nil {
			t.Fatal("failed to get azure encryption key info: %w", err)
		}
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		switch globalOpts.Platform {
		case hyperv1.AWSPlatform:
			g.Expect(hostedCluster.Spec.SecretEncryption.KMS.AWS.ActiveKey.ARN).To(Equal(*kmsKeyArn))
			g.Expect(hostedCluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN).ToNot(BeEmpty())
		case hyperv1.AzurePlatform:
			g.Expect(hostedCluster.Spec.SecretEncryption).ToNot(BeNil(), "SecretEncryption must be set")
			g.Expect(hostedCluster.Spec.SecretEncryption.KMS).ToNot(BeNil(), "SecretEncryption.KMS must be set")
			g.Expect(hostedCluster.Spec.SecretEncryption.KMS.Azure).ToNot(BeNil(), "KMS.Azure must be set")
			g.Expect(hostedCluster.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVaultName).To(Equal(kmsKeyInfo.KeyVaultName))
			g.Expect(hostedCluster.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyName).To(Equal(kmsKeyInfo.KeyName))
			g.Expect(hostedCluster.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVersion).To(Equal(kmsKeyInfo.KeyVersion))
			g.Expect(hostedCluster.Spec.SecretEncryption.KMS.Azure.KMS.CredentialsSecretName).To(Equal(kmsUserAssignedCredsSecretName))
			g.Expect(hostedCluster.Spec.SecretEncryption.KMS.Azure.KMS.ObjectEncoding).To(Equal(hyperv1.ObjectEncodingFormat("utf-8")))
		}

		guestClient := e2eutil.WaitForGuestClient(t, testContext, mgtClient, hostedCluster)
		e2eutil.EnsureSecretEncryptedUsingKMSV2(t, ctx, hostedCluster, guestClient)
		// test oauth with identity provider
		e2eutil.EnsureOAuthWithIdentityProvider(t, ctx, mgtClient, hostedCluster)

		clients := e2eutil.InitGuestClients(ctx, t, g, mgtClient, hostedCluster)

		// ensure image registry component is disabled
		e2eutil.EnsureImageRegistryCapabilityDisabled(ctx, t, g, clients)

		// ensure openshift-samples component is disabled
		e2eutil.EnsureOpenshiftSamplesCapabilityDisabled(ctx, t, g, clients)

		// ensure insights component is disabled
		e2eutil.EnsureInsightsCapabilityDisabled(ctx, t, g, clients)

		// ensure console component is disabled
		e2eutil.EnsureConsoleCapabilityDisabled(ctx, t, g, clients)

		// ensure NodeTuning component is disabled
		e2eutil.EnsureNodeTuningCapabilityDisabled(ctx, t, clients, mgtClient, hostedCluster)

		// ensure ingress component is disabled
		e2eutil.EnsureIngressCapabilityDisabled(ctx, t, clients, mgtClient, hostedCluster)

		// ensure CNO operator configuration changes are properly handled
		e2eutil.EnsureCNOOperatorConfiguration(t, ctx, mgtClient, guestClient, hostedCluster)
	}).WithAssetReader(content.ReadFile).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "custom-config", globalOpts.ServiceAccountSigningKey)
}

// TestCreateClusterProxy implements a test that creates a cluster behind a proxy with the code under test.
func TestCreateClusterProxy(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
	t.Parallel()
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	clusterOpts.AWSPlatform.EnableProxy = true
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)

	e2eutil.NewHypershiftTest(t, ctx, nil).
		Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "proxy", globalOpts.ServiceAccountSigningKey)
}

func TestCreateClusterPrivate(t *testing.T) {
	testCreateClusterPrivate(t, false)
}

func TestCreateClusterPrivateWithRouteKAS(t *testing.T) {
	testCreateClusterPrivate(t, true)
}

// testCreateClusterPrivate implements a smoke test that creates a private cluster.
// Validations requiring guest cluster client are dropped here since the kas is not accessible when private.
// In the future we might want to leverage https://issues.redhat.com/browse/HOSTEDCP-697 to access guest cluster.
func testCreateClusterPrivate(t *testing.T, enableExternalDNS bool) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	if e2eutil.IsLessThan(e2eutil.Version415) {
		cleanupAnnotationIndex := slices.Index(clusterOpts.Annotations, fmt.Sprintf("%s=true", hyperv1.CleanupCloudResourcesAnnotation))
		if cleanupAnnotationIndex != -1 {
			clusterOpts.Annotations = slices.Delete(clusterOpts.Annotations, cleanupAnnotationIndex, cleanupAnnotationIndex+1)
		}
	}

	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)
	clusterOpts.AWSPlatform.EndpointAccess = string(hyperv1.Private)
	expectGuestKubeconfHostChange := false
	if !enableExternalDNS {
		clusterOpts.ExternalDNSDomain = ""
		expectGuestKubeconfHostChange = true
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Private -> PublicAndPrivate
		t.Run("SwitchFromPrivateToPublic", testSwitchEndpointAccess(ctx, mgtClient, hostedCluster, hyperv1.PublicAndPrivate, expectGuestKubeconfHostChange))
		// PublicAndPrivate -> Private
		t.Run("SwitchFromPublicToPrivate", testSwitchEndpointAccess(ctx, mgtClient, hostedCluster, hyperv1.Private, expectGuestKubeconfHostChange))
		// Get up to date hostedCluster object before EnsureHostedCluster in after()
		err := mgtClient.Get(ctx, crclient.ObjectKeyFromObject(hostedCluster), hostedCluster)
		g.Expect(err).ToNot(HaveOccurred(), "failed to get hostedCluster")
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, "private", globalOpts.ServiceAccountSigningKey)
}

func testSwitchEndpointAccess(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, endpointAccess hyperv1.AWSEndpointAccessType, expectGuestKubeconfHostChange bool) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		if globalOpts.Platform != hyperv1.AWSPlatform {
			t.Skip("test only supported on platform AWS")
		}

		// Get guest kubeconfig host before switching endpoint access
		host, err := e2eutil.GetGuestKubeconfigHost(t, ctx, client, hostedCluster)
		g.Expect(err).ToNot(HaveOccurred(), "failed to get guest kubeconfig host")
		t.Logf("Found guest kubeconfig host before switching endpoint access: %s", host)

		// Switch endpoint access
		err = e2eutil.UpdateObject(t, ctx, client, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Platform.AWS.EndpointAccess = endpointAccess
		})
		g.Expect(err).ToNot(HaveOccurred(), "failed to update hostedcluster EndpointAccess")

		if expectGuestKubeconfHostChange {
			e2eutil.WaitForGuestKubeconfigHostUpdate(t, ctx, client, hostedCluster, host)
		} else {
			e2eutil.WaitForGuestKubeconfigHostResolutionUpdate(t, ctx, host, endpointAccess)
		}
	}
}
