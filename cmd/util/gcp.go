package util

import (
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

const (
	DefaultGCPMachineTypeAMD64 = "n2-standard-4"
	DefaultGCPMachineTypeARM64 = "t2a-standard-4"

	GCPMachineTypeHelp = "GCP machine type for node instances (default: " +
		DefaultGCPMachineTypeAMD64 + " for AMD64, " +
		DefaultGCPMachineTypeARM64 + " for ARM64)"
)

// DefaultGCPMachineType returns the default machine type for arch,
// or an empty string if the architecture is unsupported.
func DefaultGCPMachineType(arch string) string {
	switch strings.ToLower(arch) {
	case hyperv1.ArchitectureARM64:
		return DefaultGCPMachineTypeARM64
	case hyperv1.ArchitectureAMD64:
		return DefaultGCPMachineTypeAMD64
	default:
		// Empty string will fail API validation (machineType has +kubebuilder:validation:MinLength=1)
		return ""
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
