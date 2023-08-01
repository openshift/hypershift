package ocm

import (
	v1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	config2 "github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/globalconfig"
	corev1 "k8s.io/api/core/v1"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileOpenShiftControllerManagerConfig(t *testing.T) {
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
		"openshift-controller-manager": "quay.io/test/openshift-controller-manager",
		"docker-builder":               "quay.io/test/docker-builder",
		"deployer":                     "quay.io/test/deployer",
	}
	imageProvider := imageprovider.NewFromImages(images)

	imageConfig := &v1.Image{
		Status: v1.ImageStatus{
			InternalRegistryHostname: "image-registry.openshift-image-registry.svc:5000",
		},
	}

	buildConfig := &v1.Build{
		Spec: v1.BuildSpec{
			BuildDefaults: v1.BuildDefaults{
				Env: []corev1.EnvVar{
					{
						Name:  "TEST_VAR",
						Value: "TEST_VALUE",
					},
				},
			},
		},
	}

	networkConfig := &v1.NetworkSpec{
		ExternalIP: &v1.ExternalIPConfig{
			AutoAssignCIDRs: []string{"99.1.0.0/24"},
		},
	}

	observedConfig := &globalconfig.ObservedConfig{
		Build: buildConfig,
		Image: imageConfig,
	}

	params := NewOpenShiftControllerManagerParams(hcp, observedConfig, imageProvider, true)
	configMap := manifests.OpenShiftControllerManagerConfig(hcp.Namespace)

	if err := ReconcileOpenShiftControllerManagerConfig(configMap, config2.OwnerRefFrom(hcp), params.DeployerImage, params.DockerBuilderImage, params.MinTLSVersion(), params.CipherSuites(), imageConfig, buildConfig, networkConfig); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	configMapYaml, err := util.SerializeResource(configMap, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, configMapYaml)
}
