package framework

import (
	"fmt"
	"os"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// TestOptions holds configuration for the e2e test framework
type TestOptions struct {
	// General test configuration
	ArtifactDir            string
	Platform               string
	BaseDomain             string
	PullSecretFile         string
	SSHKeyFile             string
	LatestReleaseImage     string
	PreviousReleaseImage   string
	NodePoolReplicas       int
	ClusterCreationTimeout time.Duration
	NodePoolReadyTimeout   time.Duration

	// Test behavior
	SkipTeardown  bool
	ParallelTests bool
	TestFilter    string

	// AWS-specific options
	AWSCredentialsFile    string
	AWSRegion             string
	AWSOidcS3BucketName   string

	// Azure-specific options
	AzureCredentialsFile string
	AzureLocation        string

	// KubeVirt-specific options
	KubeVirtInfraKubeconfigFile string
	KubeVirtInfraNamespace      string

	// OpenStack-specific options
	OpenStackCredentialsFile    string
	OpenStackExternalNetworkID  string
	OpenStackNodeFlavor         string
	OpenStackNodeImageName      string

	// PowerVS-specific options
	PowerVSRegion        string
	PowerVSZone          string
	PowerVSResourceGroup string
}

// Complete fills in default values for unset options
func (o *TestOptions) Complete() error {
	// Set default timeouts if not specified
	if o.ClusterCreationTimeout == 0 {
		o.ClusterCreationTimeout = 30 * time.Minute
	}
	if o.NodePoolReadyTimeout == 0 {
		o.NodePoolReadyTimeout = 15 * time.Minute
	}

	// Set default node pool replicas if not specified
	if o.NodePoolReplicas == 0 {
		o.NodePoolReplicas = 2
	}

	// Set default platform if not specified
	if o.Platform == "" {
		o.Platform = string(hyperv1.AWSPlatform)
	}

	// Set default AWS region if not specified and platform is AWS
	if o.Platform == string(hyperv1.AWSPlatform) && o.AWSRegion == "" {
		o.AWSRegion = "us-east-1"
	}

	// Set default Azure location if not specified and platform is Azure
	if o.Platform == string(hyperv1.AzurePlatform) && o.AzureLocation == "" {
		o.AzureLocation = "eastus"
	}

	return nil
}

// Validate checks that required options are set and valid
func (o *TestOptions) Validate() error {
	// Validate platform
	switch hyperv1.PlatformType(o.Platform) {
	case hyperv1.AWSPlatform, hyperv1.AzurePlatform, hyperv1.KubevirtPlatform,
		 hyperv1.OpenStackPlatform, hyperv1.PowerVSPlatform, hyperv1.NonePlatform:
		// Valid platforms
	default:
		return fmt.Errorf("unsupported platform: %s", o.Platform)
	}

	// Validate pull secret file exists if specified
	if o.PullSecretFile != "" {
		if _, err := os.Stat(o.PullSecretFile); os.IsNotExist(err) {
			return fmt.Errorf("pull secret file not found: %s", o.PullSecretFile)
		}
	}

	// Validate SSH key file exists if specified
	if o.SSHKeyFile != "" {
		if _, err := os.Stat(o.SSHKeyFile); os.IsNotExist(err) {
			return fmt.Errorf("SSH key file not found: %s", o.SSHKeyFile)
		}
	}

	// Platform-specific validation
	if err := o.validatePlatformSpecific(); err != nil {
		return fmt.Errorf("platform-specific validation failed: %w", err)
	}

	// Validate timeouts are positive
	if o.ClusterCreationTimeout <= 0 {
		return fmt.Errorf("cluster creation timeout must be positive")
	}
	if o.NodePoolReadyTimeout <= 0 {
		return fmt.Errorf("nodepool ready timeout must be positive")
	}

	// Validate node pool replicas is positive
	if o.NodePoolReplicas <= 0 {
		return fmt.Errorf("node pool replicas must be positive")
	}

	return nil
}

// validatePlatformSpecific performs platform-specific validation
func (o *TestOptions) validatePlatformSpecific() error {
	switch hyperv1.PlatformType(o.Platform) {
	case hyperv1.AWSPlatform:
		return o.validateAWS()
	case hyperv1.AzurePlatform:
		return o.validateAzure()
	case hyperv1.KubevirtPlatform:
		return o.validateKubeVirt()
	case hyperv1.OpenStackPlatform:
		return o.validateOpenStack()
	case hyperv1.PowerVSPlatform:
		return o.validatePowerVS()
	default:
		// No validation needed for other platforms
		return nil
	}
}

// validateAWS validates AWS-specific options
func (o *TestOptions) validateAWS() error {
	// AWS credentials file validation
	if o.AWSCredentialsFile != "" {
		if _, err := os.Stat(o.AWSCredentialsFile); os.IsNotExist(err) {
			return fmt.Errorf("AWS credentials file not found: %s", o.AWSCredentialsFile)
		}
	}

	// AWS region validation
	if o.AWSRegion == "" {
		return fmt.Errorf("AWS region is required for AWS platform")
	}

	return nil
}

// validateAzure validates Azure-specific options
func (o *TestOptions) validateAzure() error {
	// Azure credentials file validation
	if o.AzureCredentialsFile != "" {
		if _, err := os.Stat(o.AzureCredentialsFile); os.IsNotExist(err) {
			return fmt.Errorf("Azure credentials file not found: %s", o.AzureCredentialsFile)
		}
	}

	// Azure location validation
	if o.AzureLocation == "" {
		return fmt.Errorf("Azure location is required for Azure platform")
	}

	return nil
}

// validateKubeVirt validates KubeVirt-specific options
func (o *TestOptions) validateKubeVirt() error {
	// KubeVirt infra kubeconfig validation
	if o.KubeVirtInfraKubeconfigFile != "" {
		if _, err := os.Stat(o.KubeVirtInfraKubeconfigFile); os.IsNotExist(err) {
			return fmt.Errorf("KubeVirt infra kubeconfig file not found: %s", o.KubeVirtInfraKubeconfigFile)
		}
	}

	return nil
}

// validateOpenStack validates OpenStack-specific options
func (o *TestOptions) validateOpenStack() error {
	// OpenStack credentials file validation
	if o.OpenStackCredentialsFile != "" {
		if _, err := os.Stat(o.OpenStackCredentialsFile); os.IsNotExist(err) {
			return fmt.Errorf("OpenStack credentials file not found: %s", o.OpenStackCredentialsFile)
		}
	}

	return nil
}

// validatePowerVS validates PowerVS-specific options
func (o *TestOptions) validatePowerVS() error {
	// Basic PowerVS validation - most options have defaults
	return nil
}

// IsAWS returns true if the platform is AWS
func (o *TestOptions) IsAWS() bool {
	return o.Platform == string(hyperv1.AWSPlatform)
}

// IsAzure returns true if the platform is Azure
func (o *TestOptions) IsAzure() bool {
	return o.Platform == string(hyperv1.AzurePlatform)
}

// IsKubeVirt returns true if the platform is KubeVirt
func (o *TestOptions) IsKubeVirt() bool {
	return o.Platform == string(hyperv1.KubevirtPlatform)
}

// IsOpenStack returns true if the platform is OpenStack
func (o *TestOptions) IsOpenStack() bool {
	return o.Platform == string(hyperv1.OpenStackPlatform)
}

// IsPowerVS returns true if the platform is PowerVS
func (o *TestOptions) IsPowerVS() bool {
	return o.Platform == string(hyperv1.PowerVSPlatform)
}