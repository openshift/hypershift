package backwardcompat

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/mod/modfile"
)

// This file holds a temporary guard against an unplanned bump of github.com/openshift/api.
//
// Bumping openshift/api past the pinned version brings in openshift/api#3086, which removes the
// ImageStreamImportMode feature gate. After `make api`, spec.configuration.image.imageStreamImportMode
// becomes part of the Default HostedCluster and HostedControlPlane CRDs, so the API server starts
// accepting non-empty values (Legacy, PreserveOriginal) that it prunes today.
//
// The problem is a hash mismatch on HostedClusters running a 4.14-4.18 control plane:
//   - The HyperShift operator hashes the HostedCluster configuration with the new types and writes the
//     result to the NodePool token secret (hc-configuration-hash). An empty value is stripped, a non-empty
//     one is included:
//     https://github.com/openshift/hypershift/blob/a8057ec9644494852f2356f8b29521bbca3e0292/hypershift-operator/controllers/nodepool/token.go#L135
//     https://github.com/openshift/hypershift/blob/a8057ec9644494852f2356f8b29521bbca3e0292/support/backwardcompat/backwardcompat.go#L29-L34
//   - The control-plane-operator of 4.14-4.18 releases vendors openshift/api types without the field, so
//     it drops the value when decoding the HostedControlPlane and writes a different hash to the
//     machine-config-server ConfigMap (configuration-hash):
//     https://github.com/openshift/hypershift/blob/6a1fa56f8b2a2546f74f203d986cb90541d743b5/control-plane-operator/controllers/hostedcontrolplane/mcs/params.go#L49
//   - The ignition server (from the same 4.14-4.18 payload) requires the two hashes to match and keeps
//     returning "machine-config-server configmap is out of date", so new nodes cannot get their payload
//     and a rollout or scale-up of that NodePool never completes:
//     https://github.com/openshift/hypershift/blob/6a1fa56f8b2a2546f74f203d986cb90541d743b5/ignition-server/controllers/local_ignitionprovider.go#L170
//
// Control planes on 4.19 or later have the field in their types and use the same backwardcompat hash,
// so they are not affected. If the field stays unset or empty, the hashes match on every version.
//
// The bump also changes how the field is serialized (openshift/api#3159 and #3172 add omitempty) and
// requires bumping openshift/client-go in the same change.
//
// The bump needs a guard for this first; see https://issues.redhat.com/browse/CNTRLPLANE-5059.
// Remove this file in the PR that bumps openshift/api together with that guard.

const (
	openShiftAPIModulePath    = "github.com/openshift/api"
	pinnedOpenShiftAPIVersion = "v0.0.0-20261028163820-1f109e9eefc7"
)

const openShiftAPIPinMessage = `

This failure is intentional: this test blocks bumping github.com/openshift/api past ` + pinnedOpenShiftAPIVersion + `.
Do NOT update the pinned version or delete this test as part of a routine dependency bump.

Bumping openshift/api past this version exposes spec.configuration.image.imageStreamImportMode on the
HostedCluster and HostedControlPlane CRDs (openshift/api#3086). Control-plane-operators of 4.14-4.18
releases vendor openshift/api types without this field. If a user sets it to a non-empty value on such a
HostedCluster, the HyperShift operator includes it in the token configuration hash while the older
control-plane-operator drops it from the machine-config-server configuration hash. The ignition server
check then keeps failing ("machine-config-server configmap is out of date") and new nodes cannot join.
See the comment at the top of this file for the code links. The bump also changes how the field is
serialized (openshift/api#3159, #3172) and requires bumping openshift/client-go.

The bump needs a guard for this first. Details and plan: https://issues.redhat.com/browse/CNTRLPLANE-5059
Remove this test in the PR that bumps openshift/api together with that guard.`

//nolint:hypershiftlinter // testfuncstructure: temporary dependency pin for CNTRLPLANE-5059, not tied to a production function; remove it in the openshift/api bump PR.
func TestOpenShiftAPIVersionPin(t *testing.T) {
	repoRoot := filepath.Join("..", "..")

	tests := []struct {
		name   string
		goMod  string
		module string
	}{
		{
			name:   "When the root module requires openshift/api, it should stay at the pinned version",
			goMod:  filepath.Join(repoRoot, "go.mod"),
			module: "github.com/openshift/hypershift",
		},
		{
			name:   "When the api module requires openshift/api, it should stay at the pinned version",
			goMod:  filepath.Join(repoRoot, "api", "go.mod"),
			module: "github.com/openshift/hypershift/api",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := os.ReadFile(tt.goMod)
			if err != nil {
				t.Fatalf("failed to read %s: %v", tt.goMod, err)
			}
			file, err := modfile.Parse(tt.goMod, data, nil)
			if err != nil {
				t.Fatalf("failed to parse %s: %v", tt.goMod, err)
			}
			if file.Module == nil || file.Module.Mod.Path != tt.module {
				t.Fatalf("%s does not declare module %s; update this test if the module layout changed", tt.goMod, tt.module)
			}

			var versions []string
			for _, req := range file.Require {
				if req.Mod.Path == openShiftAPIModulePath {
					versions = append(versions, req.Mod.Version)
				}
			}
			if len(versions) != 1 {
				t.Fatalf("expected exactly one require of %s in %s, found %d (%v)"+openShiftAPIPinMessage,
					openShiftAPIModulePath, tt.goMod, len(versions), versions)
			}
			if versions[0] != pinnedOpenShiftAPIVersion {
				t.Fatalf("%s requires %s %s, but this test pins it to %s"+openShiftAPIPinMessage,
					tt.goMod, openShiftAPIModulePath, versions[0], pinnedOpenShiftAPIVersion)
			}

			for _, rep := range file.Replace {
				if rep.Old.Path == openShiftAPIModulePath {
					t.Fatalf("%s replaces %s with %s %s; replacing it bypasses this pin"+openShiftAPIPinMessage,
						tt.goMod, openShiftAPIModulePath, rep.New.Path, rep.New.Version)
				}
			}
		})
	}
}
