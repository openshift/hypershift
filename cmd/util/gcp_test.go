package util

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestDefaultGCPMachineType(t *testing.T) {
	tests := []struct {
		name     string
		arch     string
		expected string
	}{
		{
			name:     "When arch is amd64, it should return n2-standard-4",
			arch:     "amd64",
			expected: DefaultGCPMachineTypeAMD64,
		},
		{
			name:     "When arch is arm64, it should return t2a-standard-4",
			arch:     "arm64",
			expected: DefaultGCPMachineTypeARM64,
		},
		{
			name:     "When arch is empty, it should return empty string",
			arch:     "",
			expected: "",
		},
		{
			name:     "When arch is unknown, it should return empty string",
			arch:     "unknown",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DefaultGCPMachineType(tt.arch)
			if result != tt.expected {
				t.Errorf("DefaultGCPMachineType(%q) = %q, want %q", tt.arch, result, tt.expected)
			}
		})
	}
}

func TestBuildGCPNodePoolPlatform(t *testing.T) {
	tests := []struct {
		name     string
		opts     GCPNodePoolPlatformOptions
		validate func(*testing.T, *hyperv1.GCPNodePoolPlatform)
	}{
		{
			name: "When all fields provided, it should use them",
			opts: GCPNodePoolPlatformOptions{
				Zone:        "us-east1-b",
				Subnet:      "custom-subnet",
				MachineType: "n2-standard-8",
				Arch:        "amd64",
				Image:       "projects/test/images/custom",
			},
			validate: func(t *testing.T, platform *hyperv1.GCPNodePoolPlatform) {
				if platform.Zone != "us-east1-b" {
					t.Errorf("Zone = %q, want %q", platform.Zone, "us-east1-b")
				}
				if string(platform.Subnet) != "custom-subnet" {
					t.Errorf("Subnet = %q, want %q", platform.Subnet, "custom-subnet")
				}
				if platform.MachineType != "n2-standard-8" {
					t.Errorf("MachineType = %q, want %q", platform.MachineType, "n2-standard-8")
				}
				if platform.Image != "projects/test/images/custom" {
					t.Errorf("Image = %q, want %q", platform.Image, "projects/test/images/custom")
				}
			},
		},
		{
			name: "When MachineType empty and Arch is amd64, it should default to n2-standard-4",
			opts: GCPNodePoolPlatformOptions{
				Zone:   "us-central1-a",
				Subnet: "test-subnet",
				Arch:   "amd64",
			},
			validate: func(t *testing.T, platform *hyperv1.GCPNodePoolPlatform) {
				if platform.MachineType != DefaultGCPMachineTypeAMD64 {
					t.Errorf("MachineType = %q, want %q", platform.MachineType, DefaultGCPMachineTypeAMD64)
				}
			},
		},
		{
			name: "When MachineType empty and Arch is arm64, it should default to t2a-standard-4",
			opts: GCPNodePoolPlatformOptions{
				Zone:   "us-west1-a",
				Subnet: "test-subnet",
				Arch:   "arm64",
			},
			validate: func(t *testing.T, platform *hyperv1.GCPNodePoolPlatform) {
				if platform.MachineType != DefaultGCPMachineTypeARM64 {
					t.Errorf("MachineType = %q, want %q", platform.MachineType, DefaultGCPMachineTypeARM64)
				}
			},
		},
		{
			name: "When Image empty, it should be empty in platform",
			opts: GCPNodePoolPlatformOptions{
				Zone:   "us-central1-a",
				Subnet: "test-subnet",
				Arch:   "amd64",
			},
			validate: func(t *testing.T, platform *hyperv1.GCPNodePoolPlatform) {
				if platform.Image != "" {
					t.Errorf("Image = %q, want empty", platform.Image)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform := BuildGCPNodePoolPlatform(tt.opts)
			if platform == nil {
				t.Fatal("BuildGCPNodePoolPlatform returned nil")
			}
			tt.validate(t, platform)
		})
	}
}
