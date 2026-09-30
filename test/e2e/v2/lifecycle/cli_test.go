//go:build e2ev2

package lifecycle

import (
	"strings"
	"testing"
)

func TestLifecycleCLI(t *testing.T) {
	tests := []struct {
		name             string
		hypershiftBinary string
		hcpBinary        string
		want             CLI
	}{
		{
			name: "When neither binary is set, it should default to hypershift on PATH",
			want: CLI{Kind: CLIKindHypershift, Binary: "hypershift"},
		},
		{
			name:             "When only HYPERSHIFT_BINARY is set, it should use the developer CLI",
			hypershiftBinary: "/hypershift/bin/hypershift",
			want:             CLI{Kind: CLIKindHypershift, Binary: "/hypershift/bin/hypershift"},
		},
		{
			name:      "When only HCP_BINARY is set, it should use the product CLI",
			hcpBinary: "/hypershift/bin/hcp",
			want:      CLI{Kind: CLIKindHCP, Binary: "/hypershift/bin/hcp"},
		},
		{
			name:             "When both binaries are set, HCP_BINARY should win",
			hypershiftBinary: "/hypershift/bin/hypershift",
			hcpBinary:        "/hypershift/bin/hcp",
			want:             CLI{Kind: CLIKindHCP, Binary: "/hypershift/bin/hcp"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(HypershiftBinaryEnvVar, tt.hypershiftBinary)
			t.Setenv(HCPBinaryEnvVar, tt.hcpBinary)

			if got := LifecycleCLI(); got != tt.want {
				t.Errorf("LifecycleCLI() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDeveloperCLIIgnoresHCPBinary(t *testing.T) {
	t.Setenv(HypershiftBinaryEnvVar, "/hypershift/bin/hypershift")
	t.Setenv(HCPBinaryEnvVar, "/hypershift/bin/hcp")

	want := CLI{Kind: CLIKindHypershift, Binary: "/hypershift/bin/hypershift"}
	if got := DeveloperCLI(); got != want {
		t.Errorf("DeveloperCLI() = %v, want %v", got, want)
	}
}

func TestValidateArgs(t *testing.T) {
	hcp := CLI{Kind: CLIKindHCP, Binary: "hcp"}
	hypershift := CLI{Kind: CLIKindHypershift, Binary: "hypershift"}

	tests := []struct {
		name    string
		cli     CLI
		args    []string
		wantErr string
	}{
		{
			name: "When every flag is a shared core flag, it should accept them for the product CLI",
			cli:  hcp,
			args: []string{"create", "cluster", "azure", "--name=x", "--generate-ssh", "--node-pool-replicas=2"},
		},
		{
			name: "When a flag is passed separately from its value, it should not treat the value as a flag",
			cli:  hcp,
			args: []string{"create", "cluster", "azure", "--name", "not-a-flag"},
		},
		{
			name:    "When the product CLI is given a developer-only flag, it should reject it",
			cli:     hcp,
			args:    []string{"create", "cluster", "aws", "--infra-json=/tmp/infra.json"},
			wantErr: `"hcp create cluster aws" does not accept --infra-json`,
		},
		{
			name:    "When the product CLI is given the developer spelling of assign roles, it should reject it",
			cli:     hcp,
			args:    []string{"create", "cluster", "azure", "--assign-service-principal-roles"},
			wantErr: `"hcp create cluster azure" does not accept --assign-service-principal-roles`,
		},
		{
			name: "When the product CLI is given its own spelling of assign roles, it should accept it",
			cli:  hcp,
			args: []string{"create", "cluster", "azure", "--auto-assign-roles"},
		},
		{
			name:    "When the developer CLI is given the product spelling of assign roles, it should reject it",
			cli:     hypershift,
			args:    []string{"create", "cluster", "azure", "--auto-assign-roles"},
			wantErr: `"hypershift create cluster azure" does not accept --auto-assign-roles`,
		},
		{
			name: "When the developer CLI is given developer-only flags, it should accept them",
			cli:  hypershift,
			args: []string{"create", "cluster", "aws", "--infra-json=/tmp/infra.json", "--single-nat-gateway"},
		},
		{
			name: "When the product CLI is given assumed-role credentials, it should accept them",
			cli:  hcp,
			args: []string{"create", "cluster", "aws", "--role-arn=arn:aws:iam::1:role/r", "--sts-creds=/tmp/sts"},
		},
		{
			name: "When destroying, it should accept the platform destroy flags for both CLIs",
			cli:  hcp,
			args: []string{"destroy", "cluster", "azure", "--azure-creds=/tmp/creds", "--location=centralus", "--dns-zone-rg-name=os4-common", "--cluster-grace-period=40m"},
		},
		{
			name:    "When several flags are unsupported, it should report all of them sorted",
			cli:     hcp,
			args:    []string{"create", "cluster", "aws", "--single-nat-gateway", "--infra-json=/tmp/infra.json"},
			wantErr: "--infra-json, --single-nat-gateway",
		},
		{
			name:    "When the subcommand does not exist, it should report the command path",
			cli:     hcp,
			args:    []string{"dump", "cluster", "--name=x"},
			wantErr: `"hcp" has no subcommand "dump"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateArgs(tt.cli, tt.args)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("ValidateArgs() = %v, want nil", err)
			case tt.wantErr != "" && err == nil:
				t.Errorf("ValidateArgs() = nil, want error containing %q", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("ValidateArgs() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
