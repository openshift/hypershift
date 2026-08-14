package util

import (
	"context"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNewCleanupTracker(t *testing.T) {
	g := NewWithT(t)
	tracker := NewCleanupTracker()

	g.Expect(tracker).ToNot(BeNil())
	g.Expect(tracker.failures).ToNot(BeNil())
}

func TestRecordFailure(t *testing.T) {
	g := NewWithT(t)
	tracker := NewCleanupTracker()
	hcpKey := "test-namespace/test-hcp"

	// Record first failure
	tracker.RecordFailure(hcpKey)
	g.Expect(tracker.GetFailureCount(hcpKey)).To(Equal(1))
	firstFailureTime := tracker.GetFirstFailureTime(hcpKey)
	g.Expect(firstFailureTime).ToNot(BeZero())

	// Record second failure
	tracker.RecordFailure(hcpKey)
	g.Expect(tracker.GetFailureCount(hcpKey)).To(Equal(2))
	// First failure time should remain the same
	g.Expect(tracker.GetFirstFailureTime(hcpKey)).To(Equal(firstFailureTime))

	// Record third failure
	tracker.RecordFailure(hcpKey)
	g.Expect(tracker.GetFailureCount(hcpKey)).To(Equal(3))
}

func TestResetFailures(t *testing.T) {
	g := NewWithT(t)
	tracker := NewCleanupTracker()
	hcpKey := "test-namespace/test-hcp"

	// Record failures
	tracker.RecordFailure(hcpKey)
	tracker.RecordFailure(hcpKey)
	g.Expect(tracker.GetFailureCount(hcpKey)).To(Equal(2))

	// Reset failures
	tracker.ResetFailures(hcpKey)
	g.Expect(tracker.GetFailureCount(hcpKey)).To(Equal(0))
	g.Expect(tracker.GetFirstFailureTime(hcpKey)).To(BeZero())
}

func TestShouldSkipCleanup_KubeAPIServerUnavailable(t *testing.T) {
	g := NewWithT(t)
	tracker := NewCleanupTracker()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}

	// Create fake client without kube-apiserver deployment
	cpClient := fake.NewClientBuilder().Build()

	shouldSkip, reason, err := tracker.ShouldSkipCleanup(context.Background(), hcp, cpClient)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(shouldSkip).To(BeTrue())
	g.Expect(reason).To(Equal("KubeAPIServerUnavailable"))
}

func TestShouldSkipCleanup_KubeAPIServerAvailable(t *testing.T) {
	g := NewWithT(t)
	tracker := NewCleanupTracker()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}

	// Create fake client with kube-apiserver deployment
	kasDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "test-namespace",
		},
	}
	cpClient := fake.NewClientBuilder().WithObjects(kasDeployment).Build()

	shouldSkip, reason, err := tracker.ShouldSkipCleanup(context.Background(), hcp, cpClient)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(shouldSkip).To(BeFalse())
	g.Expect(reason).To(BeEmpty())
}

func TestShouldSkipCleanup_MaxFailuresExceeded(t *testing.T) {
	g := NewWithT(t)
	tracker := NewCleanupTracker()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}
	hcpKey := client.ObjectKeyFromObject(hcp).String()

	// Create fake client with kube-apiserver deployment
	kasDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "test-namespace",
		},
	}
	cpClient := fake.NewClientBuilder().WithObjects(kasDeployment).Build()

	// Record MaxCleanupFailures failures
	for i := 0; i < MaxCleanupFailures; i++ {
		tracker.RecordFailure(hcpKey)
	}

	shouldSkip, reason, err := tracker.ShouldSkipCleanup(context.Background(), hcp, cpClient)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(shouldSkip).To(BeTrue())
	g.Expect(reason).To(Equal("MaxConnectionFailuresExceeded"))
}

func TestShouldSkipCleanup_BelowMaxFailures(t *testing.T) {
	g := NewWithT(t)
	tracker := NewCleanupTracker()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}
	hcpKey := client.ObjectKeyFromObject(hcp).String()

	// Create fake client with kube-apiserver deployment
	kasDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "test-namespace",
		},
	}
	cpClient := fake.NewClientBuilder().WithObjects(kasDeployment).Build()

	// Record failures below the threshold
	for i := 0; i < MaxCleanupFailures-1; i++ {
		tracker.RecordFailure(hcpKey)
	}

	shouldSkip, reason, err := tracker.ShouldSkipCleanup(context.Background(), hcp, cpClient)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(shouldSkip).To(BeFalse())
	g.Expect(reason).To(BeEmpty())
}

