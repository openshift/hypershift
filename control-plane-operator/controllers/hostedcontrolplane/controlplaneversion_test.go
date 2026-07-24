package hostedcontrolplane

import (
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/clock"
	testingclock "k8s.io/utils/clock/testing"
)

// newHCP creates a minimal HostedControlPlane for testing.
func newHCP(releaseImage string, controlPlaneReleaseImage *string, generation int64) *hyperv1.HostedControlPlane {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test-hcp",
			Namespace:  "test-ns",
			Generation: generation,
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ReleaseImage: releaseImage,
		},
	}
	if controlPlaneReleaseImage != nil {
		hcp.Spec.ControlPlaneReleaseImage = controlPlaneReleaseImage
	}
	return hcp
}

// newComponent creates a ControlPlaneComponent with the given version and rollout status.
func newComponent(name, version string, rolloutComplete bool) hyperv1.ControlPlaneComponent {
	conditions := []metav1.Condition{
		{
			Type:               string(hyperv1.ControlPlaneComponentAvailable),
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		},
	}
	if rolloutComplete {
		conditions = append(conditions, metav1.Condition{
			Type:               string(hyperv1.ControlPlaneComponentRolloutComplete),
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Now(),
		})
	} else {
		conditions = append(conditions, metav1.Condition{
			Type:               string(hyperv1.ControlPlaneComponentRolloutComplete),
			Status:             metav1.ConditionFalse,
			LastTransitionTime: metav1.Now(),
		})
	}
	return hyperv1.ControlPlaneComponent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "test-ns",
		},
		Status: hyperv1.ControlPlaneComponentStatus{
			Version:    version,
			Conditions: conditions,
		},
	}
}

func strPtr(s string) *string { return &s }

// TestReconcileControlPlaneVersion_AllComponentsComplete verifies that
// When all ControlPlaneComponent resources report the target version with
// RolloutComplete=True, controlPlaneVersion.history[0].State transitions
// to Completed and completionTime is set.
func TestReconcileControlPlaneVersion_AllComponentsComplete(t *testing.T) {
	fakeClock := testingclock.NewFakeClock(time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC))
	hcp := newHCP("quay.io/ocp/release@sha256:aaa", nil, 1)
	hcp.Status.ControlPlaneVersion = hyperv1.ControlPlaneVersionStatus{
		Desired: configv1.Release{
			Version: "4.17.0",
			Image:   "quay.io/ocp/release@sha256:aaa",
		},
		History: []hyperv1.ControlPlaneUpdateHistory{
			{
				State:       configv1.PartialUpdate,
				StartedTime: metav1.NewTime(fakeClock.Now().Add(-10 * time.Minute)),
				Version:     "4.17.0",
				Image:       "quay.io/ocp/release@sha256:aaa",
			},
		},
		ObservedGeneration: 1,
	}

	components := []hyperv1.ControlPlaneComponent{
		newComponent("kube-apiserver", "4.17.0", true),
		newComponent("kube-controller-manager", "4.17.0", true),
		newComponent("openshift-apiserver", "4.17.0", true),
	}

	result := reconcileControlPlaneVersion(hcp, components, fakeClock, "4.17.0", hcp.Spec.ReleaseImage)

	if result.History[0].State != configv1.CompletedUpdate {
		t.Errorf("When all components are complete, it should transition to Completed, got %s", result.History[0].State)
	}
	if result.History[0].CompletionTime.IsZero() {
		t.Error("When all components are complete, it should set completionTime")
	}
	if result.History[0].Version != "4.17.0" {
		t.Errorf("When all components are complete, version should be 4.17.0, got %s", result.History[0].Version)
	}
	if result.History[0].Image != hcp.Spec.ReleaseImage {
		t.Errorf("When all components are complete, image should be %s, got %s", hcp.Spec.ReleaseImage, result.History[0].Image)
	}
}

