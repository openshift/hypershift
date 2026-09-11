package karpenterignition

import (
	"testing"

	"github.com/openshift/hypershift/support/ntotuning"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const testTunedYAML = `
apiVersion: tuned.openshift.io/v1
kind: Tuned
metadata:
  name: tuned-1
  namespace: openshift-cluster-node-tuning-operator
spec:
  profile:
  - name: tuned-1-profile
    data: |
      [main]
      summary=test
  recommend:
  - priority: 20
    profile: tuned-1-profile
`

func TestGetTuningConfigFromMirroredConfigMap(t *testing.T) {
	tuningCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "tuned-1",
			Namespace: "clusters-test",
		},
		Data: map[string]string{ntotuning.ConfigKey: testTunedYAML},
	}

	scheme := runtime.NewScheme()
	_ = corev1.AddToScheme(scheme)

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tuningCM).Build()

	tuned, pp, ppName, err := ntotuning.GetTuningConfig(t.Context(), c, "clusters-test", []corev1.LocalObjectReference{{Name: "tuned-1"}})
	if err != nil {
		t.Fatalf("GetTuningConfig: %v", err)
	}
	if tuned == "" {
		t.Fatal("expected tuned config")
	}
	if pp != "" || ppName != "" {
		t.Fatalf("expected no performance profile, got pp=%q name=%q", pp, ppName)
	}
}
