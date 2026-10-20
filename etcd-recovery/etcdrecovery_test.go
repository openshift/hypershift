package etcdrecovery

import (
	"context"
	"fmt"
	"sync"
	"testing"

	. "github.com/onsi/gomega"

	hyperapi "github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
	etcdclient "go.etcd.io/etcd/client/v3"
)

const testNamespace = "clusters-example"

type fakeMemberLister struct {
	members []*etcdserverpb.Member
}

func (f *fakeMemberLister) MemberList(_ context.Context, _ ...etcdclient.OpOption) (*etcdclient.MemberListResponse, error) {
	return &etcdclient.MemberListResponse{Members: f.members}, nil
}

func peerURL(name string) string {
	return fmt.Sprintf("https://%s.etcd-discovery.%s.svc:2380", name, testNamespace)
}

func clientURL(name string) string {
	return fmt.Sprintf("https://%s.etcd-discovery.%s.svc:2379", name, testNamespace)
}

func startedMember(name string) *etcdserverpb.Member {
	return &etcdserverpb.Member{
		Name:       name,
		PeerURLs:   []string{peerURL(name)},
		ClientURLs: []string{clientURL(name)},
	}
}

// unstartedMember mimics how etcd reports a member that has been added to the cluster
// but whose process has not started yet: no name and no client URLs.
func unstartedMember(name string) *etcdserverpb.Member {
	return &etcdserverpb.Member{
		PeerURLs: []string{peerURL(name)},
	}
}

func etcdPod(name string, waiting bool, restartCount int32) *corev1.Pod {
	state := corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}
	if waiting {
		state = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels:    map[string]string{"app": "etcd"},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "etcd", State: state, RestartCount: restartCount},
			},
		},
	}
}

func settledEtcdStatefulSet() *appsv1.StatefulSet {
	sts := etcdStatefulSet(testNamespace)
	sts.Status.CurrentRevision = "etcd-7d4b9f6c58"
	sts.Status.UpdateRevision = "etcd-7d4b9f6c58"
	return sts
}

func TestMemberName(t *testing.T) {
	tests := []struct {
		name     string
		member   *etcdserverpb.Member
		expected string
	}{
		{
			name:     "When the member reports a name, it should return that name",
			member:   startedMember("etcd-1"),
			expected: "etcd-1",
		},
		{
			name:     "When the member has no name, it should derive the name from its peer URL",
			member:   unstartedMember("etcd-0"),
			expected: "etcd-0",
		},
		{
			name:     "When the member has no name and no peer URLs, it should return an empty name",
			member:   &etcdserverpb.Member{},
			expected: "",
		},
		{
			name:     "When the member has no name and an unparsable peer URL, it should return an empty name",
			member:   &etcdserverpb.Member{PeerURLs: []string{"://not-a-url"}},
			expected: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(memberName(test.member)).To(Equal(test.expected))
		})
	}
}

