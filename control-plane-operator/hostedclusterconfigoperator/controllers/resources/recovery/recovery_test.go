package recovery

import (
	"context"
	"fmt"
	"sync"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// resetGlobalState resets the global variables for testing
func resetGlobalState() {
	pvcsDeleted.Store(false)
	podsDeleted.Store(false)
}

func TestRecoverMonitoringStack(t *testing.T) {
	tests := []struct {
		name           string
		setupObjects   []client.Object
		expectedResult bool
		expectError    bool
		errorContains  string
		multipleCalls  bool
	}{
		{
			name: "When monitoring stack is healthy it should return true",
			setupObjects: []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s",
						Namespace: "openshift-monitoring",
					},
					Spec: appsv1.StatefulSetSpec{
						Replicas: ptr.To[int32](3),
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "prometheus",
							},
						},
					},
					Status: appsv1.StatefulSetStatus{
						Replicas:          3,
						AvailableReplicas: 3,
					},
				},
			},
			expectedResult: true,
			expectError:    false,
		},
		{
			name:           "When prometheus statefulset is not found it should return error",
			setupObjects:   []client.Object{},
			expectedResult: false,
			expectError:    true,
			errorContains:  "prometheus statefulSet is still starting, rescheduling reconciliation",
		},
		{
			name: "When prometheus statefulset is not ready it should delete PVCs and pods and return false",
			setupObjects: []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s",
						Namespace: "openshift-monitoring",
					},
					Spec: appsv1.StatefulSetSpec{
						Replicas: ptr.To[int32](3),
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "prometheus",
							},
						},
					},
					Status: appsv1.StatefulSetStatus{
						Replicas:          3,
						AvailableReplicas: 1,
					},
				},
				&corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s-db-prometheus-k8s-0",
						Namespace: "openshift-monitoring",
					},
				},
				&corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s-db-prometheus-k8s-1",
						Namespace: "openshift-monitoring",
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s-0",
						Namespace: "openshift-monitoring",
						Labels: map[string]string{
							"app": "prometheus",
						},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s-1",
						Namespace: "openshift-monitoring",
						Labels: map[string]string{
							"app": "prometheus",
						},
					},
				},
			},
			expectedResult: false,
			expectError:    false,
		},
		{
			name: "When prometheus statefulset is not ready and no pods exist it should delete PVCs and return false",
			setupObjects: []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s",
						Namespace: "openshift-monitoring",
					},
					Spec: appsv1.StatefulSetSpec{
						Replicas: ptr.To[int32](3),
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "prometheus",
							},
						},
					},
					Status: appsv1.StatefulSetStatus{
						Replicas:          3,
						AvailableReplicas: 1,
					},
				},
				&corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s-db-prometheus-k8s-0",
						Namespace: "openshift-monitoring",
					},
				},
			},
			expectedResult: false,
			expectError:    false,
		},
		{
			name: "When prometheus statefulset is not ready and PVC listing fails it should return error",
			setupObjects: []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s",
						Namespace: "openshift-monitoring",
					},
					Spec: appsv1.StatefulSetSpec{
						Replicas: ptr.To[int32](3),
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "prometheus",
							},
						},
					},
					Status: appsv1.StatefulSetStatus{
						Replicas:          3,
						AvailableReplicas: 1,
					},
				},
			},
			expectedResult: false,
			expectError:    true,
			errorContains:  "failed to list PVCs",
		},
		{
			name: "When prometheus statefulset is not ready and PVC deletion fails it should return error",
			setupObjects: []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s",
						Namespace: "openshift-monitoring",
					},
					Spec: appsv1.StatefulSetSpec{
						Replicas: ptr.To[int32](3),
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "prometheus",
							},
						},
					},
					Status: appsv1.StatefulSetStatus{
						Replicas:          3,
						AvailableReplicas: 1,
					},
				},
				&corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s-db-prometheus-k8s-0",
						Namespace: "openshift-monitoring",
					},
				},
			},
			expectedResult: false,
			expectError:    true,
			errorContains:  "failed to delete PVC",
		},
		{
			name: "When prometheus statefulset is not ready and pod listing fails it should return error",
			setupObjects: []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s",
						Namespace: "openshift-monitoring",
					},
					Spec: appsv1.StatefulSetSpec{
						Replicas: ptr.To[int32](3),
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "prometheus",
							},
						},
					},
					Status: appsv1.StatefulSetStatus{
						Replicas:          3,
						AvailableReplicas: 1,
					},
				},
			},
			expectedResult: false,
			expectError:    true,
			errorContains:  "failed to list prometheus pods",
		},
		{
			name: "When prometheus statefulset is not ready and pod deletion fails it should return error",
			setupObjects: []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s",
						Namespace: "openshift-monitoring",
					},
					Spec: appsv1.StatefulSetSpec{
						Replicas: ptr.To[int32](3),
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "prometheus",
							},
						},
					},
					Status: appsv1.StatefulSetStatus{
						Replicas:          3,
						AvailableReplicas: 1,
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s-0",
						Namespace: "openshift-monitoring",
						Labels: map[string]string{
							"app": "prometheus",
						},
					},
				},
			},
			expectedResult: false,
			expectError:    true,
			errorContains:  "failed to delete pod",
		},
		{
			name: "When prometheus statefulset is not ready and called multiple times it should only delete PVCs and pods once",
			setupObjects: []client.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s",
						Namespace: "openshift-monitoring",
					},
					Spec: appsv1.StatefulSetSpec{
						Replicas: ptr.To[int32](3),
						Selector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"app": "prometheus",
							},
						},
					},
					Status: appsv1.StatefulSetStatus{
						Replicas:          3,
						AvailableReplicas: 1,
					},
				},
				&corev1.PersistentVolumeClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s-db-prometheus-k8s-0",
						Namespace: "openshift-monitoring",
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "prometheus-k8s-0",
						Namespace: "openshift-monitoring",
						Labels: map[string]string{
							"app": "prometheus",
						},
					},
				},
			},
			expectedResult: false,
			expectError:    false,
			multipleCalls:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset global state before each test
			resetGlobalState()

			g := NewWithT(t)

			// Create a fake client with the test objects
			scheme := runtime.NewScheme()
			_ = appsv1.AddToScheme(scheme)
			_ = corev1.AddToScheme(scheme)

			var fakeClient client.Client
			switch tt.name {
			case "When prometheus statefulset is not ready and PVC listing fails it should return error":
				// Create a client that will fail on PVC List operations
				fakeClient = &failingPVCListClient{
					Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.setupObjects...).Build(),
				}
			case "When prometheus statefulset is not ready and PVC deletion fails it should return error":
				// Create a client that will fail on PVC Delete operations
				fakeClient = &failingPVCDeleteClient{
					Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.setupObjects...).Build(),
				}
			case "When prometheus statefulset is not ready and pod listing fails it should return error":
				// Create a client that will fail on Pod List operations
				fakeClient = &failingPodListClient{
					Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.setupObjects...).Build(),
				}
			case "When prometheus statefulset is not ready and pod deletion fails it should return error":
				// Create a client that will fail on Pod Delete operations
				fakeClient = &failingPodDeleteClient{
					Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.setupObjects...).Build(),
				}
			default:
				fakeClient = fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.setupObjects...).Build()
			}

			// Create a test context with logger
			ctx := context.Background()
			logger := ctrl.Log.WithName("test")
			ctx = ctrl.LoggerInto(ctx, logger)

			// Create a test HostedControlPlane
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
			}

			// Call the function
			result, err := RecoverMonitoringStack(ctx, hcp, fakeClient)

			// Assert results
			g.Expect(result).To(Equal(tt.expectedResult))
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				if tt.errorContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tt.errorContains))
				}
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}

			// For the multiple calls test, verify that calling again doesn't delete resources again
			if tt.multipleCalls {
				// Call the function again
				result2, err2 := RecoverMonitoringStack(ctx, hcp, fakeClient)
				g.Expect(err2).NotTo(HaveOccurred())
				g.Expect(result2).To(Equal(false)) // Should still return false as statefulset is not ready

				// Verify that PVCs and pods are still marked as deleted (atomic flags should be true)
				g.Expect(pvcsDeleted.Load()).To(BeTrue())
				g.Expect(podsDeleted.Load()).To(BeTrue())

				// Test concurrent calls to verify thread-safety
				var wg sync.WaitGroup
				results := make([]bool, 10)
				errors := make([]error, 10)

				// Launch 10 concurrent calls
				for i := 0; i < 10; i++ {
					wg.Add(1)
					go func(index int) {
						defer wg.Done()
						results[index], errors[index] = RecoverMonitoringStack(ctx, hcp, fakeClient)
					}(i)
				}

				// Wait for all goroutines to complete
				wg.Wait()

				// Verify all calls returned the same result (false) and no errors
				for i := 0; i < 10; i++ {
					g.Expect(errors[i]).NotTo(HaveOccurred())
					g.Expect(results[i]).To(Equal(false))
				}

				// Verify that PVCs and pods are still marked as deleted (atomic flags should be true)
				// This confirms that the atomic flags prevent multiple deletions even under concurrent access
				g.Expect(pvcsDeleted.Load()).To(BeTrue())
				g.Expect(podsDeleted.Load()).To(BeTrue())
			}
		})
	}
}

