//go:build e2ev2

package main

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

func azureConfig(cli lifecycle.CLI) envConfig {
	return envConfig{
		baseDomain:   "example.com",
		nodeCount:    6,
		namespace:    "clusters",
		releaseImage: "release-image",
		pullSecret:   "/tmp/pull-secret",
		platform:     lifecycle.NewAzurePlatformConfig(""),
		cli:          cli,
	}
}

func TestBuildCreateArgs(t *testing.T) {
	t.Parallel()

	override := 2
	cfg := azureConfig(lifecycle.CLI{Kind: lifecycle.CLIKindHypershift, Binary: "hypershift"})

	tests := []struct {
		name     string
		override *int
		want     int
	}{
		{
			name:     "When ClusterSpec has an initial replica override, it should use that value",
			override: &override,
			want:     2,
		},
		{
			name: "When ClusterSpec has no initial replica override, it should use the global node count",
			want: 6,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ns := namedSpec{
				ClusterSpec: lifecycle.ClusterSpec{
					Variant:                 "public",
					InitialNodePoolReplicas: tt.override,
				},
				name: "public-test",
			}
			args, err := buildCreateArgs(cfg, ns)
			if err != nil {
				t.Fatalf("buildCreateArgs: %v", err)
			}
			want := "--node-pool-replicas=" + strconv.Itoa(tt.want)
			if !slices.Contains(args, want) {
				t.Errorf("create args %v do not contain %q", args, want)
			}
		})
	}
}

// TestBuildCreateArgsPerCLI asserts that the generated args are valid for
// whichever CLI is selected. buildCreateArgs validates them against the real
// command tree, so an unsupported flag surfaces here rather than in CI.
func TestBuildCreateArgsPerCLI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cli     lifecycle.CLI
		wantArg string
	}{
		{
			name:    "When the developer CLI is selected, it should use the developer flag spelling",
			cli:     lifecycle.CLI{Kind: lifecycle.CLIKindHypershift, Binary: "hypershift"},
			wantArg: "--assign-service-principal-roles",
		},
		{
			name:    "When the product CLI is selected, it should use the product flag spelling",
			cli:     lifecycle.CLI{Kind: lifecycle.CLIKindHCP, Binary: "hcp"},
			wantArg: "--auto-assign-roles",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ns := namedSpec{
				ClusterSpec: lifecycle.ClusterSpec{Variant: "public"},
				name:        "public-test",
			}
			args, err := buildCreateArgs(azureConfig(tt.cli), ns)
			if err != nil {
				t.Fatalf("buildCreateArgs: %v", err)
			}
			if !slices.Contains(args, tt.wantArg) {
				t.Errorf("create args %v do not contain %q", args, tt.wantArg)
			}
		})
	}
}

// TestBuildCreateArgsRejectsUnsupportedExtraArgs proves the validation is
// wired into arg construction, so a variant cannot smuggle a developer-only
// flag into a run driven by the product CLI.
func TestBuildCreateArgsRejectsUnsupportedExtraArgs(t *testing.T) {
	t.Parallel()

	cfg := azureConfig(lifecycle.CLI{Kind: lifecycle.CLIKindHCP, Binary: "hcp"})
	ns := namedSpec{
		ClusterSpec: lifecycle.ClusterSpec{
			Variant:   "public",
			ExtraArgs: []string{"--control-plane-operator-image=example.com/cpo:latest"},
		},
		name: "public-test",
	}

	args, err := buildCreateArgs(cfg, ns)
	if err == nil {
		t.Fatalf("buildCreateArgs() = %v, want error for a developer-only flag", args)
	}
}

func TestBuildCreateArgsAvailabilityPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		extraArgs []string
		want      string
	}{
		{
			name: "When the variant does not set an availability policy, it should pin the default explicitly",
			want: "--control-plane-availability-policy=SingleReplica",
		},
		{
			name:      "When the variant sets an availability policy, it should not be overridden",
			extraArgs: []string{"--control-plane-availability-policy=HighlyAvailable"},
			want:      "--control-plane-availability-policy=HighlyAvailable",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := azureConfig(lifecycle.CLI{Kind: lifecycle.CLIKindHCP, Binary: "hcp"})
			ns := namedSpec{
				ClusterSpec: lifecycle.ClusterSpec{Variant: "public", ExtraArgs: tt.extraArgs},
				name:        "public-test",
			}
			args, err := buildCreateArgs(cfg, ns)
			if err != nil {
				t.Fatalf("buildCreateArgs: %v", err)
			}

			var got []string
			for _, arg := range args {
				if strings.HasPrefix(arg, "--control-plane-availability-policy") {
					got = append(got, arg)
				}
			}
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("availability policy args = %v, want exactly [%q]", got, tt.want)
			}
		})
	}
}
