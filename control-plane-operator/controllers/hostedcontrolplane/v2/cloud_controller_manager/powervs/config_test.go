package powervs

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/k8sutil"
	"github.com/openshift/hypershift/support/testutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptConfig(t *testing.T) {
	t.Parallel()

	hcp := newTestHCP()
	hcp.Namespace = "HCP_NAMESPACE"

	cm := &corev1.ConfigMap{}
	_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cpContext := component.WorkloadContext{
		HCP: hcp,
	}
	err = adaptConfig(cpContext, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	yaml, err := k8sutil.SerializeResource(cm, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, yaml)
}

func TestAdaptConfigTemplateExecution(t *testing.T) {
	t.Parallel()

	t.Run("When PowerVS platform is configured, it should populate template with correct values", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := newTestHCP()
		hcp.Spec.Platform.PowerVS.AccountID = "account-123"
		hcp.Spec.Platform.PowerVS.ServiceInstanceID = "service-instance-456"
		hcp.Spec.Platform.PowerVS.Region = "us-south"
		hcp.Spec.Platform.PowerVS.Zone = "us-south-1"
		hcp.Spec.Platform.PowerVS.ResourceGroup = "my-resource-group"
		hcp.Spec.Platform.PowerVS.VPC = &hyperv1.PowerVSVPC{
			Name:   "my-vpc",
			Region: "us-south",
			Subnet: "my-subnet",
		}

		cm := &corev1.ConfigMap{
			Data: map[string]string{
				configKey: `ClusterID={{.ClusterID}},AccountID={{.AccountID}},ServiceInstanceID={{.PowerVSCloudInstanceID}},Region={{.Region}},PowerVSRegion={{.PowerVSRegion}},PowerVSZone={{.PowerVSZone}},ResourceGroup={{.G2ResourceGroupName}},VPCName={{.G2VpcName}},SubnetNames={{.G2VpcSubnetNames}}`,
			},
		}

		cpContext := component.WorkloadContext{
			HCP: hcp,
		}
		err := adaptConfig(cpContext, cm)
		g.Expect(err).ToNot(HaveOccurred())

		result := cm.Data[configKey]
		g.Expect(result).To(ContainSubstring("ClusterID=test-cluster"))
		g.Expect(result).To(ContainSubstring("AccountID=account-123"))
		g.Expect(result).To(ContainSubstring("ServiceInstanceID=service-instance-456"))
		g.Expect(result).To(ContainSubstring("Region=us-south"))
		g.Expect(result).To(ContainSubstring("PowerVSRegion=us-south"))
		g.Expect(result).To(ContainSubstring("PowerVSZone=us-south-1"))
		g.Expect(result).To(ContainSubstring("ResourceGroup=my-resource-group"))
		g.Expect(result).To(ContainSubstring("VPCName=my-vpc"))
		g.Expect(result).To(ContainSubstring("SubnetNames=my-subnet"))
	})
}

func TestAdaptConfigErrorStates(t *testing.T) {
	t.Parallel()

	t.Run("When PowerVS platform is nil, it should return error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cluster",
				Namespace: "test-namespace",
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				Platform: hyperv1.PlatformSpec{
					Type:    hyperv1.PowerVSPlatform,
					PowerVS: nil,
				},
			},
		}

		cm := &corev1.ConfigMap{
			Data: map[string]string{
				configKey: "{{.ClusterID}}",
			},
		}

		cpContext := component.WorkloadContext{
			HCP: hcp,
		}
		err := adaptConfig(cpContext, cm)
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring(".spec.platform.powervs is not defined"))
	})

	t.Run("When VPC is nil, it should panic", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := newTestHCP()
		hcp.Spec.Platform.PowerVS.VPC = nil

		cm := &corev1.ConfigMap{
			Data: map[string]string{
				configKey: "{{.ClusterID}}",
			},
		}

		cpContext := component.WorkloadContext{
			HCP: hcp,
		}
		g.Expect(func() { _ = adaptConfig(cpContext, cm) }).To(Panic())
	})
}

func TestConfigKeyConstant(t *testing.T) {
	t.Parallel()

	t.Run("When configKey is used, it should match expected value", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		g.Expect(configKey).To(Equal("ccm-config"))
	})
}

func TestAdaptConfigMapDataStructure(t *testing.T) {
	t.Parallel()

	t.Run("When PowerVS config is complete, it should build config map with all fields", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		hcp := newTestHCP()
		hcp.Name = "my-cluster"
		hcp.Namespace = "test-ns"
		hcp.Spec.Platform.PowerVS.AccountID = "acc-id"
		hcp.Spec.Platform.PowerVS.ServiceInstanceID = "svc-instance"
		hcp.Spec.Platform.PowerVS.Region = "eu-gb"
		hcp.Spec.Platform.PowerVS.Zone = "eu-gb-1"
		hcp.Spec.Platform.PowerVS.ResourceGroup = "rg-test"
		hcp.Spec.Platform.PowerVS.VPC = &hyperv1.PowerVSVPC{
			Name:   "vpc-name",
			Region: "eu-gb",
			Subnet: "subnet-1",
		}

		templateContent := strings.Join([]string{
			"AccountID={{.AccountID}}",
			"G2workerServiceAccountID={{.G2workerServiceAccountID}}",
			"G2ResourceGroupName={{.G2ResourceGroupName}}",
			"G2VpcSubnetNames={{.G2VpcSubnetNames}}",
			"G2VpcName={{.G2VpcName}}",
			"ClusterID={{.ClusterID}}",
			"Region={{.Region}}",
			"PowerVSCloudInstanceID={{.PowerVSCloudInstanceID}}",
			"PowerVSRegion={{.PowerVSRegion}}",
			"PowerVSZone={{.PowerVSZone}}",
		}, "\n")

		cm := &corev1.ConfigMap{
			Data: map[string]string{
				configKey: templateContent,
			},
		}

		cpContext := component.WorkloadContext{
			HCP: hcp,
		}
		err := adaptConfig(cpContext, cm)
		g.Expect(err).ToNot(HaveOccurred())

		result := cm.Data[configKey]
		g.Expect(result).To(Equal(strings.Join([]string{
			"AccountID=acc-id",
			"G2workerServiceAccountID=acc-id",
			"G2ResourceGroupName=rg-test",
			"G2VpcSubnetNames=subnet-1",
			"G2VpcName=vpc-name",
			"ClusterID=my-cluster",
			"Region=eu-gb",
			"PowerVSCloudInstanceID=svc-instance",
			"PowerVSRegion=eu-gb",
			"PowerVSZone=eu-gb-1",
		}, "\n")))
	})
}

// newTestHCP creates a HostedControlPlane with default PowerVS configuration for testing.
func newTestHCP() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.PowerVSPlatform,
				PowerVS: &hyperv1.PowerVSPlatformSpec{
					AccountID:         "test-account-id",
					ServiceInstanceID: "test-service-instance-id",
					Region:            "us-south",
					Zone:              "us-south-1",
					ResourceGroup:     "test-resource-group",
					VPC: &hyperv1.PowerVSVPC{
						Name:   "test-vpc",
						Region: "us-south",
						Subnet: "test-subnet",
					},
				},
			},
		},
	}
}