func TestShouldSkipCleanup_MaxDurationExceeded(t *testing.T) {
	g := NewWithT(t)
	tracker := NewCleanupTracker()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}
	hcpKey := client.ObjectKeyFromObject(hcp).String()

	// Create fake client with kube-apiserver deployment
	kasDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "test-namespace",
		},
	}
	cpClient := fake.NewClientBuilder().WithObjects(kasDeployment).Build()

	// Record a failure and manually set the first failure time to the past
	tracker.RecordFailure(hcpKey)
	tracker.mutex.Lock()
	tracker.failures[hcpKey].firstFailureTime = time.Now().Add(-MaxCleanupFailureDuration - time.Second)
	tracker.mutex.Unlock()

	shouldSkip, reason, err := tracker.ShouldSkipCleanup(context.Background(), hcp, cpClient)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(shouldSkip).To(BeTrue())
	g.Expect(reason).To(Equal("ConnectionFailureTimeout"))
}

func TestShouldSkipCleanup_BelowMaxDuration(t *testing.T) {
	g := NewWithT(t)
	tracker := NewCleanupTracker()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}
	hcpKey := client.ObjectKeyFromObject(hcp).String()

	// Create fake client with kube-apiserver deployment
	kasDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "test-namespace",
		},
	}
	cpClient := fake.NewClientBuilder().WithObjects(kasDeployment).Build()

	// Record a failure (first failure time will be set to now)
	tracker.RecordFailure(hcpKey)

	shouldSkip, reason, err := tracker.ShouldSkipCleanup(context.Background(), hcp, cpClient)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(shouldSkip).To(BeFalse())
	g.Expect(reason).To(BeEmpty())
}

func TestConcurrentAccess(t *testing.T) {
	g := NewWithT(t)
	tracker := NewCleanupTracker()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}
	hcpKey := client.ObjectKeyFromObject(hcp).String()

	// Create fake client with kube-apiserver deployment
	kasDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kube-apiserver",
			Namespace: "test-namespace",
		},
	}
	cpClient := fake.NewClientBuilder().WithObjects(kasDeployment).Build()

	// Simulate concurrent access
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			tracker.RecordFailure(hcpKey)
			tracker.GetFailureCount(hcpKey)
			tracker.GetFirstFailureTime(hcpKey)
			_, _, _ = tracker.ShouldSkipCleanup(context.Background(), hcp, cpClient)
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify the final count is consistent
	count := tracker.GetFailureCount(hcpKey)
	g.Expect(count).To(Equal(10))
}

func TestMultipleHCPs(t *testing.T) {
	g := NewWithT(t)
	tracker := NewCleanupTracker()

	hcp1Key := "namespace1/hcp1"
	hcp2Key := "namespace2/hcp2"

	// Record failures for both HCPs
	tracker.RecordFailure(hcp1Key)
	tracker.RecordFailure(hcp1Key)
	tracker.RecordFailure(hcp2Key)

	// Verify independent tracking
	g.Expect(tracker.GetFailureCount(hcp1Key)).To(Equal(2))
	g.Expect(tracker.GetFailureCount(hcp2Key)).To(Equal(1))

	// Reset only hcp1
	tracker.ResetFailures(hcp1Key)
	g.Expect(tracker.GetFailureCount(hcp1Key)).To(Equal(0))
	g.Expect(tracker.GetFailureCount(hcp2Key)).To(Equal(1))
}

func TestIsKubeAPIServerAvailable(t *testing.T) {
	tests := []struct {
		name        string
		deployment  *appsv1.Deployment
		expected    bool
		expectError bool
	}{
		{
			name: "KubeAPIServer exists",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kube-apiserver",
					Namespace: "test-namespace",
				},
			},
			expected:    true,
			expectError: false,
		},
		{
			name:        "KubeAPIServer does not exist",
			deployment:  nil,
			expected:    false,
			expectError: false,
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

			var cpClient client.Client
			if tt.deployment != nil {
				cpClient = fake.NewClientBuilder().WithObjects(tt.deployment).Build()
			} else {
				cpClient = fake.NewClientBuilder().Build()
			}

			available, err := isKubeAPIServerAvailable(context.Background(), hcp, cpClient)

			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(available).To(Equal(tt.expected))
			}
		})
	}
}
