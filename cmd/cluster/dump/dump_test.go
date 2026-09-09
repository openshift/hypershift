package dump

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/util"
	hyperapi "github.com/openshift/hypershift/support/api"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/rest"
	clientgotesting "k8s.io/client-go/testing"

	capiaws "sigs.k8s.io/cluster-api-provider-aws/v2/api/v1beta2"
	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

func TestDumpOptionsManagementClient(t *testing.T) {
	defaultClient := crfake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()
	impersonatedClient := crfake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()

	t.Run("When an injected client is provided without impersonation, it should reuse the client", func(t *testing.T) {
		opts := &DumpOptions{Client: defaultClient}
		got, err := opts.managementClient()
		NewWithT(t).Expect(err).NotTo(HaveOccurred())
		NewWithT(t).Expect(got).To(BeIdenticalTo(defaultClient))
	})
	t.Run("When impersonation is requested, it should use the impersonated client", func(t *testing.T) {
		opts := &DumpOptions{
			Client:        defaultClient,
			ImpersonateAs: "test-user",
			ClientProvider: &util.ClientProvider{ImpersonatedClient: func(user string) (client.Client, error) {
				if user != "test-user" {
					return nil, errors.New("unexpected user")
				}
				return impersonatedClient, nil
			}},
		}
		got, err := opts.managementClient()
		NewWithT(t).Expect(err).NotTo(HaveOccurred())
		NewWithT(t).Expect(got).To(BeIdenticalTo(impersonatedClient))
	})
	t.Run("When no client provider is configured, it should return an error", func(t *testing.T) {
		_, err := (&DumpOptions{}).managementClient()
		NewWithT(t).Expect(err).To(HaveOccurred())
	})
	t.Run("When the provider returns an error, it should propagate the error", func(t *testing.T) {
		_, err := (&DumpOptions{ClientProvider: &util.ClientProvider{
			ControllerRuntimeClient: func(string) (client.Client, error) {
				return nil, errors.New("client unavailable")
			},
		}}).managementClient()
		NewWithT(t).Expect(err).To(MatchError("client unavailable"))
	})
}

func TestDumpOptionsManagementConfig(t *testing.T) {
	expectedConfig := &rest.Config{Host: "https://management.example"}
	t.Run("When a provider is configured, it should request the configured kubeconfig", func(t *testing.T) {
		opts := &DumpOptions{
			Kubeconfig: "/tmp/management-kubeconfig",
			ClientProvider: &util.ClientProvider{Config: func(path string) (*rest.Config, error) {
				if path != "/tmp/management-kubeconfig" {
					return nil, errors.New("unexpected kubeconfig path")
				}
				return expectedConfig, nil
			}},
		}
		got, err := opts.managementConfig()
		NewWithT(t).Expect(err).NotTo(HaveOccurred())
		NewWithT(t).Expect(got).To(BeIdenticalTo(expectedConfig))
	})
	t.Run("When no client provider is configured, it should return an error", func(t *testing.T) {
		_, err := (&DumpOptions{}).managementConfig()
		NewWithT(t).Expect(err).To(HaveOccurred())
	})
}

func TestDumpClusterWithRetry(t *testing.T) {
	t.Run("When context is already canceled, it should return an error without retrying", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		cancel() // cancel immediately so the ctx.Done() path is taken

		// Clear PATH so DumpCluster cannot find the oc binary and returns
		// an error on every attempt, which lets us exercise the ctx.Done()
		// select branch in the retry loop.
		t.Setenv("PATH", "")

		opts := &DumpOptions{
			Namespace:   "test-ns",
			Name:        "test-cluster",
			ArtifactDir: t.TempDir(),
			Log:         logr.Discard(),
		}

		err := DumpClusterWithRetry(ctx, opts)
		if err == nil {
			t.Fatal("expected an error when context is canceled, got nil")
		}
	})
}

