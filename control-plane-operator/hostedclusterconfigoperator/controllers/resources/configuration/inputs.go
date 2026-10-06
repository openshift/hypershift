package configuration

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type GlobalConfigParams struct {
	Name                             string
	Namespace                        string
	SwiftPodNetworkInstance          string
	Endpoint                         hyperv1.APIEndpoint
	KubeAPIServerDNSName             string
	InfraID                          string
	InfrastructureAvailabilityPolicy hyperv1.AvailabilityPolicy
	DNS                              hyperv1.DNSSpec
	Platform                         hyperv1.PlatformSpec
	Networking                       hyperv1.ClusterNetworking
	Configuration                    *hyperv1.ClusterConfiguration
	IssuerURL                        string
	ImageContentSources              []hyperv1.ImageContentSource
}

func (params GlobalConfigParams) hostedControlPlane() *hyperv1.HostedControlPlane {
	return (&hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: params.Name, Namespace: params.Namespace, Annotations: map[string]string{hyperv1.SwiftPodNetworkInstanceAnnotation: params.SwiftPodNetworkInstance}},
		Spec: hyperv1.HostedControlPlaneSpec{
			KubeAPIServerDNSName: params.KubeAPIServerDNSName, InfraID: params.InfraID, InfrastructureAvailabilityPolicy: params.InfrastructureAvailabilityPolicy,
			DNS: params.DNS, Platform: params.Platform, Networking: params.Networking, Configuration: params.Configuration, IssuerURL: params.IssuerURL, ImageContentSources: params.ImageContentSources,
		}, Status: hyperv1.HostedControlPlaneStatus{ControlPlaneEndpoint: params.Endpoint},
	}).DeepCopy()
}

type ProxyParams struct {
	Namespace string
	TrustedCA string
}

func ProxyParamsFor(namespace string, configuration *hyperv1.ClusterConfiguration) ProxyParams {
	params := ProxyParams{Namespace: namespace}
	if configuration != nil && configuration.Proxy != nil {
		params.TrustedCA = configuration.Proxy.TrustedCA.Name
	}
	return params
}

type ObservedConfigParams struct {
	Namespace string
	OwnerRef  config.OwnerRef
}
