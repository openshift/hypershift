package util

import hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

const (
	DefaultGCPMachineTypeAMD64 = "n2-standard-4"
	DefaultGCPMachineTypeARM64 = "t2a-standard-4"
)

// DefaultGCPMachineType returns appropriate machine type for architecture.
// Returns n2-standard-4 for AMD64 and t2a-standard-4 (Tau T2A) for ARM64.
func DefaultGCPMachineType(arch string) string {
	switch arch {
	case "arm64":
		return DefaultGCPMachineTypeARM64
	case "amd64":
		fallthrough
	default:
		return DefaultGCPMachineTypeAMD64
	}
}

// GCPNodePoolPlatformOptions contains options for building basic GCP NodePool platform spec.
// Only includes fields shared between cluster and nodepool commands.
type GCPNodePoolPlatformOptions struct {
	// Zone is the GCP zone for the NodePool
	Zone string

	// Subnet is the subnet name for node instances
	Subnet string

	// MachineType is the GCP machine type. If empty, defaults based on Arch.
	MachineType string

	// Arch is the architecture for machine type defaulting
	Arch string

	// Image is the boot image override (optional)
	Image string
}

// BuildGCPNodePoolPlatform constructs basic GCPNodePoolPlatform with shared fields.
// Handles machine type defaulting based on architecture.
// Advanced fields (BootDisk, ServiceAccount, etc.) should be set separately.
func BuildGCPNodePoolPlatform(opts GCPNodePoolPlatformOptions) *hyperv1.GCPNodePoolPlatform {
	machineType := opts.MachineType
	if machineType == "" {
		machineType = DefaultGCPMachineType(opts.Arch)
	}

	return &hyperv1.GCPNodePoolPlatform{
		MachineType: machineType,
		Zone:        opts.Zone,
		Subnet:      hyperv1.GCPResourceName(opts.Subnet),
		Image:       opts.Image,
	}
}