func TestIsResourceRegistered(t *testing.T) {
	dummyGroup := "dummy.group.io"
	dummyVersion := "v2beta3"
	dummyKind := "machinedeployment"

	fakeDiscoveryClient := &fakediscovery.FakeDiscovery{
		Fake: &clientgotesting.Fake{
			Resources: []*metav1.APIResourceList{
				{
					GroupVersion: fmt.Sprintf("%s/%s", dummyGroup, dummyVersion),
					APIResources: []metav1.APIResource{
						{
							Kind: dummyKind,
						},
					},
				},
			},
		},
	}

	tests := []struct {
		name        string
		gvk         schema.GroupVersionKind
		expected    bool
		expectError bool
	}{
		{
			name:        "When group version is not found, it should return false",
			gvk:         schema.GroupVersionKind{Group: "non.existing.group.io", Version: dummyVersion, Kind: dummyKind},
			expected:    false,
			expectError: false,
		},
		{
			name:        "When group version is found but kind is not found, it should return false",
			gvk:         schema.GroupVersionKind{Group: dummyGroup, Version: dummyVersion, Kind: "non-existing-kind"},
			expected:    false,
			expectError: false,
		},
		{
			name:        "When group version kind is found, it should return true",
			gvk:         schema.GroupVersionKind{Group: dummyGroup, Version: dummyVersion, Kind: dummyKind},
			expected:    true,
			expectError: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := isResourceRegistered(fakeDiscoveryClient, test.gvk)
			if result != test.expected {
				t.Errorf("expected %v, got %v", test.expected, result)
			}
			if (err != nil) != test.expectError {
				t.Errorf("expected error: %v, got error: %v", test.expectError, err)
			}
		})
	}
}

