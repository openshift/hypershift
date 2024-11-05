//go:build e2e
// +build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/assets"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/integration"
	integrationframework "github.com/openshift/hypershift/test/integration/framework"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestOnCreateAPIUX(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	t.Run("HostedCluster creation", func(t *testing.T) {
		g := NewWithT(t)
		client, err := e2eutil.GetClient()
		g.Expect(err).NotTo(HaveOccurred(), "couldn't get client")

		testCases := []struct {
			name                   string
			file                   string
			expectedErrorSubstring string
		}{
			{
				name:                   "Azure requires services publishing strategy with route and hostname",
				file:                   "azure-services-ignition-route-not-hostname.yaml",
				expectedErrorSubstring: "Azure platform requires Ignition Route service with a hostname to be defined",
			},
			{
				name:                   "HostedCluster should fail if OpenStack value is set as platform type and no TechPreviewNoUpgrade",
				file:                   "openstack-platform-enum.yaml",
				expectedErrorSubstring: "spec.platform.type: Unsupported value: \"OpenStack\"",
			},
		}

		for _, tc := range testCases {
			hc := assets.ShouldHostedCluster(content.ReadFile, fmt.Sprintf("assets/%s", tc.file))
			defer client.Delete(ctx, hc)
			err = client.Create(ctx, hc)
			g.Expect(err).To(HaveOccurred())
			g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrorSubstring))
		}
	})

	t.Run("NodePool creation", func(t *testing.T) {
		g := NewWithT(t)
		client, err := e2eutil.GetClient()
		g.Expect(err).NotTo(HaveOccurred(), "couldn't get client")

		testCases := []struct {
			name        string
			file        string
			validations []struct {
				name                   string
				mutateInput            func(*hyperv1.NodePool)
				expectedErrorSubstring string
			}
		}{
			{
				name: "When Taint key/value is not a qualified name with an optional subdomain prefix to upstream validation, it should fail",
				file: "nodepool-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.NodePool)
					expectedErrorSubstring string
				}{
					{
						name: "when key prefix is not a valid sudomain it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "prefix@/suffix", Value: "value", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "key must be a qualified name with an optional subdomain prefix e.g. example.com/MyName",
					},
					{
						name: "when key suffix is not a valid qualified name it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "prefix/suffix@", Value: "value", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "key must be a qualified name with an optional subdomain prefix e.g. example.com/MyName",
					},
					{
						name: "when key is empty it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "", Value: "value", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "spec.taints[0].key in body should be at least 1 chars long",
					},
					{
						name: "when key is a valid qualified name with no prefix it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "valid-suffix", Value: "", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when key is a valid qualified name with a subdomain prefix it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "valid-prefix.com/valid-suffix", Value: "", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when key is a valid qualified name with a subdomain prefix and value is a valid qualified name it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "valid-prefix.com/valid-suffix", Value: "value", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when value contains strange chars it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Taints = []hyperv1.Taint{{Key: "valid-prefix.com/valid-suffix", Value: "@", Effect: "NoSchedule"}}
						},
						expectedErrorSubstring: "Value must start and end with alphanumeric characters and can only contain '-', '_', '.' in the middle",
					},
				},
			},
			{
				name: "when pausedUntil is not a date with RFC3339 format or a boolean as in 'true', 'false', 'True', 'False' it should fail",
				file: "nodepool-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.NodePool)
					expectedErrorSubstring string
				}{
					{
						name: "when pausedUntil is a random string it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("fail")
						},
						expectedErrorSubstring: "PausedUntil must be a date in RFC3339 format or 'True', 'true', 'False' or 'false'",
					},
					{
						name: "when pausedUntil date is not RFC3339 format it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("2022-01-01")
						},
						expectedErrorSubstring: "PausedUntil must be a date in RFC3339 format or 'True', 'true', 'False' or 'false'",
					},
					{
						name: "when pausedUntil is an allowed bool False it shoud pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("False")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when pausedUntil is an allowed bool false it shoud pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("false")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when pausedUntil is an allowed bool true it shoud pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("true")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when pausedUntil is an allowed bool True it shoud pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("True")
						},
						expectedErrorSubstring: "",
					},
					{
						name: "when pausedUntil date is RFC3339 it shoud pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.PausedUntil = ptr.To("2022-01-01T00:00:00Z")
						},
						expectedErrorSubstring: "",
					},
				},
			},
			{
				name: "when release does not have a valid image value it should fail",
				file: "nodepool-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.NodePool)
					expectedErrorSubstring string
				}{
					{
						name: "when image is bad format it shoud fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Release.Image = "@"
						},
						expectedErrorSubstring: "Image must start with a word character (letters, digits, or underscores) and contain no white spaces",
					},
					{
						name: "when image is empty it shoud fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Release.Image = "@"
						},
						expectedErrorSubstring: "Image must start with a word character (letters, digits, or underscores) and contain no white spaces",
					},
					{
						name: "when image is valid it should pass",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.17.0-rc.0-x86_64"
						},
						expectedErrorSubstring: "",
					},
				},
			},
			{
				name: "when management has invalid input it should fail",
				file: "nodepool-base.yaml",
				validations: []struct {
					name                   string
					mutateInput            func(*hyperv1.NodePool)
					expectedErrorSubstring string
				}{
					{
						name: "when replace upgrade type is set with inPlace configuration it shoud fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Management = hyperv1.NodePoolManagement{
								UpgradeType: hyperv1.UpgradeTypeReplace,
								InPlace: &hyperv1.InPlaceUpgrade{
									MaxUnavailable: ptr.To(intstr.FromInt32(1)),
								},
							}
						},
						expectedErrorSubstring: "The 'inPlace' field can only be set when 'upgradeType' is 'InPlace'",
					},
					{
						name: "when  strategy is onDelete with RollingUpdate configuration it should fail",
						mutateInput: func(np *hyperv1.NodePool) {
							np.Spec.Management = hyperv1.NodePoolManagement{
								UpgradeType: hyperv1.UpgradeTypeReplace,
								Replace: &hyperv1.ReplaceUpgrade{
									Strategy: hyperv1.UpgradeStrategyOnDelete,
									RollingUpdate: &hyperv1.RollingUpdate{
										MaxUnavailable: ptr.To(intstr.FromInt32(1)),
									},
								},
							}
						},
						expectedErrorSubstring: "The 'rollingUpdate' field can only be set when 'strategy' is 'RollingUpdate'",
					},
				},
			},
		}

		for _, tc := range testCases {
			for _, v := range tc.validations {
				t.Logf("Running validation %q", v.name)
				nodePool := assets.ShouldNodePool(content.ReadFile, fmt.Sprintf("assets/%s", tc.file))
				defer client.Delete(ctx, nodePool)
				v.mutateInput(nodePool)

				err = client.Create(ctx, nodePool)
				if v.expectedErrorSubstring != "" {
					g.Expect(err).To(HaveOccurred())
					g.Expect(err.Error()).To(ContainSubstring(v.expectedErrorSubstring))
				} else {
					g.Expect(err).ToNot(HaveOccurred())
				}
				client.Delete(ctx, nodePool)
			}
		}
	})
}