func TestCheckEtcdStatus(t *testing.T) {
	tests := []struct {
		name                string
		members             []*etcdserverpb.Member
		healthyEndpoints    []string
		objects             []crclient.Object
		expectErr           bool
		expectRecoverable   bool
		expectFailingPod    *string
		expectMissingMember *string
		expectUnhealthy     bool
	}{
		{
			name: "When a member has not started and its pod is crash looping, it should report a failing pod without erroring",
			members: []*etcdserverpb.Member{
				unstartedMember("etcd-0"),
				startedMember("etcd-1"),
				startedMember("etcd-2"),
			},
			healthyEndpoints: []string{"etcd-1", "etcd-2"},
			objects: []crclient.Object{
				settledEtcdStatefulSet(),
				etcdPod("etcd-0", true, 3),
				etcdPod("etcd-1", false, 0),
				etcdPod("etcd-2", false, 0),
			},
			expectRecoverable: true,
			expectFailingPod:  ptr.To("etcd-0"),
			expectUnhealthy:   true,
		},
		{
			name: "When a member has not started but no pod has restarted, it should not report the cluster as unhealthy",
			members: []*etcdserverpb.Member{
				unstartedMember("etcd-0"),
				startedMember("etcd-1"),
				startedMember("etcd-2"),
			},
			healthyEndpoints: []string{"etcd-1", "etcd-2"},
			objects: []crclient.Object{
				settledEtcdStatefulSet(),
				etcdPod("etcd-0", true, 0),
				etcdPod("etcd-1", false, 0),
				etcdPod("etcd-2", false, 0),
			},
			expectRecoverable: true,
			expectUnhealthy:   false,
		},
		{
			name: "When two of three members are unhealthy, it should report the cluster as not recoverable",
			members: []*etcdserverpb.Member{
				unstartedMember("etcd-0"),
				startedMember("etcd-1"),
				startedMember("etcd-2"),
			},
			healthyEndpoints: []string{"etcd-2"},
			objects: []crclient.Object{
				settledEtcdStatefulSet(),
				etcdPod("etcd-0", true, 3),
				etcdPod("etcd-1", true, 3),
				etcdPod("etcd-2", false, 0),
			},
			expectRecoverable: false,
			expectUnhealthy:   false,
		},
		{
			name: "When a member reports more than one client URL, it should return an error",
			members: []*etcdserverpb.Member{
				{
					Name:       "etcd-0",
					PeerURLs:   []string{peerURL("etcd-0")},
					ClientURLs: []string{clientURL("etcd-0"), "https://etcd-client.clusters-example.svc:2379"},
				},
				startedMember("etcd-1"),
				startedMember("etcd-2"),
			},
			healthyEndpoints: []string{"etcd-1", "etcd-2"},
			objects:          []crclient.Object{settledEtcdStatefulSet()},
			expectErr:        true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			healthy := sets.New(test.healthyEndpoints...)
			isHealthy := func(_ context.Context, _ options, name, _ string) bool {
				return healthy.Has(name)
			}

			k := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(test.objects...).Build()
			status, err := checkEtcdStatus(
				t.Context(),
				options{namespace: testNamespace},
				&fakeMemberLister{members: test.members},
				k,
				isHealthy,
			)

			if test.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(status.recoverable).To(Equal(test.expectRecoverable))
			g.Expect(status.failingPod).To(Equal(test.expectFailingPod))
			g.Expect(status.missingMember).To(Equal(test.expectMissingMember))
			g.Expect(status.isUnhealthy()).To(Equal(test.expectUnhealthy))
		})
	}
}

func TestIsUnhealthy(t *testing.T) {
	tests := []struct {
		name     string
		status   etcdStatus
		expected bool
	}{
		{
			name:     "When no failing pod or missing member, it should return false",
			status:   etcdStatus{recoverable: true},
			expected: false,
		},
		{
			name:     "When there is a failing pod, it should return true",
			status:   etcdStatus{recoverable: true, failingPod: ptr.To("etcd-0")},
			expected: true,
		},
		{
			name:     "When there is a missing member, it should return true",
			status:   etcdStatus{recoverable: true, missingMember: ptr.To("etcd-1")},
			expected: true,
		},
		{
			name:     "When there is both a failing pod and missing member, it should return true",
			status:   etcdStatus{recoverable: true, failingPod: ptr.To("etcd-0"), missingMember: ptr.To("etcd-0")},
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(test.status.isUnhealthy()).To(Equal(test.expected))
		})
	}
}

func TestEtcdStatusString(t *testing.T) {
	tests := []struct {
		name     string
		status   etcdStatus
		expected string
	}{
		{
			name:     "When healthy, it should return healthy",
			status:   etcdStatus{recoverable: true},
			expected: "healthy",
		},
		{
			name:     "When there is a missing member, it should include missing member",
			status:   etcdStatus{recoverable: true, missingMember: ptr.To("etcd-1")},
			expected: "missing member: etcd-1",
		},
		{
			name:     "When there is a failing pod, it should include failing pod",
			status:   etcdStatus{recoverable: true, failingPod: ptr.To("etcd-2")},
			expected: "failing pod: etcd-2",
		},
		{
			name:     "When node is unreachable, it should include node unreachable",
			status:   etcdStatus{recoverable: true, failingPod: ptr.To("etcd-0"), nodeUnreachable: true},
			expected: "failing pod: etcd-0,node unreachable",
		},
		{
			name:     "When not recoverable, it should include NOT RECOVERABLE",
			status:   etcdStatus{recoverable: false},
			expected: "broken: NOT RECOVERABLE",
		},
		{
			name:     "When there is a missing member and a failing pod on unreachable node, it should include all",
			status:   etcdStatus{recoverable: true, missingMember: ptr.To("etcd-0"), failingPod: ptr.To("etcd-0"), nodeUnreachable: true},
			expected: "missing member: etcd-0,failing pod: etcd-0,node unreachable",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(test.status.String()).To(Equal(test.expected))
		})
	}
}

