package reconcilerpolicy

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestNewCleanupTracker(t *testing.T) {
	t.Run("When a CleanupTracker is created, it should initialize empty failure tracking", func(t *testing.T) {
		g := NewWithT(t)

		tracker := NewCleanupTracker()

		g.Expect(tracker).ToNot(BeNil())
		g.Expect(tracker.failures).ToNot(BeNil())
		g.Expect(tracker.failures).To(BeEmpty())
	})
}

func TestRecordFailure(t *testing.T) {
	t.Run("When failures are recorded repeatedly for one HCP, it should increment the count and preserve the first failure time", func(t *testing.T) {
		g := NewWithT(t)
		tracker := NewCleanupTracker()
		hcpKey := "test-namespace/test-hcp"

		tracker.RecordFailure(hcpKey)
		g.Expect(tracker.GetFailureCount(hcpKey)).To(Equal(1))
		firstFailureTime := tracker.GetFirstFailureTime(hcpKey)
		g.Expect(firstFailureTime).ToNot(BeZero())

		tracker.RecordFailure(hcpKey)
		g.Expect(tracker.GetFailureCount(hcpKey)).To(Equal(2))
		g.Expect(tracker.GetFirstFailureTime(hcpKey)).To(Equal(firstFailureTime))

		tracker.RecordFailure(hcpKey)
		g.Expect(tracker.GetFailureCount(hcpKey)).To(Equal(3))
	})

	t.Run("When failures are recorded for multiple HCPs, it should track each HCP independently", func(t *testing.T) {
		g := NewWithT(t)
		tracker := NewCleanupTracker()
		firstHCPKey := "namespace1/hcp1"
		secondHCPKey := "namespace2/hcp2"

		tracker.RecordFailure(firstHCPKey)
		tracker.RecordFailure(firstHCPKey)
		tracker.RecordFailure(secondHCPKey)

		g.Expect(tracker.GetFailureCount(firstHCPKey)).To(Equal(2))
		g.Expect(tracker.GetFailureCount(secondHCPKey)).To(Equal(1))
	})

	t.Run("When failures are recorded and read concurrently, it should retain every recorded failure", func(t *testing.T) {
		g := NewWithT(t)
		tracker := NewCleanupTracker()
		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: "test-namespace",
			},
		}
		hcpKey := client.ObjectKeyFromObject(hcp).String()
		cpClient := fake.NewClientBuilder().WithObjects(&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kube-apiserver",
				Namespace: hcp.Namespace,
			},
		}).Build()
		ctx := t.Context()

		const workers = 10
		done := make(chan error, workers)
		for i := 0; i < workers; i++ {
			go func() {
				tracker.RecordFailure(hcpKey)
				tracker.GetFailureCount(hcpKey)
				tracker.GetFirstFailureTime(hcpKey)
				_, _, err := tracker.ShouldSkipCleanup(ctx, hcp, cpClient)
				done <- err
			}()
		}

		for i := 0; i < workers; i++ {
			g.Expect(<-done).ToNot(HaveOccurred())
		}
		g.Expect(tracker.GetFailureCount(hcpKey)).To(Equal(workers))
	})
}

func TestResetFailures(t *testing.T) {
	t.Run("When failures for an HCP are reset, it should clear the count and first failure time", func(t *testing.T) {
		g := NewWithT(t)
		tracker := NewCleanupTracker()
		hcpKey := "test-namespace/test-hcp"

		tracker.RecordFailure(hcpKey)
		tracker.RecordFailure(hcpKey)
		g.Expect(tracker.GetFailureCount(hcpKey)).To(Equal(2))

		tracker.ResetFailures(hcpKey)

		g.Expect(tracker.GetFailureCount(hcpKey)).To(BeZero())
		g.Expect(tracker.GetFirstFailureTime(hcpKey)).To(BeZero())
	})

	t.Run("When one of multiple HCPs is reset, it should preserve the other HCP's failures", func(t *testing.T) {
		g := NewWithT(t)
		tracker := NewCleanupTracker()
		firstHCPKey := "namespace1/hcp1"
		secondHCPKey := "namespace2/hcp2"

		tracker.RecordFailure(firstHCPKey)
		tracker.RecordFailure(firstHCPKey)
		tracker.RecordFailure(secondHCPKey)

		tracker.ResetFailures(firstHCPKey)

		g.Expect(tracker.GetFailureCount(firstHCPKey)).To(BeZero())
		g.Expect(tracker.GetFirstFailureTime(firstHCPKey)).To(BeZero())
		g.Expect(tracker.GetFailureCount(secondHCPKey)).To(Equal(1))
		g.Expect(tracker.GetFirstFailureTime(secondHCPKey)).ToNot(BeZero())
	})
}

