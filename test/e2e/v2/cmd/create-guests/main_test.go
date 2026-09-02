//go:build e2ev2

package main

import (
	"strconv"
	"testing"

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
					InitialNodePoolReplicas: tt.override,
				},
				name: "public-test",
			}
			args := buildCreateArgs(cfg, ns)
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
