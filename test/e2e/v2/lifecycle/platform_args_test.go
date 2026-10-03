//go:build e2ev2

package lifecycle

import (
	"slices"
	"strings"
	"testing"
)

var (
	hcpCLI        = CLI{Kind: CLIKindHCP, Binary: "hcp"}
	hypershiftCLI = CLI{Kind: CLIKindHypershift, Binary: "hypershift"}
)

func TestAzureCreateArgsPerCLI(t *testing.T) {
	tests := []struct {
		name       string
		cli        CLI
		wantArg    string
		notWantArg string
	}{
		{
			name:       "When using the developer CLI, it should assign roles with the developer flag",
			cli:        hypershiftCLI,
			wantArg:    "--assign-service-principal-roles",
			notWantArg: "--auto-assign-roles",
		},
		{
			name:       "When using the product CLI, it should assign roles with the product flag",
			cli:        hcpCLI,
			wantArg:    "--auto-assign-roles",
			notWantArg: "--assign-service-principal-roles",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := NewAzurePlatformConfig("").CreateArgs(tt.cli)
			if err != nil {
				t.Fatalf("CreateArgs: %v", err)
			}
			if !slices.Contains(args, tt.wantArg) {
				t.Errorf("args %v do not contain %q", args, tt.wantArg)
			}
			if slices.Contains(args, tt.notWantArg) {
				t.Errorf("args %v unexpectedly contain %q", args, tt.notWantArg)
			}
		})
	}
}

func TestAWSArgsRequireAssumedRoleForProductCLI(t *testing.T) {
	withCreds := AWSPlatformOptions{
		Region:       "us-east-1",
		Zones:        "us-east-1a",
		RoleARN:      "arn:aws:iam::123456789012:role/e2e",
		STSCredsFile: "/tmp/sts-creds.json",
	}
	withoutCreds := AWSPlatformOptions{Region: "us-east-1", Zones: "us-east-1a"}

	tests := []struct {
		name     string
		cli      CLI
		opts     AWSPlatformOptions
		wantArgs []string
		wantErr  bool
	}{
		{
			name: "When using the developer CLI without credentials, it should rely on the AWS default credential chain",
			cli:  hypershiftCLI,
			opts: withoutCreds,
		},
		{
			name: "When using the developer CLI with credentials configured, it should still not pass them",
			cli:  hypershiftCLI,
			opts: withCreds,
		},
		{
			name:     "When using the product CLI with credentials, it should pass the assumed role",
			cli:      hcpCLI,
			opts:     withCreds,
			wantArgs: []string{"--role-arn=arn:aws:iam::123456789012:role/e2e", "--sts-creds=/tmp/sts-creds.json"},
		},
		{
			name:    "When using the product CLI without credentials, it should fail before provisioning",
			cli:     hcpCLI,
			opts:    withoutCreds,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := NewAWSPlatformConfig(tt.opts, "")

			for _, tc := range []struct {
				verb string
				args func() ([]string, error)
			}{
				{"create", func() ([]string, error) { return cfg.CreateArgs(tt.cli) }},
				{"destroy", func() ([]string, error) { return cfg.DestroyArgs(tt.cli) }},
			} {
				args, err := tc.args()
				if tt.wantErr {
					if err == nil {
						t.Errorf("%sArgs() = %v, want error", tc.verb, args)
						continue
					}
					for _, envVar := range []string{awsRoleARNEnvVar, awsSTSCredsEnvVar} {
						if !strings.Contains(err.Error(), envVar) {
							t.Errorf("%sArgs() error %v does not name %s", tc.verb, err, envVar)
						}
					}
					continue
				}
				if err != nil {
					t.Fatalf("%sArgs: %v", tc.verb, err)
				}
				for _, want := range tt.wantArgs {
					if !slices.Contains(args, want) {
						t.Errorf("%s args %v do not contain %q", tc.verb, args, want)
					}
				}
				if tt.wantArgs == nil {
					for _, arg := range args {
						if strings.HasPrefix(arg, "--role-arn") || strings.HasPrefix(arg, "--sts-creds") {
							t.Errorf("%s args %v unexpectedly contain %q", tc.verb, args, arg)
						}
					}
				}
			}
		})
	}
}

// TestPlatformArgsAreAcceptedByBothCLIs guards against a platform adding a
// flag that only one of the two CLIs binds. It checks the args the platforms
// actually emit, so it fails when a flag is renamed or dropped in either CLI.
func TestPlatformArgsAreAcceptedByBothCLIs(t *testing.T) {
	awsOpts := AWSPlatformOptions{
		Region:       "us-east-1",
		Zones:        "us-east-1a",
		RoleARN:      "arn:aws:iam::123456789012:role/e2e",
		STSCredsFile: "/tmp/sts-creds.json",
	}

	platforms := map[string]PlatformConfig{
		"aws":   NewAWSPlatformConfig(awsOpts, ""),
		"azure": NewAzurePlatformConfig(""),
	}

	for name, platform := range platforms {
		for _, cli := range []CLI{hypershiftCLI, hcpCLI} {
			t.Run(name+"/"+string(cli.Kind), func(t *testing.T) {
				createArgs, err := platform.CreateArgs(cli)
				if err != nil {
					t.Fatalf("CreateArgs: %v", err)
				}
				createArgs = append([]string{"create", "cluster", platform.Name()}, createArgs...)
				if err := ValidateArgs(cli, createArgs); err != nil {
					t.Errorf("create: %v", err)
				}

				destroyArgs, err := platform.DestroyArgs(cli)
				if err != nil {
					t.Fatalf("DestroyArgs: %v", err)
				}
				destroyArgs = append([]string{"destroy", "cluster", platform.Name()}, destroyArgs...)
				if err := ValidateArgs(cli, destroyArgs); err != nil {
					t.Errorf("destroy: %v", err)
				}
			})
		}
	}
}

// TestClusterSpecExtraArgsAreAcceptedByBothCLIs covers the per-variant flags,
// which are appended to the platform args and therefore also have to be
// spelled in a way both CLIs understand.
func TestClusterSpecExtraArgsAreAcceptedByBothCLIs(t *testing.T) {
	// A non-empty NAT subnet ID and encryption key keep the Azure private and
	// public variants from dropping their conditional flags.
	t.Setenv("AZURE_PRIVATE_NAT_SUBNET_ID", "/subscriptions/x/nat-subnet")
	t.Setenv("AZURE_ENCRYPTION_KEY_ID", "https://vault.example.com/keys/k/v")

	platforms := map[string]PlatformConfig{
		"aws":   NewAWSPlatformConfig(AWSPlatformOptions{Region: "us-east-1", Zones: "us-east-1a"}, ""),
		"azure": NewAzurePlatformConfig(""),
	}

	for name, platform := range platforms {
		specs := platform.ClusterSpecs("release:latest", "release:n1")
		if len(specs) == 0 {
			t.Fatalf("platform %s returned no cluster specs", name)
		}
		for _, spec := range specs {
			for _, cli := range []CLI{hypershiftCLI, hcpCLI} {
				t.Run(name+"/"+spec.Variant+"/"+string(cli.Kind), func(t *testing.T) {
					args := append([]string{"create", "cluster", platform.Name()}, spec.ExtraArgs...)
					if err := ValidateArgs(cli, args); err != nil {
						t.Errorf("variant %q: %v", spec.Variant, err)
					}
				})
			}
		}
	}
}
