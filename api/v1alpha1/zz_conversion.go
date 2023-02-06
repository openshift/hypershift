package v1alpha1

import (
	"bytes"
	"fmt"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/conversion"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/api/util/configrefs"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/json"
	"k8s.io/apimachinery/pkg/util/sets"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var (
	localScheme = runtime.NewScheme()
	serializer  = json.NewSerializerWithOptions(
		json.DefaultMetaFactory, localScheme, localScheme,
		json.SerializerOptions{Strict: false},
	)
	v1beta1GroupVersion = schema.GroupVersion{Group: GroupVersion.Group, Version: "v1beta1"}
)

func init() {
	v1beta1.AddToScheme(localScheme)
	configv1.AddToScheme(localScheme)
	clientgoscheme.AddToScheme(localScheme)
	AddToScheme(localScheme)
}

// HostedCluster conversion
func (h *HostedCluster) ConvertTo(rawDst conversion.Hub) error {
	temp := h.DeepCopy()
	if err := fixupHostedClusterBeforeConversion(temp); err != nil {
		return err
	}
	return serializationConvert(temp, rawDst)
}

func (h *HostedCluster) ConvertFrom(rawSrc conversion.Hub) error {
	err := serializationConvert(rawSrc, h)
	if err != nil {
		return err
	}
	return fixupHostedClusterAfterConversion(h)
}

func (n *NodePool) ConvertTo(rawDst conversion.Hub) error {
	if n.Spec.NodeCount != nil && n.Spec.Replicas == nil {
		n.Spec.Replicas = n.Spec.NodeCount
	}
	return serializationConvert(n, rawDst)
}

func (n *NodePool) ConvertFrom(rawSrc conversion.Hub) error {
	return serializationConvert(rawSrc, n)
}

func (e *AWSEndpointService) ConvertTo(rawDst conversion.Hub) error {
	return serializationConvert(e, rawDst)
}
func (e *AWSEndpointService) ConvertFrom(rawSrc conversion.Hub) error {
	return serializationConvert(rawSrc, e)
}

func (h *HostedControlPlane) ConvertTo(rawDst conversion.Hub) error {
	temp := h.DeepCopy()
	if err := fixupHostedControlPlaneBeforeConversion(temp); err != nil {
		return err
	}
	return serializationConvert(temp, rawDst)
}

func (h *HostedControlPlane) ConvertFrom(rawSrc conversion.Hub) error {
	err := serializationConvert(rawSrc, h)
	if err != nil {
		return err
	}
	return fixupHostedControlPlaneAfterConversion(h)
}

func serializationConvert(from runtime.Object, to runtime.Object) error {
	b := &bytes.Buffer{}
	from.GetObjectKind().SetGroupVersionKind(schema.GroupVersionKind{})
	if err := serializer.Encode(from, b); err != nil {
		return fmt.Errorf("cannot serialize %T: %w", from, err)
	}
	if _, _, err := serializer.Decode(b.Bytes(), nil, to); err != nil {
		return fmt.Errorf("cannot decode %T: %w", to, err)
	}
	gvks, _, err := localScheme.ObjectKinds(to)
	if err != nil || len(gvks) == 0 {
		return fmt.Errorf("cannot get gvk for %T: %w", to, err)
	}
	to.GetObjectKind().SetGroupVersionKind(gvks[0])
	return nil
}

func fixupHostedClusterBeforeConversion(hc *HostedCluster) error {
	if hc.Spec.Platform.AWS != nil {
		reconcileDeprecatedAWSRoles(hc.Spec.Platform.AWS)
	}
	if err := reconcileDeprecatedGlobalConfig(hc.Spec.Configuration); err != nil {
		return err
	}
	if err := reconcileDeprecatedNetworkSettings(&hc.Spec.Networking); err != nil {
		return err
	}
	if hc.Spec.SecretEncryption != nil && hc.Spec.SecretEncryption.KMS != nil &&
		hc.Spec.SecretEncryption.KMS.AWS != nil && hc.Spec.SecretEncryption.KMS.AWS.Auth.Credentials.Name != "" &&
		hc.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN == "" {
		hc.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN = convertSecretNameToARN(hc.Spec.SecretEncryption.KMS.AWS.Auth.Credentials.Name)
	}

	return nil
}

func fixupHostedClusterAfterConversion(hc *HostedCluster) error {
	if hc.Spec.SecretEncryption != nil && hc.Spec.SecretEncryption.KMS != nil &&
		hc.Spec.SecretEncryption.KMS.AWS != nil {
		hc.Spec.SecretEncryption.KMS.AWS.Auth.Credentials.Name = convertARNToSecretName(hc.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN)
	}

	return nil
}

func fixupHostedControlPlaneBeforeConversion(hcp *HostedControlPlane) error {
	if hcp.Spec.Platform.AWS != nil {
		reconcileDeprecatedAWSRoles(hcp.Spec.Platform.AWS)
	}
	if err := reconcileDeprecatedGlobalConfig(hcp.Spec.Configuration); err != nil {
		return err
	}
	reconcileDeprecatedHCPNetworkSettings(hcp)

	if hcp.Spec.SecretEncryption != nil && hcp.Spec.SecretEncryption.KMS != nil &&
		hcp.Spec.SecretEncryption.KMS.AWS != nil && hcp.Spec.SecretEncryption.KMS.AWS.Auth.Credentials.Name != "" {
		hcp.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN = convertSecretNameToARN(hcp.Spec.SecretEncryption.KMS.AWS.Auth.Credentials.Name)
	}

	if err := reconcileDeprecatedNetworkSettings(&hcp.Spec.Networking); err != nil {
		return err
	}
	return nil
}

func fixupHostedControlPlaneAfterConversion(hcp *HostedControlPlane) error {
	populateDeprecatedNetworkingFields(&hcp.Spec.Networking)
	populateDeprecatedHCPNetworkingFields(hcp)
	if hcp.Spec.Platform.AWS != nil {
		populateDeprecatedAWSRoles(hcp.Spec.Platform.AWS)
	}

	if hcp.Spec.SecretEncryption != nil && hcp.Spec.SecretEncryption.KMS != nil &&
		hcp.Spec.SecretEncryption.KMS.AWS != nil && hcp.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN != "" {
		hcp.Spec.SecretEncryption.KMS.AWS.Auth.Credentials.Name = convertARNToSecretName(hcp.Spec.SecretEncryption.KMS.AWS.Auth.AWSKMSRoleARN)
	}

	return populateDeprecatedGlobalConfig(hcp.Spec.Configuration)
}

func populateDeprecatedAWSRoles(aws *AWSPlatformSpec) {
	aws.KubeCloudControllerCreds.Name = convertARNToSecretName(aws.RolesRef.KubeCloudControllerARN)
	aws.NodePoolManagementCreds.Name = convertARNToSecretName(aws.RolesRef.NodePoolManagementARN)
	aws.ControlPlaneOperatorCreds.Name = convertARNToSecretName(aws.RolesRef.ControlPlaneOperatorARN)
	aws.Roles = []AWSRoleCredentials{
		{
			ARN:       aws.RolesRef.NetworkARN,
			Namespace: "openshift-cloud-network-config-controller",
			Name:      "cloud-credentials",
		},
		{
			ARN:       aws.RolesRef.StorageARN,
			Namespace: "openshift-cluster-csi-drivers",
			Name:      "ebs-cloud-credentials",
		},
		{
			ARN:       aws.RolesRef.ImageRegistryARN,
			Namespace: "openshift-image-registry",
			Name:      "installer-cloud-credentials",
		},
		{
			ARN:       aws.RolesRef.IngressARN,
			Namespace: "openshift-ingress-operator",
			Name:      "cloud-credentials",
		},
	}
}

func reconcileDeprecatedAWSRoles(aws *AWSPlatformSpec) {
	// Migrate ARNs from slice into typed fields.
	for _, v := range aws.Roles {
		switch v.Namespace {
		case "openshift-image-registry":
			aws.RolesRef.ImageRegistryARN = v.ARN
		case "openshift-ingress-operator":
			aws.RolesRef.IngressARN = v.ARN
		case "openshift-cloud-network-config-controller":
			aws.RolesRef.NetworkARN = v.ARN
		case "openshift-cluster-csi-drivers":
			aws.RolesRef.StorageARN = v.ARN
		}
	}

	// For arns stored in secrets, delay the retrieval of the secret to the HostedCluster controller by setting a
	// placeholder ARN here that tells the controller to do the lookup before reconciling the HostedCluster.

	// Migrate ARNs from secrets into typed fields.
	if aws.NodePoolManagementCreds.Name != "" && aws.RolesRef.NodePoolManagementARN == "" {
		aws.RolesRef.NodePoolManagementARN = convertSecretNameToARN(aws.NodePoolManagementCreds.Name)
	}

	if aws.ControlPlaneOperatorCreds.Name != "" && aws.RolesRef.ControlPlaneOperatorARN == "" {
		aws.RolesRef.ControlPlaneOperatorARN = convertSecretNameToARN(aws.ControlPlaneOperatorCreds.Name)
	}

	if aws.KubeCloudControllerCreds.Name != "" && aws.RolesRef.KubeCloudControllerARN == "" {
		aws.RolesRef.KubeCloudControllerARN = convertSecretNameToARN(aws.KubeCloudControllerCreds.Name)
	}
}

func convertSecretNameToARN(name string) string {
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "arn::") {
		return strings.TrimPrefix(name, "arn::")
	}
	return fmt.Sprintf("arn-from-secret::%s", name)
}

