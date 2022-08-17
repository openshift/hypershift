package hostedcluster

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/zapr"
	configv1 "github.com/openshift/api/config/v1"
	hyperapi "github.com/openshift/hypershift/api"
	"github.com/openshift/hypershift/api/util/ipnet"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	"go.uber.org/zap/zaptest"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	utilpointer "k8s.io/utils/pointer"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache/informertest"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestWebhookAllowsHostedClusterReconcilerUpdates(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name              string
		hostedCluster     *hyperv1.HostedCluster
		additionalObjects []crclient.Object
	}{
		{
			name: "None cluster on azure management cluster",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "none-cluster",
					Namespace: "some-ns",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.NonePlatform,
					},
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
							},
						},
					},
				},
			},
			additionalObjects: []crclient.Object{
				&configv1.Infrastructure{
					ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
					Spec:       configv1.InfrastructureSpec{PlatformSpec: configv1.PlatformSpec{Type: configv1.AzurePlatformType}},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Namespace: "some-ns"},
					Data:       map[string][]byte{".dockerconfigjson": []byte("something")},
				},
				&configv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "cluster"}},
				&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "none-cluster"}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.hostedCluster.Annotations = map[string]string{
				hyperv1.ControlPlaneOperatorImageAnnotation: "some-image",
			}

			mgr, err := ctrl.NewManager(&rest.Config{}, ctrl.Options{
				MetricsBindAddress: "0",
				MapperProvider: func(*rest.Config) (meta.RESTMapper, error) {
					return restmapper.NewDiscoveryRESTMapper(nil), nil
				},
				NewClient: func(cache.Cache, *rest.Config, crclient.Options, ...crclient.Object) (crclient.Client, error) {
					return &hostedClusterUpdateValidatingClient{
						Client: fake.NewClientBuilder().
							WithScheme(hyperapi.Scheme).
							WithObjects(append(tc.additionalObjects, tc.hostedCluster)...).
							Build(),
					}, nil
				},
				NewCache: func(config *rest.Config, opts cache.Options) (cache.Cache, error) {
					return &informertest.FakeInformers{}, nil
				},
				Scheme: hyperapi.Scheme,
			})
			if err != nil {
				t.Fatalf("failed to construct manager: %v", err)
			}
			hostedClusterReconciler := &HostedClusterReconciler{
				Client:                        mgr.GetClient(),
				ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
				ImageMetadataProvider: imageMetadataProviderFunc(func(context.Context, string, []byte) (*dockerv1client.DockerImageConfig, error) {
					return &dockerv1client.DockerImageConfig{}, nil
				}),
				ReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{},
			}
			if err := hostedClusterReconciler.SetupWithManager(mgr, upsert.New(true)); err != nil {
				t.Fatalf("failed to set up hostedClusterReconciler: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = log.IntoContext(ctx, zapr.NewLogger(zaptest.NewLogger(t)))

			if _, err := hostedClusterReconciler.Reconcile(ctx, ctrl.Request{NamespacedName: crclient.ObjectKeyFromObject(tc.hostedCluster)}); err != nil {
				t.Errorf("failed to reconcile cluster: %v", err)
			}
		})
	}
}

type hostedClusterUpdateValidatingClient struct {
	crclient.Client
}

func (h *hostedClusterUpdateValidatingClient) Update(ctx context.Context, obj crclient.Object, opts ...crclient.UpdateOption) error {
	hcluster, isHcluster := obj.(*hyperv1.HostedCluster)
	if !isHcluster {
		return h.Client.Update(ctx, obj, opts...)
	}

	oldCluster := &hyperv1.HostedCluster{}
	if err := h.Client.Get(ctx, crclient.ObjectKeyFromObject(hcluster), oldCluster); err != nil {
		return fmt.Errorf("failed to validate hostedcluster update: failed to get old hosted cluster: %w", err)
	}

	if err := validateHostedClusterUpdate(hcluster.DeepCopy(), oldCluster.DeepCopy()); err != nil {
		return fmt.Errorf("update rejected by admission: %w", err)
	}

	return h.Client.Update(ctx, obj, opts...)
}

type imageMetadataProviderFunc func(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error)

func (f imageMetadataProviderFunc) ImageMetadata(ctx context.Context, imageRef string, pullSecret []byte) (*dockerv1client.DockerImageConfig, error) {
	return f(ctx, imageRef, pullSecret)
}

