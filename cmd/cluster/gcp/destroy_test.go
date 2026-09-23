package gcp

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	gcpinfra "github.com/openshift/hypershift/cmd/infra/gcp"
	"github.com/openshift/hypershift/cmd/log"

	"github.com/go-logr/logr"
)

func TestNewDestroyCommand(t *testing.T) {
	g := NewGomegaWithT(t)
	opts := &core.DestroyOptions{Log: log.Log}

	cmd := NewDestroyCommand(opts)

	g.Expect(cmd).ToNot(BeNil())
	g.Expect(cmd.Use).To(Equal("gcp"))
	g.Expect(opts.GCPPlatform.PreserveIAM).To(BeFalse())
	g.Expect(opts.GCPPlatform.PreserveInfra).To(BeFalse())

	err := cmd.ParseFlags([]string{"--preserve-iam", "--preserve-infra", "--project-id", "test-proj", "--region", "us-west1"})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(opts.GCPPlatform.PreserveIAM).To(BeTrue())
	g.Expect(opts.GCPPlatform.PreserveInfra).To(BeTrue())
	g.Expect(opts.GCPPlatform.ProjectID).To(Equal("test-proj"))
	g.Expect(opts.GCPPlatform.Region).To(Equal("us-west1"))
}

func TestExtractParameters(t *testing.T) {
	tests := map[string]struct {
		hostedCluster   *hyperv1.HostedCluster
		initialOpts     *core.DestroyOptions
		expectedInfraID string
		expectedProject string
		expectedRegion  string
	}{
		"When HostedCluster provided, it should extract values from cluster": {
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "test-project",
							Region:  "us-central1",
						},
					},
				},
			},
			initialOpts:     &core.DestroyOptions{Log: log.Log},
			expectedInfraID: "test-infra",
			expectedProject: "test-project",
			expectedRegion:  "us-central1",
		},
		"When HostedCluster is nil, it should preserve flag values": {
			hostedCluster: nil,
			initialOpts: &core.DestroyOptions{
				Log:     log.Log,
				InfraID: "flag-infra",
				GCPPlatform: core.GCPPlatformDestroyOptions{
					ProjectID: "flag-project",
					Region:    "flag-region",
				},
			},
			expectedInfraID: "flag-infra",
			expectedProject: "flag-project",
			expectedRegion:  "flag-region",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			extractParameters(test.hostedCluster, test.initialOpts)

			g.Expect(test.initialOpts.InfraID).To(Equal(test.expectedInfraID))
			g.Expect(test.initialOpts.GCPPlatform.ProjectID).To(Equal(test.expectedProject))
			g.Expect(test.initialOpts.GCPPlatform.Region).To(Equal(test.expectedRegion))
		})
	}
}

func TestDestroyPlatformSpecifics(t *testing.T) {
	tests := map[string]struct {
		preserveIAM       bool
		preserveInfra     bool
		expectIAMCalled   bool
		expectInfraCalled bool
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

			// Track call order and parameters
			var callOrder []string
			var capturedIAMOpts *gcpinfra.DestroyIAMOptions
			var capturedInfraOpts *gcpinfra.DestroyInfraOptions

			// Stub out the destroy functions
			originalIAM := runDestroyIAM
			originalInfra := runDestroyInfra
			defer func() {
				runDestroyIAM = originalIAM
				runDestroyInfra = originalInfra
			}()

			runDestroyIAM = func(ctx context.Context, opts gcpinfra.DestroyIAMOptions, log logr.Logger) error {
				callOrder = append(callOrder, "IAM")
				capturedIAMOpts = &opts
				return nil
			}
			runDestroyInfra = func(ctx context.Context, opts gcpinfra.DestroyInfraOptions, log logr.Logger) error {
				callOrder = append(callOrder, "Infra")
				capturedInfraOpts = &opts
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

			// Verify call expectations
			if test.expectIAMCalled {
				g.Expect(capturedIAMOpts).ToNot(BeNil(), "IAM destroy should have been called")
				g.Expect(capturedIAMOpts.ProjectID).To(Equal("test-project"))
				g.Expect(capturedIAMOpts.InfraID).To(Equal("test-infra"))
			} else {
				g.Expect(capturedIAMOpts).To(BeNil(), "IAM destroy should not have been called")
			}

			if test.expectInfraCalled {
				g.Expect(capturedInfraOpts).ToNot(BeNil(), "Infra destroy should have been called")
				g.Expect(capturedInfraOpts.ProjectID).To(Equal("test-project"))
				g.Expect(capturedInfraOpts.Region).To(Equal("us-central1"))
				g.Expect(capturedInfraOpts.InfraID).To(Equal("test-infra"))
			} else {
				g.Expect(capturedInfraOpts).To(BeNil(), "Infra destroy should not have been called")
			}

			// Verify call order when both are called
			if test.expectIAMCalled && test.expectInfraCalled {
				g.Expect(callOrder).To(Equal([]string{"IAM", "Infra"}), "IAM should be destroyed before infrastructure")
			}
		})
	}
}

func TestValidateInputs(t *testing.T) {
	tests := map[string]struct {
		opts        *core.DestroyOptions
		expectError bool
	}{
		"When all inputs provided, it should pass validation": {
			opts: &core.DestroyOptions{
				InfraID: "valid",
				GCPPlatform: core.GCPPlatformDestroyOptions{
					ProjectID: "proj",
					Region:    "us-west1",
				},
			},
			expectError: false,
		},
		"When InfraID is missing, it should return error": {
			opts: &core.DestroyOptions{
				GCPPlatform: core.GCPPlatformDestroyOptions{
					ProjectID: "proj",
					Region:    "us-west1",
				},
			},
			expectError: true,
		},
		"When ProjectID is missing, it should return error": {
			opts: &core.DestroyOptions{
				InfraID: "valid",
				GCPPlatform: core.GCPPlatformDestroyOptions{
					Region: "us-west1",
				},
			},
			expectError: true,
		},
		"When Region is missing, it should return error": {
			opts: &core.DestroyOptions{
				InfraID: "valid",
				GCPPlatform: core.GCPPlatformDestroyOptions{
					ProjectID: "proj",
				},
			},
			expectError: true,
		},
		"When all inputs are missing, it should return error": {
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
