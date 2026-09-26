package controlplanecomponent

import (
	"testing"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestSetReplicasAndStrategy(t *testing.T) {
	tests := []struct {
		name               string
		replicas           int32
		isRequestServing   bool
		wantMaxSurge       *intstr.IntOrString
		wantMaxUnavailable *intstr.IntOrString
	}{
		{
			name:               "1 replica: no rolling update strategy set",
			replicas:           1,
			isRequestServing:   false,
			wantMaxSurge:       nil,
			wantMaxUnavailable: nil,
		},
		{
			name:               "1 replica request-serving: no rolling update strategy set",
			replicas:           1,
			isRequestServing:   true,
			wantMaxSurge:       nil,
			wantMaxUnavailable: nil,
		},
		{
			name:               "2 replicas: maxSurge=1, maxUnavailable=0",
			replicas:           2,
			isRequestServing:   false,
			wantMaxSurge:       intOrStringPtr(1),
			wantMaxUnavailable: intOrStringPtr(0),
		},
		{
			name:               "2 replicas request-serving: maxSurge=1, maxUnavailable=0",
			replicas:           2,
			isRequestServing:   true,
			wantMaxSurge:       intOrStringPtr(1),
			wantMaxUnavailable: intOrStringPtr(0),
		},
		{
			name:               "3 replicas: maxSurge=0, maxUnavailable=1",
			replicas:           3,
			isRequestServing:   false,
			wantMaxSurge:       intOrStringPtr(0),
			wantMaxUnavailable: intOrStringPtr(1),
		},
		{
			name:               "3 replicas request-serving: maxSurge=0, maxUnavailable=1",
			replicas:           3,
			isRequestServing:   true,
			wantMaxSurge:       intOrStringPtr(0),
			wantMaxUnavailable: intOrStringPtr(1),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			deploy := &appsv1.Deployment{}
			provider := &deploymentProvider{}

			provider.SetReplicasAndStrategy(deploy, tc.replicas, tc.isRequestServing)

			g.Expect(*deploy.Spec.Replicas).To(Equal(tc.replicas), "replicas should match")
			g.Expect(*deploy.Spec.RevisionHistoryLimit).To(Equal(int32(2)), "revision history limit should be 2")

			if tc.wantMaxSurge == nil {
				g.Expect(deploy.Spec.Strategy.RollingUpdate).To(BeNil(), "rolling update should not be set for single replica")
			} else {
				g.Expect(deploy.Spec.Strategy.Type).To(Equal(appsv1.RollingUpdateDeploymentStrategyType), "strategy type should be RollingUpdate")
				g.Expect(deploy.Spec.Strategy.RollingUpdate).ToNot(BeNil(), "rolling update should be set")
				g.Expect(deploy.Spec.Strategy.RollingUpdate.MaxSurge).To(Equal(tc.wantMaxSurge), "maxSurge should match")
				g.Expect(deploy.Spec.Strategy.RollingUpdate.MaxUnavailable).To(Equal(tc.wantMaxUnavailable), "maxUnavailable should match")
			}
		})
	}
}

func intOrStringPtr(val int) *intstr.IntOrString {
	v := intstr.FromInt(val)
	return &v
}
