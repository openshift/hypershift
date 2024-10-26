package pkioperator

import (
	"testing"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestReconcileControlPlanePKIOperatorDeployment(t *testing.T) {
	hcp := &hypershiftv1beta1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
			UID:       "test-uid",
			Annotations: map[string]string{
				hypershiftv1beta1.ControlPlanePriorityClass: "whatever",
				hypershiftv1beta1.RestartDateAnnotation:     "sometime",
			},
		},
		Spec: hypershiftv1beta1.HostedControlPlaneSpec{
			ReleaseImage: "quay.io/ocp-dev/test-release-image:latest",
			IssuerURL:    "https://www.example.com",
			Networking: hypershiftv1beta1.ClusterNetworking{
				APIServer: &hypershiftv1beta1.APIServerNetworking{
					Port: ptr.To[int32](1234),
				},
			},
		},
	}
	sa := manifests.PKIOperatorServiceAccount(hcp.Namespace)
	deployment := manifests.PKIOperatorDeployment(hcp.Namespace)
	if err := ReconcileDeployment(deployment, true, hcp, "cpo-pki-image", true, sa, 25*time.Minute); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	deploymentYaml, err := util.SerializeResource(deployment, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, deploymentYaml)
}

func TestReconcileControlPlanePKIOperatorRole(t *testing.T) {
	hcp := &hypershiftv1beta1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
			UID:       "test-uid",
			Annotations: map[string]string{
				hypershiftv1beta1.ControlPlanePriorityClass: "whatever",
				hypershiftv1beta1.RestartDateAnnotation:     "sometime",
			},
		},
	}
	role := manifests.PKIOperatorRole("test-namespace")
	if err := ReconcileRole(role, config.OwnerRefFrom(hcp)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	roleYaml, err := util.SerializeResource(role, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, roleYaml)
}

func TestReconcileControlPlanePKIOperatorRoleBinding(t *testing.T) {
	hcp := &hypershiftv1beta1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
			UID:       "test-uid",
			Annotations: map[string]string{
				hypershiftv1beta1.ControlPlanePriorityClass: "whatever",
				hypershiftv1beta1.RestartDateAnnotation:     "sometime",
			},
		},
	}
	sa := manifests.PKIOperatorServiceAccount("test-namespace")
	role := manifests.PKIOperatorRole("test-namespace")
	roleBinding := manifests.PKIOperatorRoleBinding("test-namespace")
	if err := ReconcileRoleBinding(roleBinding, role, sa, config.OwnerRefFrom(hcp)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	roleBindingYaml, err := util.SerializeResource(roleBinding, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, roleBindingYaml)
}
