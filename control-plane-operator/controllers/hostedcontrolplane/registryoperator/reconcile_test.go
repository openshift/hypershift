package registryoperator

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileDeployment(t *testing.T) {
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
		"cluster-image-registry-operator": "quay.io/openshift/cluster-image-registry-operator:latest",
		"token-minter":                    "quay.io/openshift/token-minter:latest",
		"docker-registry":                 "quay.io/openshift/docker-registry:latest",
		"cli":                             "quay.io/openshift/cli:latest",
	}
	deployment := manifests.ImageRegistryOperatorDeployment("test-namespace")
	imageProvider := imageprovider.NewFromImages(images)
	params := NewParams(hcp, "1.0.0", imageProvider, imageProvider, true)
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
	pm := manifests.ImageRegistryOperatorPodMonitor("test-namespace")
	ReconcilePodMonitor(pm, "the-cluster-id", metrics.MetricsSetTelemetry)
	pmYaml, err := util.SerializeResource(pm, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, pmYaml)
}
