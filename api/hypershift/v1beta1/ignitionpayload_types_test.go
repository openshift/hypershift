package v1beta1

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestIgnitionPayloadRoundTripAndDeepCopy(t *testing.T) {
	// Use a time that round-trips cleanly through JSON (RFC3339 without nanoseconds).
	now := metav1.NewTime(time.Now().Truncate(time.Second))
	in := &IgnitionPayload{
		ObjectMeta: metav1.ObjectMeta{Name: "np-1", Namespace: "hcp-ns"},
		Spec: IgnitionPayloadSpec{
			ReleaseImage:             "quay.io/openshift-release-dev/ocp-release@sha256:abc",
			PullSecret:               SecretReference{Name: "pull-secret"},
			AdditionalTrustBundle:    ConfigMapReference{Name: "trust-bundle"},
			OSImageStream:            OSImageStreamReference{Name: "rhel-9"},
			RolloutGlobalConfig:      ConfigMapReference{Name: "np-1-rollout-global"},
			AdditionalRolloutInputs:  "private=true;port=6443",
			RolloutConfigMaps:        []ConfigMapReference{{Name: "user-cfg"}, {Name: "core-cfg"}},
			ManagementConfigMaps:     []ConfigMapReference{{Name: "haproxy-cfg"}},
			RetiredRolloutGeneration: 2,
		},
		Status: IgnitionPayloadStatus{
			Conditions:         []metav1.Condition{{Type: IgnitionPayloadPayloadGeneratedCondition, Status: metav1.ConditionTrue, Reason: "AsExpected", LastTransitionTime: now}},
			Current:            PayloadReference{ConfigHash: "h1", RolloutHash: "r1", UserDataSecretName: "np-1-3", RolloutGeneration: 3},
			Previous:           PayloadReference{ConfigHash: "h0", RolloutHash: "r0", UserDataSecretName: "np-1-2", RolloutGeneration: 2},
			RolloutHashVersion: 1,
		},
	}

	// JSON round-trip preserves all fields.
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("failed to marshal IgnitionPayload: %v", err)
	}
	out := &IgnitionPayload{}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatalf("failed to unmarshal IgnitionPayload: %v", err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("JSON round-trip failed: got %+v, want %+v", out, in)
	}

	// DeepCopy is a true copy (no shared backing arrays).
	cp := in.DeepCopy()
	if !reflect.DeepEqual(in, cp) {
		t.Errorf("DeepCopy failed: got %+v, want %+v", cp, in)
	}
	cp.Spec.RolloutConfigMaps[0].Name = "mutated"
	if in.Spec.RolloutConfigMaps[0].Name != "user-cfg" {
		t.Errorf("DeepCopy shares backing array: original was modified to %q", in.Spec.RolloutConfigMaps[0].Name)
	}
}
