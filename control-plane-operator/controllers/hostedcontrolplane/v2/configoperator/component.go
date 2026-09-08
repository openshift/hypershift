package configoperator

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	awsutil "github.com/openshift/hypershift/support/awsutil"
	"github.com/openshift/hypershift/support/capabilities"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	ComponentName = "hosted-cluster-config-operator"
)

var _ component.ComponentOptions = &hcco{}

type hcco struct {
	registryOverrides               map[string]string
	openShiftImageRegistryOverrides map[string][]string
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (h *hcco) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (h *hcco) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (h *hcco) NeedsManagementKASAccess() bool {
	return true
}

func NewComponent(registryOverrides map[string]string, openShiftImageRegistryOverrides map[string][]string, caps *hyperv1.Capabilities) component.ControlPlaneComponent {
	hcco := &hcco{
		registryOverrides:               registryOverrides,
		openShiftImageRegistryOverrides: openShiftImageRegistryOverrides,
	}

	availabilityProberOpts := hccpAvailabilityProberOpts(caps)

	builder := component.NewDeploymentComponent(ComponentName, hcco).
		WithAdaptFunction(hcco.adaptDeployment).
		WithManifestAdapter(
			"podmonitor.yaml",
			component.WithAdaptFunction(adaptPodMonitor),
		).
		WithManifestAdapter(
			"role.yaml",
			component.WithAdaptFunction(adaptRole),
		).
		InjectAvailabilityProberContainer(availabilityProberOpts)
	for _, tokenMinterOpts := range tokenMinterContainerOptions(caps) {
		builder = builder.InjectTokenMinterContainer(tokenMinterOpts)
	}
	return builder.Build()
}

func tokenMinterContainerOptions(caps *hyperv1.Capabilities) []component.TokenMinterContainerOptions {
	options := []component.TokenMinterContainerOptions{
		{
			TokenType:               component.CloudToken,
			ServiceAccountName:      "kube-controller-manager",
			ServiceAccountNameSpace: "kube-system",
			NamePrefix:              "cloud-controller",
			TokenMountPath:          awsutil.CloudControllerTokenMountPath,
			PlatformTypes:           []hyperv1.PlatformType{hyperv1.AWSPlatform},
			KubeconfingVolumeName:   "kubeconfig",
		},
	}
	if capabilities.IsIngressCapabilityEnabled(caps) {
		options = append(options, component.TokenMinterContainerOptions{
			TokenType:               component.CloudToken,
			ServiceAccountName:      "ingress-operator",
			ServiceAccountNameSpace: "openshift-ingress-operator",
			NamePrefix:              "ingress",
			TokenMountPath:          awsutil.IngressTokenMountPath,
			PlatformTypes:           []hyperv1.PlatformType{hyperv1.AWSPlatform},
			KubeconfingVolumeName:   "kubeconfig",
		})
	}
	return options
}

func hccpAvailabilityProberOpts(caps *hyperv1.Capabilities) podspec.AvailabilityProberOpts {
	availabilityProberOpts := podspec.AvailabilityProberOpts{
		KubeconfigVolumeName: "kubeconfig",
		RequiredAPIs: []schema.GroupVersionKind{
			{Group: "imageregistry.operator.openshift.io", Version: "v1", Kind: "Config"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Infrastructure"},
			{Group: "config.openshift.io", Version: "v1", Kind: "DNS"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Ingress"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Network"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Proxy"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Build"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Image"},
			{Group: "config.openshift.io", Version: "v1", Kind: "Project"},
			{Group: "config.openshift.io", Version: "v1", Kind: "ClusterVersion"},
			{Group: "config.openshift.io", Version: "v1", Kind: "FeatureGate"},
			{Group: "config.openshift.io", Version: "v1", Kind: "ClusterOperator"},
			{Group: "config.openshift.io", Version: "v1", Kind: "OperatorHub"},
			{Group: "operator.openshift.io", Version: "v1", Kind: "Network"},
			{Group: "operator.openshift.io", Version: "v1", Kind: "CloudCredential"},
			{Group: "operator.openshift.io", Version: "v1", Kind: "Storage"},
			{Group: "operator.openshift.io", Version: "v1", Kind: "CSISnapshotController"},
			{Group: "operator.openshift.io", Version: "v1", Kind: "ClusterCSIDriver"},
		},
	}
	if capabilities.IsIngressCapabilityEnabled(caps) {
		availabilityProberOpts.RequiredAPIs = append(availabilityProberOpts.RequiredAPIs, schema.GroupVersionKind{Group: "operator.openshift.io", Version: "v1", Kind: "IngressController"})
	}
	return availabilityProberOpts
}
