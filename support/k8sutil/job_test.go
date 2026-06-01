package k8sutil

import (
	"testing"

	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFindJobCondition(t *testing.T) {
	t.Run("When condition exists it should return a pointer to it", func(t *testing.T) {
		g := NewWithT(t)
		job := &batchv1.Job{
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{Type: batchv1.JobComplete, Status: "True", Reason: "done"},
				},
			},
		}
		cond := FindJobCondition(job, batchv1.JobComplete)
		g.Expect(cond).ToNot(BeNil())
		g.Expect(cond.Reason).To(Equal("done"))
	})

	t.Run("When condition does not exist it should return nil", func(t *testing.T) {
		g := NewWithT(t)
		job := &batchv1.Job{
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{
					{Type: batchv1.JobComplete, Status: "True"},
				},
			},
		}
		cond := FindJobCondition(job, batchv1.JobFailed)
		g.Expect(cond).To(BeNil())
	})

	t.Run("When job has no conditions it should return nil", func(t *testing.T) {
		g := NewWithT(t)
		job := &batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
		}
		cond := FindJobCondition(job, batchv1.JobComplete)
		g.Expect(cond).To(BeNil())
	})
}
