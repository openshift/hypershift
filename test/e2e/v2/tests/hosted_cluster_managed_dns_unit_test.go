//go:build e2ev2

package tests

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestManagedDNSRequested(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name      string
		requested bool
		status    hyperv1.HostedClusterStatus
	}{
		{name: "When an older consumer has no opt-in or DNS status, it should not require managed DNS"},
		{name: "When managed DNS is requested without platform status, it should still require assertions", requested: true},
		{name: "When managed DNS is requested with empty AWS status, it should still require assertions", requested: true, status: hyperv1.HostedClusterStatus{Platform: &hyperv1.PlatformStatus{AWS: &hyperv1.AWSPlatformStatus{}}}},
		{name: "When managed DNS reports Unknown, it should still require assertions", requested: true, status: hyperv1.HostedClusterStatus{Conditions: []metav1.Condition{{Type: string(hyperv1.AWSManagedDNSAvailable), Status: metav1.ConditionUnknown}}}},
		{name: "When managed DNS reports False, it should still require assertions", requested: true, status: hyperv1.HostedClusterStatus{Conditions: []metav1.Condition{{Type: string(hyperv1.AWSManagedDNSAvailable), Status: metav1.ConditionFalse}}}},
		{name: "When managed DNS reports True, it should require assertions", requested: true, status: hyperv1.HostedClusterStatus{Conditions: []metav1.Condition{{Type: string(hyperv1.AWSManagedDNSAvailable), Status: metav1.ConditionTrue}}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			cluster := &hyperv1.HostedCluster{Spec: hyperv1.HostedClusterSpec{Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform, AWS: &hyperv1.AWSPlatformSpec{}}}, Status: testCase.status}
			if testCase.requested {
				cluster.Spec.Platform.AWS.ManagedDNS.IngressDomainPrefix = "in"
			}
			if actual := managedDNSRequested(cluster); actual != testCase.requested {
				t.Fatalf("managedDNSRequested = %v, want %v", actual, testCase.requested)
			}
		})
	}
	t.Run("When the hosted cluster or AWS spec is absent, it should not require managed DNS", func(t *testing.T) {
		t.Parallel()
		if managedDNSRequested(nil) || managedDNSRequested(&hyperv1.HostedCluster{}) {
			t.Fatal("managed DNS was requested without an AWS spec")
		}
	})
}
