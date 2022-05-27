package scheduler

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"github.com/openshift/hypershift/support/config"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	componentbasev1 "k8s.io/component-base/config/v1alpha1"
	schedulerv1beta2 "k8s.io/kube-scheduler/config/v1beta2"
	"k8s.io/utils/pointer"
)

func TestGenerateConfig(t *testing.T) {
	g := NewWithT(t)
	leaseDuration, err := time.ParseDuration(config.RecommendedLeaseDuration)
	g.Expect(err).ShouldNot(HaveOccurred())
	renewDeadline, err := time.ParseDuration(config.RecommendedRenewDeadline)
	g.Expect(err).ShouldNot(HaveOccurred())
	retryPeriod, err := time.ParseDuration(config.RecommendedRetryPeriod)
	g.Expect(err).ShouldNot(HaveOccurred())

	testCases := []struct {
		name     string
		expected componentbasev1.LeaderElectionConfiguration
	}{
		{
			name: "Leader elect args get set correctly",
			expected: componentbasev1.LeaderElectionConfiguration{
				LeaderElect:   pointer.BoolPtr(true),
				LeaseDuration: metav1.Duration{Duration: leaseDuration},
				RenewDeadline: metav1.Duration{Duration: renewDeadline},
				RetryPeriod:   metav1.Duration{Duration: retryPeriod},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config, err := generateConfig()
			if err != nil {
				t.Errorf("unexpected error generated in config")
			}
			var data schedulerv1beta2.KubeSchedulerConfiguration
			err = json.Unmarshal([]byte(config), &data)
			if err != nil {
				t.Errorf("unexpected error parsing config")
			}

			if !reflect.DeepEqual(data.LeaderElection, tc.expected) {
				t.Errorf("expected leader election parameters not found")
			}
		})
	}
}