func TestClassifyEtcdPods(t *testing.T) {
	tests := []struct {
		name                    string
		pods                    []corev1.Pod
		expectedFailingPodNames []string
		expectedNodeUnreachable bool
	}{
		{
			name: "When all pods are healthy, it should return no failing pods",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-0", Namespace: "test-ns"},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-1", Namespace: "test-ns"},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-2", Namespace: "test-ns"},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
			},
			expectedFailingPodNames: nil,
			expectedNodeUnreachable: false,
		},
		{
			name: "When one pod is crash-looping, it should return that pod as failing",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-0", Namespace: "test-ns"},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-1", Namespace: "test-ns"},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								Name:         "etcd",
								State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
								RestartCount: 5,
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-2", Namespace: "test-ns"},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
			},
			expectedFailingPodNames: []string{"etcd-1"},
			expectedNodeUnreachable: false,
		},
		{
			name: "When one pod is on an unreachable node, it should return that pod with nodeUnreachable=true",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-0", Namespace: "test-ns"},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-1", Namespace: "test-ns"},
					Status: corev1.PodStatus{
						// Pod has stale Running status — kubelet is unreachable.
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.DisruptionTarget,
								Status: corev1.ConditionTrue,
								Reason: "DeletionByTaintManager",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-2", Namespace: "test-ns"},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
			},
			expectedFailingPodNames: []string{"etcd-1"},
			expectedNodeUnreachable: true,
		},
		{
			name: "When two pods are on unreachable nodes, it should return both as failing",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-0", Namespace: "test-ns"},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.DisruptionTarget,
								Status: corev1.ConditionTrue,
								Reason: "DeletionByTaintManager",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-1", Namespace: "test-ns"},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
						Conditions: []corev1.PodCondition{
							{
								Type:   corev1.DisruptionTarget,
								Status: corev1.ConditionTrue,
								Reason: "DeletionByTaintManager",
							},
						},
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-2", Namespace: "test-ns"},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
						},
					},
				},
			},
			expectedFailingPodNames: []string{"etcd-0", "etcd-1"},
			expectedNodeUnreachable: true,
		},
		{
			name: "When a non-etcd container is crash-looping, it should not detect the pod as failing",
			pods: []corev1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "etcd-0", Namespace: "test-ns"},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{Name: "etcd", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}, RestartCount: 0},
							{
								Name:         "sidecar",
								State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
								RestartCount: 5,
							},
						},
					},
				},
			},
			expectedFailingPodNames: nil,
			expectedNodeUnreachable: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			failingPods, nodeUnreachable := classifyEtcdPods(t.Context(), test.pods)
			var failingNames []string
			for _, p := range failingPods {
				failingNames = append(failingNames, p.Name)
			}
			g.Expect(failingNames).To(Equal(test.expectedFailingPodNames))
			g.Expect(nodeUnreachable).To(Equal(test.expectedNodeUnreachable))
		})
	}
}

// deleteCall records the arguments of a single Delete invocation.
type deleteCall struct {
	name string
	opts []crclient.DeleteOption
}