func convertARNToSecretName(arn string) string {
	if strings.HasPrefix(arn, "arn-from-secret::") {
		return strings.TrimPrefix(arn, "arn-from-secret::")
	}
	return ""
}

// reconcileDeprecatedGlobalConfig converts previously specified configuration in RawExtension format to
func reconcileDeprecatedGlobalConfig(config *ClusterConfiguration) error {

	// Skip if no deprecated configuration is set
	if config == nil || len(config.Items) == 0 {
		return nil
	}

	gconfig, err := ParseGlobalConfig(config)
	if err != nil {
		// This should never happen because at this point, the global configuration
		// should be valid
		return err
	}

	// Copy over config from the raw extension
	if gconfig.APIServer != nil {
		config.APIServer = &gconfig.APIServer.Spec
	}
	if gconfig.Authentication != nil {
		config.Authentication = &gconfig.Authentication.Spec
	}
	if gconfig.FeatureGate != nil {
		config.FeatureGate = &gconfig.FeatureGate.Spec
	}
	if gconfig.Image != nil {
		config.Image = &gconfig.Image.Spec
	}
	if gconfig.Ingress != nil {
		config.Ingress = &gconfig.Ingress.Spec
	}
	if gconfig.Network != nil {
		config.Network = &gconfig.Network.Spec
	}
	if gconfig.OAuth != nil {
		config.OAuth = &gconfig.OAuth.Spec
	}
	if gconfig.Scheduler != nil {
		config.Scheduler = &gconfig.Scheduler.Spec
	}
	if gconfig.Proxy != nil {
		config.Proxy = &gconfig.Proxy.Spec
	}

	return nil
}

