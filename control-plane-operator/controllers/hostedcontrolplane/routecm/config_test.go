package routecm

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	config2 "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	v1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileOpenShiftRouteControllerManagerConfig(t *testing.T) {
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
		"route-controller-manager": "quay.io/test/route-controller-manager",
	}
	imageProvider := imageprovider.NewFromImages(images)

	params := NewOpenShiftRouteControllerManagerParams(hcp, imageProvider, true)
	configMap := manifests.OpenShiftRouteControllerManagerConfig(hcp.Namespace)

	networkConfig := &v1.NetworkSpec{
		ExternalIP: &v1.ExternalIPConfig{
			AutoAssignCIDRs: []string{"99.1.0.0/24"},
		},
	}

	if err := ReconcileOpenShiftRouteControllerManagerConfig(configMap, config2.OwnerRefFrom(hcp), params.MinTLSVersion(), params.CipherSuites(), networkConfig); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	configMapYaml, err := util.SerializeResource(configMap, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, configMapYaml)
}
