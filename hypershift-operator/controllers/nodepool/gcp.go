package nodepool

import (
	"context"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	supportutil "github.com/openshift/hypershift/support/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capigcp "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// gcpMachineTemplate creates a GCPMachineTemplate for the given NodePool.
// This follows the AWS and Azure patterns for CAPI machine template generation.
func (c *CAPI) gcpMachineTemplate(ctx context.Context, templateNameGenerator func(spec any) (string, error)) (client.Object, error) {
	nodePool := c.nodePool
	hc := c.hostedCluster

	// Validate GCP platform configuration
	if nodePool.Spec.Platform.GCP == nil {
		return nil, fmt.Errorf("GCP platform configuration is required")
	}

	if hc.Spec.Platform.GCP == nil {
		return nil, fmt.Errorf("HostedCluster GCP platform configuration is required")
	}

	// Generate template spec based on NodePool configuration
	templateSpec, err := gcpMachineTemplateSpec(
		hc.Spec.InfraID,
		hc,
		nodePool,
		c.releaseImage,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate GCP machine template spec: %w", err)
	}

	// Create hash of the template spec for naming
	templateName, err := templateNameGenerator(templateSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to generate template name: %w", err)
	}

	// Create the GCP machine template
	template := &capigcp.GCPMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: c.controlplaneNamespace,
			Name:      templateName,
			Labels: map[string]string{
				capiv1.ClusterNameLabel:                 c.capiClusterName,
				hyperv1.NodePoolLabel:                   c.nodePool.Name,
				supportutil.HostedClusterAnnotation:     hc.Name,
				capiv1.TemplateClonedFromNameAnnotation: templateName,
			},
		},
		Spec: capigcp.GCPMachineTemplateSpec{
			Template: capigcp.GCPMachineTemplateResource{
				Spec: *templateSpec,
			},
		},
	}

	return template, nil
}

// gcpMachineTemplateSpec generates the GCP machine template specification.
// This function handles image resolution, network configuration, and GCP-specific settings.
func gcpMachineTemplateSpec(
	infraName string,
	hostedCluster *hyperv1.HostedCluster,
	nodePool *hyperv1.NodePool,
	releaseImage *releaseinfo.ReleaseImage,
) (*capigcp.GCPMachineSpec, error) {
	gcpPlatform := nodePool.Spec.Platform.GCP
	hcGCPPlatform := hostedCluster.Spec.Platform.GCP

	// Resolve image
	image, err := resolveGCPImage(nodePool, releaseImage)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve GCP image: %w", err)
	}

	// Configure network placement with PSC subnet
	subnet, err := resolveGCPSubnet(hcGCPPlatform)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve GCP subnet: %w", err)
	}

	// Configure service account
	serviceAccounts := configureGCPServiceAccount(gcpPlatform.ServiceAccount)

	// Configure boot disk
	bootDisk := configureGCPBootDisk(gcpPlatform.BootDisk)

	// Configure labels
	labels := configureGCPLabels(hcGCPPlatform, gcpPlatform, infraName, hostedCluster.Name)

	// Configure network tags
	networkTags := configureGCPNetworkTags(gcpPlatform.NetworkTags, infraName)

	// Configure maintenance behavior
	onHostMaintenance := configureGCPMaintenanceBehavior(gcpPlatform.OnHostMaintenance, gcpPlatform.Preemptible)

	preemptible := false
	if gcpPlatform.Preemptible != nil {
		preemptible = *gcpPlatform.Preemptible
	}

	spec := &capigcp.GCPMachineSpec{
		InstanceType:          gcpPlatform.MachineType,
		Subnet:                &subnet,
		Image:                 &image,
		RootDeviceSize:        bootDisk.Size,
		RootDeviceType:        bootDisk.Type,
		ServiceAccount:        serviceAccounts,
		AdditionalLabels:      labels,
		AdditionalNetworkTags: networkTags,
		Preemptible:           preemptible,
		OnHostMaintenance:     &onHostMaintenance,
		RootDiskEncryptionKey: bootDisk.EncryptionKey,
		// Additional metadata for ignition and identification
		AdditionalMetadata: []capigcp.MetadataItem{
			{Key: "hypershift-cluster", Value: &hostedCluster.Name},
			{Key: "hypershift-nodepool", Value: &nodePool.Name},
			{Key: "hypershift-infra-id", Value: &infraName},
		},
	}

	return spec, nil
}

// resolveGCPImage determines the correct image to use based on NodePool configuration and release info.
func resolveGCPImage(nodePool *hyperv1.NodePool, releaseImage *releaseinfo.ReleaseImage) (string, error) {
	gcpPlatform := nodePool.Spec.Platform.GCP

	// If user specified a custom image, use it
	if gcpPlatform.Image != nil && *gcpPlatform.Image != "" {
		return *gcpPlatform.Image, nil
	}

	// Resolve image from release metadata
	image, err := defaultNodePoolGCPImage(nodePool.Spec.Arch, releaseImage)
	if err != nil {
		return "", fmt.Errorf("couldn't discover a GCP image for release image: %w", err)
	}

	return image, nil
}

// resolveGCPSubnet configures the subnet for node placement using PSC subnet.
func resolveGCPSubnet(hcGCPPlatform *hyperv1.GCPPlatformSpec) (string, error) {
	// If PrivateServiceConnectSubnet is configured, use it
	if hcGCPPlatform.NetworkConfig.PrivateServiceConnectSubnet.Name != "" {
		// CAPG will automatically prepend "projects/{project}/regions/{region}/subnetworks/"
		// so we only provide the subnet name
		return hcGCPPlatform.NetworkConfig.PrivateServiceConnectSubnet.Name, nil
	}

	// Default to using the default subnet name - CAPG will construct the full path
	return "default", nil
}