type globalConfig struct {
	APIServer      *configv1.APIServer
	Authentication *configv1.Authentication
	FeatureGate    *configv1.FeatureGate
	Image          *configv1.Image
	Ingress        *configv1.Ingress
	Network        *configv1.Network
	OAuth          *configv1.OAuth
	Scheduler      *configv1.Scheduler
	Proxy          *configv1.Proxy
	Build          *configv1.Build
	Project        *configv1.Project
}

func ParseGlobalConfig(cfg *ClusterConfiguration) (globalConfig, error) {
	result := globalConfig{}
	if cfg == nil {
		return result, nil
	}
	kinds := sets.NewString() // keeps track of which kinds have been found
	for i, cfg := range cfg.Items {
		cfgObject, gvk, err := serializer.Decode(cfg.Raw, nil, nil)
		if err != nil {
			return result, fmt.Errorf("cannot parse configuration at index %d: %w", i, err)
		}
		if gvk.GroupVersion().String() != configv1.GroupVersion.String() {
			return result, fmt.Errorf("invalid resource type found in configuration: kind: %s, apiVersion: %s", gvk.Kind, gvk.GroupVersion().String())
		}
		if kinds.Has(gvk.Kind) {
			return result, fmt.Errorf("duplicate config type found: %s", gvk.Kind)
		}
		kinds.Insert(gvk.Kind)
		switch obj := cfgObject.(type) {
		case *configv1.APIServer:
			if obj.Spec.Audit.Profile == "" {
				// Populate kubebuilder default for comparison
				// https://github.com/openshift/api/blob/f120778bee805ad1a7a4f05a6430332cf5811813/config/v1/types_apiserver.go#L57
				obj.Spec.Audit.Profile = configv1.DefaultAuditProfileType
			}
			result.APIServer = obj
		case *configv1.Authentication:
			result.Authentication = obj
		case *configv1.FeatureGate:
			result.FeatureGate = obj
		case *configv1.Ingress:
			result.Ingress = obj
		case *configv1.Network:
			result.Network = obj
		case *configv1.OAuth:
			result.OAuth = obj
		case *configv1.Scheduler:
			result.Scheduler = obj
		case *configv1.Proxy:
			result.Proxy = obj
		}
	}
	return result, nil
}

