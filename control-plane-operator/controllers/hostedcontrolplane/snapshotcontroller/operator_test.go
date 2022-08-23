package snapshotcontroller

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileOperatorDeployment(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ReleaseImage: "quay.io/ocp-dev/test-release-image:latest",
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
			IssuerURL: "https://www.example.com",
		},
	}
	images := map[string]string{
		"cluster-csi-snapshot-controller-operator": "quay.io/openshift/cluster-csi-snapshot-controller-operator:latest",
		"token-minter":                    "quay.io/openshift/token-minter:latest",
		"csi-snapshot-controller":         "quay.io/openshift/csi-snapshot-controller:latest",
		"csi-snapshot-validation-webhook": "quay.io/openshift/csi-snapshot-validation-webhook:latest",
	}
	deployment := manifests.CSISnapshotControllerOperatorDeployment("test-namespace")
	params := NewParams(hcp, "1.0.0", images, true)
	if err := ReconcileOperatorDeployment(deployment, params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	deploymentYaml, err := util.SerializeResource(deployment, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, deploymentYaml)
}
