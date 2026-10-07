package configuration

import (
	"context"
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type configurationTestClients struct {
	client   client.Client
	cpClient client.Client
	upsert.CreateOrUpdateProvider
}

func (root configurationTestClients) hosted() HostedCluster {
	var createOrUpdate upsert.CreateOrUpdateFN
	if root.CreateOrUpdateProvider != nil {
		createOrUpdate = root.CreateOrUpdate
	}
	return NewHostedCluster(root.client, createOrUpdate)
}
func (root configurationTestClients) controlPlane() ControlPlane {
	var createOrUpdate upsert.CreateOrUpdateFN
	if root.CreateOrUpdateProvider != nil {
		createOrUpdate = root.CreateOrUpdate
	}
	return NewControlPlane(root.cpClient, createOrUpdate)
}

type configurationUpserter struct{ upsert.CreateOrUpdateFN }

func (provider configurationUpserter) CreateOrUpdate(ctx context.Context, target client.Client, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return provider.CreateOrUpdateFN(ctx, target, object, mutate)
}

type simpleCreateOrUpdater struct{}

func (*simpleCreateOrUpdater) CreateOrUpdate(ctx context.Context, target client.Client, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return controllerutil.CreateOrUpdate(ctx, target, object, mutate)
}

type errorCreateOrUpdater struct{ err error }

func (provider *errorCreateOrUpdater) CreateOrUpdate(context.Context, client.Client, client.Object, controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return controllerutil.OperationResultNone, provider.err
}
func globalParams(hcp *hyperv1.HostedControlPlane) GlobalConfigParams {
	return GlobalConfigParams{
		Name: hcp.Name, Namespace: hcp.Namespace, SwiftPodNetworkInstance: hcp.Annotations[hyperv1.SwiftPodNetworkInstanceAnnotation],
		Endpoint: hcp.Status.ControlPlaneEndpoint, KubeAPIServerDNSName: hcp.Spec.KubeAPIServerDNSName,
		InfraID: hcp.Spec.InfraID, InfrastructureAvailabilityPolicy: hcp.Spec.InfrastructureAvailabilityPolicy,
		DNS: hcp.Spec.DNS, Platform: hcp.Spec.Platform, Networking: hcp.Spec.Networking,
		Configuration: hcp.Spec.Configuration, IssuerURL: hcp.Spec.IssuerURL, ImageContentSources: hcp.Spec.ImageContentSources,
	}
}

func populatedConfigurationHCP(t *testing.T) *hyperv1.HostedControlPlane {
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
