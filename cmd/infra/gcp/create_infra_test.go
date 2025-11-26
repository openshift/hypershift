package gcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateInfraOptionsValidate(t *testing.T) {
	tests := []struct {
		name          string
		opts          *CreateInfraOptions
		expectedError string
	}{
		{
			name: "When all required fields are provided it should pass validation",
			opts: &CreateInfraOptions{
				ProjectID: "test-project-id",
				InfraID:   "test-infra-id",
				Region:    "us-central1",
			},
		},
		{
			name: "When project-id is missing it should return error",
			opts: &CreateInfraOptions{
				ProjectID: "",
				InfraID:   "test-infra-id",
				Region:    "us-central1",
			},
			expectedError: "--project-id is required",
		},
		{
			name: "When infra-id is missing it should return error",
			opts: &CreateInfraOptions{
				ProjectID: "test-project-id",
				InfraID:   "",
				Region:    "us-central1",
			},
			expectedError: "--infra-id is required",
		},
		{
			name: "When region is missing it should return error",
			opts: &CreateInfraOptions{
				ProjectID: "test-project-id",
				InfraID:   "test-infra-id",
				Region:    "",
			},
			expectedError: "--region is required",
		},
		{
			name: "When all fields including optional VPCCidr are provided it should pass validation",
			opts: &CreateInfraOptions{
				ProjectID: "test-project-id",
				InfraID:   "test-infra-id",
				Region:    "us-central1",
				VPCCidr:   "10.0.0.0/24",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.Validate()

			if tt.expectedError != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.expectedError)
					return
				}
				if !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestCreateInfraOptionsOutput(t *testing.T) {
	tests := []struct {
		name          string
		outputFile    string
		result        *CreateInfraOutput
		expectedError string
		validateJSON  bool
	}{
		{
			name:       "When output file is specified it should write JSON to file",
			outputFile: "output.json",
			result: &CreateInfraOutput{
				Region:           "us-central1",
				ProjectID:        "test-project",
				InfraID:          "test-infra",
				NetworkName:      "test-infra-network",
				NetworkSelfLink:  "https://www.googleapis.com/compute/v1/projects/test-project/global/networks/test-infra-network",
				SubnetName:       "test-infra-subnet",
				SubnetSelfLink:   "https://www.googleapis.com/compute/v1/projects/test-project/regions/us-central1/subnetworks/test-infra-subnet",
				SubnetCIDR:       "10.0.0.0/24",
				RouterName:       "test-infra-router",
				NATName:          "test-infra-nat",
				FirewallRuleName: "test-infra-egress-allow",
			},
			validateJSON: true,
		},
		{
			name:       "When output file is in invalid directory it should return error",
			outputFile: "/nonexistent/directory/output.json",
			result: &CreateInfraOutput{
				ProjectID: "test-project",
			},
			expectedError: "cannot create output file",
		},
		{
			name:       "When output file is empty string it should write to stdout without error",
			outputFile: "",
			result: &CreateInfraOutput{
				Region:    "us-central1",
				ProjectID: "test-project",
				InfraID:   "test-infra",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			var outputPath string

			if tt.outputFile != "" && !filepath.IsAbs(tt.outputFile) {
				outputPath = filepath.Join(tmpDir, tt.outputFile)
			} else {
				outputPath = tt.outputFile
			}

			opts := &CreateInfraOptions{
				OutputFile: outputPath,
			}

			err := opts.Output(tt.result)

			if tt.expectedError != "" {
				if err == nil {
					t.Errorf("expected error containing %q, got nil", tt.expectedError)
					return
				}
				if !strings.Contains(err.Error(), tt.expectedError) {
					t.Errorf("expected error containing %q, got %q", tt.expectedError, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("expected no error, got %v", err)
				return
			}

			if tt.validateJSON && outputPath != "" {
				// Read the file and validate it's valid JSON
				data, err := os.ReadFile(outputPath)
				if err != nil {
					t.Fatalf("failed to read output file: %v", err)
				}

				// Unmarshal to validate JSON structure
				var output CreateInfraOutput
				if err := json.Unmarshal(data, &output); err != nil {
					t.Errorf("output is not valid JSON: %v", err)
					return
				}

				// Validate content matches
				if output.Region != tt.result.Region {
					t.Errorf("expected Region %q, got %q", tt.result.Region, output.Region)
				}
				if output.ProjectID != tt.result.ProjectID {
					t.Errorf("expected ProjectID %q, got %q", tt.result.ProjectID, output.ProjectID)
				}
				if output.InfraID != tt.result.InfraID {
					t.Errorf("expected InfraID %q, got %q", tt.result.InfraID, output.InfraID)
				}
				if output.NetworkName != tt.result.NetworkName {
					t.Errorf("expected NetworkName %q, got %q", tt.result.NetworkName, output.NetworkName)
				}
				if output.SubnetName != tt.result.SubnetName {
					t.Errorf("expected SubnetName %q, got %q", tt.result.SubnetName, output.SubnetName)
				}
				if output.SubnetCIDR != tt.result.SubnetCIDR {
					t.Errorf("expected SubnetCIDR %q, got %q", tt.result.SubnetCIDR, output.SubnetCIDR)
				}
				if output.RouterName != tt.result.RouterName {
					t.Errorf("expected RouterName %q, got %q", tt.result.RouterName, output.RouterName)
				}
				if output.NATName != tt.result.NATName {
					t.Errorf("expected NATName %q, got %q", tt.result.NATName, output.NATName)
				}
				if output.FirewallRuleName != tt.result.FirewallRuleName {
					t.Errorf("expected FirewallRuleName %q, got %q", tt.result.FirewallRuleName, output.FirewallRuleName)
				}
			}
		})
	}
}

func TestNetworkManagerFormatNames(t *testing.T) {
	tests := []struct {
		name        string
		infraID     string
		expectedNet string
		expectedSub string
		expectedRtr string
		expectedNAT string
		expectedFW  string
	}{
		{
			name:        "When infraID is simple it should format names correctly",
			infraID:     "my-cluster",
			expectedNet: "my-cluster-network",
			expectedSub: "my-cluster-subnet",
			expectedRtr: "my-cluster-router",
			expectedNAT: "my-cluster-nat",
			expectedFW:  "my-cluster-egress-allow",
		},
		{
			name:        "When infraID has numbers it should format names correctly",
			infraID:     "cluster-12345",
			expectedNet: "cluster-12345-network",
			expectedSub: "cluster-12345-subnet",
			expectedRtr: "cluster-12345-router",
			expectedNAT: "cluster-12345-nat",
			expectedFW:  "cluster-12345-egress-allow",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nm := &NetworkManager{
				infraID: tt.infraID,
			}

			if got := nm.formatNetworkName(); got != tt.expectedNet {
				t.Errorf("formatNetworkName() = %q, want %q", got, tt.expectedNet)
			}
			if got := nm.formatSubnetName(); got != tt.expectedSub {
				t.Errorf("formatSubnetName() = %q, want %q", got, tt.expectedSub)
			}
			if got := nm.formatRouterName(); got != tt.expectedRtr {
				t.Errorf("formatRouterName() = %q, want %q", got, tt.expectedRtr)
			}
			if got := nm.formatNATName(); got != tt.expectedNAT {
				t.Errorf("formatNATName() = %q, want %q", got, tt.expectedNAT)
			}
			if got := nm.formatFirewallName(); got != tt.expectedFW {
				t.Errorf("formatFirewallName() = %q, want %q", got, tt.expectedFW)
			}
		})
	}
}

func TestDefaultSubnetCIDR(t *testing.T) {
	if DefaultSubnetCIDR != "10.0.0.0/24" {
		t.Errorf("DefaultSubnetCIDR = %q, want %q", DefaultSubnetCIDR, "10.0.0.0/24")
	}
}
