package scheduler

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	componentbasev1 "k8s.io/component-base/config/v1alpha1"
	schedulerv1 "k8s.io/kube-scheduler/config/v1"
	"k8s.io/utils/ptr"
)

func TestGenerateConfig(t *testing.T) {
	g := NewWithT(t)
	leaseDuration, err := time.ParseDuration(config.RecommendedLeaseDuration)
	g.Expect(err).ShouldNot(HaveOccurred())
	renewDeadline, err := time.ParseDuration(config.RecommendedRenewDeadline)
	g.Expect(err).ShouldNot(HaveOccurred())
	retryPeriod, err := time.ParseDuration(config.RecommendedRetryPeriod)
	g.Expect(err).ShouldNot(HaveOccurred())

	leaderElectionConfig := componentbasev1.LeaderElectionConfiguration{
		LeaderElect:   ptr.To(true),
		LeaseDuration: metav1.Duration{Duration: leaseDuration},
		RenewDeadline: metav1.Duration{Duration: renewDeadline},
		RetryPeriod:   metav1.Duration{Duration: retryPeriod},
	}

	testCases := []struct {
		name                   string
		profile                configv1.SchedulerProfile
		expectedLeaderElection componentbasev1.LeaderElectionConfiguration
		expectedProfiles       []schedulerv1.KubeSchedulerProfile
	}{
		{
			name:                   "Leader elect args get set correctly, default profile",
			profile:                configv1.LowNodeUtilization,
			expectedLeaderElection: leaderElectionConfig,
			expectedProfiles:       []schedulerv1.KubeSchedulerProfile{},
		},
		{
			name:                   "high node utilization profile",
			profile:                configv1.HighNodeUtilization,
			expectedLeaderElection: leaderElectionConfig,
			expectedProfiles:       []schedulerv1.KubeSchedulerProfile{highNodeUtilizationProfile()},
		},
		{
			name:                   "no scoring profile",
			profile:                configv1.NoScoring,
			expectedLeaderElection: leaderElectionConfig,
			expectedProfiles:       []schedulerv1.KubeSchedulerProfile{highNodeUtilizationProfile()},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cpContext := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Configuration: &hyperv1.ClusterConfiguration{
							Scheduler: &configv1.SchedulerSpec{
								Profile: tc.profile,
							},
						},
					},
				},
			}

			cm := &corev1.ConfigMap{}
			_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", cm)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptConfigMap(cpContext, cm)
			g.Expect(err).ToNot(HaveOccurred())

			var data schedulerv1.KubeSchedulerConfiguration
			err = json.Unmarshal([]byte(cm.Data[kubeSchedulerConfigKey]), &data)
			if err != nil {
				t.Errorf("unexpected error parsing config")
			}

			if !reflect.DeepEqual(data.LeaderElection, tc.expectedLeaderElection) {
				t.Errorf("expected leader election parameters not found")
			}
			g.Expect(data.Profiles).To(HaveLen(len(tc.expectedProfiles)))

			configMapYaml, err := util.SerializeResource(cm, api.Scheme)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			testutil.CompareWithFixture(t, configMapYaml)
		})
	}
}
