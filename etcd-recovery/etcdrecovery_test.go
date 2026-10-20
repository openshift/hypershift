package etcdrecovery

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperapi "github.com/openshift/hypershift/support/api"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/ptr"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

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