// TestReconcileControlPlaneVersion_NewDesiredRelease verifies that
// When the desired release changes (version bump), a new Partial history entry is prepended.
func TestReconcileControlPlaneVersion_NewDesiredRelease(t *testing.T) {
	fakeClock := testingclock.NewFakeClock(time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC))
	hcp := newHCP("quay.io/ocp/release@sha256:bbb", nil, 2)
	hcp.Status.ControlPlaneVersion = hyperv1.ControlPlaneVersionStatus{
		Desired: configv1.Release{
			Version: "4.17.0",
			Image:   "quay.io/ocp/release@sha256:aaa",
		},
		History: []hyperv1.ControlPlaneUpdateHistory{
			{
				State:          configv1.CompletedUpdate,
				StartedTime:    metav1.NewTime(fakeClock.Now().Add(-1 * time.Hour)),
				CompletionTime: metav1.NewTime(fakeClock.Now().Add(-30 * time.Minute)),
				Version:        "4.17.0",
				Image:          "quay.io/ocp/release@sha256:aaa",
			},
		},
		ObservedGeneration: 1,
	}

	// Components still report old version — they haven't rolled yet
	components := []hyperv1.ControlPlaneComponent{
		newComponent("kube-apiserver", "4.17.0", true),
	}

	result := reconcileControlPlaneVersion(hcp, components, fakeClock, "4.17.0", hcp.Spec.ReleaseImage)

	if len(result.History) < 2 {
		t.Fatalf("When desired release changes, it should prepend new entry, got %d entries", len(result.History))
	}
	if result.History[0].State != configv1.PartialUpdate {
		t.Errorf("When desired release changes, it should prepend Partial entry, got %s", result.History[0].State)
	}
	if result.History[0].Image != "quay.io/ocp/release@sha256:bbb" {
		t.Errorf("When desired release changes, it should use new image, got %s", result.History[0].Image)
	}
	// Version comes from aggregateComponentVersion which reports what components currently run
	if result.History[0].Version != "4.17.0" {
		t.Errorf("When desired release changes but components haven't rolled, it should record current version, got %s", result.History[0].Version)
	}
}

// TestReconcileControlPlaneVersion_ImageOnlyChange verifies that
// When the image digest changes but the semver is the same, a new Partial
// entry is prepended (CVE patch scenario).
func TestReconcileControlPlaneVersion_ImageOnlyChange(t *testing.T) {
	fakeClock := testingclock.NewFakeClock(time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC))
	hcp := newHCP("quay.io/ocp/release@sha256:bbb", nil, 2)
	hcp.Status.ControlPlaneVersion = hyperv1.ControlPlaneVersionStatus{
		Desired: configv1.Release{
			Version: "4.17.0",
			Image:   "quay.io/ocp/release@sha256:aaa",
		},
		History: []hyperv1.ControlPlaneUpdateHistory{
			{
				State:          configv1.CompletedUpdate,
				StartedTime:    metav1.NewTime(fakeClock.Now().Add(-1 * time.Hour)),
				CompletionTime: metav1.NewTime(fakeClock.Now().Add(-30 * time.Minute)),
				Version:        "4.17.0",
				Image:          "quay.io/ocp/release@sha256:aaa",
			},
		},
		ObservedGeneration: 1,
	}

	components := []hyperv1.ControlPlaneComponent{
		newComponent("kube-apiserver", "4.17.0", true),
	}

	result := reconcileControlPlaneVersion(hcp, components, fakeClock, "4.17.0", hcp.Spec.ReleaseImage)

	if len(result.History) < 2 {
		t.Fatalf("When image changes (same version), it should prepend new entry, got %d entries", len(result.History))
	}
	if result.History[0].State != configv1.PartialUpdate {
		t.Errorf("When image changes (same version), it should prepend Partial entry, got %s", result.History[0].State)
	}
	if result.History[0].Image != "quay.io/ocp/release@sha256:bbb" {
		t.Errorf("When image changes (same version), it should use new image, got %s", result.History[0].Image)
	}
	if result.History[0].Version != "4.17.0" {
		t.Errorf("When image changes (same version), version should remain 4.17.0, got %s", result.History[0].Version)
	}
}

