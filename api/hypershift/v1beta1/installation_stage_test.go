package v1beta1

import (
	"encoding/json"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// hostedClusterStatusNMinus1 represents the N-1 version of HostedClusterStatus
// before the installation field was added. Only the fields relevant to
// serialization compatibility are included.
type hostedClusterStatusNMinus1 struct {
	// conditions is the N-1 conditions field.
	// +listType=map
	// +listMapKey=type
	// +optional
	// +kubebuilder:validation:MaxItems=100
	Conditions       []metav1.Condition          `json:"conditions,omitempty"`      //nolint:kubeapilinter // test-only N-1 compat struct
	Version          *ClusterVersionStatus       `json:"version,omitempty"`         //nolint:kubeapilinter // test-only N-1 compat struct
	KubeConfig       *corev1LocalObjectReference `json:"kubeconfig,omitempty"`      //nolint:kubeapilinter // test-only N-1 compat struct
	SecretEncryption SecretEncryptionStatus      `json:"secretEncryption,omitzero"` //nolint:kubeapilinter // test-only N-1 compat struct
}

// corev1LocalObjectReference avoids importing corev1 in this test-only struct.
type corev1LocalObjectReference struct {
	Name string `json:"name,omitempty"` //nolint:kubeapilinter // test-only compat struct
}

func TestHostedClusterStatusInstallationStageSerializationCompatibility(t *testing.T) {
	tests := []struct {
		name string
		// installation to set on the current type
		installation InstallationStatus
		// whether the field should appear in serialized JSON
		expectInJSON bool
	}{
		{
			name:         "When installation is zero-value it should be omitted from JSON",
			installation: InstallationStatus{},
			expectInJSON: false,
		},
		{
			name: "When installation.stage is Initializing it should appear in JSON",
			installation: InstallationStatus{
				Stage: InstallationStageInitializing,
			},
			expectInJSON: true,
		},
		{
			name: "When installation.stage is InfrastructureReady it should appear in JSON",
			installation: InstallationStatus{
				Stage: InstallationStageInfrastructureReady,
			},
			expectInJSON: true,
		},
		{
			name: "When installation.stage is InstallationComplete it should appear in JSON",
			installation: InstallationStatus{
				Stage: InstallationStageInstallationComplete,
			},
			expectInJSON: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			current := HostedClusterStatus{
				Installation: tt.installation,
			}

			data, err := json.Marshal(current)
			if err != nil {
				t.Fatalf("failed to marshal current struct: %v", err)
			}

			// Verify N-1 can deserialize N output
			var nMinus1 hostedClusterStatusNMinus1
			if err := json.Unmarshal(data, &nMinus1); err != nil {
				t.Fatalf("N-1 failed to unmarshal JSON from N: %v", err)
			}

			// Verify field presence in JSON
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatalf("failed to unmarshal into raw map: %v", err)
			}
			_, found := raw["installation"]
			if found != tt.expectInJSON {
				t.Errorf("installation presence in JSON: got %v, want %v; JSON: %s", found, tt.expectInJSON, string(data))
			}

			// Reverse: N-1 output can be deserialized by N
			nMinus1Data, err := json.Marshal(nMinus1)
			if err != nil {
				t.Fatalf("failed to marshal N-1 struct: %v", err)
			}
			var roundTripped HostedClusterStatus
			if err := json.Unmarshal(nMinus1Data, &roundTripped); err != nil {
				t.Fatalf("N failed to unmarshal JSON from N-1: %v", err)
			}
			// N-1 has no installation, so it should deserialize as zero-value
			if roundTripped.Installation.Stage != "" {
				t.Errorf("Installation.Stage should be empty after N-1 round-trip: got %+v", roundTripped.Installation)
			}
		})
	}
}