func TestValidateHostedClusterUpdate(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		old  *hyperv1.HostedCluster
		new  *hyperv1.HostedCluster

		expectedErrorString string
		expectError         bool
	}{
		{
			name: "APIServer port was unset and gets set, allowed",
			old:  &hyperv1.HostedCluster{},
			new: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Networking: hyperv1.ClusterNetworking{APIServer: &hyperv1.APIServerNetworking{Port: utilpointer.Int32(7443)}}},
			},
			expectError:         false,
			expectedErrorString: "",
		},
		{
			name: "APIServer port remains unchanged, allowed",
			old: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Networking: hyperv1.ClusterNetworking{APIServer: &hyperv1.APIServerNetworking{Port: utilpointer.Int32(7443)}}},
			},
			new: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Networking: hyperv1.ClusterNetworking{APIServer: &hyperv1.APIServerNetworking{Port: utilpointer.Int32(7443)}}},
			},
			expectError:         false,
			expectedErrorString: "",
		},
		{
			name: "APIServer port gets updated, not allowed",
			old: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Networking: hyperv1.ClusterNetworking{APIServer: &hyperv1.APIServerNetworking{Port: utilpointer.Int32(7443)}}},
			},
			new: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{Networking: hyperv1.ClusterNetworking{APIServer: &hyperv1.APIServerNetworking{Port: utilpointer.Int32(8443)}}},
			},
			expectError:         true,
			expectedErrorString: "HostedCluster.spec.networking.apiServer.port: Invalid value: 8443: Attempted to change an immutable field",
		},
		{
			name: "when .AWSPlatformSpec.RolesRef, .AWSPlatformSpec.roles .AWSPlatformSpec.*Creds are changed it should be allowed",
			old: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							RolesRef: hyperv1.AWSRolesRef{},
							Roles: []hyperv1.AWSRoleCredentials{
								{
									ARN:       "test",
									Namespace: "test",
									Name:      "test",
								},
								{
									ARN:       "test",
									Namespace: "test",
									Name:      "test",
								}},
							KubeCloudControllerCreds:  corev1.LocalObjectReference{Name: "test"},
							NodePoolManagementCreds:   corev1.LocalObjectReference{Name: "test"},
							ControlPlaneOperatorCreds: corev1.LocalObjectReference{Name: "test"},
						},
					},
				},
			},
			new: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
						AWS: &hyperv1.AWSPlatformSpec{
							RolesRef: hyperv1.AWSRolesRef{
								IngressARN:              "test",
								ImageRegistryARN:        "test",
								StorageARN:              "test",
								NetworkARN:              "test",
								KubeCloudControllerARN:  "test",
								NodePoolManagementARN:   "test",
								ControlPlaneOperatorARN: "test",
							},
							Roles:                     nil,
							KubeCloudControllerCreds:  corev1.LocalObjectReference{},
							NodePoolManagementCreds:   corev1.LocalObjectReference{},
							ControlPlaneOperatorCreds: corev1.LocalObjectReference{},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "Changing immutable slice of services, not allowed",
			new: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
							},
						},
					},
				},
			},
			old: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.Ignition,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			expectError:         true,
			expectedErrorString: "HostedCluster.spec.services.servicePublishingStrategy.type: Invalid value: \"Route\": Attempted to change an immutable field",
		},
		{
			name: "Multiple immutable fields changed, not allowed",
			old: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{Port: utilpointer.Int32(7443)},
					},
				},
			},
			new: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						APIServer: &hyperv1.APIServerNetworking{Port: utilpointer.Int32(8443)},
					},
					DNS: hyperv1.DNSSpec{BaseDomain: "hypershift2"},
				},
			},
			expectError:         true,
			expectedErrorString: "[HostedCluster.spec.dns.baseDomain: Invalid value: \"hypershift2\": Attempted to change an immutable field, HostedCluster.spec.networking.apiServer.port: Invalid value: 8443: Attempted to change an immutable field]",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateHostedClusterUpdate(tc.new, tc.old)
			if (err != nil) != tc.expectError {
				t.Errorf("expected error to be %t, was %t", tc.expectError, err != nil)
			}
			if len(tc.expectedErrorString) > 0 && tc.expectedErrorString != err.Error() {
				t.Errorf("expected error to be %s, was %s", tc.expectedErrorString, err)
			}

		})
	}
}