func reconcileDeprecatedNetworkSettings(networking *ClusterNetworking) error {
	if networking.MachineCIDR != "" {
		cidr, err := ipnet.ParseCIDR(networking.MachineCIDR)
		if err != nil {
			return fmt.Errorf("failed to parse machine CIDR %q: %w", networking.MachineCIDR, err)
		}
		networking.MachineNetwork = []MachineNetworkEntry{
			{
				CIDR: *cidr,
			},
		}
	}
	if networking.PodCIDR != "" {
		cidr, err := ipnet.ParseCIDR(networking.PodCIDR)
		if err != nil {
			return fmt.Errorf("failed to parse pod CIDR %q: %w", networking.PodCIDR, err)
		}
		networking.ClusterNetwork = []ClusterNetworkEntry{
			{
				CIDR: *cidr,
			},
		}
	}
	if networking.ServiceCIDR != "" {
		cidr, err := ipnet.ParseCIDR(networking.ServiceCIDR)
		if err != nil {
			return fmt.Errorf("failed to parse service CIDR: %w", err)
		}
		networking.ServiceNetwork = []ServiceNetworkEntry{
			{
				CIDR: *cidr,
			},
		}
	}
	return nil
}

func populateDeprecatedNetworkingFields(networking *ClusterNetworking) {
	if len(networking.ServiceNetwork) > 0 {
		networking.ServiceCIDR = cidrToString(networking.ServiceNetwork[0].CIDR)
	} else {
		networking.ServiceCIDR = ""
	}
	if len(networking.ClusterNetwork) > 0 {
		networking.PodCIDR = cidrToString(networking.ClusterNetwork[0].CIDR)
	} else {
		networking.PodCIDR = ""
	}
	if len(networking.MachineNetwork) > 0 {
		networking.MachineCIDR = cidrToString(networking.MachineNetwork[0].CIDR)
	} else {
		networking.MachineCIDR = ""
	}
}

func cidrToString(cidr ipnet.IPNet) string {
	if len(cidr.IP) == 0 {
		return ""
	}
	return cidr.String()
}

func populateDeprecatedHCPNetworkingFields(hcp *HostedControlPlane) {
	if len(hcp.Spec.Networking.ServiceNetwork) > 0 {
		hcp.Spec.ServiceCIDR = hcp.Spec.Networking.ServiceNetwork[0].CIDR.String()
	} else {
		hcp.Spec.ServiceCIDR = ""
	}
	if len(hcp.Spec.Networking.ClusterNetwork) > 0 {
		hcp.Spec.PodCIDR = hcp.Spec.Networking.ClusterNetwork[0].CIDR.String()
	} else {
		hcp.Spec.PodCIDR = ""
	}
	if len(hcp.Spec.Networking.MachineNetwork) > 0 {
		hcp.Spec.MachineCIDR = hcp.Spec.Networking.MachineNetwork[0].CIDR.String()
	} else {
		hcp.Spec.MachineCIDR = ""
	}
	hcp.Spec.NetworkType = hcp.Spec.Networking.NetworkType

	if hcp.Spec.Networking.APIServer != nil {
		hcp.Spec.APIPort = hcp.Spec.Networking.APIServer.Port
		hcp.Spec.APIAdvertiseAddress = hcp.Spec.Networking.APIServer.AdvertiseAddress
		hcp.Spec.APIAllowedCIDRBlocks = hcp.Spec.Networking.APIServer.AllowedCIDRBlocks
	}
}

func reconcileDeprecatedHCPNetworkSettings(hcp *HostedControlPlane) {
	if hcp.Spec.ServiceCIDR != "" && hcp.Spec.Networking.ServiceCIDR == "" {
		hcp.Spec.Networking.ServiceCIDR = hcp.Spec.ServiceCIDR
	}
	if hcp.Spec.PodCIDR != "" && hcp.Spec.Networking.PodCIDR == "" {
		hcp.Spec.Networking.PodCIDR = hcp.Spec.PodCIDR
	}
	if hcp.Spec.MachineCIDR != "" && hcp.Spec.Networking.MachineCIDR == "" {
		hcp.Spec.Networking.MachineCIDR = hcp.Spec.MachineCIDR
	}
	if hcp.Spec.NetworkType != "" && hcp.Spec.Networking.NetworkType == "" {
		hcp.Spec.Networking.NetworkType = hcp.Spec.NetworkType
	}
	if hcp.Spec.APIPort != nil && (hcp.Spec.Networking.APIServer == nil || hcp.Spec.Networking.APIServer.Port == nil) {
		if hcp.Spec.Networking.APIServer == nil {
			hcp.Spec.Networking.APIServer = &APIServerNetworking{}
		}
		hcp.Spec.Networking.APIServer.Port = hcp.Spec.APIPort
	}
	if hcp.Spec.APIAdvertiseAddress != nil && (hcp.Spec.Networking.APIServer == nil || hcp.Spec.Networking.APIServer.AdvertiseAddress == nil) {
		if hcp.Spec.Networking.APIServer == nil {
			hcp.Spec.Networking.APIServer = &APIServerNetworking{}
		}
		hcp.Spec.Networking.APIServer.AdvertiseAddress = hcp.Spec.APIAdvertiseAddress
	}
	if len(hcp.Spec.APIAllowedCIDRBlocks) != 0 && (hcp.Spec.Networking.APIServer == nil || len(hcp.Spec.Networking.APIServer.AllowedCIDRBlocks) == 0) {
		if hcp.Spec.Networking.APIServer == nil {
			hcp.Spec.Networking.APIServer = &APIServerNetworking{}
		}
		hcp.Spec.Networking.APIServer.AllowedCIDRBlocks = hcp.Spec.APIAllowedCIDRBlocks
	}
}

