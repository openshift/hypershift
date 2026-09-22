package gcp

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
)

// Command construction tests

func TestNewDestroyCommand(t *testing.T) {
	g := NewGomegaWithT(t)

	opts := &core.DestroyOptions{
		Log: log.Log,
	}

	cmd := NewDestroyCommand(opts)

	g.Expect(cmd).ToNot(BeNil(), "Command should be created")
	g.Expect(cmd.Use).To(Equal("gcp"), "Command use should be 'gcp'")
	g.Expect(cmd.Short).To(ContainSubstring("Destroys a GCP HostedCluster"), "Command should have description")

	// Verify default values
	g.Expect(opts.GCPPlatform.PreserveIAM).To(BeFalse(), "PreserveIAM should default to false")
	g.Expect(opts.GCPPlatform.PreserveInfra).To(BeFalse(), "PreserveInfra should default to false")

	// Verify flags are registered
	g.Expect(cmd.Flags().Lookup("preserve-iam")).ToNot(BeNil(), "preserve-iam flag should be registered")
	g.Expect(cmd.Flags().Lookup("preserve-infra")).ToNot(BeNil(), "preserve-infra flag should be registered")
	g.Expect(cmd.Flags().Lookup("project-id")).ToNot(BeNil(), "project-id flag should be registered")
	g.Expect(cmd.Flags().Lookup("region")).ToNot(BeNil(), "region flag should be registered")
}

func TestNewDestroyCommandFlagParsing(t *testing.T) {
	tests := map[string]struct {
		args                  []string
		expectedPreserveIAM   bool
		expectedPreserveInfra bool
		expectedProjectID     string
		expectedRegion        string
	}{
		"When preserve-iam flag is set, it should be true": {
			args:                []string{"--preserve-iam"},
			expectedPreserveIAM: true,
		},
		"When preserve-infra flag is set, it should be true": {
			args:                  []string{"--preserve-infra"},
			expectedPreserveInfra: true,
		},
		"When project-id is provided, it should be captured": {
			args:              []string{"--project-id", "test-project-123"},
			expectedProjectID: "test-project-123",
		},
		"When region is provided, it should be captured": {
			args:           []string{"--region", "us-west1"},
			expectedRegion: "us-west1",
		},
		"When all flags are provided, it should capture all values": {
			args:                  []string{"--preserve-iam", "--preserve-infra", "--project-id", "my-project", "--region", "europe-west1"},
			expectedPreserveIAM:   true,
			expectedPreserveInfra: true,
			expectedProjectID:     "my-project",
			expectedRegion:        "europe-west1",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			opts := &core.DestroyOptions{
				Log: log.Log,
			}

			cmd := NewDestroyCommand(opts)
			err := cmd.ParseFlags(test.args)
			g.Expect(err).ToNot(HaveOccurred(), "Flags should parse without error")

			g.Expect(opts.GCPPlatform.PreserveIAM).To(Equal(test.expectedPreserveIAM), "PreserveIAM should match")
			g.Expect(opts.GCPPlatform.PreserveInfra).To(Equal(test.expectedPreserveInfra), "PreserveInfra should match")

			if test.expectedProjectID != "" {
				g.Expect(opts.GCPPlatform.ProjectID).To(Equal(test.expectedProjectID), "ProjectID should match")
			}

			if test.expectedRegion != "" {
				g.Expect(opts.GCPPlatform.Region).To(Equal(test.expectedRegion), "Region should match")
			}
		})
	}
}

// Parameter extraction tests