// TestCreateCluster implements a test that creates a cluster with the code under test
// vs upgrading to the code under test as TestUpgradeControlPlane does.
func TestCreateCluster(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	zones := strings.Split(globalOpts.configurableClusterOptions.Zone.String(), ",")
	if len(zones) >= 3 {
		// CreateCluster also tests multi-zone workers work properly if a sufficient number of zones are configured
		t.Logf("Sufficient zones available for InfrastructureAvailabilityPolicy HighlyAvailable")
		clusterOpts.AWSPlatform.Zones = zones
		clusterOpts.InfrastructureAvailabilityPolicy = string(hyperv1.HighlyAvailable)
		clusterOpts.NodePoolReplicas = 1
	}
	if !e2eutil.IsLessThan(e2eutil.Version418) {
		clusterOpts.FeatureSet = string(configv1.TechPreviewNoUpgrade)
		// We want to do the toleration test with TechPreviewNoUpgrade feature set enabled
		// so we cover all pods, including ones that are only deployed with TechPreviewNoUpgrade
		clusterOpts.Tolerations = []string{"key=hypershift.openshift.io/e2e-test,operator=Exists,effect=NoSchedule"}
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Sanity check the cluster by waiting for the nodes to report ready
		_ = e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

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
		e2eutil.EnsureCustomTolerations(t, ctx, mgtClient, hostedCluster)
	}).
		Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