func TestPlatformSpecificResources(t *testing.T) {
	tests := []struct {
		name     string
		platform hyperv1.PlatformType
		want     []string
	}{
		{
			name:     "When platform is AWS, it should return AWS infrastructure resources",
			platform: hyperv1.AWSPlatform,
			want: []string{
				"awsmachine.infrastructure.cluster.x-k8s.io",
				"awsmachinetemplate.infrastructure.cluster.x-k8s.io",
				"awscluster.infrastructure.cluster.x-k8s.io",
				"awsendpointservice.hypershift.openshift.io",
			},
		},
		{
			name:     "When platform is Azure, it should return Azure infrastructure resources",
			platform: hyperv1.AzurePlatform,
			want: []string{
				"azurecluster.infrastructure.cluster.x-k8s.io",
				"azureclusteridentity.infrastructure.cluster.x-k8s.io",
				"azuremachine.infrastructure.cluster.x-k8s.io",
				"azuremachinetemplate.infrastructure.cluster.x-k8s.io",
			},
		},
		{
			name:     "When platform is GCP, it should return GCP infrastructure resources",
			platform: hyperv1.GCPPlatform,
			want: []string{
				"gcpcluster.infrastructure.cluster.x-k8s.io",
				"gcpmachine.infrastructure.cluster.x-k8s.io",
				"gcpmachinetemplate.infrastructure.cluster.x-k8s.io",
			},
		},
		{
			name:     "When platform is IBMCloud, it should return IBM VPC infrastructure resources",
			platform: hyperv1.IBMCloudPlatform,
			want: []string{
				"ibmvpccluster.infrastructure.cluster.x-k8s.io",
				"ibmvpcmachine.infrastructure.cluster.x-k8s.io",
				"ibmvpcmachinetemplate.infrastructure.cluster.x-k8s.io",
			},
		},
		{
			name:     "When platform is PowerVS, it should return PowerVS infrastructure resources",
			platform: hyperv1.PowerVSPlatform,
			want: []string{
				"ibmpowervscluster.infrastructure.cluster.x-k8s.io",
				"ibmpowervsimage.infrastructure.cluster.x-k8s.io",
				"ibmpowervsmachine.infrastructure.cluster.x-k8s.io",
				"ibmpowervsmachinetemplate.infrastructure.cluster.x-k8s.io",
			},
		},
		{
			name:     "When platform is OpenStack, it should return OpenStack infrastructure resources",
			platform: hyperv1.OpenStackPlatform,
			want: []string{
				"openstackserver.infrastructure.cluster.x-k8s.io",
				"openstackcluster.infrastructure.cluster.x-k8s.io",
				"openstackmachine.infrastructure.cluster.x-k8s.io",
				"openstackmachinetemplate.infrastructure.cluster.x-k8s.io",
				"image.openstack.k-orc.cloud",
			},
		},
		{
			name:     "When platform is Agent, it should return Agent infrastructure resources",
			platform: hyperv1.AgentPlatform,
			want: []string{
				"agentmachine.capi-provider.agent-install.openshift.io",
				"agentmachinetemplate.capi-provider.agent-install.openshift.io",
				"agentcluster.capi-provider.agent-install.openshift.io",
			},
		},
		{
			name:     "When platform is KubeVirt, it should return KubeVirt infrastructure resources",
			platform: hyperv1.KubevirtPlatform,
			want: []string{
				"kubevirtmachine.infrastructure.cluster.x-k8s.io",
				"kubevirtmachinetemplate.infrastructure.cluster.x-k8s.io",
				"kubevirtcluster.infrastructure.cluster.x-k8s.io",
			},
		},
		{
			name:     "When platform is None, it should return no infrastructure resources",
			platform: hyperv1.NonePlatform,
			want:     []string{},
		},
		{
			name:     "When platform is unknown, it should return all platform infrastructure resources",
			platform: hyperv1.PlatformType(""),
			want: []string{
				"awsmachine.infrastructure.cluster.x-k8s.io",
				"awsmachinetemplate.infrastructure.cluster.x-k8s.io",
				"awscluster.infrastructure.cluster.x-k8s.io",
				"awsendpointservice.hypershift.openshift.io",
				"azurecluster.infrastructure.cluster.x-k8s.io",
				"azureclusteridentity.infrastructure.cluster.x-k8s.io",
				"azuremachine.infrastructure.cluster.x-k8s.io",
				"azuremachinetemplate.infrastructure.cluster.x-k8s.io",
				"gcpcluster.infrastructure.cluster.x-k8s.io",
				"gcpmachine.infrastructure.cluster.x-k8s.io",
				"gcpmachinetemplate.infrastructure.cluster.x-k8s.io",
				"ibmvpccluster.infrastructure.cluster.x-k8s.io",
				"ibmvpcmachine.infrastructure.cluster.x-k8s.io",
				"ibmvpcmachinetemplate.infrastructure.cluster.x-k8s.io",
				"ibmpowervscluster.infrastructure.cluster.x-k8s.io",
				"ibmpowervsimage.infrastructure.cluster.x-k8s.io",
				"ibmpowervsmachine.infrastructure.cluster.x-k8s.io",
				"ibmpowervsmachinetemplate.infrastructure.cluster.x-k8s.io",
				"openstackserver.infrastructure.cluster.x-k8s.io",
				"openstackcluster.infrastructure.cluster.x-k8s.io",
				"openstackmachine.infrastructure.cluster.x-k8s.io",
				"openstackmachinetemplate.infrastructure.cluster.x-k8s.io",
				"image.openstack.k-orc.cloud",
				"agentmachine.capi-provider.agent-install.openshift.io",
				"agentmachinetemplate.capi-provider.agent-install.openshift.io",
				"agentcluster.capi-provider.agent-install.openshift.io",
				"kubevirtmachine.infrastructure.cluster.x-k8s.io",
				"kubevirtmachinetemplate.infrastructure.cluster.x-k8s.io",
				"kubevirtcluster.infrastructure.cluster.x-k8s.io",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := resourceTypes(platformSpecificResources(test.platform))
			if fmt.Sprint(got) != fmt.Sprint(test.want) {
				t.Fatalf("expected resource types %v, got %v", test.want, got)
			}
		})
	}
}