func TestValidateHostedClusterCreate(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		hc   *hyperv1.HostedCluster

		expectedErrorString string
		expectError         bool
	}{
		{
			name: "Setting network CIDRs, allowed",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ServiceCIDR: "192.168.0.0/24",
						PodCIDR:     "192.168.1.0/24",
						MachineCIDR: "192.168.2.0/24",
					},
				},
			},
			expectError:         false,
			expectedErrorString: "",
		},
		{
			name: "Setting network CIDRs IPv6, allowed",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ServiceCIDR: "fd63:c754:4851:46da::/64",
						PodCIDR:     "fdf3:6f16:b298:b2b1::/64",
						MachineCIDR: "fd76:c0ee:a695:3b14::/64",
					},
				},
			},
			expectError:         false,
			expectedErrorString: "",
		},
		{
			name: "Setting network CIDRs ipv6 with overlap, not allowed",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ServiceCIDR: "fd63:c754:4851:46da::/64",
						PodCIDR:     "fdf3:6f16:b298:b2b1::/64",
						MachineCIDR: "fd63:c754:4851:46da::/80",
					},
				},
			},
			expectError:         true,
			expectedErrorString: `spec.networking.serviceCIDR: Invalid value: "fd63:c754:4851:46da::/64": spec.networking.serviceCIDR and spec.networking.machineCIDR overlap: fd63:c754:4851:46da::/64 and fd63:c754:4851:46da::/80`,
		},
		{
			name: "Setting network CIDRs overlapped, not allowed",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ServiceCIDR: "192.168.0.0/24",
						PodCIDR:     "192.168.1.0/24",
						MachineCIDR: "192.168.2.0/16",
					},
				},
			},
			expectError:         true,
			expectedErrorString: `[spec.networking.serviceCIDR: Invalid value: "192.168.0.0/24": spec.networking.serviceCIDR and spec.networking.machineCIDR overlap: 192.168.0.0/24 and 192.168.0.0/16, spec.networking.podCIDR: Invalid value: "192.168.1.0/24": spec.networking.podCIDR and spec.networking.machineCIDR overlap: 192.168.1.0/24 and 192.168.0.0/16]`,
		},
		{
			name: "Setting network CIDRs overlapped, not allowed",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ServiceCIDR: "192.168.0.0/24",
						PodCIDR:     "192.168.1.0/24",
						MachineCIDR: "192.168.1.4/30",
					},
				},
			},
			expectError:         true,
			expectedErrorString: `spec.networking.podCIDR: Invalid value: "192.168.1.0/24": spec.networking.podCIDR and spec.networking.machineCIDR overlap: 192.168.1.0/24 and 192.168.1.4/30`,
		},
		{
			// Note that more values are set below as they required initialization
			// with functions
			name: "Setting overlapping slice network CIDRs in same slice, not allowed",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ServiceCIDR: "192.168.0.0/24",
						PodCIDR:     "192.168.1.0/24",
						MachineCIDR: "192.168.2.0/24",
					},
				},
			},
			expectError:         true,
			expectedErrorString: `spec.networking.ClusterNetwork: Invalid value: "192.168.0.0/24": spec.networking.ClusterNetwork and spec.networking.ClusterNetwork overlap: 192.168.0.0/24 and 192.168.0.80/30`,
		},
		{
			// Note that more values are set below as they required initialization
			// with functions
			name: "Setting overlapping slice network CIDRs, not allowed",
			hc: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ServiceCIDR: "192.168.0.0/24",
						PodCIDR:     "192.168.1.0/24",
						MachineCIDR: "192.168.2.0/24",
					},
				},
			},
			expectError:         true,
			expectedErrorString: `spec.networking.MachineNetwork: Invalid value: "172.16.1.0/24": spec.networking.MachineNetwork and spec.networking.ServiceNetwork overlap: 172.16.1.0/24 and 172.16.1.252/32`,
		},
	}

	clusterNet := make([]hyperv1.ClusterNetworkEntry, 2)
	cidr, _ := ipnet.ParseCIDR("192.168.0.0/24")
	clusterNet[0].CIDR = *cidr
	cidr, _ = ipnet.ParseCIDR("192.168.0.80/30")
	clusterNet[1].CIDR = *cidr
	testCases[5].hc.Spec.Networking.ClusterNetwork = clusterNet

	machineNet := make([]hyperv1.MachineNetworkEntry, 2)
	cidr, _ = ipnet.ParseCIDR("172.16.0.0/24")
	machineNet[0].CIDR = *cidr
	cidr, _ = ipnet.ParseCIDR("172.16.1.0/24")
	machineNet[1].CIDR = *cidr
	serviceNet := make([]hyperv1.ServiceNetworkEntry, 2)
	cidr, _ = ipnet.ParseCIDR("172.16.1.252/32")
	serviceNet[0].CIDR = *cidr
	cidr, _ = ipnet.ParseCIDR("172.16.3.0/24")
	serviceNet[1].CIDR = *cidr
	testCases[6].hc.Spec.Networking.ServiceNetwork = serviceNet
	testCases[6].hc.Spec.Networking.MachineNetwork = machineNet

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateHostedClusterCreate(tc.hc)
			if (err != nil) != tc.expectError {
				t.Errorf("expected error to be '%t', was '%t'", tc.expectError, err != nil)
			}
			if err != nil && len(tc.expectedErrorString) > 0 && tc.expectedErrorString != err.Error() {
				t.Errorf("expected error to be '%s', was '%s'", tc.expectedErrorString, err)
			}

		})
	}
}
