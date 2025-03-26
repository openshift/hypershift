package controlplanepkioperator_test

import (
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplanepkioperator"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileCSRApproverClusterRole(t *testing.T) {
	hostedCluster := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
		Namespace: "test-namespace",
		Name:      "test-hc",
	}}
	clusterRole := controlplanepkioperator.CSRApproverClusterRole(hostedCluster)
	if err := controlplanepkioperator.ReconcileCSRApproverClusterRole(clusterRole, hostedCluster, certificates.CustomerBreakGlassSigner, certificates.SREBreakGlassSigner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	clusterRoleYaml, err := util.SerializeResource(clusterRole, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, clusterRoleYaml)
}

func TestReconcileCSRSignerClusterRole(t *testing.T) {
	hostedCluster := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
		Namespace: "test-namespace",
		Name:      "test-hc",
	}}
	clusterRole := controlplanepkioperator.CSRSignerClusterRole(hostedCluster)
	if err := controlplanepkioperator.ReconcileCSRSignerClusterRole(clusterRole, hostedCluster, certificates.CustomerBreakGlassSigner, certificates.SREBreakGlassSigner); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	clusterRoleYaml, err := util.SerializeResource(clusterRole, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, clusterRoleYaml)
}

func TestReconcileCSRApproverClusterRoleBinding(t *testing.T) {
	hostedCluster := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
		Namespace: "test-namespace",
		Name:      "test-hc",
	}}
	serviceAccount := manifests.PKIOperatorServiceAccount("test-namespace")
	clusterRole := controlplanepkioperator.CSRApproverClusterRole(hostedCluster)
	clusterRoleBinding := controlplanepkioperator.ClusterRoleBinding(hostedCluster, clusterRole)
	if err := controlplanepkioperator.ReconcileClusterRoleBinding(clusterRoleBinding, clusterRole, serviceAccount); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	clusterRoleBindingYaml, err := util.SerializeResource(clusterRoleBinding, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, clusterRoleBindingYaml)
}

func TestReconcileCSRSignerClusterRoleBinding(t *testing.T) {
	hostedCluster := &hypershiftv1beta1.HostedCluster{ObjectMeta: metav1.ObjectMeta{
		Namespace: "test-namespace",
		Name:      "test-hc",
	}}
	serviceAccount := manifests.PKIOperatorServiceAccount("test-namespace")
	clusterRole := controlplanepkioperator.CSRSignerClusterRole(hostedCluster)
	clusterRoleBinding := controlplanepkioperator.ClusterRoleBinding(hostedCluster, clusterRole)
	if err := controlplanepkioperator.ReconcileClusterRoleBinding(clusterRoleBinding, clusterRole, serviceAccount); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	clusterRoleBindingYaml, err := util.SerializeResource(clusterRoleBinding, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, clusterRoleBindingYaml)
}
