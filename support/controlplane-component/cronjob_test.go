package controlplanecomponent

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestCronJobProviderIsAvailable(t *testing.T) {
	provider := &cronJobProvider{}

	t.Run("When CronJob is not suspended it should be available", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cronJob := &batchv1.CronJob{}

		status, reason, _ := provider.IsAvailable(cronJob)
		g.Expect(status).To(Equal(metav1.ConditionTrue))
		g.Expect(reason).To(Equal(hyperv1.AsExpectedReason))
	})

	t.Run("When CronJob is suspended it should not be available", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cronJob := &batchv1.CronJob{
			Spec: batchv1.CronJobSpec{
				Suspend: ptr.To(true),
			},
		}

		status, reason, message := provider.IsAvailable(cronJob)
		g.Expect(status).To(Equal(metav1.ConditionFalse))
		g.Expect(reason).To(Equal("CronJobSuspended"))
		g.Expect(message).To(Equal("CronJob is suspended"))
	})

	t.Run("When Suspend is explicitly false it should be available", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cronJob := &batchv1.CronJob{
			Spec: batchv1.CronJobSpec{
				Suspend: ptr.To(false),
			},
		}

		status, reason, _ := provider.IsAvailable(cronJob)
		g.Expect(status).To(Equal(metav1.ConditionTrue))
		g.Expect(reason).To(Equal(hyperv1.AsExpectedReason))
	})
}

func TestCronJobProviderIsReady(t *testing.T) {
	provider := &cronJobProvider{}
	now := metav1.NewTime(time.Now())

	t.Run("When CronJob has a successful run it should be ready", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cronJob := &batchv1.CronJob{
			Status: batchv1.CronJobStatus{
				LastSuccessfulTime: &now,
				LastScheduleTime:   &now,
			},
		}

		status, reason, _ := provider.IsReady(cronJob)
		g.Expect(status).To(Equal(metav1.ConditionTrue))
		g.Expect(reason).To(Equal(hyperv1.AsExpectedReason))
	})

	t.Run("When CronJob has been scheduled but not succeeded it should not be ready", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cronJob := &batchv1.CronJob{
			Status: batchv1.CronJobStatus{
				LastScheduleTime: &now,
			},
		}

		status, reason, message := provider.IsReady(cronJob)
		g.Expect(status).To(Equal(metav1.ConditionFalse))
		g.Expect(reason).To(Equal("WaitingForSuccess"))
		g.Expect(message).To(ContainSubstring("no successful completion"))
	})

	t.Run("When CronJob has never been scheduled it should not be ready", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cronJob := &batchv1.CronJob{}

		status, reason, message := provider.IsReady(cronJob)
		g.Expect(status).To(Equal(metav1.ConditionFalse))
		g.Expect(reason).To(Equal("WaitingForFirstSchedule"))
		g.Expect(message).To(ContainSubstring("not been scheduled"))
	})

	t.Run("When CronJob is suspended it should not be ready", func(t *testing.T) {
		g := NewGomegaWithT(t)
		cronJob := &batchv1.CronJob{
			Spec: batchv1.CronJobSpec{
				Suspend: ptr.To(true),
			},
			Status: batchv1.CronJobStatus{
				LastSuccessfulTime: &now,
			},
		}

		status, reason, _ := provider.IsReady(cronJob)
		g.Expect(status).To(Equal(metav1.ConditionFalse))
		g.Expect(reason).To(Equal("CronJobSuspended"))
	})
}