func TestDeleteEtcdMemberResources(t *testing.T) {
	tests := []struct {
		name                     string
		nodeUnreachable          bool
		podExists                bool
		pvcExists                bool
		expectPodDeleted         bool
		expectPvcDeleted         bool
		expectForceDeleteOptions bool // true when GracePeriodSeconds(0) must be passed
	}{
		{
			name:                     "When node is reachable and resources exist, it should delete pod and PVC",
			nodeUnreachable:          false,
			podExists:                true,
			pvcExists:                true,
			expectPodDeleted:         true,
			expectPvcDeleted:         true,
			expectForceDeleteOptions: false,
		},
		{
			name:                     "When node is unreachable and resources exist, it should force-delete pod and PVC with GracePeriodSeconds=0",
			nodeUnreachable:          true,
			podExists:                true,
			pvcExists:                true,
			expectPodDeleted:         true,
			expectPvcDeleted:         true,
			expectForceDeleteOptions: true,
		},
		{
			name:             "When node is unreachable and resources do not exist, it should not error",
			nodeUnreachable:  true,
			podExists:        false,
			pvcExists:        false,
			expectPodDeleted: false,
			expectPvcDeleted: false,
		},
		{
			name:             "When node is reachable and resources do not exist, it should not error",
			nodeUnreachable:  false,
			podExists:        false,
			pvcExists:        false,
			expectPodDeleted: false,
			expectPvcDeleted: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "etcd-1", Namespace: "test-ns"},
			}
			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "data-etcd-1", Namespace: "test-ns"},
			}

			var objects []crclient.Object
			if test.podExists {
				objects = append(objects, pod.DeepCopy())
			}
			if test.pvcExists {
				objects = append(objects, pvc.DeepCopy())
			}

			// Record delete calls via an interceptor so we can assert on the options
			// passed to each Delete. The fake client ignores GracePeriodSeconds, so
			// without this a regression to graceful deletion would still pass.
			var (
				mu          sync.Mutex
				deleteCalls []deleteCall
			)

			fakeClient := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithObjects(objects...).
				WithInterceptorFuncs(interceptor.Funcs{
					Delete: func(ctx context.Context, client crclient.WithWatch, obj crclient.Object, opts ...crclient.DeleteOption) error {
						mu.Lock()
						deleteCalls = append(deleteCalls, deleteCall{name: obj.GetName(), opts: opts})
						mu.Unlock()
						return client.Delete(ctx, obj, opts...)
					},
				}).
				Build()

			deleteErr := deleteEtcdMemberResources(t.Context(), fakeClient, pod, pvc, test.nodeUnreachable)
			g.Expect(deleteErr).ToNot(HaveOccurred())

			// Verify pod was deleted
			err := fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(pod), &corev1.Pod{})
			if test.expectPodDeleted {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected pod to be deleted")
			} else if !test.podExists {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "pod should not exist")
			}

			// Verify PVC was deleted
			err = fakeClient.Get(t.Context(), crclient.ObjectKeyFromObject(pvc), &corev1.PersistentVolumeClaim{})
			if test.expectPvcDeleted {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected PVC to be deleted")
			} else if !test.pvcExists {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "PVC should not exist")
			}

			// Verify the delete options: force-delete must use GracePeriodSeconds(0),
			// graceful delete must not.
			if test.expectForceDeleteOptions {
				// Expect exactly 2 delete calls (PVC + pod), each with GracePeriodSeconds=0
				g.Expect(deleteCalls).To(HaveLen(2), "expected 2 force-delete calls")
				for _, call := range deleteCalls {
					deleteOpts := &crclient.DeleteOptions{}
					for _, o := range call.opts {
						o.ApplyToDelete(deleteOpts)
					}
					g.Expect(deleteOpts.GracePeriodSeconds).ToNot(BeNil(),
						"expected GracePeriodSeconds to be set for %s", call.name)
					g.Expect(*deleteOpts.GracePeriodSeconds).To(Equal(int64(0)),
						"expected GracePeriodSeconds=0 (force-delete) for %s", call.name)
				}
			} else if test.podExists || test.pvcExists {
				// Graceful path: verify no GracePeriodSeconds was set
				for _, call := range deleteCalls {
					deleteOpts := &crclient.DeleteOptions{}
					for _, o := range call.opts {
						o.ApplyToDelete(deleteOpts)
					}
					g.Expect(deleteOpts.GracePeriodSeconds).To(BeNil(),
						"expected no GracePeriodSeconds for graceful delete of %s", call.name)
				}
			}
		})
	}
}

func TestMissingMember(t *testing.T) {

	tests := []struct {
		names    []string
		expected *string
	}{
		{
			names:    []string{"etcd-1", "etcd-2"},
			expected: ptr.To("etcd-0"),
		},
		{
			names:    []string{"etcd-0", "etcd-1"},
			expected: ptr.To("etcd-2"),
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("test-%d", i), func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual := missingMember(&etcdserverpb.Member{Name: test.names[0]}, &etcdserverpb.Member{Name: test.names[1]})
			g.Expect(actual).To(Equal(test.expected))
		})
	}
}

func TestIsRecoverableMemberHealth(t *testing.T) {
	tests := []struct {
		memberHealth        map[string]bool
		expectedRecoverable bool
	}{
		{
			memberHealth: map[string]bool{
				"etcd-0": true,
			},
			expectedRecoverable: false,
		},
		{
			memberHealth: map[string]bool{
				"etcd-0": true,
				"etcd-1": true,
			},
			expectedRecoverable: true,
		},
		{
			memberHealth: map[string]bool{
				"etcd-0": true,
				"etcd-2": false,
			},
			expectedRecoverable: false,
		},
		{
			memberHealth: map[string]bool{
				"etcd-0": false,
				"etcd-1": true,
				"etcd-2": true,
			},
			expectedRecoverable: true,
		},
		{
			memberHealth: map[string]bool{
				"etcd-0": false,
				"etcd-1": false,
				"etcd-2": true,
			},
			expectedRecoverable: false,
		},
		{
			memberHealth: map[string]bool{
				"etcd-0": true,
				"etcd-1": true,
				"etcd-2": true,
			},
			expectedRecoverable: true,
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("test-%d", i), func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual := isRecoverableMemberHealth(t.Context(), test.memberHealth)
			g.Expect(actual).To(Equal(test.expectedRecoverable))
		})
	}
}

