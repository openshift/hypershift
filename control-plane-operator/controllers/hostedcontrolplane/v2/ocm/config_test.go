package ocm

import (
	"testing"

	v1 "github.com/openshift/api/config/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	controlplanecomponent "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	corev1 "k8s.io/api/core/v1"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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
			Configuration: &hyperv1.ClusterConfiguration{
				Image: &v1.ImageSpec{},
				Network: &v1.NetworkSpec{
					ExternalIP: &v1.ExternalIPConfig{
						AutoAssignCIDRs: []string{"99.1.0.0/24"},
					},
				},
			},
		},
	}
	images := map[string]string{
		"openshift-controller-manager": "quay.io/test/openshift-controller-manager",
		"docker-builder":               "quay.io/test/docker-builder",
		"deployer":                     "quay.io/test/deployer",
	}
	imageProvider := imageprovider.NewFromImages(images)

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

	configMap := &corev1.ConfigMap{}
	controlplanecomponent.LoadManifestInto(ComponentName, "config.yaml", configMap)

	config := &openshiftcpv1.OpenShiftControllerManagerConfig{}
	err := util.DeserializeResource(configMap.Data[configKey], config, api.Scheme)
	if err != nil {
		t.Fatalf("unable to decode existing openshift controller manager configuration: %v", err)
	}

	adaptConfig(config, hcp.Spec.Configuration, imageProvider, buildConfig)
	configStr, err := util.SerializeResource(config, api.Scheme)
	if err != nil {
		t.Fatalf("failed to serialize openshift controller manager configuration: %v", err)
	}
	configMap.Data[configKey] = configStr

	configMapYaml, err := util.SerializeResource(configMap, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, configMapYaml)
}
