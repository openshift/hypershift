package configuration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/support/globalconfig"

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

func reviewConfiguredHCP(t *testing.T) *hyperv1.HostedControlPlane {
	t.Helper()
	hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Name: "tenant", Namespace: "control-plane"}}
	NewWithT(t).Expect(json.Unmarshal([]byte(`{
		"infraID":"tenant-infra","platform":{"type":"None"},
		"dns":{"baseDomain":"example.com","baseDomainPrefix":"custom","publicZoneID":"public-zone","privateZoneID":"private-zone"},
		"networking":{"networkType":"OVNKubernetes","clusterNetwork":[{"cidr":"10.132.0.0/14","hostPrefix":24}],"serviceNetwork":[{"cidr":"172.31.0.0/16"}]},
		"infrastructureAvailabilityPolicy":"HighlyAvailable","kubeAPIServerDNSName":"api.custom.example.com","issuerURL":"https://issuer.example.com",
		"configuration":{
			"image":{"externalRegistryHostnames":["registry.example.com"]},
			"ingress":{"domain":"apps.custom.example.com","appsDomain":"custom-apps.example.com"},
			"network":{"serviceNodePortRange":"31000-32000","externalIP":{"policy":{"allowedCIDRs":["192.0.2.0/24"]}}},
			"proxy":{"httpProxy":"http://proxy.example.com:8080","httpsProxy":"http://proxy.example.com:8443","noProxy":".example.com","trustedCA":{"name":"custom-ca"}},
			"authentication":{"type":"IntegratedOAuth","serviceAccountIssuer":"https://ignored.example.com"},
			"apiServer":{"audit":{"profile":"WriteRequestBodies"}}
		},
		"imageContentSources":[{"source":"quay.io/openshift-release-dev/ocp-release","mirrors":["mirror.example.com/release"]}]
	}`), &hcp.Spec)).To(Succeed())
	hcp.Status.ControlPlaneEndpoint = hyperv1.APIEndpoint{Host: "api.example.com", Port: 6443}
	return hcp
}

func TestReconcileGlobalReviewFindings(t *testing.T) {
	t.Run("When configuration is populated and updated, it should reconcile owned fields without changing inputs or unowned fields", func(t *testing.T) {
		assert := NewWithT(t)
		hcp := reviewConfiguredHCP(t)
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
			hcp := reviewConfiguredHCP(t)
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
	t.Run("When Infrastructure writes fail, it should aggregate in order and suppress status after spec failure", func(t *testing.T) {
		for _, specFailure := range []bool{false, true} {
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
			hcp := reviewConfiguredHCP(t)
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
		}
	})
}

func TestReconcileImagePolicyReviewFindings(t *testing.T) {
	t.Run("When ICSP and stale IDMS exist, it should delete ICSP before replacing mirrors and remain idempotent", func(t *testing.T) {
		assert := NewWithT(t)
		hcp := reviewConfiguredHCP(t)
		idms := globalconfig.ImageDigestMirrorSet()
		idms.Spec.ImageDigestMirrors = []configv1.ImageDigestMirrors{{Source: "stale.example.com", Mirrors: []configv1.ImageMirror{"stale-mirror.example.com"}}}
		var operations []string
		guest := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(globalconfig.ImageContentSourcePolicy(), idms).WithInterceptorFuncs(interceptor.Funcs{Delete: func(ctx context.Context, target client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
			operations = append(operations, fmt.Sprintf("delete %T %s/%s", object, object.GetNamespace(), object.GetName()))
			return target.Delete(ctx, object, opts...)
		}}).Build()
		root := &configurationTestClients{client: guest, CreateOrUpdateProvider: configurationUpserter{func(ctx context.Context, target client.Client, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
			operations = append(operations, fmt.Sprintf("upsert %T %s/%s", object, object.GetNamespace(), object.GetName()))
			assert.Expect(apierrors.IsNotFound(guest.Get(ctx, client.ObjectKeyFromObject(globalconfig.ImageContentSourcePolicy()), globalconfig.ImageContentSourcePolicy()))).To(BeTrue())
			return controllerutil.CreateOrUpdate(ctx, target, object, mutate)
		}}}
		assert.Expect(ReconcileImagePolicy(t.Context(), root.hosted(), hcp.Spec.ImageContentSources)).To(Succeed())
		assert.Expect(operations).To(Equal([]string{fmt.Sprintf("delete %T /%s", globalconfig.ImageContentSourcePolicy(), globalconfig.ImageContentSourcePolicy().Name), fmt.Sprintf("upsert %T /%s", idms, idms.Name)}))
		assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(idms), idms)).To(Succeed())
		assert.Expect(idms.Spec.ImageDigestMirrors).To(Equal([]configv1.ImageDigestMirrors{{Source: "quay.io/openshift-release-dev/ocp-release", Mirrors: []configv1.ImageMirror{"mirror.example.com/release"}}}))
		assert.Expect(idms.Labels["machineconfiguration.openshift.io/role"]).To(Equal("worker"))
		before := idms.DeepCopy()
		operations = nil
		assert.Expect(ReconcileImagePolicy(t.Context(), root.hosted(), hcp.Spec.ImageContentSources)).To(Succeed())
		assert.Expect(operations).To(Equal([]string{fmt.Sprintf("upsert %T /%s", idms, idms.Name)}))
		assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(idms), idms)).To(Succeed())
		assert.Expect(idms).To(Equal(before))
	})
}

