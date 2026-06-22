package hostedcluster

import (
	"context"
	"sync"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	interceptor "sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestGetNextAvailableSecurityContextUID(t *testing.T) {
	// Setup test cases
	tests := []struct {
		name                        string
		namespaces                  []corev1.Namespace
		expectedUID                 int64
		expectedFor3SubsequentCalls []int64
		expectErr                   bool
	}{
		{
			name:                        "when no namespaces, it should return first available UID",
			namespaces:                  []corev1.Namespace{},
			expectedUID:                 controlplanecomponent.DefaultSecurityContextUID,
			expectedFor3SubsequentCalls: []int64{controlplanecomponent.DefaultSecurityContextUID + 1, controlplanecomponent.DefaultSecurityContextUID + 2, controlplanecomponent.DefaultSecurityContextUID + 3},
			expectErr:                   false,
		},
		{
			name: "when there are multiple namespaces, it should allocate the lower available UID",
			namespaces: []corev1.Namespace{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ns1",
						Labels: map[string]string{
							ControlPlaneNamespaceLabelKey: "true",
						},
						Annotations: map[string]string{
							DefaultSecurityContextUIDAnnnotation: "1001",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ns2",
						Labels: map[string]string{
							ControlPlaneNamespaceLabelKey: "true",
						},
						Annotations: map[string]string{
							DefaultSecurityContextUIDAnnnotation: "1005",
						},
					},
				},
			},
			expectedUID:                 1002,
			expectedFor3SubsequentCalls: []int64{1003, 1004, 1006},
			expectErr:                   false,
		},
		{
			name: "when there are namespaces with invalid annotation it should be ignored",
			namespaces: []corev1.Namespace{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ns1",
						Labels: map[string]string{
							ControlPlaneNamespaceLabelKey: "true",
						},
						Annotations: map[string]string{
							DefaultSecurityContextUIDAnnnotation: "notanumber",
						},
					},
				},
			},
			expectedUID:                 controlplanecomponent.DefaultSecurityContextUID,
			expectedFor3SubsequentCalls: []int64{controlplanecomponent.DefaultSecurityContextUID + 1, controlplanecomponent.DefaultSecurityContextUID + 2, controlplanecomponent.DefaultSecurityContextUID + 3},
			expectErr:                   false,
		},
		{
			name: "when there are namespaces without the control plane label it should be ignored",
			namespaces: []corev1.Namespace{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ns1",
						Annotations: map[string]string{
							DefaultSecurityContextUIDAnnnotation: "1000123456",
						},
					},
				},
			},
			expectedUID:                 controlplanecomponent.DefaultSecurityContextUID,
			expectedFor3SubsequentCalls: []int64{controlplanecomponent.DefaultSecurityContextUID + 1, controlplanecomponent.DefaultSecurityContextUID + 2, controlplanecomponent.DefaultSecurityContextUID + 3},
			expectErr:                   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			// Reset the global allocator for each test
			globalUIDAllocator = &securityContextUIDAllocator{
				allocatedUIDs: make(map[int64]struct{}),
			}

			objs := []crclient.Object{}
			for i := range tc.namespaces {
				ns := tc.namespaces[i]
				objs = append(objs, &ns)
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objs...).
				Build()

			uid, err := getNextAvailableSecurityContextUID(context.Background(), fakeClient)
			if tc.expectErr && err == nil {
				t.Errorf("expected error but got none")
				return
			}
			if !tc.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if !tc.expectErr && err == nil && uid != tc.expectedUID {
				t.Errorf("expected UID %d, got %d", tc.expectedUID, uid)
			}

			// Now make subsequent calls and check they each get a sequential value
			if !tc.expectErr && err == nil {
				// Wrap the List method to panic if called again (should only be called on the first invocation)
				fakeClient = fake.NewClientBuilder().
					WithScheme(api.Scheme).
					WithInterceptorFuncs(interceptor.Funcs{
						List: func(ctx context.Context, client crclient.WithWatch, list crclient.ObjectList, opts ...crclient.ListOption) error {
							t.Errorf("client.List should not be called after the first getNextAvailableSecurityContextUID call")
							return nil
						},
					}).WithObjects(objs...).
					Build()

				var wg sync.WaitGroup
				results := make(chan int64, 3)
				for i := 0; i < 3; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						uid, err := getNextAvailableSecurityContextUID(context.Background(), fakeClient)
						if err != nil {
							t.Errorf("unexpected error: %v", err)
						}
						results <- uid
					}()
				}
				wg.Wait()
				close(results)

				// All UIDs should be unique and sequential
				seen := make(map[int64]bool)
				for uid := range results {
					if seen[uid] {
						t.Errorf("duplicate UID returned: %d", uid)
					}
					seen[uid] = true
				}

				g.Expect(seen).To(HaveKeyWithValue(tc.expectedFor3SubsequentCalls[0], true))
				g.Expect(seen).To(HaveKeyWithValue(tc.expectedFor3SubsequentCalls[1], true))
				g.Expect(seen).To(HaveKeyWithValue(tc.expectedFor3SubsequentCalls[2], true))
			}
		})
	}
}

