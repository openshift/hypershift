package collectprofiles

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptCronJob(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		namespace        string
		expectedSchedule string
	}{
		{
			name:             "When namespace is test-namespace, it should generate consistent schedule",
			namespace:        "test-namespace",
			expectedSchedule: "9 21 * * *", // Based on modular calculation of "test-namespace"
		},
		{
			name:             "When namespace is different, it should generate different schedule",
			namespace:        "another-namespace",
			expectedSchedule: "29 5 * * *", // Based on modular calculation of "another-namespace"
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: tc.namespace,
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			cronJob, err := assets.LoadCronJobManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())
			cronJob.Namespace = tc.namespace

			err = adaptCronJob(cpContext, cronJob)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(cronJob.Spec.Schedule).To(Equal(tc.expectedSchedule))
		})
	}
}

func TestGenerateModularDailyCronSchedule(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		input            []byte
		expectedSchedule string
	}{
		{
			name:             "When input is empty, it should return 0 0 * * *",
			input:            []byte{},
			expectedSchedule: "0 0 * * *",
		},
		{
			name:             "When input is single byte, it should calculate modulo correctly",
			input:            []byte{65}, // ASCII 'A'
			expectedSchedule: "5 17 * * *",
		},
		{
			name:             "When input is test-namespace, it should return deterministic schedule",
			input:            []byte("test-namespace"),
			expectedSchedule: "9 21 * * *",
		},
		{
			name:             "When input is another-namespace, it should return different schedule",
			input:            []byte("another-namespace"),
			expectedSchedule: "29 5 * * *",
		},
		{
			name:             "When input is very-long-namespace-name, it should handle large values",
			input:            []byte("very-long-namespace-name-with-many-characters"),
			expectedSchedule: "11 11 * * *",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			schedule := generateModularDailyCronSchedule(tc.input)
			g.Expect(schedule).To(Equal(tc.expectedSchedule))
		})
	}
}

func TestGenerateModularDailyCronScheduleProperties(t *testing.T) {
	t.Parallel()

	t.Run("When generating schedules, minute should be within 0-59", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		for i := 0; i < 1000; i++ {
			input := []byte{byte(i), byte(i >> 8)}
			schedule := generateModularDailyCronSchedule(input)

			// Parse the schedule
			var minute, hour int
			_, err := fmt.Sscanf(schedule, "%d %d * * *", &minute, &hour)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(minute).To(BeNumerically(">=", 0))
			g.Expect(minute).To(BeNumerically("<", 60))
		}
	})

	t.Run("When generating schedules, hour should be within 0-23", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		for i := 0; i < 1000; i++ {
			input := []byte{byte(i), byte(i >> 8)}
			schedule := generateModularDailyCronSchedule(input)

			// Parse the schedule
			var minute, hour int
			_, err := fmt.Sscanf(schedule, "%d %d * * *", &minute, &hour)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(hour).To(BeNumerically(">=", 0))
			g.Expect(hour).To(BeNumerically("<", 24))
		}
	})

	t.Run("When input is same, it should return same schedule", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		input := []byte("consistent-namespace")
		schedule1 := generateModularDailyCronSchedule(input)
		schedule2 := generateModularDailyCronSchedule(input)

		g.Expect(schedule1).To(Equal(schedule2))
	})
}