// failingPVCListClient is a client that fails on PVC List operations
type failingPVCListClient struct {
	client.Client
}

func (c *failingPVCListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	// Only fail for PVC lists
	if _, ok := list.(*corev1.PersistentVolumeClaimList); ok {
		return errors.NewInternalError(fmt.Errorf("simulated PVC list failure"))
	}
	return c.Client.List(ctx, list, opts...)
}

// failingPVCDeleteClient is a client that fails on PVC Delete operations
type failingPVCDeleteClient struct {
	client.Client
}

func (c *failingPVCDeleteClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	// Only fail for PVC deletes
	if _, ok := obj.(*corev1.PersistentVolumeClaim); ok {
		return errors.NewInternalError(fmt.Errorf("simulated PVC delete failure"))
	}
	return c.Client.Delete(ctx, obj, opts...)
}

// failingPodListClient is a client that fails on Pod List operations
type failingPodListClient struct {
	client.Client
}

func (c *failingPodListClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	// Only fail for Pod lists
	if _, ok := list.(*corev1.PodList); ok {
		return errors.NewInternalError(fmt.Errorf("simulated pod list failure"))
	}
	return c.Client.List(ctx, list, opts...)
}

// failingPodDeleteClient is a client that fails on Pod Delete operations
type failingPodDeleteClient struct {
	client.Client
}

func (c *failingPodDeleteClient) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	// Only fail for Pod deletes
	if _, ok := obj.(*corev1.Pod); ok {
		return errors.NewInternalError(fmt.Errorf("simulated pod delete failure"))
	}
	return c.Client.Delete(ctx, obj, opts...)
}
