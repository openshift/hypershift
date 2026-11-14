package kas

import (
	"slices"
	"testing"

	hcpconfig "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/omnicpo"

	kcpv1 "github.com/openshift/api/kubecontrolplane/v1"
)

type fakeOmniContext struct{ version string }

func (f fakeOmniContext) ControlPlaneVersion() string { return f.version }
func (f fakeOmniContext) GetImage(string) string      { return "" }

func TestKasConfigTransforms(t *testing.T) {
	const alphaRuntimeConfig = "admissionregistration.k8s.io/v1alpha1=true"
	withMAP := []string{"MutatingAdmissionPolicy=true"}
	newConfig := func() *kcpv1.KubeAPIServerConfig {
		return &kcpv1.KubeAPIServerConfig{APIServerArguments: map[string]kcpv1.Arguments{"runtime-config": {}}}
	}
	hasAlpha := func(cfg *kcpv1.KubeAPIServerConfig) bool {
		return slices.Contains(cfg.APIServerArguments["runtime-config"], alphaRuntimeConfig)
	}

	t.Run("When driving a pre-4.23 plane with MutatingAdmissionPolicy enabled, it should re-emit the v1alpha1 runtime-config", func(t *testing.T) {
		t.Setenv(hcpconfig.OmniCPOEnvVar, "1")
		cfg := newConfig()
		if err := omnicpo.Apply(fakeOmniContext{version: "4.20.0"}, cfg, kasConfigTransforms(withMAP)); err != nil {
			t.Fatal(err)
		}
		if !hasAlpha(cfg) {
			t.Errorf("expected v1alpha1 runtime-config, got %v", cfg.APIServerArguments["runtime-config"])
		}
	})

	t.Run("When driving a 4.23 or newer plane, it should not re-emit the v1alpha1 runtime-config", func(t *testing.T) {
		t.Setenv(hcpconfig.OmniCPOEnvVar, "1")
		cfg := newConfig()
		if err := omnicpo.Apply(fakeOmniContext{version: "5.0.0"}, cfg, kasConfigTransforms(withMAP)); err != nil {
			t.Fatal(err)
		}
		if hasAlpha(cfg) {
			t.Error("expected no v1alpha1 runtime-config for a 5.0 plane")
		}
	})

	t.Run("When MutatingAdmissionPolicy is not enabled, it should not re-emit the runtime-config", func(t *testing.T) {
		t.Setenv(hcpconfig.OmniCPOEnvVar, "1")
		cfg := newConfig()
		if err := omnicpo.Apply(fakeOmniContext{version: "4.20.0"}, cfg, kasConfigTransforms(nil)); err != nil {
			t.Fatal(err)
		}
		if hasAlpha(cfg) {
			t.Error("expected no v1alpha1 runtime-config when the gate is off")
		}
	})

	t.Run("When OMNI-CPO is off, it should not change the runtime-config", func(t *testing.T) {
		t.Setenv(hcpconfig.OmniCPOEnvVar, "0")
		cfg := newConfig()
		if err := omnicpo.Apply(fakeOmniContext{version: "4.20.0"}, cfg, kasConfigTransforms(withMAP)); err != nil {
			t.Fatal(err)
		}
		if hasAlpha(cfg) {
			t.Error("expected no change when OMNI-CPO is off")
		}
	})
}
