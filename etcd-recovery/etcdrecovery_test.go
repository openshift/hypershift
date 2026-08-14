package etcdrecovery

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	"k8s.io/utils/ptr"

	"go.etcd.io/etcd/api/v3/etcdserverpb"
)

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
