//go:build e2ev2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openshift/hypershift/test/e2e/v2/lifecycle"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestRunWithManagementClient(t *testing.T) {
	for _, test := range []struct {
		name, extraArgs, wantError string
	}{
		{name: "When east common arguments have a separate west override, it should reject before automatic OIDC or SDK construction", extraArgs: "--region us-west-2", wantError: "differs from manifest destroy region"},
		{name: "When east common arguments have an equals west override, it should reject before automatic OIDC or SDK construction", extraArgs: "--region=us-west-2", wantError: "differs from manifest destroy region"},
		{name: "When a separate region flag lacks its value, it should reject before automatic OIDC or SDK construction", extraArgs: "--region", wantError: "invalid AWS fixture create region"},
		{name: "When a separate region flag is followed by another flag, it should reject before automatic OIDC or SDK construction", extraArgs: "--region --zones=us-west-2a", wantError: "invalid AWS fixture create region"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("EXTRA_ARGS", test.extraArgs)
			dir := t.TempDir()
			cfg := envConfig{
				platform:     lifecycle.NewAWSPlatformConfig(lifecycle.AWSPlatformOptions{Region: "us-east-1", Zones: "us-east-1a"}, dir),
				sharedDir:    dir,
				releaseImage: discoveredReleaseImage,
				prowJobID:    "region-preflight",
			}
			err := runWithManagementClient(t.Context(), cfg, func() (crclient.WithWatch, error) {
				t.Fatal("region rejection reached management construction before automatic shared OIDC setup")
				return nil, nil
			})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("unexpected early region rejection: %v", err)
			}
			for _, file := range []string{lifecycle.ManifestFileName, lifecycle.SharedOIDCStateFileName} {
				if _, err := os.Stat(filepath.Join(dir, file)); !os.IsNotExist(err) {
					t.Fatalf("invalid region reached persistent setup %s: %v", file, err)
				}
			}
		})
	}
}
