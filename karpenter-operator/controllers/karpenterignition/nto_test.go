package karpenterignition

import (
	"testing"

	"github.com/openshift/hypershift/support/ntotuning"

	"k8s.io/apimachinery/pkg/runtime"
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

func TestGetTuningConfigFromRaw(t *testing.T) {
	rawConfigs := []runtime.RawExtension{
		{Raw: []byte(testTunedYAML)},
	}

	tuned, pp, ppName, err := ntotuning.GetTuningConfigFromRaw(rawConfigs)
	if err != nil {
		t.Fatalf("GetTuningConfigFromRaw: %v", err)
	}
	if tuned == "" {
		t.Fatal("expected tuned config")
	}
	if pp != "" || ppName != "" {
		t.Fatalf("expected no performance profile, got pp=%q name=%q", pp, ppName)
	}
}
