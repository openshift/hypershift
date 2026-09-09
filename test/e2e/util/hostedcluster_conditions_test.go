package util

import (
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/conditions"

	configv1 "github.com/openshift/api/config/v1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestHostedClusterConditionsPredicate(t *testing.T) {
	originalVersion := releaseVersion
	releaseVersion = Version51
	t.Cleanup(func() { releaseVersion = originalVersion })
	for _, tc := range []struct {
		name            string
		version         string
		wrongCredential bool
		missingIdentity bool
	}{
		{name: "When a 5.0 control plane has unknown credentials, it should pass", version: "5.0.0"},
		{name: "When a 5.1 control plane has valid credentials, it should pass", version: "5.1.0"},
		{name: "When a 5.1 credential condition is unknown, it should report the mismatch", version: "5.1.0", wrongCredential: true},
		{name: "When a required identity condition is missing, it should report the missing condition", version: "5.1.0", missingIdentity: true},
		{name: "When multiple conditions fail, it should report every failure", version: "5.1.0", wrongCredential: true, missingIdentity: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hc := &hyperv1.HostedCluster{}
			hc.Spec.Platform.Type = hyperv1.GCPPlatform
			hc.Status.ControlPlaneVersion.Desired.Version = tc.version
			for conditionType, status := range conditions.ExpectedHCConditions(hc) {
				hc.Status.Conditions = append(hc.Status.Conditions, metav1.Condition{Type: string(conditionType), Status: status})
			}
			var expectedReasons []string
			if tc.wrongCredential {
				meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ValidGCPCredentials)).Status = metav1.ConditionUnknown
				expectedReasons = append(expectedReasons, "incorrect condition: wanted ValidGCPCredentials=True, got ValidGCPCredentials=Unknown")
			}
			if tc.missingIdentity {
				meta.RemoveStatusCondition(&hc.Status.Conditions, string(hyperv1.ValidGCPWorkloadIdentity))
				expectedReasons = append(expectedReasons, "missing condition: wanted ValidGCPWorkloadIdentity=True, did not find condition of this type")
			}

			done, reason, err := hostedClusterConditionsPredicate(true, nil)(hc)
			if err != nil {
				t.Fatalf("unexpected predicate error: %v", err)
			}
			if wantDone := len(expectedReasons) == 0; done != wantDone {
				t.Fatalf("expected done=%t, got %t: %s", wantDone, done, reason)
			}
			if len(expectedReasons) == 0 {
				if reason != "" {
					t.Errorf("expected no failure reason, got %q", reason)
				}
				return
			}
			actualReasons := strings.Split(reason, "; ")
			if len(actualReasons) != len(expectedReasons) {
				t.Errorf("expected %d failure reasons, got %q", len(expectedReasons), reason)
			}
			for _, expectedReason := range expectedReasons {
				if !strings.Contains(reason, expectedReason) {
					t.Errorf("expected failure reason %q in %q", expectedReason, reason)
				}
			}
		})
	}
}

func TestValidateHostedClusterConditions(t *testing.T) {
	originalVersion := releaseVersion
	releaseVersion = Version51
	t.Cleanup(func() { releaseVersion = originalVersion })
	for _, tc := range []struct {
		name, initialVersion, currentVersion string
		workers                              bool
	}{
		{"When a 5.0 control plane is healthy, it should accept unknown credentials", "5.0.0", "5.0.0", true},
		{"When the fetched control plane upgrades to 5.1, it should require fresh runtime results", "5.0.0", "5.1.0", true},
		{"When the fetched control plane is 5.0, it should refresh a stale 5.1 expectation", "5.1.0", "5.0.0", true},
		{"When there are no workers, it should retain connection and version exceptions", "5.0.0", "5.1.0", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hc := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Name: "gcp", Namespace: "clusters"}}
			hc.Spec.Platform.Type = hyperv1.GCPPlatform
			hc.Status.ControlPlaneVersion.Desired.Version = tc.initialVersion
			fetched := hc.DeepCopy()
			fetched.Status.ControlPlaneVersion.Desired.Version = tc.currentVersion
			fetched.Status.ControlPlaneVersion.Desired.Image = "quay.io/openshift-release-dev/ocp-release:5.1.0-x86_64"
			fetched.Status.ControlPlaneVersion.History = []hyperv1.ControlPlaneUpdateHistory{{State: configv1.CompletedUpdate}}
			expected := conditions.ExpectedHCConditions(fetched)
			if !tc.workers {
				expected[hyperv1.ClusterVersionAvailable] = metav1.ConditionFalse
				expected[hyperv1.ClusterVersionSucceeding] = metav1.ConditionFalse
				expected[hyperv1.ClusterVersionProgressing] = metav1.ConditionTrue
				expected[hyperv1.DataPlaneConnectionAvailable] = metav1.ConditionUnknown
				expected[hyperv1.ControlPlaneConnectionAvailable] = metav1.ConditionUnknown
			}
			for conditionType, status := range expected {
				fetched.Status.Conditions = append(fetched.Status.Conditions, metav1.Condition{Type: string(conditionType), Status: status, Reason: hyperv1.AsExpectedReason})
			}
			c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(fetched).Build()
			ValidateHostedClusterConditions(t, t.Context(), c, hc, tc.workers, time.Second, nil)
		})
	}
}
