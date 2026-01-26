package nodepool

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"
	supportutil "github.com/openshift/hypershift/support/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capigcp "sigs.k8s.io/cluster-api-provider-gcp/api/v1beta1"
	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta1"
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

	// Create hash of the template spec for naming.
	// Exclude AdditionalLabels from the hash so that label-only changes
	// do not trigger a rolling upgrade of nodes (following the AWS pattern).
	hashedSpec := templateSpec.DeepCopy()
	hashedSpec.AdditionalLabels = nil
	templateName, err := templateNameGenerator(hashedSpec)
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

	// Configure network placement - NodePool subnet takes precedence
	subnet, err := resolveGCPSubnet(gcpPlatform, hcGCPPlatform)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve GCP subnet: %w", err)
	}

	// Configure service account
	// If not specified, GCP will use the default Compute Engine service account
	serviceAccounts := configureGCPServiceAccount(gcpPlatform.ServiceAccount)

	// Configure boot disk
	bootDisk := configureGCPBootDisk(gcpPlatform.BootDisk)

	// Configure labels
	labels := configureGCPLabels(hcGCPPlatform, gcpPlatform, infraName, hostedCluster.Name)

	// Configure network tags
	networkTags := configureGCPNetworkTags(gcpPlatform.NetworkTags, infraName)

	// Configure maintenance behavior
	onHostMaintenance := configureGCPMaintenanceBehavior(gcpPlatform.OnHostMaintenance, gcpPlatform.ProvisioningModel)

	// Determine preemptible setting and provisioning model for CAPG
	preemptible := gcpPlatform.ProvisioningModel != nil &&
		*gcpPlatform.ProvisioningModel == hyperv1.GCPProvisioningModelPreemptible

	// Map hypershift provisioning model to CAPG provisioning model
	// CAPG uses ProvisioningModel for Spot VMs (separate from Preemptible boolean)
	var provisioningModel *capigcp.ProvisioningModel
	if gcpPlatform.ProvisioningModel != nil {
		switch *gcpPlatform.ProvisioningModel {
		case hyperv1.GCPProvisioningModelSpot:
			spot := capigcp.ProvisioningModelSpot
			provisioningModel = &spot
		case hyperv1.GCPProvisioningModelStandard:
			standard := capigcp.ProvisioningModelStandard
			provisioningModel = &standard
			// For Preemptible, we use the Preemptible boolean field (legacy)
			// and don't set ProvisioningModel
		}
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
		ProvisioningModel:     provisioningModel,
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

// resolveGCPSubnet configures the subnet for node placement.
// Priority: NodePool subnet > HostedCluster PSC subnet > "default"
func resolveGCPSubnet(gcpPlatform *hyperv1.GCPNodePoolPlatform, hcGCPPlatform *hyperv1.GCPPlatformSpec) (string, error) {
	// NodePool-specified subnet takes precedence
	if gcpPlatform != nil && gcpPlatform.Subnet != "" {
		// CAPG will automatically prepend "projects/{project}/regions/{region}/subnetworks/"
		// so we only provide the subnet name
		return gcpPlatform.Subnet, nil
	}

	// Fall back to HostedCluster PrivateServiceConnectSubnet if configured
	if hcGCPPlatform.NetworkConfig.PrivateServiceConnectSubnet.Name != "" {
		return hcGCPPlatform.NetworkConfig.PrivateServiceConnectSubnet.Name, nil
	}

	// Default to using the default subnet name - CAPG will construct the full path
	return "default", nil
}

// configureGCPServiceAccount sets up the service account configuration.
// If saConfig is nil, returns nil which tells GCP to use the default Compute Engine service account.
// The default compute SA (PROJECT_NUMBER-compute@developer.gserviceaccount.com) has broad permissions.
// For custom service accounts, specify email and scopes in the NodePool spec.
func configureGCPServiceAccount(saConfig *hyperv1.GCPNodeServiceAccount) *capigcp.ServiceAccount {
	if saConfig == nil {
		// Return nil to use GCP's default Compute Engine service account
		return nil
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
	diskSizeGB := int64(64)                     // Default size
	diskType := capigcp.DiskType("pd-balanced") // Default type (matches API +kubebuilder:default="pd-balanced")

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
	for _, label := range hcGCPPlatform.ResourceLabels {
		labels[label.Key] = ptr.Deref(label.Value, "")
	}

	// Add NodePool-level resource labels (overrides cluster labels)
	for _, label := range gcpPlatform.ResourceLabels {
		labels[label.Key] = ptr.Deref(label.Value, "")
	}

	// Add HyperShift-specific labels for resource identification
	labels[supportutil.GCPLabelCluster] = clusterName
	if infraID != "" {
		labels[supportutil.GCPLabelInfraID] = infraID
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
func configureGCPMaintenanceBehavior(userMaintenance *string, provisioningModel *hyperv1.GCPProvisioningModel) capigcp.HostMaintenancePolicy {
	if userMaintenance != nil && *userMaintenance != "" {
		if *userMaintenance == string(hyperv1.GCPOnHostMaintenanceTerminate) {
			return capigcp.HostMaintenancePolicyTerminate
		}
		return capigcp.HostMaintenancePolicyMigrate
	}

	// For preemptible or spot instances, must use TERMINATE
	if provisioningModel != nil && (*provisioningModel == hyperv1.GCPProvisioningModelPreemptible || *provisioningModel == hyperv1.GCPProvisioningModelSpot) {
		return capigcp.HostMaintenancePolicyTerminate
	}

	// Default for standard instances is MIGRATE (live migration)
	return capigcp.HostMaintenancePolicyMigrate
}
