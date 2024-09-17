package aws

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func TestConfig(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "HCP_NAMESPACE",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				AWS: &hyperv1.AWSPlatformSpec{
					CloudProviderConfig: &hyperv1.AWSCloudProviderConfig{
						VPC:    "my-vpc",
						Subnet: &hyperv1.AWSResourceReference{ID: ptr.To("my-subnet-ID")},
						Zone:   "my-zone",
					},
				},
			},
			InfraID: "my-infra-ID",
		},
	}

	cm := &corev1.ConfigMap{}
	_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", cm)
	if err != nil {
		t.Fatalf("LoadManifestInto: unexpected error: %v", err)
	}
	cpContext := component.WorkloadContext{
		HCP: hcp,
	}
	err = adaptConfig(cpContext, cm)
	if err != nil {
		t.Fatalf("adaptConfig: unexpected error: %v", err)
	}

	yaml, err := util.SerializeResource(cm, api.Scheme)
	if err != nil {
		t.Fatalf("SerializeResource: unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, yaml)
}