func TestSetInstallationStage(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(time.Minute))

	tests := []struct {
		name          string
		currentStage  InstallationStage
		newStage      InstallationStage
		expectUpdated bool
		expectStage   InstallationStage
	}{
		{
			name:          "From empty to Initializing should succeed",
			currentStage:  "",
			newStage:      InstallationStageInitializing,
			expectUpdated: true,
			expectStage:   InstallationStageInitializing,
		},
		{
			name:          "From Initializing to InfrastructureReady should succeed",
			currentStage:  InstallationStageInitializing,
			newStage:      InstallationStageInfrastructureReady,
			expectUpdated: true,
			expectStage:   InstallationStageInfrastructureReady,
		},
		{
			name:          "From InfrastructureReady to ControlPlaneProvisioning should succeed",
			currentStage:  InstallationStageInfrastructureReady,
			newStage:      InstallationStageControlPlaneProvisioning,
			expectUpdated: true,
			expectStage:   InstallationStageControlPlaneProvisioning,
		},
		{
			name:          "From ControlPlaneProvisioning to APIServerAvailable should succeed",
			currentStage:  InstallationStageControlPlaneProvisioning,
			newStage:      InstallationStageAPIServerAvailable,
			expectUpdated: true,
			expectStage:   InstallationStageAPIServerAvailable,
		},
		{
			name:          "From APIServerAvailable to WorkerAvailable should succeed",
			currentStage:  InstallationStageAPIServerAvailable,
			newStage:      InstallationStageWorkerAvailable,
			expectUpdated: true,
			expectStage:   InstallationStageWorkerAvailable,
		},
		{
			name:          "From WorkerAvailable to WaitingForOperators should succeed",
			currentStage:  InstallationStageWorkerAvailable,
			newStage:      InstallationStageWaitingForOperators,
			expectUpdated: true,
			expectStage:   InstallationStageWaitingForOperators,
		},
		{
			name:          "From WaitingForOperators to InstallationComplete should succeed",
			currentStage:  InstallationStageWaitingForOperators,
			newStage:      InstallationStageInstallationComplete,
			expectUpdated: true,
			expectStage:   InstallationStageInstallationComplete,
		},
		{
			name:          "Backward transition should be rejected",
			currentStage:  InstallationStageWaitingForOperators,
			newStage:      InstallationStageInitializing,
			expectUpdated: false,
			expectStage:   InstallationStageWaitingForOperators,
		},
		{
			name:          "Same stage transition should be rejected",
			currentStage:  InstallationStageControlPlaneProvisioning,
			newStage:      InstallationStageControlPlaneProvisioning,
			expectUpdated: false,
			expectStage:   InstallationStageControlPlaneProvisioning,
		},
		{
			name:          "Unknown stage should be rejected",
			currentStage:  InstallationStageInitializing,
			newStage:      InstallationStage("Unknown"),
			expectUpdated: false,
			expectStage:   InstallationStageInitializing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := &InstallationStatus{
				Stage:              tt.currentStage,
				LastTransitionTime: now,
			}
			updated := SetInstallationStage(status, tt.newStage, later)
			if updated != tt.expectUpdated {
				t.Errorf("updated: got %v, want %v", updated, tt.expectUpdated)
			}
			if status.Stage != tt.expectStage {
				t.Errorf("stage: got %q, want %q", status.Stage, tt.expectStage)
			}
			if tt.expectUpdated && !status.LastTransitionTime.Equal(&later) {
				t.Errorf("lastTransitionTime should be updated to %v, got %v", later, status.LastTransitionTime)
			}
			if !tt.expectUpdated && !status.LastTransitionTime.Equal(&now) {
				t.Errorf("lastTransitionTime should not change: got %v, want %v", status.LastTransitionTime, now)
			}
		})
	}
}

func TestInstallationStageOrdinal(t *testing.T) {
	tests := []struct {
		stage    InstallationStage
		expected int
	}{
		{InstallationStageInitializing, 0},
		{InstallationStageInfrastructureReady, 1},
		{InstallationStageControlPlaneProvisioning, 2},
		{InstallationStageAPIServerAvailable, 3},
		{InstallationStageWorkerAvailable, 4},
		{InstallationStageWaitingForOperators, 5},
		{InstallationStageInstallationComplete, 6},
		{InstallationStage("Unknown"), -1},
		{InstallationStage(""), -1},
	}

	for _, tt := range tests {
		t.Run(string(tt.stage), func(t *testing.T) {
			got := InstallationStageOrdinal(tt.stage)
			if got != tt.expected {
				t.Errorf("InstallationStageOrdinal(%q) = %d, want %d", tt.stage, got, tt.expected)
			}
		})
	}
}