func TestExtractParameters(t *testing.T) {
	tests := map[string]struct {
		hostedCluster    *hyperv1.HostedCluster
		initialProjectID string
		initialRegion    string
		initialInfraID   string
		expectedProject  string
		expectedRegion   string
		expectedInfraID  string
	}{
		"When HostedCluster has GCP platform spec, it should extract all parameters": {
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra-123",
					Platform: hyperv1.PlatformSpec{
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "test-project-456",
							Region:  "us-central1",
						},
					},
				},
			},
			expectedProject: "test-project-456",
			expectedRegion:  "us-central1",
			expectedInfraID: "test-infra-123",
		},
		"When HostedCluster is nil, it should use flag values": {
			hostedCluster:    nil,
			initialProjectID: "flag-project",
			initialRegion:    "us-east1",
			initialInfraID:   "flag-infra",
			expectedProject:  "flag-project",
			expectedRegion:   "us-east1",
			expectedInfraID:  "flag-infra",
		},
		"When HostedCluster overrides flag values, it should use HostedCluster values": {
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "hc-infra",
					Platform: hyperv1.PlatformSpec{
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "hc-project",
							Region:  "us-west1",
						},
					},
				},
			},
			initialProjectID: "flag-project",
			initialRegion:    "flag-region",
			initialInfraID:   "flag-infra",
			expectedProject:  "hc-project",
			expectedRegion:   "us-west1",
			expectedInfraID:  "hc-infra",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			opts := &core.DestroyOptions{
				ClusterGracePeriod: 10 * time.Minute,
				Log:                log.Log,
				InfraID:            test.initialInfraID,
				GCPPlatform: core.GCPPlatformDestroyOptions{
					ProjectID: test.initialProjectID,
					Region:    test.initialRegion,
				},
			}

			extractParameters(test.hostedCluster, opts)

			g.Expect(opts.InfraID).To(Equal(test.expectedInfraID), "InfraID should match")
			g.Expect(opts.GCPPlatform.ProjectID).To(Equal(test.expectedProject), "ProjectID should match")
			g.Expect(opts.GCPPlatform.Region).To(Equal(test.expectedRegion), "Region should match")
		})
	}
}

// Platform-specific destroy tests

func TestDestroyPlatformSpecificsWithPreserveFlags(t *testing.T) {
	g := NewGomegaWithT(t)

	opts := &core.DestroyOptions{
		InfraID: "test-infra",
		Log:     log.Log,
		GCPPlatform: core.GCPPlatformDestroyOptions{
			ProjectID:     "test-project",
			Region:        "us-central1",
			PreserveIAM:   true,
			PreserveInfra: true,
		},
	}

	err := destroyPlatformSpecifics(context.Background(), opts)

	g.Expect(err).To(BeNil(), "Should not error when preserve flags skip actual destroy calls")
}

// Validation tests

func TestValidateInputs(t *testing.T) {
	tests := map[string]struct {
		infraID     string
		projectID   string
		region      string
		expectError bool
		errorSubstr string
	}{
		"When all required inputs are provided, it should not return error": {
			infraID:     "valid-infra",
			projectID:   "valid-project",
			region:      "us-central1",
			expectError: false,
		},
		"When infraID is missing, it should return an error": {
			infraID:     "",
			projectID:   "valid-project",
			region:      "us-central1",
			expectError: true,
			errorSubstr: "infrastructure ID is required",
		},
		"When projectID is missing, it should return an error": {
			infraID:     "valid-infra",
			projectID:   "",
			region:      "us-central1",
			expectError: true,
			errorSubstr: "project ID is required",
		},
		"When region is missing, it should return an error": {
			infraID:     "valid-infra",
			projectID:   "valid-project",
			region:      "",
			expectError: true,
			errorSubstr: "region is required",
		},
		"When multiple inputs are missing, it should return combined error": {
			infraID:     "",
			projectID:   "",
			region:      "",
			expectError: true,
			errorSubstr: "required inputs are missing",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			opts := &core.DestroyOptions{
				InfraID: test.infraID,
				GCPPlatform: core.GCPPlatformDestroyOptions{
					ProjectID: test.projectID,
					Region:    test.region,
				},
			}

			err := validateInputs(opts)

			if test.expectError {
				g.Expect(err).To(HaveOccurred(), "Should return validation error")
				g.Expect(err.Error()).To(ContainSubstring(test.errorSubstr), "Error message should contain expected substring")
			} else {
				g.Expect(err).To(BeNil(), "Should not return error when all inputs valid")
			}
		})
	}
}
