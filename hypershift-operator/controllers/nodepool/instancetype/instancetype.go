package instancetype

import (
	"context"
)

// CPUArch represents a normalized CPU architecture type.
type CPUArch string

const (
	// ArchAMD64 is the normalized architecture name for amd64/x86_64.
	ArchAMD64 CPUArch = "amd64"
	// ArchARM64 is the normalized architecture name for arm64/aarch64.
	ArchARM64 CPUArch = "arm64"
)

// Provider knows how to fetch instance type information for a given cloud platform.
// Different cloud providers (AWS, Azure, GCP, etc.) implement this interface to provide
// instance type specifications needed for cluster autoscaler scale-from-zero functionality.
type Provider interface {
	// GetInstanceTypeInfo returns the specifications for a given instance type.
	// The instanceType parameter is the cloud provider specific instance type name
	// (e.g., "m5.large" for AWS, "Standard_D4s_v3" for Azure).
	GetInstanceTypeInfo(ctx context.Context, instanceType string) (*InstanceTypeInfo, error)
}

// InstanceTypeInfo contains cloud instance type specifications.
// This information is used to populate cluster autoscaler capacity annotations
// for scaling from zero replicas.
type InstanceTypeInfo struct {
	// InstanceType is the cloud provider specific instance type name
	InstanceType string

	// VCPU is the number of virtual CPUs
	VCPU int32

	// MemoryMb is the amount of memory in megabytes
	MemoryMb int64

	// GPU is the number of GPUs (0 if none)
	GPU int32

	// CPUArchitecture is the normalized CPU architecture
	CPUArchitecture CPUArch
}