func TestShouldSkipCleanup(t *testing.T) {
	getError := errors.New("kube-apiserver lookup failed")
	tests := []struct {
		name                   string
		kubeAPIServerAvailable bool
		failureCount           int
		firstFailureAge        time.Duration
		getError               error
		expectedSkip           bool
		expectedReason         string
	}{
		{
			name:           "When the KubeAPIServer deployment is missing, it should skip cleanup because the KubeAPIServer is unavailable",
			expectedSkip:   true,
			expectedReason: "KubeAPIServerUnavailable",
		},
		{
			name:                   "When the KubeAPIServer is available and no failures are recorded, it should allow cleanup",
			kubeAPIServerAvailable: true,
		},
		{
			name:                   "When the cleanup failure count reaches the maximum, it should skip cleanup",
			kubeAPIServerAvailable: true,
			failureCount:           MaxCleanupFailures,
			expectedSkip:           true,
			expectedReason:         "MaxConnectionFailuresExceeded",
		},
		{
			name:                   "When the cleanup failure count is below the maximum, it should allow cleanup",
			kubeAPIServerAvailable: true,
			failureCount:           MaxCleanupFailures - 1,
		},
		{
			name:                   "When cleanup failures reach the maximum duration, it should skip cleanup",
			kubeAPIServerAvailable: true,
			failureCount:           1,
			firstFailureAge:        MaxCleanupFailureDuration,
			expectedSkip:           true,
			expectedReason:         "ConnectionFailureTimeout",
		},
		{
			name:                   "When cleanup failures remain below the maximum duration, it should allow cleanup",
			kubeAPIServerAvailable: true,
			failureCount:           1,
			firstFailureAge:        MaxCleanupFailureDuration - time.Minute,
		},
		{
			name:     "When the KubeAPIServer lookup fails, it should return the original error without skipping cleanup",
			getError: getError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			tracker := NewCleanupTracker()
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
			}
			hcpKey := client.ObjectKeyFromObject(hcp).String()

			for i := 0; i < tt.failureCount; i++ {
				tracker.RecordFailure(hcpKey)
			}
			if tt.firstFailureAge > 0 {
				tracker.mutex.Lock()
				tracker.failures[hcpKey].firstFailureTime = time.Now().Add(-tt.firstFailureAge)
				tracker.mutex.Unlock()
			}

			clientBuilder := fake.NewClientBuilder()
			if tt.kubeAPIServerAvailable {
				clientBuilder = clientBuilder.WithObjects(&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: hcp.Namespace,
					},
				})
			}
			if tt.getError != nil {
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return tt.getError
					},
				})
			}

			shouldSkip, reason, err := tracker.ShouldSkipCleanup(t.Context(), hcp, clientBuilder.Build())

			g.Expect(shouldSkip).To(Equal(tt.expectedSkip))
			g.Expect(reason).To(Equal(tt.expectedReason))
			if tt.getError != nil {
				g.Expect(err).To(BeIdenticalTo(tt.getError))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestIsKubeAPIServerAvailable(t *testing.T) {
	tests := []struct {
		name                   string
		kubeAPIServerAvailable bool
		readerError            error
		expected               bool
	}{
		{
			name:                   "When KubeAPIServer exists, it should return true",
			kubeAPIServerAvailable: true,
			expected:               true,
		},
		{
			name: "When KubeAPIServer does not exist, it should return false",
		},
		{
			name:        "When the KubeAPIServer lookup fails, it should return the error",
			readerError: errors.New("reader unavailable"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
			}

			clientBuilder := fake.NewClientBuilder()
			if tt.kubeAPIServerAvailable {
				clientBuilder = clientBuilder.WithObjects(&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kube-apiserver",
						Namespace: hcp.Namespace,
					},
				})
			}
			if tt.readerError != nil {
				clientBuilder = clientBuilder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
						return tt.readerError
					},
				})
			}
			cpClient := clientBuilder.Build()

			available, err := isKubeAPIServerAvailable(t.Context(), hcp, cpClient)

			g.Expect(available).To(Equal(tt.expected))
			if tt.readerError != nil {
				g.Expect(err).To(BeIdenticalTo(tt.readerError))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