// TestCreateClusterV2 tests the new CPO implementation, which is currently hidden behind an annotation.
func TestCreateClusterV2(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)
	zones := strings.Split(globalOpts.configurableClusterOptions.Zone.String(), ",")
	if len(zones) >= 3 {
		// CreateCluster also tests multi-zone workers work properly if a sufficient number of zones are configured
		t.Logf("Sufficient zones available for InfrastructureAvailabilityPolicy HighlyAvailable")
		clusterOpts.AWSPlatform.Zones = zones
		clusterOpts.InfrastructureAvailabilityPolicy = string(hyperv1.HighlyAvailable)
		clusterOpts.NodePoolReplicas = 1
	}
	clusterOpts.BeforeApply = func(o crclient.Object) {
		switch obj := o.(type) {
		case *hyperv1.HostedCluster:
			if obj.Annotations == nil {
				obj.Annotations = make(map[string]string)
			}
			obj.Annotations[hyperv1.ControlPlaneOperatorV2Annotation] = "true"
		}
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Sanity check the cluster by waiting for the nodes to report ready
		_ = e2eutil.WaitForGuestClient(t, ctx, mgtClient, hostedCluster)

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
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
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
	zones := strings.Split(globalOpts.configurableClusterOptions.Zone.String(), ",")
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
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

func TestCreateClusterCustomConfig(t *testing.T) {
	if globalOpts.Platform != hyperv1.AWSPlatform {
		t.Skip("test only supported on platform AWS")
	}
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	// find kms key ARN using alias
	kmsKeyArn, err := e2eutil.GetKMSKeyArn(clusterOpts.AWSPlatform.Credentials.AWSCredentialsFile, clusterOpts.AWSPlatform.Region, globalOpts.configurableClusterOptions.AWSKmsKeyAlias)
	if err != nil || kmsKeyArn == nil {
		t.Fatal("failed to retrieve kms key arn")
	}

	clusterOpts.AWSPlatform.EtcdKMSKeyARN = *kmsKeyArn

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {

		g.Expect(hostedCluster.Spec.SecretEncryption.KMS.AWS.ActiveKey.ARN).To(Equal(*kmsKeyArn))
		g.Expect(hostedCluster.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN).ToNot(BeEmpty())

		guestClient := e2eutil.WaitForGuestClient(t, testContext, mgtClient, hostedCluster)
		e2eutil.EnsureSecretEncryptedUsingKMSV2(t, ctx, hostedCluster, guestClient)
		// test oauth with identity provider
		e2eutil.EnsureOAuthWithIdentityProvider(t, ctx, mgtClient, hostedCluster)
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

func TestNoneCreateCluster(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testContext)
	defer cancel()

	clusterOpts := globalOpts.DefaultClusterOptions(t)

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Wait for the rollout to be reported complete
		// Since the None platform has no workers, CVO will not have expectations set,
		// which in turn means that the ClusterVersion object will never be populated.
		// Therefore only test if the control plane comes up (etc, apiserver, ...)
		e2eutil.WaitForConditionsOnHostedControlPlane(t, ctx, mgtClient, hostedCluster, globalOpts.LatestReleaseImage)

		// etcd restarts for me once always and apiserver two times before running stable
		// e2eutil.EnsureNoCrashingPods(t, ctx, client, hostedCluster)
	}).Execute(&clusterOpts, hyperv1.NonePlatform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
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
		Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
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
	clusterOpts.ControlPlaneAvailabilityPolicy = string(hyperv1.SingleReplica)
	clusterOpts.AWSPlatform.EndpointAccess = string(hyperv1.Private)
	expectGuestKubeconfHostChange := false
	if !enableExternalDNS {
		clusterOpts.ExternalDNSDomain = ""
		expectGuestKubeconfHostChange = true
	}

	e2eutil.NewHypershiftTest(t, ctx, func(t *testing.T, g Gomega, mgtClient crclient.Client, hostedCluster *hyperv1.HostedCluster) {
		// Private -> publicAndPrivate
		t.Run("SwitchFromPrivateToPublic", testSwitchFromPrivateToPublic(ctx, mgtClient, hostedCluster, &clusterOpts, expectGuestKubeconfHostChange))
		// publicAndPrivate -> Private
		t.Run("SwitchFromPublicToPrivate", testSwitchFromPublicToPrivate(ctx, mgtClient, hostedCluster, &clusterOpts))
	}).Execute(&clusterOpts, globalOpts.Platform, globalOpts.ArtifactDir, globalOpts.ServiceAccountSigningKey)
}

func testSwitchFromPrivateToPublic(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts *e2eutil.PlatformAgnosticOptions, expectGuestKubeconfHostChange bool) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		if globalOpts.Platform != hyperv1.AWSPlatform {
			t.Skip("test only supported on platform AWS")
		}

		var (
			host string
			err  error
		)
		if expectGuestKubeconfHostChange {
			// Get guest kubeconfig host before switching endpoint access
			host, err = e2eutil.GetGuestKubeconfigHost(t, ctx, client, hostedCluster)
			g.Expect(err).ToNot(HaveOccurred(), "failed to get guest kubeconfig host")
			t.Logf("Found guest kubeconfig host before switching endpoint access: %s", host)
		}

		// Switch to PublicAndPrivate endpoint access
		err = e2eutil.UpdateObject(t, ctx, client, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Platform.AWS.EndpointAccess = hyperv1.PublicAndPrivate
		})
		g.Expect(err).ToNot(HaveOccurred(), "failed to update hostedcluster EndpointAccess")

		if expectGuestKubeconfHostChange {
			e2eutil.WaitForGuestKubeconfigHostUpdate(t, ctx, client, hostedCluster, host)
		}

		e2eutil.ValidatePublicCluster(t, ctx, client, hostedCluster, clusterOpts)
	}
}

func testSwitchFromPublicToPrivate(ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, clusterOpts *e2eutil.PlatformAgnosticOptions) func(t *testing.T) {
	return func(t *testing.T) {
		g := NewWithT(t)
		if globalOpts.Platform != hyperv1.AWSPlatform {
			t.Skip("test only supported on platform AWS")
		}

		err := e2eutil.UpdateObject(t, ctx, client, hostedCluster, func(obj *hyperv1.HostedCluster) {
			obj.Spec.Platform.AWS.EndpointAccess = hyperv1.Private
		})
		g.Expect(err).ToNot(HaveOccurred(), "failed to update hostedcluster EndpointAccess")

		e2eutil.ValidatePrivateCluster(t, ctx, client, hostedCluster, clusterOpts)
	}
}