func TestFilterRegisteredResources(t *testing.T) {
	t.Run("When candidates mix registered and unregistered resources, it should keep only the registered ones", func(t *testing.T) {
		c := crfake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()

		// Resources the (fake) management cluster reports as registered.
		fakeDiscoveryClient := &fakediscovery.FakeDiscovery{
			Fake: &clientgotesting.Fake{
				Resources: discoveryResourcesFor(t, c, []client.Object{
					&capiaws.AWSMachine{},
					&capiaws.AWSCluster{},
				}),
			},
		}

		candidates := []client.Object{
			&capiaws.AWSMachine{},         // registered
			&capiaws.AWSMachineTemplate{}, // not registered
			&capiaws.AWSCluster{},         // registered
			&hyperv1.AWSEndpointService{}, // not registered
			&capiazure.AzureCluster{},     // registered on a different platform, absent here
		}

		got, err := filterRegisteredResources(c, fakeDiscoveryClient, candidates)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := []string{
			"awsmachine.infrastructure.cluster.x-k8s.io",
			"awscluster.infrastructure.cluster.x-k8s.io",
		}
		if fmt.Sprint(resourceTypes(got)) != fmt.Sprint(want) {
			t.Fatalf("expected registered resource types %v, got %v", want, resourceTypes(got))
		}
	})
}

func TestBuildDumpResourceList(t *testing.T) {
	t.Run("When the hosted cluster has a platform, it should include base and registered platform/optional resources and exclude unregistered and other-platform resources", func(t *testing.T) {
		hostedCluster := &hyperv1.HostedCluster{
			ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "example"},
			Spec:       hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform}},
		}
		c := crfake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(hostedCluster).Build()

		// AzureCluster is registered on the (fake) management cluster but must
		// still be excluded because the hosted cluster is AWS. AWSMachine and
		// ControlPlaneComponent are registered and should be kept.
		fakeDiscoveryClient := &fakediscovery.FakeDiscovery{
			Fake: &clientgotesting.Fake{
				Resources: discoveryResourcesFor(t, c, []client.Object{
					&capiaws.AWSMachine{},
					&capiazure.AzureCluster{},
					&hyperv1.ControlPlaneComponent{},
				}),
			},
		}

		opts := &DumpOptions{Namespace: "clusters", Name: "example", Log: logr.Discard()}
		got, err := buildDumpResourceList(context.Background(), c, fakeDiscoveryClient, opts, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// coreResources and capiCoreResources are always included; the
		// control-plane resources are always included; then only the registered
		// platform/optional resources are appended.
		want := append([]string{}, resourceTypes(coreResources)...)
		want = append(want, resourceTypes(capiCoreResources)...)
		want = append(want,
			"hostedcontrolplane.hypershift.openshift.io",    // control-plane, always included
			"poddisruptionbudget.policy",                    // control-plane, always included
			"networkpolicy.networking.k8s.io",               // control-plane, always included
			"awsmachine.infrastructure.cluster.x-k8s.io",    // AWS platform, registered
			"controlplanecomponent.hypershift.openshift.io", // feature-gated, registered
		)
		if fmt.Sprint(resourceTypes(got)) != fmt.Sprint(want) {
			t.Fatalf("expected resource types %v, got %v", want, resourceTypes(got))
		}
	})
}

// discoveryResourcesFor builds fake API discovery entries for the given objects
// using the client's scheme, so filterRegisteredResources treats them as
// registered on the cluster.
func discoveryResourcesFor(t *testing.T, c client.Client, objs []client.Object) []*metav1.APIResourceList {
	t.Helper()
	byGroupVersion := map[string][]metav1.APIResource{}
	for _, obj := range objs {
		gvk, err := c.GroupVersionKindFor(obj)
		if err != nil {
			t.Fatalf("failed to get GVK for %T: %v", obj, err)
		}
		groupVersion := gvk.GroupVersion().String()
		byGroupVersion[groupVersion] = append(byGroupVersion[groupVersion], metav1.APIResource{Kind: gvk.Kind})
	}
	lists := make([]*metav1.APIResourceList, 0, len(byGroupVersion))
	for groupVersion, resources := range byGroupVersion {
		lists = append(lists, &metav1.APIResourceList{GroupVersion: groupVersion, APIResources: resources})
	}
	return lists
}

