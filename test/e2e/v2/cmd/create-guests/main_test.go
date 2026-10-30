//go:build e2ev2

package main

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"
)

func TestBuildCreateArgs(t *testing.T) {
	t.Parallel()

	override := 2
	cfg := envConfig{
		baseDomain:   "example.com",
		nodeCount:    6,
		namespace:    "clusters",
		releaseImage: "release-image",
		pullSecret:   "/tmp/pull-secret",
		platform:     lifecycle.NewAzurePlatformConfig(""),
	}

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
					CreateArgs:              []string{"--location=centralus", "--encryption-key-id=test-key"},
					DestroyArgs:             []string{"--cluster-grace-period=40m"},
					InitialNodePoolReplicas: tt.override,
				},
				name: "public-test",
			}
			args := buildCreateArgs(cfg, ns)
			if slices.Contains(args, ns.DestroyArgs[0]) {
				t.Errorf("create args %v contain destroy-only argument %q", args, ns.DestroyArgs[0])
			}
			if !slices.Equal(args[len(args)-len(ns.CreateArgs):], ns.CreateArgs) {
				t.Errorf("create args %v do not end with spec arguments %v", args, ns.CreateArgs)
			}
			want := "--node-pool-replicas=" + strconv.Itoa(tt.want)
			for _, arg := range args {
				if arg == want {
					return
				}
			}
			t.Errorf("create args %v do not contain %q", args, want)
		})
	}
}

// TestBuildCreateArgsPinsGuestAndOverridesCPO verifies the main-CPO-on-4.20 experiment
// wiring: buildCreateArgs always uses the pinned base release (never a per-spec
// ReleaseImage), and emits the control-plane-operator-image override annotation.
func TestBuildCreateArgsPinsGuestAndOverridesCPO(t *testing.T) {
	t.Parallel()

	const cpoImage = "quay.io/example/hypershift:pr-build"
	cfg := envConfig{
		baseDomain:       "example.com",
		nodeCount:        3,
		namespace:        "clusters",
		releaseImage:     e2eutil.PinnedGuestReleaseImage, // already pinned in loadEnvConfig
		pullSecret:       "/tmp/pull-secret",
		cpoOverrideImage: cpoImage,
		platform:         lifecycle.NewAzurePlatformConfig(""),
	}

	// Even when a spec carries its own ReleaseImage (as the "upgrade" variant does),
	// buildCreateArgs must NOT let it leak a non-pinned guest at create time.
	ns := namedSpec{
		ClusterSpec: lifecycle.ClusterSpec{
			Variant:      "upgrade",
			ReleaseImage: "quay.io/example/ocp-release:4.99.0-should-not-leak",
		},
		name: "upgrade-test",
	}

	args := buildCreateArgs(cfg, ns)

	wantRelease := "--release-image=" + e2eutil.PinnedGuestReleaseImage
	if !slices.Contains(args, wantRelease) {
		t.Errorf("create args %v do not contain pinned %q", args, wantRelease)
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--release-image=") && arg != wantRelease {
			t.Errorf("per-spec ReleaseImage leaked into create args: %q", arg)
		}
	}

	wantAnnotation := "--annotations=" + hyperv1.ControlPlaneOperatorImageAnnotation + "=" + cpoImage
	if !slices.Contains(args, wantAnnotation) {
		t.Errorf("create args %v do not contain CPO override annotation %q", args, wantAnnotation)
	}
}
