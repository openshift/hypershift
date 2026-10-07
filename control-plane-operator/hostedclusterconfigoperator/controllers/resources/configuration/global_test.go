package configuration

import (
	"context"
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/globalconfig"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func configurationHCP() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant", Namespace: "control-plane", UID: "hcp-uid"},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{Type: hyperv1.NonePlatform},
			DNS:      hyperv1.DNSSpec{BaseDomain: "example.com"}, InfraID: "tenant-infra",
		},
		Status: hyperv1.HostedControlPlaneStatus{ControlPlaneEndpoint: hyperv1.APIEndpoint{Host: "api.example.com", Port: 6443}},
	}
}

func configurationClients(objects ...client.Object) (client.Client, client.Client) {
	return fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).WithStatusSubresource(&configv1.Infrastructure{}).Build(),
		fake.NewClientBuilder().WithScheme(api.Scheme).Build()
}

func TestReconcileGlobal(t *testing.T) {
	t.Run("When image policy deletion fails, it should skip IDMS but attempt subsequent global resources", func(t *testing.T) {
		assert := NewWithT(t)
		failure := errors.New("delete failure")
		guest := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(globalconfig.ProxyConfig(), globalconfig.ImageContentSourcePolicy()).WithStatusSubresource(&configv1.Infrastructure{}).WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error { return failure },
		}).Build()
		_, controlPlane := configurationClients()
		root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
		err := ReconcileGlobal(t.Context(), root.hosted(), root.controlPlane(), globalParams(configurationHCP()))
		assert.Expect(errors.Is(err, failure)).To(BeTrue())
		assert.Expect(err).To(MatchError("failed to delete image content source policy configuration configmap: error deleting *v1alpha1.ImageContentSourcePolicy: delete failure"))
		idms := globalconfig.ImageDigestMirrorSet()
		assert.Expect(apierrors.IsNotFound(guest.Get(t.Context(), client.ObjectKeyFromObject(idms), idms))).To(BeTrue())
		last := globalconfig.APIServerConfiguration()
		assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(last), last)).To(Succeed())
	})
	t.Run("When network validation fails, it should still persist the network and continue global policy", func(t *testing.T) {
		assert := NewWithT(t)
		hcp := configurationHCP()
		hcp.Spec.Networking.ClusterNetwork = []hyperv1.ClusterNetworkEntry{{}}
		guest, controlPlane := configurationClients(globalconfig.ProxyConfig())
		root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
		assert.Expect(ReconcileGlobal(t.Context(), root.hosted(), root.controlPlane(), globalParams(hcp))).To(MatchError(ContainSubstring("failed to reconcile network config")))
		for _, object := range []client.Object{globalconfig.NetworkConfig(), globalconfig.APIServerConfiguration()} {
			assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(object), object)).To(Succeed())
		}
	})
	t.Run("When global configuration changes, it should preserve platform fields and input ownership", func(t *testing.T) {
		platforms := []hyperv1.PlatformSpec{
			{Type: hyperv1.NonePlatform},
			{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{Region: "us-east-1", EndpointAccess: hyperv1.Private}},
			{Type: hyperv1.AzurePlatform, Azure: &hyperv1.AzurePlatformSpec{ResourceGroupName: "tenant-rg"}},
			{Type: hyperv1.GCPPlatform, GCP: &hyperv1.GCPPlatformSpec{Region: "us-central1", Project: "tenant-project"}},
			{Type: hyperv1.PowerVSPlatform, PowerVS: &hyperv1.PowerVSPlatformSpec{Region: "dal", Zone: "dal10"}},
			{Type: hyperv1.OpenStackPlatform},
		}
		for _, platform := range platforms {
			t.Run(fmt.Sprintf("When platform is %s, it should reconcile its global policy", platform.Type), func(t *testing.T) {
				assert := NewWithT(t)
				hcp := configurationHCP()
				hcp.Spec.Platform = platform
				guest, controlPlane := configurationClients(globalconfig.ProxyConfig())
				root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
				for _, infraID := range []string{"original", "updated", "updated"} {
					hcp.Spec.InfraID = infraID
					original := hcp.DeepCopy()
					assert.Expect(ReconcileGlobal(t.Context(), root.hosted(), root.controlPlane(), globalParams(hcp))).To(Succeed())
					assert.Expect(hcp).To(Equal(original))
					infra := globalconfig.InfrastructureConfig()
					assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(infra), infra)).To(Succeed())
					assert.Expect(infra.Status.InfrastructureName).To(Equal(infraID))
					assert.Expect(infra.Status.PlatformStatus.Type).To(Equal(configv1.PlatformType(platform.Type)))
					if platform.Type == hyperv1.AWSPlatform {
						assert.Expect(infra.Status.APIServerInternalURL).To(Equal("https://api.tenant.hypershift.local:6443"))
					}
					objects := []client.Object{globalconfig.DNSConfig(), globalconfig.ImageConfig(), globalconfig.IngressConfig(), globalconfig.NetworkConfig(), globalconfig.ProxyConfig(), globalconfig.ImageDigestMirrorSet(), manifests.CloudCredential(), globalconfig.AuthenticationConfiguration(), globalconfig.APIServerConfiguration()}
					for _, object := range objects {
						assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(object), object)).To(Succeed())
					}
					install := manifests.InstallConfigConfigMap()
					assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(install), install)).To(Succeed())
					assert.Expect(install.Data).To(Equal(map[string]string{"install-config": globalconfig.NewInstallConfig(original).String()}))
				}
			})
		}
	})
	t.Run("When the endpoint is absent, it should fail before any global write", func(t *testing.T) {
		assert := NewWithT(t)
		hcp := configurationHCP()
		hcp.Status.ControlPlaneEndpoint.Host = ""
		root := &configurationTestClients{}
		assert.Expect(ReconcileGlobal(t.Context(), root.hosted(), root.controlPlane(), globalParams(hcp))).To(MatchError("hosted control plane does not have an APIServer endpoint address"))
	})
	t.Run("When multiple global writes fail, it should aggregate in order and attempt later resources", func(t *testing.T) {
		assert := NewWithT(t)
		guest, controlPlane := configurationClients(globalconfig.ProxyConfig())
		failure := errors.New("injected failure")
		var attempts []string
		root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: configurationUpserter{upsert.CreateOrUpdateFN(func(ctx context.Context, target client.Client, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
			attempts = append(attempts, fmt.Sprintf("%T", object))
			switch object.(type) {
			case *configv1.Infrastructure, *configv1.DNS:
				return controllerutil.OperationResultNone, failure
			}
			return controllerutil.CreateOrUpdate(ctx, target, object, mutate)
		})}}
		err := ReconcileGlobal(t.Context(), root.hosted(), root.controlPlane(), globalParams(configurationHCP()))
		assert.Expect(errors.Is(err, failure)).To(BeTrue())
		assert.Expect(err).To(MatchError("[failed to reconcile infrastructure config spec: injected failure, failed to reconcile dns config: injected failure]"))
		assert.Expect(attempts).To(Equal([]string{"*v1.Infrastructure", "*v1.DNS", "*v1.Image", "*v1.Ingress", "*v1.Network", "*v1.Proxy", "*v1.ImageDigestMirrorSet", "*v1.ConfigMap", "*v1.CloudCredential", "*v1.Authentication", "*v1.APIServer"}))
	})
	t.Run("When Infrastructure status is unchanged, it should not write status again", func(t *testing.T) {
		assert := NewWithT(t)
		writes := 0
		guest := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(globalconfig.ProxyConfig()).WithStatusSubresource(&configv1.Infrastructure{}).WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, target client.Client, name string, object client.Object, opts ...client.SubResourceUpdateOption) error {
				writes++
				return target.SubResource(name).Update(ctx, object, opts...)
			},
		}).Build()
		_, controlPlane := configurationClients()
		root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
		assert.Expect(ReconcileGlobal(t.Context(), root.hosted(), root.controlPlane(), globalParams(configurationHCP()))).To(Succeed())
		assert.Expect(ReconcileGlobal(t.Context(), root.hosted(), root.controlPlane(), globalParams(configurationHCP()))).To(Succeed())
		assert.Expect(writes).To(Equal(1))
	})
	t.Run("When guest Proxy is missing, it should report the read error but create later resources", func(t *testing.T) {
		assert := NewWithT(t)
		guest, controlPlane := configurationClients()
		root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
		assert.Expect(ReconcileGlobal(t.Context(), root.hosted(), root.controlPlane(), globalParams(configurationHCP()))).To(MatchError(ContainSubstring("failed to reconcile proxy TrustedCA configmap")))
		proxy := globalconfig.ProxyConfig()
		assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(proxy), proxy)).To(Succeed())
		assert.Expect(ReconcileGlobal(t.Context(), root.hosted(), root.controlPlane(), globalParams(configurationHCP()))).To(Succeed())
	})

	t.Run("When configuration is populated and updated, it should reconcile owned fields without changing inputs or unowned fields", func(t *testing.T) {
		assert := NewWithT(t)
		hcp := populatedConfigurationHCP(t)
		guest := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(globalconfig.ProxyConfig()).WithStatusSubresource(&configv1.Infrastructure{}).Build()
		controlPlane := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "custom-ca", Namespace: hcp.Namespace}, Data: map[string]string{"ca-bundle.crt": "fixture"}}).Build()
		root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
		for iteration := 0; iteration < 3; iteration++ {
			if iteration == 1 {
				hcp.Spec.DNS.BaseDomainPrefix = ptr.To("updated")
				hcp.Spec.DNS.PublicZoneID = "updated-public"
				hcp.Spec.DNS.PrivateZoneID = "updated-private"
				hcp.Spec.Configuration.Image.ExternalRegistryHostnames = []string{"updated.example.com"}
				hcp.Spec.Configuration.Ingress.Domain = "updated-apps.example.com"
				hcp.Spec.Configuration.Proxy.NoProxy = "updated.example.com"
				hcp.Spec.Configuration.Network.ServiceNodePortRange = "32000-32700"
				hcp.Spec.IssuerURL = "https://updated-issuer.example.com"
				hcp.Spec.Configuration.Authentication.Type = configv1.AuthenticationTypeNone
				hcp.Spec.Configuration.APIServer.Audit.Profile = configv1.DefaultAuditProfileType
			}
			original := hcp.DeepCopy()
			assert.Expect(ReconcileGlobal(t.Context(), root.hosted(), root.controlPlane(), globalParams(hcp))).To(Succeed())
			assert.Expect(hcp).To(Equal(original))
			dns, image, ingress := globalconfig.DNSConfig(), globalconfig.ImageConfig(), globalconfig.IngressConfig()
			network, proxy := globalconfig.NetworkConfig(), globalconfig.ProxyConfig()
			authentication, apiServer := globalconfig.AuthenticationConfiguration(), globalconfig.APIServerConfiguration()
			objects := []client.Object{dns, image, ingress, network, proxy, authentication, apiServer}
			for _, object := range objects {
				assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(object), object)).To(Succeed())
				if iteration > 0 {
					assert.Expect(object.GetAnnotations()["user.example.com/keep"]).To(Equal("preserved"))
				}
			}
			assert.Expect(dns.Spec.BaseDomain).To(Equal(*hcp.Spec.DNS.BaseDomainPrefix + ".example.com"))
			assert.Expect(dns.Spec.PublicZone).To(Equal(&configv1.DNSZone{ID: hcp.Spec.DNS.PublicZoneID}))
			assert.Expect(dns.Spec.PrivateZone).To(Equal(&configv1.DNSZone{ID: hcp.Spec.DNS.PrivateZoneID}))
			assert.Expect(image.Spec).To(Equal(*hcp.Spec.Configuration.Image))
			assert.Expect(ingress.Spec).To(Equal(*hcp.Spec.Configuration.Ingress))
			assert.Expect(network.Spec.ClusterNetwork).To(Equal([]configv1.ClusterNetworkEntry{{CIDR: "10.132.0.0/14", HostPrefix: 24}}))
			assert.Expect(network.Spec.ServiceNetwork).To(Equal([]string{"172.31.0.0/16"}))
			assert.Expect(network.Spec.NetworkType).To(Equal("OVNKubernetes"))
			assert.Expect(network.Spec.ExternalIP).To(Equal(hcp.Spec.Configuration.Network.ExternalIP))
			assert.Expect(network.Spec.ServiceNodePortRange).To(Equal(hcp.Spec.Configuration.Network.ServiceNodePortRange))
			assert.Expect(proxy.Spec).To(Equal(*hcp.Spec.Configuration.Proxy))
			assert.Expect(proxy.Annotations["hypershift.io/hosted-cluster-proxy-config"]).To(Equal("true"))
			expectedAuthentication := *hcp.Spec.Configuration.Authentication
			expectedAuthentication.ServiceAccountIssuer = hcp.Spec.IssuerURL
			assert.Expect(authentication.Spec).To(Equal(expectedAuthentication))
			assert.Expect(apiServer.Spec).To(Equal(*hcp.Spec.Configuration.APIServer))
			if iteration == 0 {
				for _, object := range objects {
					annotations := object.GetAnnotations()
					if annotations == nil {
						annotations = map[string]string{}
					}
					annotations["user.example.com/keep"] = "preserved"
					object.SetAnnotations(annotations)
					assert.Expect(guest.Update(t.Context(), object)).To(Succeed())
				}
			} else {
				for _, object := range objects {
					before := object.DeepCopyObject()
					assert.Expect(ReconcileGlobal(t.Context(), root.hosted(), root.controlPlane(), globalParams(hcp))).To(Succeed())
					assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(object), object)).To(Succeed())
					assert.Expect(object).To(Equal(before))
				}
			}
		}
	})
	for _, testCase := range []struct {
		name     string
		platform hyperv1.PlatformSpec
		legacy   bool
		private  bool
	}{
		{name: "When AWS uses SharedVPC, it should project the DNS role and filter reserved tags", private: true, platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{Region: "us-east-1", EndpointAccess: hyperv1.Private, SharedVPC: &hyperv1.AWSSharedVPC{RolesRef: hyperv1.AWSSharedVPCRolesRef{IngressARN: "arn:aws:iam::123456789012:role/ingress"}}, ResourceTags: []hyperv1.AWSClusterResourceTag{{Key: "team", Value: "control-plane"}, {Key: "kubernetes.io/cluster/tenant", Value: "owned"}}}}},
		{name: "When Azure uses private topology, it should project cloud fields and the private endpoint", private: true, platform: hyperv1.PlatformSpec{Type: hyperv1.AzurePlatform, Azure: &hyperv1.AzurePlatformSpec{Topology: hyperv1.AzureTopologyPrivate, Cloud: "AzureUSGovernmentCloud", ResourceGroupName: "tenant-rg"}}},
		{name: "When Azure uses Swift fields, it should select the private endpoint without annotations", private: true, platform: hyperv1.PlatformSpec{Type: hyperv1.AzurePlatform, Azure: &hyperv1.AzurePlatformSpec{Private: hyperv1.AzurePrivateSpec{Type: hyperv1.AzurePrivateTypeSwift, Swift: hyperv1.AzureSwiftSpec{PodNetworkInstance: "swift-instance"}}, ResourceGroupName: "tenant-rg"}}},
		{name: "When managed Azure uses the legacy Swift annotation, it should retain the private endpoint", private: true, legacy: true, platform: hyperv1.PlatformSpec{Type: hyperv1.AzurePlatform, Azure: &hyperv1.AzurePlatformSpec{ResourceGroupName: "tenant-rg"}}},
		{name: "When GCP uses private access, it should project the endpoint and resource labels", private: true, platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform, GCP: &hyperv1.GCPPlatformSpec{Project: "tenant-project", Region: "us-central1", EndpointAccess: hyperv1.GCPEndpointAccessPrivate, ResourceLabels: []hyperv1.GCPResourceLabel{{Key: "team", Value: ptr.To("control-plane")}, {Key: "empty"}, {Key: "kubernetes-io-cluster", Value: ptr.To("reserved")}}}}},
		{name: "When PowerVS is configured, it should project location and resource fields", platform: hyperv1.PlatformSpec{Type: hyperv1.PowerVSPlatform, PowerVS: &hyperv1.PowerVSPlatformSpec{Region: "dal", Zone: "dal10", CISInstanceCRN: "crn:cis:tenant", ResourceGroup: "tenant-rg"}}},
		{name: "When OpenStack is configured, it should project cloud config and user managed load balancing", platform: hyperv1.PlatformSpec{Type: hyperv1.OpenStackPlatform, OpenStack: &hyperv1.OpenStackPlatformSpec{}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			assert := NewWithT(t)
			t.Setenv("MANAGED_SERVICE", "")
			hcp := populatedConfigurationHCP(t)
			hcp.Spec.Platform = testCase.platform
			if testCase.legacy {
				t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
				hcp.Annotations = map[string]string{hyperv1.SwiftPodNetworkInstanceAnnotation: "legacy-swift"}
			}
			guest := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(globalconfig.ProxyConfig()).WithStatusSubresource(&configv1.Infrastructure{}).Build()
			controlPlane := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "custom-ca", Namespace: hcp.Namespace}}).Build()
			root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: &simpleCreateOrUpdater{}}
			for _, infraID := range []string{"tenant-infra", "updated-infra", "updated-infra"} {
				hcp.Spec.InfraID = infraID
				original := hcp.DeepCopy()
				assert.Expect(ReconcileGlobal(t.Context(), root.hosted(), root.controlPlane(), globalParams(hcp))).To(Succeed())
				assert.Expect(hcp).To(Equal(original))
				infra := globalconfig.InfrastructureConfig()
				assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(infra), infra)).To(Succeed())
				assert.Expect(infra.Status.InfrastructureName).To(Equal(infraID))
				assert.Expect(infra.Status.APIServerURL).To(Equal("https://api.custom.example.com:6443"))
				internalURL := "https://api.example.com:6443"
				if testCase.private {
					internalURL = "https://api.tenant.hypershift.local:6443"
				}
				assert.Expect(infra.Status.APIServerInternalURL).To(Equal(internalURL))
				assert.Expect(infra.Status.InfrastructureTopology).To(Equal(configv1.HighlyAvailableTopologyMode))
				assert.Expect(infra.Status.ControlPlaneTopology).To(Equal(configv1.ExternalTopologyMode))
				assert.Expect(infra.Status.EtcdDiscoveryDomain).To(Equal("custom.example.com"))
				assert.Expect(infra.Spec.PlatformSpec.Type).To(Equal(configv1.PlatformType(testCase.platform.Type)))
				switch testCase.platform.Type {
				case hyperv1.AWSPlatform:
					assert.Expect(infra.Status.PlatformStatus.AWS).To(Equal(&configv1.AWSPlatformStatus{Region: "us-east-1", ResourceTags: []configv1.AWSResourceTag{{Key: "team", Value: "control-plane"}}}))
					dns := globalconfig.DNSConfig()
					assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(dns), dns)).To(Succeed())
					assert.Expect(dns.Spec.Platform.AWS.PrivateZoneIAMRole).To(Equal("arn:aws:iam::123456789012:role/ingress"))
				case hyperv1.AzurePlatform:
					cloud := configv1.AzurePublicCloud
					if testCase.platform.Azure.Cloud != "" {
						cloud = configv1.AzureCloudEnvironment(testCase.platform.Azure.Cloud)
					}
					assert.Expect(infra.Status.PlatformStatus.Azure).To(Equal(&configv1.AzurePlatformStatus{CloudName: cloud, ResourceGroupName: "tenant-rg"}))
					assert.Expect(infra.Spec.CloudConfig.Name).To(Equal("cloud.conf"))
				case hyperv1.GCPPlatform:
					assert.Expect(infra.Status.PlatformStatus.GCP).To(Equal(&configv1.GCPPlatformStatus{ProjectID: "tenant-project", Region: "us-central1", ResourceLabels: []configv1.GCPResourceLabel{{Key: "team", Value: "control-plane"}, {Key: "empty", Value: ""}}}))
				case hyperv1.PowerVSPlatform:
					assert.Expect(infra.Status.PlatformStatus.PowerVS).To(Equal(&configv1.PowerVSPlatformStatus{Region: "dal", Zone: "dal10", CISInstanceCRN: "crn:cis:tenant", ResourceGroup: "tenant-rg"}))
				case hyperv1.OpenStackPlatform:
					assert.Expect(infra.Spec.CloudConfig).To(Equal(configv1.ConfigMapFileReference{Name: "cloud-provider-config", Key: "cloud.conf"}))
					assert.Expect(infra.Status.PlatformStatus.OpenStack).To(Equal(&configv1.OpenStackPlatformStatus{CloudName: "openstack", LoadBalancer: &configv1.OpenStackPlatformLoadBalancer{Type: configv1.LoadBalancerTypeUserManaged}, APIServerInternalIPs: []string{}, IngressIPs: []string{}}))
				}
			}
		})
	}
	for _, testCase := range []struct {
		name        string
		specFailure bool
	}{
		{name: "When Infrastructure status fails, it should aggregate errors in order and attempt later resources", specFailure: false},
		{name: "When Infrastructure spec fails, it should suppress status and aggregate errors in order", specFailure: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			specFailure := testCase.specFailure
			assert := NewWithT(t)
			infraFailure, laterFailure := errors.New("infra failure"), errors.New("later failure")
			statusWrites := 0
			guest := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(globalconfig.ProxyConfig()).WithStatusSubresource(&configv1.Infrastructure{}).WithInterceptorFuncs(interceptor.Funcs{SubResourceUpdate: func(context.Context, client.Client, string, client.Object, ...client.SubResourceUpdateOption) error {
				statusWrites++
				return infraFailure
			}}).Build()
			var attempts []string
			root := &configurationTestClients{client: guest, cpClient: fake.NewClientBuilder().WithScheme(api.Scheme).Build(), CreateOrUpdateProvider: configurationUpserter{func(ctx context.Context, target client.Client, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				attempts = append(attempts, fmt.Sprintf("%T", object))
				if _, ok := object.(*configv1.Infrastructure); ok && specFailure {
					return controllerutil.OperationResultNone, infraFailure
				}
				if _, ok := object.(*configv1.DNS); ok {
					return controllerutil.OperationResultNone, laterFailure
				}
				return controllerutil.CreateOrUpdate(ctx, target, object, mutate)
			}}}
			hcp := populatedConfigurationHCP(t)
			hcp.Spec.Configuration.Proxy.TrustedCA.Name = ""
			err := ReconcileGlobal(t.Context(), root.hosted(), root.controlPlane(), globalParams(hcp))
			assert.Expect(errors.Is(err, infraFailure)).To(BeTrue())
			assert.Expect(errors.Is(err, laterFailure)).To(BeTrue())
			wrapper, expectedWrites := "failed to update infrastructure status", 1
			if specFailure {
				wrapper, expectedWrites = "failed to reconcile infrastructure config spec", 0
			}
			assert.Expect(err).To(MatchError("[" + wrapper + ": infra failure, failed to reconcile dns config: later failure]"))
			assert.Expect(statusWrites).To(Equal(expectedWrites))
			assert.Expect(attempts).To(Equal([]string{"*v1.Infrastructure", "*v1.DNS", "*v1.Image", "*v1.Ingress", "*v1.Network", "*v1.Proxy", "*v1.ImageDigestMirrorSet", "*v1.ConfigMap", "*v1.CloudCredential", "*v1.Authentication", "*v1.APIServer"}))
		})
	}
}
