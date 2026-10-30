package featuregate

import (
	"github.com/openshift/hypershift/pkg/featuregates"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/component-base/featuregate"
)

// Define new featuregates here
const (
	// AROHCPManagedIdentities is a feature gate for enabling HCP components to authenticate with Azure by client certificate
	// owner: @username
	// alpha: v0.1.49
	// beta x.y.z
	AROHCPManagedIdentities featuregate.Feature = "AROHCPManagedIdentities"

	// OpenStack is a feature gate for running clusters on OpenStack.
	// owner: @username
	// alpha: v0.1.49
	// beta: x.y.z
	OpenStack featuregate.Feature = "OpenStack"

	// GCPPlatform enables experimental support for provisioning and managing OpenShift hosted control planes on Google Cloud Platform infrastructure.
	// This feature gate controls GCP-HCP specific cluster provisioning logic, resource management, and integration with GCP services for HyperShift-managed clusters.
	// owner: hcm-gcp-hcp@redhat.com
	// alpha: v0.1.49
	// beta: x.y.z
	GCPPlatform featuregate.Feature = "GCPPlatform"

	// HCPEtcdBackup enables the HCPEtcdBackup CRD for OADP-driven etcd backup orchestration.
	// owner: @jparrill
	// alpha: v0.1.49
	// beta: x.y.z
	HCPEtcdBackup featuregate.Feature = "HCPEtcdBackup"

	// KarpenterOperator gates deploying the karpenter-operator as a standalone component instead of using the embedded karpenter-operator code in HyperShift.
	// owner: openshift-autoscaling
	// alpha: v0.1.79
	// beta: x.y.z
	KarpenterOperator featuregate.Feature = "KarpenterOperator"

	// EtcdSharding enables etcd sharding by resource kind, allowing resources to be
	// routed to dedicated etcd instances for improved performance and isolation.
	// alpha: v0.1.49
	// beta: x.y.z
	EtcdSharding featuregate.Feature = "EtcdSharding"

	// OSStreams enables dual-stream RHEL 9/10 support in NodePool boot image resolution.
	// When enabled, NodePools resolve the RHEL stream dynamically from the release version
	// (e.g., OCP 5.0+ defaults to rhel-10). When disabled, boot images always use rhel-9.
	// owner: @jparrill
	// alpha: v0.1.49
	// default: OCP 5.0
	OSStreams featuregate.Feature = "OSStreams"

	// DeterministicLBIP enables deterministic LoadBalancer IP management for HostedClusters.
	// When enabled, users can specify loadBalancerClass and serviceAnnotations on
	// spec.services entries to control the IP assigned to KAS and other LB-type services.
	// owner: @jparrill
	// alpha: v0.1.49
	// beta: x.y.z
	DeterministicLBIP featuregate.Feature = "DeterministicLBIP"

	// AWSManagedDNS enables CPO-managed Route53 DNS zones for AWS hosted control planes.
	// When enabled, the managedDNS field is available on AWSPlatformSpec, allowing the
	// CPO to create and reconcile ingress DNS zones in the customer's AWS account.
	// owner: @typeid
	// alpha: v0.1.84
	AWSManagedDNS featuregate.Feature = "AWSManagedDNS"

	// KubeletHAProxyRouting gates the kubelet-through-HAProxy routing change on all public
	// HCPs. When enabled, the bootstrap kubeconfig points kubelet at the node-local HAProxy
	// advertise address instead of the KAS LB address, so a Day 2 LB IP change does not
	// strand nodes. Private clusters keep using internal DNS and are unaffected.
	// Registered here for HYPERSHIFT_FEATURESET propagation and Day 2 transition work
	// (CNTRLPLANE-4799/4800). Runtime gate check is in CPO's adaptBootstrapKubeconfigSecret.
	// owner: @jparrill
	// alpha: v0.1.84
	KubeletHAProxyRouting featuregate.Feature = "KubeletHAProxyRouting"
	// IgnitionPayloadSystem gates the re-architected ignition system: the HO deploys the
	// split ignition-payload-controller + ignition-payload-server (+ proxy) from the HO image
	// and the NodePool controller uses the IgnitionPayload path instead of the legacy
	// ignition-server. Fleet-wide selector (no per-HostedCluster opt-in).
	// owner: @muraee
	// alpha: v0.1.84
	IgnitionPayloadSystem featuregate.Feature = "IgnitionPayloadSystem"
)

