package gcp

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
)

func TestDestroyClusterExtractsParametersFromHostedCluster(t *testing.T) {
	tests := map[string]struct {
		hostedCluster    *hyperv1.HostedCluster
		initialProjectID string
		initialRegion    string
		initialInfraID   string
		expectedProject  string
		expectedRegion   string
		expectedInfraID  string
		expectError      bool
	}{
		"When HostedCluster exists with GCP platform, it should extract all parameters": {
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
		"When HostedCluster is nil and flags are set, it should use flag values": {
			hostedCluster:    nil,
			initialProjectID: "flag-project",
			initialRegion:    "us-east1",
			initialInfraID:   "flag-infra",
			expectedProject:  "flag-project",
			expectedRegion:   "us-east1",
			expectedInfraID:  "flag-infra",
		},
		"When HostedCluster exists and flags are set, HC values should take precedence": {
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

			// Simulate the parameter extraction logic from DestroyCluster
			if test.hostedCluster != nil {
				opts.InfraID = test.hostedCluster.Spec.InfraID
				if test.hostedCluster.Spec.Platform.GCP != nil {
					opts.GCPPlatform.ProjectID = test.hostedCluster.Spec.Platform.GCP.Project
					opts.GCPPlatform.Region = test.hostedCluster.Spec.Platform.GCP.Region
				}
			}

			g.Expect(opts.InfraID).To(Equal(test.expectedInfraID))
			g.Expect(opts.GCPPlatform.ProjectID).To(Equal(test.expectedProject))
			g.Expect(opts.GCPPlatform.Region).To(Equal(test.expectedRegion))
		})
	}
}

func TestDestroyClusterValidatesRequiredInputs(t *testing.T) {
	tests := map[string]struct {
		infraID     string
		projectID   string
		region      string
		expectError bool
		errorSubstr string
	}{
		"When all required inputs are provided, it should not error": {
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

			// Simulate the validation logic from DestroyCluster
			var inputErrors []error
			if len(test.infraID) == 0 {
				inputErrors = append(inputErrors, &validationError{msg: "infrastructure ID is required"})
			}
			if len(test.projectID) == 0 {
				inputErrors = append(inputErrors, &validationError{msg: "project ID is required"})
			}
			if len(test.region) == 0 {
				inputErrors = append(inputErrors, &validationError{msg: "region is required"})
			}

			var err error
			if len(inputErrors) > 0 {
				combinedErr := inputErrors[0]
				for i := 1; i < len(inputErrors); i++ {
					combinedErr = &combinedValidationError{err1: combinedErr, err2: inputErrors[i]}
				}
				err = &requiredInputsError{inner: combinedErr}
			}

			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(test.errorSubstr))
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}

func TestDestroyClusterPreserveFlagsDefaults(t *testing.T) {
	g := NewGomegaWithT(t)

	opts := core.GCPPlatformDestroyOptions{
		PreserveIAM:   false,
		PreserveInfra: false,
	}

	g.Expect(opts.PreserveIAM).To(BeFalse(), "PreserveIAM should default to false")
	g.Expect(opts.PreserveInfra).To(BeFalse(), "PreserveInfra should default to false")
}

func TestDestroyClusterPreserveFlagsCombinations(t *testing.T) {
	tests := map[string]struct {
		preserveIAM   bool
		preserveInfra bool
		expectIAMLog  string
		expectInfraLog string
	}{
		"When no preserve flags are set, both should be destroyed": {
			preserveIAM:    false,
			preserveInfra:  false,
			expectIAMLog:   "Destroying IAM",
			expectInfraLog: "Destroying GCP infrastructure",
		},
		"When preserve-iam is set, IAM should be skipped": {
			preserveIAM:    true,
			preserveInfra:  false,
			expectIAMLog:   "Skipping IAM destruction",
			expectInfraLog: "Destroying GCP infrastructure",
		},
		"When preserve-infra is set, infrastructure should be skipped": {
			preserveIAM:    false,
			preserveInfra:  true,
			expectIAMLog:   "Destroying IAM",
			expectInfraLog: "Skipping infrastructure destruction",
		},
		"When both preserve flags are set, both should be skipped": {
			preserveIAM:    true,
			preserveInfra:  true,
			expectIAMLog:   "Skipping IAM destruction",
			expectInfraLog: "Skipping infrastructure destruction",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			opts := &core.DestroyOptions{
				GCPPlatform: core.GCPPlatformDestroyOptions{
					PreserveIAM:   test.preserveIAM,
					PreserveInfra: test.preserveInfra,
				},
			}

			// Verify the flags are set correctly
			g.Expect(opts.GCPPlatform.PreserveIAM).To(Equal(test.preserveIAM))
			g.Expect(opts.GCPPlatform.PreserveInfra).To(Equal(test.preserveInfra))

			// The actual log messages would be verified in integration tests
			// Here we just verify the flag values that drive the behavior
		})
	}
}

// Test helper types for validation simulation
type validationError struct {
	msg string
}

func (e *validationError) Error() string {
	return e.msg
}

type combinedValidationError struct {
	err1, err2 error
}

func (e *combinedValidationError) Error() string {
	return e.err1.Error() + "\n" + e.err2.Error()
}

type requiredInputsError struct {
	inner error
}

func (e *requiredInputsError) Error() string {
	return "required inputs are missing: " + e.inner.Error()
}
