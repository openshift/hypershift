package pdbdrain

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func pdb(expected, desired int32) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{Status: policyv1.PodDisruptionBudgetStatus{ExpectedPods: expected, DesiredHealthy: desired}}
}

func TestIsStructurallyBlocking(t *testing.T) {
	cases := []struct {
		name     string
		pdb      *policyv1.PodDisruptionBudget
		expected bool
	}{
		{"all-must-be-healthy blocks", pdb(3, 3), true},
		{"desired>expected blocks", pdb(3, 4), true},
		{"headroom does not block", pdb(3, 2), false},
		{"no expected pods", pdb(0, 0), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsStructurallyBlocking(c.pdb); got != c.expected {
				t.Fatalf("got %v want %v", got, c.expected)
			}
		})
	}
}

func TestIsPlatformNamespace(t *testing.T) {
	prefixes := []string{"openshift-", "kube-", "redhat-"}
	cases := []struct {
		name      string
		namespace string
		want      bool
	}{
		{name: "When a namespace has the OpenShift prefix, it should be classified as platform", namespace: "openshift-monitoring", want: true},
		{name: "When a namespace has the Kubernetes prefix, it should be classified as platform", namespace: "kube-system", want: true},
		{name: "When a namespace has a configured prefix, it should be classified as platform", namespace: "redhat-ods", want: true},
		{name: "When the namespace is openshift, it should be classified as platform", namespace: "openshift", want: true},
		{name: "When the namespace is default, it should be classified as customer", namespace: "default"},
		{name: "When the namespace is a customer app, it should be classified as customer", namespace: "my-app"},
		{name: "When the namespace is a customer team, it should be classified as customer", namespace: "team-payments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPlatformNamespace(tc.namespace, prefixes); got != tc.want {
				t.Errorf("namespace %q: got %v want %v", tc.namespace, got, tc.want)
			}
		})
	}
}

func TestPlatformNamespacePrefixes(t *testing.T) {
	for _, tc := range []struct {
		name       string
		additional string
		want       []string
	}{
		{
			name: "When no prefixes are configured, it should retain the built-in prefixes",
			want: []string{"openshift-", "kube-"},
		},
		{
			name:       "When managed prefixes are configured, it should add them to the built-in prefixes",
			additional: " redhat-, open-cluster-management ",
			want:       []string{"openshift-", "kube-", "redhat-", "open-cluster-management"},
		},
		{
			name:       "When prefixes repeat or contain empty entries, it should ignore duplicates and empty entries",
			additional: ",redhat-,,openshift-,redhat-,",
			want:       []string{"openshift-", "kube-", "redhat-"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := PlatformNamespacePrefixes(tc.additional)
			if len(got) != len(tc.want) {
				t.Fatalf("prefixes = %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("prefixes = %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestIsDrainCandidate(t *testing.T) {
	base := func(mut func(*corev1.Pod)) *corev1.Pod {
		p := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{},
			Spec:       corev1.PodSpec{NodeName: "node-1"},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		}
		if mut != nil {
			mut(p)
		}
		return p
	}
	cases := []struct {
		name     string
		pod      *corev1.Pod
		expected bool
	}{
		{"running scheduled pod is a candidate", base(nil), true},
		{"unscheduled is not", base(func(p *corev1.Pod) { p.Spec.NodeName = "" }), false},
		{"completed is not", base(func(p *corev1.Pod) { p.Status.Phase = corev1.PodSucceeded }), false},
		{"failed is not", base(func(p *corev1.Pod) { p.Status.Phase = corev1.PodFailed }), false},
		{"mirror pod is not", base(func(p *corev1.Pod) {
			p.Annotations = map[string]string{corev1.MirrorPodAnnotationKey: "x"}
		}), false},
		{"daemonset pod is not", base(func(p *corev1.Pod) {
			p.OwnerReferences = []metav1.OwnerReference{{Kind: "DaemonSet", Controller: ptr.To(true)}}
		}), false},
		{"deployment(replicaset) pod is a candidate", base(func(p *corev1.Pod) {
			p.OwnerReferences = []metav1.OwnerReference{{Kind: "ReplicaSet", Controller: ptr.To(true)}}
		}), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsDrainCandidate(c.pod); got != c.expected {
				t.Fatalf("got %v want %v", got, c.expected)
			}
		})
	}
}
