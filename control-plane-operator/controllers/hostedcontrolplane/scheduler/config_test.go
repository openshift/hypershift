package scheduler

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	componentbasev1 "k8s.io/component-base/config/v1alpha1"
	schedulerv1beta2 "k8s.io/kube-scheduler/config/v1beta2"
	"k8s.io/utils/pointer"
	"github.com/openshift/hypershift/support/config"
)

func TestGenerateConfig(t *testing.T) {
	leaseDuration, _ := time.ParseDuration(config.RecommendedLeaseDuration)
	renewDeadline, _ := time.ParseDuration(config.RecommendedRenewDeadline)
	retryPeriod, _ := time.ParseDuration(config.RecommendedRetryPeriod)
	testCases := []struct {
			expected: componentbasev1.LeaderElectionConfiguration{
				LeaderElect: pointer.BoolPtr(true),
				
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
