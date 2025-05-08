package controlplanecomponent

import (
	"testing"

	. "github.com/onsi/gomega"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestJobProvider_IsAvailable(t *testing.T) {
	g := NewWithT(t)
	provider := &jobProvider{}

	testCases := []struct {
		name        string
		job         *batchv1.Job
		wantStatus  metav1.ConditionStatus
		wantReason  string
		wantMessage string
	}{
		{
			name: "Should return true when job is active",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Active: 1,
				},
			},
			wantStatus:  metav1.ConditionTrue,
			wantReason:  "JobActive",
			wantMessage: "Job is still running",
		},
		{
			name: "Should return true when job is completed",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{
							Type:   batchv1.JobComplete,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			wantStatus:  metav1.ConditionTrue,
			wantReason:  "JobComplete",
			wantMessage: "Job completed successfully",
		},
		{
			name: "Should return false when job has failed",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{
							Type:    batchv1.JobFailed,
							Status:  corev1.ConditionTrue,
							Reason:  "BackoffLimitExceeded",
							Message: "Job has reached the specified backoff limit",
						},
					},
				},
			},
			wantStatus:  metav1.ConditionFalse,
			wantReason:  "BackoffLimitExceeded",
			wantMessage: "Job has reached the specified backoff limit",
		},
		{
			name: "Should return false with Unknown reason when job status is empty",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{},
			},
			wantStatus:  metav1.ConditionFalse,
			wantReason:  "Unknown",
			wantMessage: "Job status unknown",
		},
		{
			name: "Should return false with Unknown reason when job conditions are false",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{
							Type:   batchv1.JobComplete,
							Status: corev1.ConditionFalse,
						},
						{
							Type:   batchv1.JobFailed,
							Status: corev1.ConditionFalse,
						},
					},
				},
			},
			wantStatus:  metav1.ConditionFalse,
			wantReason:  "Unknown",
			wantMessage: "Job status unknown",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			status, reason, message := provider.IsAvailable(tc.job)
			g.Expect(status).To(Equal(tc.wantStatus))
			g.Expect(reason).To(Equal(tc.wantReason))
			g.Expect(message).To(Equal(tc.wantMessage))
		})
	}
}

func TestJobProvider_IsReady(t *testing.T) {
	g := NewWithT(t)
	provider := &jobProvider{}

	testCases := []struct {
		name        string
		job         *batchv1.Job
		wantStatus  metav1.ConditionStatus
		wantReason  string
		wantMessage string
	}{
		{
			name: "Should return false when job is active",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Active: 1,
				},
			},
			wantStatus:  metav1.ConditionFalse,
			wantReason:  "JobActive",
			wantMessage: "Job is still running",
		},
		{
			name: "Should return true when job is completed",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{
							Type:   batchv1.JobComplete,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			wantStatus:  metav1.ConditionTrue,
			wantReason:  "JobComplete",
			wantMessage: "Job completed successfully",
		},
		{
			name: "Should return false when job has failed",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{
							Type:    batchv1.JobFailed,
							Status:  corev1.ConditionTrue,
							Reason:  "BackoffLimitExceeded",
							Message: "Job has reached the specified backoff limit",
						},
					},
				},
			},
			wantStatus:  metav1.ConditionFalse,
			wantReason:  "BackoffLimitExceeded",
			wantMessage: "Job has reached the specified backoff limit",
		},
		{
			name: "Should return false with Unknown reason when job status is empty",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{},
			},
			wantStatus:  metav1.ConditionFalse,
			wantReason:  "Unknown",
			wantMessage: "Job status unknown",
		},
		{
			name: "Should return false with Unknown reason when job conditions are false",
			job: &batchv1.Job{
				Status: batchv1.JobStatus{
					Conditions: []batchv1.JobCondition{
						{
							Type:   batchv1.JobComplete,
							Status: corev1.ConditionFalse,
						},
						{
							Type:   batchv1.JobFailed,
							Status: corev1.ConditionFalse,
						},
					},
				},
			},
			wantStatus:  metav1.ConditionFalse,
			wantReason:  "Unknown",
			wantMessage: "Job status unknown",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			status, reason, message := provider.IsReady(tc.job)
			g.Expect(status).To(Equal(tc.wantStatus))
			g.Expect(reason).To(Equal(tc.wantReason))
			g.Expect(message).To(Equal(tc.wantMessage))
		})
	}
}