// TestReconcileControlPlaneVersion_NewComponentMidUpgrade verifies that
// When a new ControlPlaneComponent is created mid-upgrade that hasn't reached
// the desired version, controlPlaneVersion remains Partial.
func TestReconcileControlPlaneVersion_NewComponentMidUpgrade(t *testing.T) {
	fakeClock := testingclock.NewFakeClock(time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC))
	hcp := newHCP("quay.io/ocp/release@sha256:aaa", nil, 1)
	hcp.Status.ControlPlaneVersion = hyperv1.ControlPlaneVersionStatus{
		Desired: configv1.Release{
			Version: "4.17.0",
			Image:   "quay.io/ocp/release@sha256:aaa",
		},
		History: []hyperv1.ControlPlaneUpdateHistory{
			{
				State:       configv1.PartialUpdate,
				StartedTime: metav1.NewTime(fakeClock.Now().Add(-5 * time.Minute)),
				Version:     "4.17.0",
				Image:       "quay.io/ocp/release@sha256:aaa",
			},
		},
		ObservedGeneration: 1,
	}

	components := []hyperv1.ControlPlaneComponent{
		newComponent("kube-apiserver", "4.17.0", true),
		newComponent("kube-controller-manager", "4.17.0", true),
		newComponent("new-component", "4.16.0", false), // mid-upgrade, not at desired version
	}

	result := reconcileControlPlaneVersion(hcp, components, fakeClock, "4.17.0", hcp.Spec.ReleaseImage)

	if result.History[0].State != configv1.PartialUpdate {
		t.Errorf("When a component mid-upgrade hasn't reached desired version, it should remain Partial, got %s", result.History[0].State)
	}
	if !result.History[0].CompletionTime.IsZero() {
		t.Error("When a component mid-upgrade hasn't reached desired version, it should not set completionTime")
	}
	if result.History[0].Version != "4.17.0" {
		t.Errorf("When a component mid-upgrade hasn't reached desired version, history version should remain 4.17.0, got %s", result.History[0].Version)
	}
}

// TestReconcileControlPlaneVersion_FirstPopulation verifies that
// When controlPlaneVersion is first populated on an existing cluster with all
// components at the desired version, history initializes with Partial on the
// first reconciliation, then transitions to Completed on the second.
func TestReconcileControlPlaneVersion_FirstPopulation(t *testing.T) {
	fakeClock := testingclock.NewFakeClock(time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC))
	hcp := newHCP("quay.io/ocp/release@sha256:aaa", nil, 1)
	// No existing ControlPlaneVersion status (first population)

	components := []hyperv1.ControlPlaneComponent{
		newComponent("kube-apiserver", "4.17.0", true),
		newComponent("kube-controller-manager", "4.17.0", true),
	}

	// First reconciliation: should create Partial entry
	result := reconcileControlPlaneVersion(hcp, components, fakeClock, "4.17.0", hcp.Spec.ReleaseImage)

	if len(result.History) != 1 {
		t.Fatalf("When first populated, it should create 1 history entry, got %d", len(result.History))
	}
	if result.History[0].State != configv1.PartialUpdate {
		t.Errorf("When first populated, it should initialize with Partial, got %s", result.History[0].State)
	}
	if result.History[0].Version != "4.17.0" {
		t.Errorf("When first populated, version should be 4.17.0, got %s", result.History[0].Version)
	}
	if result.History[0].Image != hcp.Spec.ReleaseImage {
		t.Errorf("When first populated, image should be %s, got %s", hcp.Spec.ReleaseImage, result.History[0].Image)
	}

	// Second reconciliation: should transition to Completed
	hcp.Status.ControlPlaneVersion = result
	fakeClock.Step(1 * time.Minute)
	result2 := reconcileControlPlaneVersion(hcp, components, fakeClock, "4.17.0", hcp.Spec.ReleaseImage)

	if result2.History[0].State != configv1.CompletedUpdate {
		t.Errorf("When first populated and all components ready, second reconcile should transition to Completed, got %s", result2.History[0].State)
	}
	if result2.History[0].CompletionTime.IsZero() {
		t.Error("When first populated and all components ready, second reconcile should set completionTime")
	}
}

// TestReconcileControlPlaneVersion_ControlPlaneReleaseImage verifies that
// When HCP.Spec.ControlPlaneReleaseImage is set, controlPlaneVersion.desired
// reflects ControlPlaneReleaseImage, not Spec.ReleaseImage.
func TestReconcileControlPlaneVersion_ControlPlaneReleaseImage(t *testing.T) {
	fakeClock := testingclock.NewFakeClock(time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC))
	cpReleaseImage := "quay.io/ocp/release@sha256:cponly"
	hcp := newHCP("quay.io/ocp/release@sha256:dataplane", strPtr(cpReleaseImage), 1)

	components := []hyperv1.ControlPlaneComponent{
		newComponent("kube-apiserver", "4.17.0", true),
	}

	// The caller resolves the image — when ControlPlaneReleaseImage is set,
	// the caller passes its resolved digest as desiredImage.
	result := reconcileControlPlaneVersion(hcp, components, fakeClock, "4.17.0", cpReleaseImage)

	if result.Desired.Image != cpReleaseImage {
		t.Errorf("When ControlPlaneReleaseImage is set, it should use it as desired, got %s", result.Desired.Image)
	}
}