func populateDeprecatedGlobalConfig(config *ClusterConfiguration) error {
	if config != nil {
		items, err := configurationFieldsToRawExtensions(config)
		if err != nil {
			return fmt.Errorf("failed to convert configuration fields to raw extension: %w", err)
		}
		config.Items = items
		secretRef := []corev1.LocalObjectReference{}
		configMapRef := []corev1.LocalObjectReference{}
		for _, secretName := range configrefs.SecretRefs(config) {
			secretRef = append(secretRef, corev1.LocalObjectReference{
				Name: secretName,
			})
		}
		for _, configMapName := range configrefs.ConfigMapRefs(config) {
			configMapRef = append(configMapRef, corev1.LocalObjectReference{
				Name: configMapName,
			})
		}
		config.SecretRefs = secretRef
		config.ConfigMapRefs = configMapRef
	}
	return nil
}

func configurationFieldsToRawExtensions(config *ClusterConfiguration) ([]runtime.RawExtension, error) {
	var result []runtime.RawExtension
	if config == nil {
		return result, nil
	}
	if config.APIServer != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.APIServer{
				Spec: *config.APIServer,
			},
		})
	}
	if config.Authentication != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.Authentication{
				Spec: *config.Authentication,
			},
		})
	}
	if config.FeatureGate != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.FeatureGate{
				Spec: *config.FeatureGate,
			},
		})
	}
	if config.Image != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.Image{
				Spec: *config.Image,
			},
		})
	}
	if config.Ingress != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.Ingress{
				Spec: *config.Ingress,
			},
		})
	}
	if config.Network != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.Network{
				Spec: *config.Network,
			},
		})
	}
	if config.OAuth != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.OAuth{
				Spec: *config.OAuth,
			},
		})
	}
	if config.Scheduler != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.Scheduler{
				Spec: *config.Scheduler,
			},
		})
	}
	if config.Proxy != nil {
		result = append(result, runtime.RawExtension{
			Object: &configv1.Proxy{
				Spec: *config.Proxy,
			},
		})
	}

	for idx := range result {
		gvks, _, err := localScheme.ObjectKinds(result[idx].Object)
		if err != nil {
			return nil, fmt.Errorf("failed to get gvk for %T: %w", result[idx].Object, err)
		}
		if len(gvks) == 0 {
			return nil, fmt.Errorf("failed to determine gvk for %T", result[idx].Object)
		}
		result[idx].Object.GetObjectKind().SetGroupVersionKind(gvks[0])

		// We do a DeepEqual in the upsert func, so we must match the deserialized version from
		// the server which has Raw set and Object unset.
		b := &bytes.Buffer{}
		if err := serializer.Encode(result[idx].Object, b); err != nil {
			return nil, fmt.Errorf("failed to marshal %+v: %w", result[idx].Object, err)
		}

		// Remove the status part of the serialized resource. We only have
		// spec to begin with and status causes incompatibilities with previous
		// versions of the CPO
		unstructuredObject := &unstructured.Unstructured{}
		if _, _, err := unstructured.UnstructuredJSONScheme.Decode(b.Bytes(), nil, unstructuredObject); err != nil {
			return nil, fmt.Errorf("failed to decode resource into unstructured: %w", err)
		}
		unstructured.RemoveNestedField(unstructuredObject.Object, "status")
		b = &bytes.Buffer{}
		if err := unstructured.UnstructuredJSONScheme.Encode(unstructuredObject, b); err != nil {
			return nil, fmt.Errorf("failed to serialize unstructured resource: %w", err)
		}

		result[idx].Raw = bytes.TrimSuffix(b.Bytes(), []byte("\n"))
		result[idx].Object = nil
	}

	return result, nil
}