// Initialize new features here
var (
	allFeatures = featuregates.NewFeatureSetAwareFeatures()

	aroHCPManagedIdentitiesFeature = featuregates.NewFeature(AROHCPManagedIdentities, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
	openStackFeature               = featuregates.NewFeature(OpenStack, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
	gcpHCPFeature                  = featuregates.NewFeature(GCPPlatform, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
	hcpEtcdBackupFeature           = featuregates.NewFeature(HCPEtcdBackup, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
	karpenterOperatorFeature       = featuregates.NewFeature(KarpenterOperator, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
	etcdShardingFeature            = featuregates.NewFeature(EtcdSharding, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
	osStreamsFeature               = featuregates.NewFeature(OSStreams, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade, configv1.Default))
	deterministicLBIPFeature       = featuregates.NewFeature(DeterministicLBIP, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
	awsManagedDNSFeature           = featuregates.NewFeature(AWSManagedDNS, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
	kubeletHAProxyRoutingFeature   = featuregates.NewFeature(KubeletHAProxyRouting, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
	ignitionPayloadSystemFeature   = featuregates.NewFeature(IgnitionPayloadSystem, featuregates.WithEnableForFeatureSets(configv1.TechPreviewNoUpgrade))
)

func init() {
	// Add featuregates here
	allFeatures.AddFeature(aroHCPManagedIdentitiesFeature)
	allFeatures.AddFeature(openStackFeature)
	allFeatures.AddFeature(gcpHCPFeature)
	allFeatures.AddFeature(hcpEtcdBackupFeature)
	allFeatures.AddFeature(karpenterOperatorFeature)
	allFeatures.AddFeature(etcdShardingFeature)
	allFeatures.AddFeature(osStreamsFeature)
	allFeatures.AddFeature(deterministicLBIPFeature)
	allFeatures.AddFeature(awsManagedDNSFeature)
	allFeatures.AddFeature(kubeletHAProxyRoutingFeature)
	allFeatures.AddFeature(ignitionPayloadSystemFeature)

	// Default to configuring the Default featureset
	ConfigureFeatureSet(string(configv1.Default))
}

// globalGate is a global variable meant to store the featuregate.MutableFeatureGate
// for the currently configured featureset. This makes it easier to check
// which feature gates are currently enabled/disabled in other places.
var globalGate featuregate.MutableFeatureGate

// Gate returns the currently configured featuregates. Useful
// for determining what feature gates are enabled or configuring the feature gates.
// Example: Gate().Enabled("Foo")
func Gate() featuregate.MutableFeatureGate {
	return globalGate
}

// globalFeatureSet is a global variable meant to store the
// currently configured feature set. This is useful when
// needing to perform specific actions, like configuring
// the control plane operator, based on the currently configured
// feature set.
var globalFeatureSet configv1.FeatureSet

// FeatureSet returns the currently configured feature set.
func FeatureSet() configv1.FeatureSet {
	return globalFeatureSet
}

// ConfigureFeatureSet is used to configure the feature gates and feature set based on the provided featureSet.
// The provided featureSet must be a known feature set name.
// ConfigureFeatureSet should only be called once on startup.
// ConfigureFeatureSet is not thread-safe.
func ConfigureFeatureSet(featureSet string) {
	featureGate, err := allFeatures.FeatureGatesForFeatureSet(configv1.FeatureSet(featureSet))
	if err != nil {
		// If we encounter an error due to an unknown featureset, just assume the default
		return
	}

	globalFeatureSet = configv1.FeatureSet(featureSet)
	globalGate = featureGate
}