func TestReconcileProxyCABundleReviewFindings(t *testing.T) {
	for _, mode := range []string{"read", "missing", "upsert", "delete"} {
		t.Run("When user CA "+mode+" fails, it should preserve destination data and propagate the existing error", func(t *testing.T) {
			assert := NewWithT(t)
			failure := errors.New(mode + " failure")
			hcp := reviewConfiguredHCP(t)
			destination := manifests.OpenShiftUserCABundle()
			destination.Data = map[string]string{"ca-bundle.crt": "preserved"}
			upserts, deletes := 0, 0
			guest := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(destination).WithInterceptorFuncs(interceptor.Funcs{Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
				deletes++
				return failure
			}}).Build()
			controlPlane := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "custom-ca", Namespace: hcp.Namespace}, Data: map[string]string{"ca-bundle.crt": "replacement"}}).WithInterceptorFuncs(interceptor.Funcs{Get: func(ctx context.Context, target client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption) error {
				if mode == "read" {
					return failure
				}
				return target.Get(ctx, key, object, opts...)
			}}).Build()
			if mode == "missing" {
				hcp.Spec.Configuration.Proxy.TrustedCA.Name = "missing"
			}
			if mode == "delete" {
				hcp.Spec.Configuration = nil
			}
			root := &configurationTestClients{client: guest, cpClient: controlPlane, CreateOrUpdateProvider: configurationUpserter{func(context.Context, client.Client, client.Object, controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				upserts++
				return controllerutil.OperationResultNone, failure
			}}}
			err := ReconcileProxyCABundle(t.Context(), root.hosted(), root.controlPlane(), ProxyParamsFor(hcp.Namespace, hcp.Spec.Configuration))
			expectedUpserts, expectedDeletes := 0, 0
			switch mode {
			case "read":
				assert.Expect(err).To(MatchError("cannot get proxy CA bundle ConfigMap: read failure"))
			case "missing":
				assert.Expect(apierrors.IsNotFound(err)).To(BeTrue())
				assert.Expect(err).To(MatchError("cannot get proxy CA bundle ConfigMap: configmaps \"missing\" not found"))
			case "upsert":
				expectedUpserts = 1
				assert.Expect(err).To(MatchError("failed to reconcile the proxy CA bundle ConfigMap: upsert failure"))
			case "delete":
				expectedDeletes = 1
				assert.Expect(err).To(MatchError("error deleting *v1.ConfigMap: delete failure"))
			}
			if mode != "missing" {
				assert.Expect(errors.Is(err, failure)).To(BeTrue())
			}
			assert.Expect(upserts).To(Equal(expectedUpserts))
			assert.Expect(deletes).To(Equal(expectedDeletes))
			actual := manifests.OpenShiftUserCABundle()
			assert.Expect(guest.Get(t.Context(), client.ObjectKeyFromObject(actual), actual)).To(Succeed())
			assert.Expect(actual.Data).To(Equal(destination.Data))
		})
	}
}