func TestEtcdStatusIsRecoverable(t *testing.T) {
	tests := []struct {
		status   etcdStatus
		expected bool
	}{
		{
			status: etcdStatus{
				recoverable: false,
			},
			expected: false,
		},
		{
			status: etcdStatus{
				recoverable: true,
			},
			expected: true,
		},
		{
			status: etcdStatus{
				recoverable:   true,
				missingMember: ptr.To("etcd-0"),
				failingPod:    ptr.To("etcd-1"),
			},
			expected: false,
		},
		{
			status: etcdStatus{
				recoverable:   true,
				missingMember: ptr.To("etcd-0"),
				failingPod:    ptr.To("etcd-0"),
			},
			expected: true,
		},
		{
			status: etcdStatus{
				recoverable:   false,
				missingMember: ptr.To("etcd-0"),
				failingPod:    ptr.To("etcd-0"),
			},
			expected: false,
		},
		{
			status: etcdStatus{
				recoverable:   true,
				missingMember: ptr.To("etcd-1"),
			},
			expected: true,
		},
		{
			status: etcdStatus{
				recoverable: true,
				failingPod:  ptr.To("etcd-2"),
			},
			expected: true,
		},
	}

	for i, test := range tests {
		t.Run(fmt.Sprintf("test-%d", i), func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual := test.status.isRecoverable()
			g.Expect(actual).To(Equal(test.expected))
		})
	}
}

func TestIsPodOnUnreachableNode(t *testing.T) {
	tests := []struct {
		name     string
		pod      corev1.Pod
		expected bool
	}{
		{
			name: "When pod has DisruptionTarget=True with reason DeletionByTaintManager, it should return true",
			pod: corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.DisruptionTarget,
							Status: corev1.ConditionTrue,
							Reason: "DeletionByTaintManager",
						},
					},
				},
			},
			expected: true,
		},
		{
			name: "When pod has DisruptionTarget=False with reason DeletionByTaintManager, it should return false",
			pod: corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.DisruptionTarget,
							Status: corev1.ConditionFalse,
							Reason: "DeletionByTaintManager",
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When pod has DisruptionTarget=True with reason PreemptionByScheduler, it should return false",
			pod: corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.DisruptionTarget,
							Status: corev1.ConditionTrue,
							Reason: "PreemptionByScheduler",
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When pod has no DisruptionTarget condition, it should return false",
			pod: corev1.Pod{
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:   corev1.PodReady,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			expected: false,
		},
		{
			name:     "When pod has no conditions at all, it should return false",
			pod:      corev1.Pod{},
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual := isPodOnUnreachableNode(&test.pod)
			g.Expect(actual).To(Equal(test.expected))
		})
	}
}

func TestEtcdStatusMemberToRecover(t *testing.T) {
	tests := []struct {
		status   etcdStatus
		expected string
	}{
		{
			status: etcdStatus{
				recoverable: true,
			},
			expected: "",
		},
		{
			status: etcdStatus{
				recoverable:   true,
				failingPod:    ptr.To("etcd-0"),
				missingMember: ptr.To("etcd-1"),
			},
			expected: "",
		},
		{
			status: etcdStatus{
				recoverable: false,
				failingPod:  ptr.To("etcd-0"),
			},
			expected: "",
		},
		{
			status: etcdStatus{
				recoverable:   true,
				missingMember: ptr.To("etcd-1"),
			},
			expected: "etcd-1",
		},
		{
			status: etcdStatus{
				recoverable: true,
				failingPod:  ptr.To("etcd-1"),
			},
			expected: "etcd-1",
		},
		{
			status: etcdStatus{
				recoverable:   true,
				missingMember: ptr.To("etcd-2"),
				failingPod:    ptr.To("etcd-2"),
			},
			expected: "etcd-2",
		},
	}
	for i, test := range tests {
		t.Run(fmt.Sprintf("test-%d", i), func(t *testing.T) {
			g := NewGomegaWithT(t)
			actual := test.status.memberToRecover()
			g.Expect(actual).To(Equal(test.expected))
		})
	}
}
