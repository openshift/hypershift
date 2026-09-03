package testutil

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewHostedClusterWithCredentialConditions creates a HostedCluster with the given
// OIDC and AWS identity provider condition statuses set.
func NewHostedClusterWithCredentialConditions(oidcStatus, awsIDPStatus metav1.ConditionStatus) *hyperv1.HostedCluster {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "clusters"},
	}
	meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
		Type:   string(hyperv1.ValidOIDCConfiguration),
		Status: oidcStatus,
		Reason: "test",
	})
	meta.SetStatusCondition(&hc.Status.Conditions, metav1.Condition{
		Type:   string(hyperv1.ValidAWSIdentityProvider),
		Status: awsIDPStatus,
		Reason: "test",
	})
	return hc
}
