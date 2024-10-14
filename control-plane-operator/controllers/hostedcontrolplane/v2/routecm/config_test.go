package routecm

import (
	"testing"

	v1 "github.com/openshift/api/config/v1"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
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
			Configuration: &hyperv1.ClusterConfiguration{
				Network: &v1.NetworkSpec{
					ExternalIP: &v1.ExternalIPConfig{
						AutoAssignCIDRs: []string{"99.1.0.0/24"},
					},
				},
			},
		},
	}

	configMap := &corev1.ConfigMap{}
	_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", configMap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	configMap.SetNamespace(hcp.Namespace)

	if err := adaptConfigMap(controlplanecomponent.ControlPlaneContext{HCP: hcp}, configMap); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	configMapYaml, err := util.SerializeResource(configMap, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, configMapYaml)
}
