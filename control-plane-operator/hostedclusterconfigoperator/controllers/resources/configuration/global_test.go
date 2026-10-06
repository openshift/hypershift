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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
}
