package karpenter

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/featuregate"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	// KarpenterFinalizer is the finalizer added to HostedControlPlane resources by the karpenter-operator.
	// This allows time for Karpenter to delete all NodePools and NodeClaims before the HostedControlPlane is deleted.
	KarpenterFinalizer = "hypershift.openshift.io/karpenter-finalizer"

	// ManagedByKarpenterLabel is a label set on the userData secrets as being managed by Karpenter Operator
	ManagedByKarpenterLabel = "hypershift.openshift.io/managed-by-karpenter"

	// KarpenterNodeClassKubeletConfigLabel is a label set on per-OpenshiftEC2NodeClass KubeletConfig ConfigMaps
	// in the HCP namespace so they can be discovered.
	KarpenterNodeClassKubeletConfigLabel = "hypershift.openshift.io/karpenter-nodeclass-kubelet-config"

	// ServiceAccount the Karpenter controller uses in the hosted cluster; cloud IAM/Azure federated
	// credentials trust this subject, and management-cluster components mint tokens for it to call AWS/Azure as Karpenter.
	KarpenterCloudServiceAccountNamespace = "kube-system"
	KarpenterCloudServiceAccountName      = "karpenter"
)

const (
	// KarpenterTaintConfigMapName is the name of the configmap containing the karpenter taint config
	KarpenterTaintConfigMapName = "set-karpenter-taint"
	// KarpenterSubnetsConfigMapName is the name of the configmap containing the aggregated subnet IDs
	// from all user-defined OpenshiftEC2NodeClass resources.
	KarpenterSubnetsConfigMapName = "karpenter-subnets"
)

// KarpenterBaseTaints is the set of taints applied to nodes registered by Karpenter.
var KarpenterBaseTaints = []corev1.Taint{
	{
		Key:    "karpenter.sh/unregistered",
		Value:  "true",
		Effect: corev1.TaintEffectNoExecute,
	},
}

// KarpenterBaseTaintMap returns the registerWithTaints kubelet config map built from KarpenterTaints.
// It is used by reconcileKubeletConfigMap to merge our taints into the user-provided kubelet config.
func KarpenterBaseTaintMap() map[string]interface{} {
	taints := make([]interface{}, len(KarpenterBaseTaints))
	for i, t := range KarpenterBaseTaints {
		taints[i] = map[string]interface{}{
			"key":    t.Key,
			"value":  t.Value,
			"effect": string(t.Effect),
		}
	}
	return map[string]interface{}{
		"registerWithTaints": taints,
	}
}

// KarpenterTaintConfigManifest returns the KubeletConfig CR YAML for the set-karpenter-taint ConfigMap.
func KarpenterTaintConfigManifest() (string, error) {
	cr := map[string]interface{}{
		"apiVersion": "machineconfiguration.openshift.io/v1",
		"kind":       "KubeletConfig",
		"metadata":   map[string]interface{}{"name": KarpenterTaintConfigMapName},
		"spec":       map[string]interface{}{"kubeletConfig": KarpenterBaseTaintMap()},
	}
	out, err := yaml.Marshal(cr)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// KarpenterNodeClassKubeletConfigName returns the name of the ConfigMap containing the
// per-OpenshiftEC2NodeClass KubeletConfig in the HCP namespace.
func KarpenterNodeClassKubeletConfigName(nodeClassName string) string {
	return fmt.Sprintf("karpenter-kubelet-%s", nodeClassName)
}

// ErrHCPNotFound is returned when no HostedControlPlane is found in the namespace.
var ErrHCPNotFound = errors.New("hostedcontrolplane not found")

// SupportedArchitectures returns the list of supported architectures for Karpenter on the given platform.
func SupportedArchitectures(platform hyperv1.PlatformType) ([]string, error) {
	switch platform {
	case hyperv1.AWSPlatform, hyperv1.AzurePlatform:
		return []string{hyperv1.ArchitectureAMD64, hyperv1.ArchitectureARM64}, nil
	default:
		return nil, fmt.Errorf("unsupported platform: %s", platform)
	}
}

// IsKarpenterEnabled checks if Karpenter is enabled for the given AutoNode configuration.
func IsKarpenterEnabled(autoNode hyperv1.AutoNode) bool {
	if autoNode.Provisioner.Name != hyperv1.ProvisionerKarpenter {
		return false
	}
	switch autoNode.Provisioner.Karpenter.Platform {
	case hyperv1.AWSPlatform, hyperv1.AzurePlatform:
		return true
	default:
		return false
	}
}

// GetHCP retrieves the HostedControlPlane from the given namespace.
// Returns ErrHCPNotFound if no HCP exists in the namespace.
func GetHCP(ctx context.Context, c client.Reader, namespace string) (*hyperv1.HostedControlPlane, error) {
	hcpList := &hyperv1.HostedControlPlaneList{}
	if err := c.List(ctx, hcpList, client.InNamespace(namespace)); err != nil {
		return nil, err
	}
	if len(hcpList.Items) == 0 {
		return nil, fmt.Errorf("%w: namespace %s", ErrHCPNotFound, namespace)
	}
	return &hcpList.Items[0], nil
}

// KarpenterNodePoolName returns the name of the Karpenter NodePool for a given NodeClass.
func KarpenterNodePoolName(nodeClass metav1.Object) string {
	return fmt.Sprintf("%s-karpenter", nodeClass.GetName())
}

// ArchToImageMetadataKey returns an architecture-specific label or annotation key.
// amd64 uses the base key; other architectures append their name as a suffix.
func ArchToImageMetadataKey(arch, baseKey string) string {
	if arch == hyperv1.ArchitectureAMD64 {
		return baseKey
	}
	return fmt.Sprintf("%s-%s", baseKey, arch)
}

const EnableStandaloneKarpenterOperatorEnvVar = "ENABLE_STANDALONE_KARPENTER_OPERATOR"

// IsStandaloneKarpenterOperatorEnabled returns true if the standalone karpenter-operator
// should be deployed instead of the embedded karpenter controller.
// Requires both:
// - the KarpenterOperator feature gate is on (via TechPreviewNoUpgrade), and
// - the ENABLE_STANDALONE_KARPENTER_OPERATOR env var is set to "1" (via --enable-standalone-karpenter-operator install flag).
// Note that this function only works in the context of the hypershift-operator since it checks that an env var is set.
func IsStandaloneKarpenterOperatorEnabled() bool {
	return featuregate.Gate().Enabled(featuregate.KarpenterOperator) &&
		os.Getenv(EnableStandaloneKarpenterOperatorEnvVar) == "1"
}