func TestNewDumpCommand(t *testing.T) {
	t.Run("When using the --dump-guest-cluster flag", func(t *testing.T) {
		tests := []struct {
			name               string
			args               []string
			isExpectingAnError bool
			isExpectingADump   bool
			expectedPolicies   []DumpGuestClusterPolicy
		}{
			{
				name:               "When the flag is not set, it should not dump the guest cluster",
				args:               []string{"--artifact-dir", "test"},
				isExpectingAnError: false,
				isExpectingADump:   false,
				expectedPolicies:   []DumpGuestClusterPolicy{},
			},
			{
				name:               "When the flag is set with no policy specified, it should dump the guest cluster with no policy",
				args:               []string{"--dump-guest-cluster", "--artifact-dir", "test"},
				isExpectingAnError: false,
				isExpectingADump:   true,
				expectedPolicies:   []DumpGuestClusterPolicy{},
			},
			{
				name:               "When the flag is set with true as a value, it should dump the guest cluster with no policy",
				args:               []string{"--dump-guest-cluster=true", "--artifact-dir", "test"},
				isExpectingAnError: false,
				isExpectingADump:   true,
				expectedPolicies:   []DumpGuestClusterPolicy{},
			},
			{
				name:               "When the flag is set with false as a value, it should not dump the guest cluster",
				args:               []string{"--dump-guest-cluster=false", "--artifact-dir", "test"},
				isExpectingAnError: false,
				isExpectingADump:   false,
				expectedPolicies:   []DumpGuestClusterPolicy{},
			},
			{
				name:               "When the deprecated --dump-guest-cluster-through-kube-service flag is set instead, it should dump the guest cluster with the direct-kube-api-service-access policy",
				args:               []string{"--dump-guest-cluster-through-kube-service", "--artifact-dir", "test"},
				isExpectingAnError: false,
				isExpectingADump:   true,
				expectedPolicies:   []DumpGuestClusterPolicy{DirectKubeApiServiceAccess},
			},
			{
				name:               "When the flag is set to direct-kube-api-service-access policy, it should dump the guest cluster with this policy only",
				args:               []string{"--dump-guest-cluster=direct-kube-api-service-access", "--artifact-dir", "test"},
				isExpectingAnError: false,
				isExpectingADump:   true,
				expectedPolicies:   []DumpGuestClusterPolicy{DirectKubeApiServiceAccess},
			},
			{
				name:               "When the flag is set with all policies, it should dump the guest cluster with these policies",
				args:               []string{"--dump-guest-cluster=direct-kube-api-service-access,fail-on-error", "--artifact-dir", "test"},
				isExpectingAnError: false,
				isExpectingADump:   true,
				expectedPolicies:   []DumpGuestClusterPolicy{DirectKubeApiServiceAccess, FailOnError},
			},
			{
				name:               "When the flag is set with an invalid policy, it should return an error",
				args:               []string{"--dump-guest-cluster=direct-kube-api-service-access,invalid-policy", "--artifact-dir", "test"},
				isExpectingAnError: true,
				isExpectingADump:   false,
				expectedPolicies:   []DumpGuestClusterPolicy{},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				var capturedOpts *DumpOptions
				cmd := NewDumpCommand(func(ctx context.Context, opts *DumpOptions) error {
					capturedOpts = opts
					return nil
				})
				cmd.SetArgs(test.args)
				err := cmd.Execute()

				if test.isExpectingAnError {
					if err == nil {
						t.Fatal("expected an error but got none")
					}
					return
				}

				if err != nil {
					t.Fatalf("did not expect an error but got: %v", err)
				}

				if capturedOpts == nil {
					t.Fatal("expected dump callback to be called but it wasn't")
				}

				if capturedOpts.IsDumpingGuestCluster != test.isExpectingADump {
					t.Fatalf("expected IsDumpingGuestCluster to be %v but got %v", test.isExpectingADump, capturedOpts.IsDumpingGuestCluster)
				}

				if len(capturedOpts.DumpGuestClusterPolicies) != len(test.expectedPolicies) {
					t.Fatalf("expected DumpGuestClusterPolicies to have length %d but got %d", len(test.expectedPolicies), len(capturedOpts.DumpGuestClusterPolicies))
				}

				for _, policy := range test.expectedPolicies {
					if _, exists := capturedOpts.DumpGuestClusterPolicies[policy]; !exists {
						t.Fatalf("expected DumpGuestClusterPolicies to contain policy %s but it did not", policy)
					}
				}
			})
		}
	})
}
