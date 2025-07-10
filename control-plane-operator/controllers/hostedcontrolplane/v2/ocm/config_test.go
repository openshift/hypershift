package ocm

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	controlplanecomponent "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	v1 "github.com/openshift/api/config/v1"
	openshiftcpv1 "github.com/openshift/api/openshiftcontrolplane/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileOpenShiftControllerManagerConfig(t *testing.T) {
	testFunc := func(capabilities *hyperv1.Capabilities) func(*testing.T) {
		return func(t *testing.T) {
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
					Capabilities: capabilities,
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
			_, _, err := controlplanecomponent.LoadManifestInto(ComponentName, "config.yaml", configMap)
			if err != nil {
				t.Fatalf("%s", err.Error())
			}

			config := &openshiftcpv1.OpenShiftControllerManagerConfig{}
			err = util.DeserializeResource(configMap.Data[configKey], config, api.Scheme)
			if err != nil {
				t.Fatalf("unable to decode existing openshift controller manager configuration: %v", err)
			}

			adaptConfig(config, hcp.Spec.Configuration, imageProvider, buildConfig, hcp.Spec.Capabilities, []string{"foo=true", "bar=false"})
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
	}

	caps := &hyperv1.Capabilities{
		Disabled: []hyperv1.OptionalCapability{hyperv1.ImageRegistryCapability},
		Enabled:  []hyperv1.OptionalCapability{hyperv1.BaremetalCapability},
	}

	t.Run("WithAllCapabilitiesEnabled", testFunc(nil))
	t.Run("WithCapabilitiesEnabledAndDisabled", testFunc(caps))
}