// configureGCPServiceAccount sets up the service account configuration.
func configureGCPServiceAccount(saConfig *hyperv1.GCPNodeServiceAccount) *capigcp.ServiceAccount {
	if saConfig == nil {
		// Use default compute service account
		return &capigcp.ServiceAccount{
			Email: "", // Empty means use default
			Scopes: []string{
				"https://www.googleapis.com/auth/devstorage.read_only",
				"https://www.googleapis.com/auth/logging.write",
				"https://www.googleapis.com/auth/monitoring.write",
				"https://www.googleapis.com/auth/servicecontrol",
				"https://www.googleapis.com/auth/service.management.readonly",
				"https://www.googleapis.com/auth/trace.append",
			},
		}
	}

	scopes := saConfig.Scopes
	if len(scopes) == 0 {
		// Default scopes for node functionality
		scopes = []string{
			"https://www.googleapis.com/auth/devstorage.read_only",
			"https://www.googleapis.com/auth/logging.write",
			"https://www.googleapis.com/auth/monitoring.write",
			"https://www.googleapis.com/auth/servicecontrol",
			"https://www.googleapis.com/auth/service.management.readonly",
			"https://www.googleapis.com/auth/trace.append",
		}
	}

	email := ""
	if saConfig.Email != nil {
		email = *saConfig.Email
	}
	return &capigcp.ServiceAccount{
		Email:  email,
		Scopes: scopes,
	}
}

// GCPBootDiskConfig holds boot disk configuration values
type GCPBootDiskConfig struct {
	Size          int64
	Type          *capigcp.DiskType
	EncryptionKey *capigcp.CustomerEncryptionKey
}

// configureGCPBootDisk creates the boot disk configuration.
func configureGCPBootDisk(bootDiskConfig *hyperv1.GCPBootDisk) GCPBootDiskConfig {
	diskSizeGB := int64(64)                // Default size
	diskType := capigcp.PdStandardDiskType // Default type

	if bootDiskConfig != nil {
		if bootDiskConfig.DiskSizeGB != nil && *bootDiskConfig.DiskSizeGB > 0 {
			diskSizeGB = *bootDiskConfig.DiskSizeGB
		}
		if bootDiskConfig.DiskType != nil && *bootDiskConfig.DiskType != "" {
			diskType = capigcp.DiskType(*bootDiskConfig.DiskType)
		}
	}

	config := GCPBootDiskConfig{
		Size: diskSizeGB,
		Type: &diskType,
	}

	// Configure encryption if specified
	if bootDiskConfig != nil && bootDiskConfig.EncryptionKey != nil {
		config.EncryptionKey = &capigcp.CustomerEncryptionKey{
			KeyType: capigcp.CustomerManagedKey,
			ManagedKey: &capigcp.ManagedKey{
				KMSKeyName: bootDiskConfig.EncryptionKey.KMSKeyName,
			},
		}
	}

	return config
}

// configureGCPLabels creates the labels map for the GCP machine.
func configureGCPLabels(hcGCPPlatform *hyperv1.GCPPlatformSpec, gcpPlatform *hyperv1.GCPNodePoolPlatform, infraID, clusterName string) map[string]string {
	labels := make(map[string]string)

	// Add cluster-level resource labels
	if hcGCPPlatform.ResourceLabels != nil {
		for k, v := range hcGCPPlatform.ResourceLabels {
			labels[k] = v
		}
	}

	// Add NodePool-level resource labels (overrides cluster labels)
	if gcpPlatform.ResourceLabels != nil {
		for k, v := range gcpPlatform.ResourceLabels {
			labels[k] = v
		}
	}

	// Add HyperShift-specific labels for resource identification
	labels[toGCPLabel("hypershift.openshift.io/cluster")] = clusterName
	if infraID != "" {
		labels[toGCPLabel("hypershift.openshift.io/infra-id")] = infraID
	}

	return labels
}

// configureGCPNetworkTags creates the network tags list for the GCP machine.
func configureGCPNetworkTags(userTags []string, infraID string) []string {
	var tags []string

	// Add user-defined network tags
	if userTags != nil {
		tags = append(tags, userTags...)
	}

	// Add HyperShift-specific tags for firewall rules
	if infraID != "" {
		tags = append(tags, fmt.Sprintf("%s-worker", infraID))
	}

	return tags
}

// configureGCPMaintenanceBehavior determines the host maintenance behavior.
func configureGCPMaintenanceBehavior(userMaintenance *string, preemptible *bool) capigcp.HostMaintenancePolicy {
	if userMaintenance != nil && *userMaintenance != "" {
		if *userMaintenance == "TERMINATE" {
			return capigcp.HostMaintenancePolicyTerminate
		}
		return capigcp.HostMaintenancePolicyMigrate
	}

	// For preemptible instances, must use TERMINATE
	if preemptible != nil && *preemptible {
		return capigcp.HostMaintenancePolicyTerminate
	}

	// Default for standard instances is MIGRATE (live migration)
	return capigcp.HostMaintenancePolicyMigrate
}

// toGCPLabel converts a label key to GCP-compliant format.
// This is the same function from the platform controller.
func toGCPLabel(label string) string {
	// Replace both dots and forward slashes with dashes for GCP compliance
	result := strings.ReplaceAll(label, ".", "-")
	result = strings.ReplaceAll(result, "/", "-")

	// Convert to lowercase to ensure compliance
	result = strings.ToLower(result)

	// Ensure it starts with a lowercase letter - prefix with 'x' if needed
	if len(result) > 0 && (result[0] < 'a' || result[0] > 'z') {
		result = "x" + result
	}

	// Truncate to 63 characters max to meet GCP requirements
	if len(result) > 63 {
		result = result[:63]
	}

	return result
}
