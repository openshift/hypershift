package gcp

import (
	"context"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	gcpinfra "github.com/openshift/hypershift/cmd/infra/gcp"
	"github.com/openshift/hypershift/cmd/log"
)

func TestNewDestroyCommand(t *testing.T) {
	g := NewGomegaWithT(t)
	opts := &core.DestroyOptions{Log: log.Log}

	cmd := NewDestroyCommand(opts)

	g.Expect(cmd).ToNot(BeNil())
	g.Expect(cmd.Use).To(Equal("gcp"))
	g.Expect(opts.GCPPlatform.PreserveIAM).To(BeFalse())
	g.Expect(opts.GCPPlatform.PreserveInfra).To(BeFalse())

	cmd.ParseFlags([]string{"--preserve-iam", "--project-id", "test-proj", "--region", "us-west1"})
	g.Expect(opts.GCPPlatform.PreserveIAM).To(BeTrue())
	g.Expect(opts.GCPPlatform.ProjectID).To(Equal("test-proj"))
	g.Expect(opts.GCPPlatform.Region).To(Equal("us-west1"))
}

func TestExtractParameters(t *testing.T) {
	g := NewGomegaWithT(t)

	opts := &core.DestroyOptions{Log: log.Log}
	hc := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			InfraID: "test-infra",
			Platform: hyperv1.PlatformSpec{
				GCP: &hyperv1.GCPPlatformSpec{
					Project: "test-project",
					Region:  "us-central1",
				},
			},
		},
	}

	extractParameters(hc, opts)

	g.Expect(opts.InfraID).To(Equal("test-infra"))
	g.Expect(opts.GCPPlatform.ProjectID).To(Equal("test-project"))
	g.Expect(opts.GCPPlatform.Region).To(Equal("us-central1"))
}

func TestDestroyPlatformSpecifics(t *testing.T) {
	tests := map[string]struct {
		preserveIAM        bool
		preserveInfra      bool
		expectIAMCalled    bool
		expectInfraCalled  bool
	}{
		"When both preserve flags are true, no destroy operations should run": {
			preserveIAM:       true,
			preserveInfra:     true,
			expectIAMCalled:   false,
			expectInfraCalled: false,
		},
		"When preserve-iam is true and preserve-infra is false, only infra destroy should run": {
			preserveIAM:       true,
			preserveInfra:     false,
			expectIAMCalled:   false,
			expectInfraCalled: true,
		},
		"When preserve-iam is false and preserve-infra is true, only IAM destroy should run": {
			preserveIAM:       false,
			preserveInfra:     true,
			expectIAMCalled:   true,
			expectInfraCalled: false,
		},
		"When both preserve flags are false, both destroy operations should run": {
			preserveIAM:       false,
			preserveInfra:     false,
			expectIAMCalled:   true,
			expectInfraCalled: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Track whether destroy functions were called
			iamCalled := false
			infraCalled := false

			// Stub out the destroy functions
			originalIAM := runDestroyIAM
			originalInfra := runDestroyInfra
			defer func() {
				runDestroyIAM = originalIAM
				runDestroyInfra = originalInfra
			}()

			runDestroyIAM = func(ctx context.Context, opts gcpinfra.DestroyIAMOptions, log logr.Logger) error {
				iamCalled = true
				return nil
			}
			runDestroyInfra = func(ctx context.Context, opts gcpinfra.DestroyInfraOptions, log logr.Logger) error {
				infraCalled = true
				return nil
			}

			opts := &core.DestroyOptions{
				InfraID: "test-infra",
				Log:     log.Log,
				GCPPlatform: core.GCPPlatformDestroyOptions{
					ProjectID:     "test-project",
					Region:        "us-central1",
					PreserveIAM:   test.preserveIAM,
					PreserveInfra: test.preserveInfra,
				},
			}

			err := destroyPlatformSpecifics(context.Background(), opts)

			g.Expect(err).To(BeNil(), "Should not error with stubbed destroy functions")
			g.Expect(iamCalled).To(Equal(test.expectIAMCalled), "IAM destroy called status should match expectation")
			g.Expect(infraCalled).To(Equal(test.expectInfraCalled), "Infrastructure destroy called status should match expectation")
		})
	}
}

func TestValidateInputs(t *testing.T) {
	tests := map[string]struct {
		opts        *core.DestroyOptions
		expectError bool
	}{
		"valid inputs": {
			opts: &core.DestroyOptions{
				InfraID: "valid",
				GCPPlatform: core.GCPPlatformDestroyOptions{
					ProjectID: "proj",
					Region:    "us-west1",
				},
			},
			expectError: false,
		},
		"missing inputs": {
			opts:        &core.DestroyOptions{},
			expectError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := validateInputs(test.opts)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