// TestReconcileControlPlaneVersion_ObservedGeneration verifies that
// When the CPO reconciles successfully, observedGeneration reflects the
// generation that was actually processed.
func TestReconcileControlPlaneVersion_ObservedGeneration(t *testing.T) {
	fakeClock := testingclock.NewFakeClock(time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC))
	hcp := newHCP("quay.io/ocp/release@sha256:aaa", nil, 7)

	components := []hyperv1.ControlPlaneComponent{
		newComponent("kube-apiserver", "4.17.0", true),
	}

	result := reconcileControlPlaneVersion(hcp, components, fakeClock, "4.17.0", hcp.Spec.ReleaseImage)

	if result.ObservedGeneration != 7 {
		t.Errorf("When reconciliation succeeds, it should set observedGeneration to current generation (7), got %d", result.ObservedGeneration)
	}
}

// TestReconcileControlPlaneVersion_ComponentFailure verifies that
// when a component is at the desired version but its rollout has not completed
// (e.g. deployment updated but pods crashing), controlPlaneVersion stays Partial.
func TestReconcileControlPlaneVersion_ComponentFailure(t *testing.T) {
	fakeClock := testingclock.NewFakeClock(time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC))
	hcp := newHCP("quay.io/ocp/release@sha256:aaa", nil, 1)
	hcp.Status.ControlPlaneVersion = hyperv1.ControlPlaneVersionStatus{
		Desired: configv1.Release{
			Version: "4.17.0",
			Image:   "quay.io/ocp/release@sha256:aaa",
		},
		History: []hyperv1.ControlPlaneUpdateHistory{
			{
				State:       configv1.PartialUpdate,
				StartedTime: metav1.NewTime(fakeClock.Now().Add(-5 * time.Minute)),
				Version:     "4.17.0",
				Image:       "quay.io/ocp/release@sha256:aaa",
			},
		},
		ObservedGeneration: 1,
	}

	components := []hyperv1.ControlPlaneComponent{
		newComponent("kube-apiserver", "4.17.0", true),
		newComponent("kube-controller-manager", "4.17.0", false), // at desired version but rollout not complete
	}

	result := reconcileControlPlaneVersion(hcp, components, fakeClock, "4.17.0", hcp.Spec.ReleaseImage)

	if result.History[0].State != configv1.PartialUpdate {
		t.Errorf("When a component fails, it should maintain Partial state, got %s", result.History[0].State)
	}
	if !result.History[0].CompletionTime.IsZero() {
		t.Error("When a component fails, it should not set completionTime")
	}
	if result.History[0].Version != "4.17.0" {
		t.Errorf("When a component fails, history version should remain 4.17.0, got %s", result.History[0].Version)
	}
}

// TestReconcileControlPlaneVersion_SupersededPartial verifies that
// When controlPlaneVersion is Partial for version X and a new desired release
// Y is detected, the Partial entry for X receives a CompletionTime stamp
// before Y's entry is prepended.
func TestReconcileControlPlaneVersion_SupersededPartial(t *testing.T) {
	fakeClock := testingclock.NewFakeClock(time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC))
	hcp := newHCP("quay.io/ocp/release@sha256:bbb", nil, 2)
	startTime := metav1.NewTime(fakeClock.Now().Add(-10 * time.Minute))
	hcp.Status.ControlPlaneVersion = hyperv1.ControlPlaneVersionStatus{
		Desired: configv1.Release{
			Version: "4.17.0",
			Image:   "quay.io/ocp/release@sha256:aaa",
		},
		History: []hyperv1.ControlPlaneUpdateHistory{
			{
				State:       configv1.PartialUpdate,
				StartedTime: startTime,
				Version:     "4.17.0",
				Image:       "quay.io/ocp/release@sha256:aaa",
			},
		},
		ObservedGeneration: 1,
	}

	components := []hyperv1.ControlPlaneComponent{
		newComponent("kube-apiserver", "4.17.0", false),
	}

	result := reconcileControlPlaneVersion(hcp, components, fakeClock, "4.17.0", hcp.Spec.ReleaseImage)

	if len(result.History) < 2 {
		t.Fatalf("When superseding a Partial entry, it should prepend new entry, got %d entries", len(result.History))
	}
	// New entry at [0]
	if result.History[0].State != configv1.PartialUpdate {
		t.Errorf("When superseding, it should prepend new Partial entry, got %s", result.History[0].State)
	}
	if result.History[0].Image != "quay.io/ocp/release@sha256:bbb" {
		t.Errorf("When superseding, it should use new image for prepended entry, got %s", result.History[0].Image)
	}
	// Superseded entry at [1] should have CompletionTime stamped
	if result.History[1].CompletionTime.IsZero() {
		t.Error("When superseding a Partial entry, it should stamp CompletionTime on the old entry")
	}
	if result.History[1].State != configv1.PartialUpdate {
		t.Errorf("When superseding, the old entry should remain Partial (not switch to Completed), got %s", result.History[1].State)
	}
}