func TestInitializeFromNamespaces(t *testing.T) {
	tests := []struct {
		name               string
		namespaces         []corev1.Namespace
		expectedAllocated  []int64
		expectedInitalized bool
	}{
		{
			name:               "when namespace list is empty, it should initialize with no allocations",
			namespaces:         []corev1.Namespace{},
			expectedAllocated:  []int64{},
			expectedInitalized: true,
		},
		{
			name: "when namespaces have control plane label and valid UIDs, it should load those UIDs",
			namespaces: []corev1.Namespace{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ns1",
						Labels: map[string]string{
							ControlPlaneNamespaceLabelKey: "true",
						},
						Annotations: map[string]string{
							DefaultSecurityContextUIDAnnnotation: "1001",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ns2",
						Labels: map[string]string{
							ControlPlaneNamespaceLabelKey: "true",
						},
						Annotations: map[string]string{
							DefaultSecurityContextUIDAnnnotation: "1005",
						},
					},
				},
			},
			expectedAllocated:  []int64{1001, 1005},
			expectedInitalized: true,
		},
		{
			name: "when namespaces lack control plane label, it should ignore them",
			namespaces: []corev1.Namespace{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ns1",
						Annotations: map[string]string{
							DefaultSecurityContextUIDAnnnotation: "1001",
						},
					},
				},
			},
			expectedAllocated:  []int64{},
			expectedInitalized: true,
		},
		{
			name: "when namespaces have invalid UID annotations, it should ignore those UIDs",
			namespaces: []corev1.Namespace{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ns1",
						Labels: map[string]string{
							ControlPlaneNamespaceLabelKey: "true",
						},
						Annotations: map[string]string{
							DefaultSecurityContextUIDAnnnotation: "notanumber",
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ns2",
						Labels: map[string]string{
							ControlPlaneNamespaceLabelKey: "true",
						},
						Annotations: map[string]string{
							DefaultSecurityContextUIDAnnnotation: "1005",
						},
					},
				},
			},
			expectedAllocated:  []int64{1005},
			expectedInitalized: true,
		},
		{
			name: "when namespaces have UIDs outside valid range, it should ignore those UIDs",
			namespaces: []corev1.Namespace{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ns1",
						Labels: map[string]string{
							ControlPlaneNamespaceLabelKey: "true",
						},
						Annotations: map[string]string{
							DefaultSecurityContextUIDAnnnotation: "999", // Below min
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ns2",
						Labels: map[string]string{
							ControlPlaneNamespaceLabelKey: "true",
						},
						Annotations: map[string]string{
							DefaultSecurityContextUIDAnnnotation: "999999999", // Above max
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: "ns3",
						Labels: map[string]string{
							ControlPlaneNamespaceLabelKey: "true",
						},
						Annotations: map[string]string{
							DefaultSecurityContextUIDAnnnotation: "1005", // Valid
						},
					},
				},
			},
			expectedAllocated:  []int64{1005},
			expectedInitalized: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			allocator := &securityContextUIDAllocator{
				allocatedUIDs: make(map[int64]struct{}),
			}

			objs := []crclient.Object{}
			for i := range tc.namespaces {
				ns := tc.namespaces[i]
				objs = append(objs, &ns)
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(api.Scheme).
				WithObjects(objs...).
				Build()

			err := allocator.initializeFromNamespaces(context.Background(), fakeClient)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(allocator.initialized).To(Equal(tc.expectedInitalized))

			// Convert allocated UIDs to slice for easier comparison
			allocated := make([]int64, 0, len(allocator.allocatedUIDs))
			for uid := range allocator.allocatedUIDs {
				allocated = append(allocated, uid)
			}
			g.Expect(allocated).To(ConsistOf(tc.expectedAllocated))
		})
	}
}
