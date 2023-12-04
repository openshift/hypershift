package cco

import (
	"testing"

	"github.com/openshift/hypershift/api"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
)

func TestReconcileDeployment(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ReleaseImage: "quay.io/ocp-dev/test-release-image:latest",
			IssuerURL:    "https://www.example.com",
			Networking: hyperv1.ClusterNetworking{
				APIServer: &hyperv1.APIServerNetworking{
					Port: pointer.Int32(1234),
				},
			},
		},
	}
	images := map[string]string{
		"cloud-credential-operator": "quay.io/openshift/cloud-credential-operator:latest",
		"token-minter":              "quay.io/openshift/token-minter:latest",
		"kube-rbac-proxy":           "quay.io/openshift/kube-rbac-proxy:latest",
		"availability-prober":       "quay.io/openshift/availability-prober:latest",
	}
	deployment := manifests.CloudCredentialOperatorDeployment("test-namespace")
	imageProvider := imageprovider.NewFromImages(images)
	params := NewParams(hcp, "1.0.0", imageProvider, true)
	if err := ReconcileDeployment(deployment, params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	deploymentYaml, err := util.SerializeResource(deployment, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, deploymentYaml)
}

func TestReconcilePodMonitor(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-namespace",
		},
	}
	pm := manifests.ImageRegistryOperatorPodMonitor("test-namespace")
	ReconcilePodMonitor(config.OwnerRefFrom(hcp), pm, "the-cluster-id", metrics.MetricsSetTelemetry)
	pmYaml, err := util.SerializeResource(pm, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, pmYaml)
}