// TestReconcileControlPlaneVersion_NoComponents verifies behavior when no
// ControlPlaneComponent resources exist yet — should create initial Partial entry.
func TestReconcileControlPlaneVersion_NoComponents(t *testing.T) {
	fakeClock := testingclock.NewFakeClock(time.Date(2026, 3, 6, 12, 0, 0, 0, time.UTC))
	hcp := newHCP("quay.io/ocp/release@sha256:aaa", nil, 1)

	var components []hyperv1.ControlPlaneComponent

	result := reconcileControlPlaneVersion(hcp, components, fakeClock, "4.17.0", hcp.Spec.ReleaseImage)

	if result.Desired.Image == "" {
		t.Fatal("When no components exist, it should still return a status with desired set")
	}
	if len(result.History) != 1 {
		t.Fatalf("When no components exist, it should create initial Partial entry, got %d entries", len(result.History))
	}
	if result.History[0].State != configv1.PartialUpdate {
		t.Errorf("When no components exist, it should create Partial entry, got %s", result.History[0].State)
	}
	if result.History[0].Version != "4.17.0" {
		t.Errorf("When no components exist, version should be 4.17.0, got %s", result.History[0].Version)
	}
	if result.History[0].Image != hcp.Spec.ReleaseImage {
		t.Errorf("When no components exist, image should be %s, got %s", hcp.Spec.ReleaseImage, result.History[0].Image)
	}
}

func TestAllComponentsAtVersion(t *testing.T) {
	tests := []struct {
		name           string
		components     []hyperv1.ControlPlaneComponent
		desiredVersion string
		want           bool
	}{
		{
			name:           "When no components exist, it should return false",
			components:     nil,
			desiredVersion: "4.17.0",
			want:           false,
		},
		{
			name: "When all components match version and have RolloutComplete, it should return true",
			components: []hyperv1.ControlPlaneComponent{
				newComponent("kube-apiserver", "4.17.0", true),
				newComponent("kube-controller-manager", "4.17.0", true),
			},
			desiredVersion: "4.17.0",
			want:           true,
		},
		{
			name: "When a component has a version mismatch, it should return false",
			components: []hyperv1.ControlPlaneComponent{
				newComponent("kube-apiserver", "4.17.0", true),
				newComponent("kube-controller-manager", "4.16.0", true),
			},
			desiredVersion: "4.17.0",
			want:           false,
		},
		{
			name: "When a component has RolloutComplete=False but version matches, it should return false",
			components: []hyperv1.ControlPlaneComponent{
				newComponent("kube-apiserver", "4.17.0", true),
				newComponent("kube-controller-manager", "4.17.0", false),
			},
			desiredVersion: "4.17.0",
			want:           false,
		},
		{
			name: "When a component has empty version, it should return false",
			components: []hyperv1.ControlPlaneComponent{
				newComponent("kube-apiserver", "4.17.0", true),
				newComponent("kube-controller-manager", "", true),
			},
			desiredVersion: "4.17.0",
			want:           false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := allComponentsAtVersion(tt.components, tt.desiredVersion)
			if got != tt.want {
				t.Errorf("allComponentsAtVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Ensure the clock.Clock interface is used for testability.
var _ clock.Clock = &testingclock.FakeClock{}
