//go:build e2ev2

package lifecycle

import (
	"slices"
	"strings"
	"testing"
)

func TestAzureCredentialsFileFromEnv(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want string
	}{
		{
			name: "When AZURE_CREDS is set, it should return the configured path",
			env:  "/etc/azure/custom-credentials.json",
			want: "/etc/azure/custom-credentials.json",
		},
		{
			name: "When AZURE_CREDS is unset, it should return the self-managed Azure default",
			want: defaultAzureCreds,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("AZURE_CREDS", test.env)
			if got := AzureCredentialsFileFromEnv(); got != test.want {
				t.Fatalf("AzureCredentialsFileFromEnv() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestAzurePlatformConfigClusterSpecs(t *testing.T) {
	t.Parallel()

	specs := (&AzurePlatformConfig{}).ClusterSpecs("release-image", "n1-image")
	specsByVariant := make(map[string]ClusterSpec, len(specs))
	for _, spec := range specs {
		specsByVariant[spec.Variant] = spec
	}

	tests := []struct {
		name    string
		variant string
		want    int
	}{
		{
			name:    "When public Azure variant is configured, it should request two initial replicas",
			variant: "public",
			want:    2,
		},
		{
			name:    "When upgrade Azure variant is configured, it should request two initial replicas",
			variant: "upgrade",
			want:    2,
		},
		{
			name:    "When private Azure variant is configured, it should request one initial replica",
			variant: "private",
			want:    1,
		},
		{
			name:    "When OAuth LoadBalancer Azure variant is configured, it should request one initial replica",
			variant: "oauth-lb",
			want:    1,
		},
		{
			name:    "When OAuth LoadBalancer private Azure variant is configured, it should request one initial replica",
			variant: "oauth-lb-private",
			want:    1,
		},
		{
			name:    "When external OIDC Azure variant is configured, it should request one initial replica",
			variant: "external-oidc",
			want:    1,
		},
	}

	if len(specsByVariant) != len(tests) {
		t.Fatalf("got %d Azure variants, want %d", len(specsByVariant), len(tests))
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			spec, ok := specsByVariant[tt.variant]
			if !ok {
				t.Fatalf("Azure variant %q was not configured", tt.variant)
			}
			if spec.InitialNodePoolReplicas == nil {
				t.Fatalf("Azure variant %q has no initial replica override", tt.variant)
			}
			if got := *spec.InitialNodePoolReplicas; got != tt.want {
				t.Errorf("Azure variant %q requests %d initial replicas, want %d", tt.variant, got, tt.want)
			}
		})
	}
}

func TestAzureOAuthLBPrivateExtraArgs(t *testing.T) {
	t.Parallel()

	specs := (&AzurePlatformConfig{}).ClusterSpecs("release-image", "n1-image")
	var spec *ClusterSpec
	for i := range specs {
		if specs[i].Variant == "oauth-lb-private" {
			spec = &specs[i]
			break
		}
	}
	if spec == nil {
		t.Fatal("oauth-lb-private variant was not configured")
	}

	// The oauth-lb-private variant combines private endpoint access with the
	// LoadBalancer OAuth publishing strategy; each flag encodes a distinct
	// contract that a regression could silently drop.
	wantArgs := []string{
		"--endpoint-access=Private",
		"--endpoint-access-private-nat-subnet-id=", // empty subnet id with zero-value config
		"--oauth-publishing-strategy=LoadBalancer",
	}
	for _, arg := range wantArgs {
		if !slices.Contains(spec.ExtraArgs, arg) {
			t.Errorf("oauth-lb-private ExtraArgs %v missing %q", spec.ExtraArgs, arg)
		}
	}
}

func TestAzurePlatformConfigCreateArgs(t *testing.T) {
	t.Run("When Azure E2E cluster arguments are built, it should enable managed boot diagnostics", func(t *testing.T) {
		if !slices.Contains((&AzurePlatformConfig{}).CreateArgs(), "--diagnostics-storage-account-type=Managed") {
			t.Fatal("boot diagnostics must be enabled before VM creation")
		}
	})
}

func TestAzurePlatformConfigTestMatrix(t *testing.T) {
	t.Run("When the default Azure plan runs, it should select diagnostics checks on public and private clusters", func(t *testing.T) {
		matrix := (&AzurePlatformConfig{}).TestMatrix()
		var foundPublic, foundPrivate, foundBootstrap bool
		for _, group := range matrix.Parallel {
			if group.Variant == "private" && strings.Contains(group.LabelFilter, "azure-machine-diagnostics") {
				foundPrivate = true
			}
		}
		for _, lane := range matrix.Sequential {
			for _, group := range lane.Steps {
				if group.Variant == "public" {
					if strings.Contains(group.LabelFilter, "azure-machine-diagnostics-bootstrap") {
						foundBootstrap = true
					} else if strings.Contains(group.LabelFilter, "azure-machine-diagnostics") {
						foundPublic = true
					}
				}
			}
		}
		if !foundPublic || !foundPrivate || !foundBootstrap {
			t.Fatal("Azure diagnostics checks are missing from the default test matrix")
		}
	})
}

func TestAzurePlatformConfigDestroyArgs(t *testing.T) {
	t.Run("When Azure teardown arguments are built, it should omit creation-only diagnostics flags", func(t *testing.T) {
		for _, arg := range (&AzurePlatformConfig{}).DestroyArgs() {
			if strings.HasPrefix(arg, "--diagnostics-") {
				t.Fatalf("teardown cannot accept %s", arg)
			}
		}
	})
}
