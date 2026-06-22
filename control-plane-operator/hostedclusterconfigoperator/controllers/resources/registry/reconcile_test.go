package registry

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"

	imageregistryv1 "github.com/openshift/api/imageregistry/v1"
	operatorv1 "github.com/openshift/api/operator/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var ibmDefaultConfig = &imageregistryv1.Config{
	ObjectMeta: metav1.ObjectMeta{
		Generation: 1,
	},
	Spec: imageregistryv1.ImageRegistrySpec{
		OperatorSpec: operatorv1.OperatorSpec{
			ManagementState: operatorv1.Removed,
		},
		Replicas: 1,
		Storage: imageregistryv1.ImageRegistryConfigStorage{
			EmptyDir: &imageregistryv1.ImageRegistryConfigStorageEmptyDir{},
		},
	},
}

func TestReconcileRegistryConfig(t *testing.T) {
	testsCases := []struct {
		name                    string
		inputConfig             *imageregistryv1.Config
		inputPlatform           hyperv1.PlatformType
		inputAvailabilityPolicy hyperv1.AvailabilityPolicy
		expectedConfig          *imageregistryv1.Config
	}{
		{
			name:                    "IBM Cloud default",
			inputAvailabilityPolicy: hyperv1.HighlyAvailable,
			inputPlatform:           hyperv1.IBMCloudPlatform,
			inputConfig:             manifests.Registry(),
			expectedConfig:          ibmDefaultConfig,
		},
		{
			name:                    "IBM Cloud bad config",
			inputAvailabilityPolicy: hyperv1.HighlyAvailable,
			inputPlatform:           hyperv1.IBMCloudPlatform,
			inputConfig: &imageregistryv1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Generation: 1,
				},
				Spec: imageregistryv1.ImageRegistrySpec{
					Storage: imageregistryv1.ImageRegistryConfigStorage{
						IBMCOS: &imageregistryv1.ImageRegistryConfigStorageIBMCOS{},
					},
				},
			},
			expectedConfig: ibmDefaultConfig,
		},
		{
			name:                    "IBM Cloud no update",
			inputAvailabilityPolicy: hyperv1.HighlyAvailable,
			inputPlatform:           hyperv1.IBMCloudPlatform,
			inputConfig: &imageregistryv1.Config{
				ObjectMeta: metav1.ObjectMeta{
					Generation:      1,
					ResourceVersion: "v1",
				},
				Spec: imageregistryv1.ImageRegistrySpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: operatorv1.Managed,
					},
					Storage: imageregistryv1.ImageRegistryConfigStorage{
						PVC: &imageregistryv1.ImageRegistryConfigStoragePVC{},
					},
				},
			},
			expectedConfig: &imageregistryv1.Config{
				Spec: imageregistryv1.ImageRegistrySpec{
					OperatorSpec: operatorv1.OperatorSpec{
						ManagementState: operatorv1.Managed,
					},
					Storage: imageregistryv1.ImageRegistryConfigStorage{
						PVC: &imageregistryv1.ImageRegistryConfigStoragePVC{},
					},
				},
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			config := tc.inputConfig
			err := ReconcileRegistryConfig(config, tc.inputPlatform, tc.inputAvailabilityPolicy)
			g.Expect(err).To(Not(HaveOccurred()))
			g.Expect(config.Spec.Storage).To(BeEquivalentTo(tc.expectedConfig.Spec.Storage))
			g.Expect(config.Spec.Replicas).To(BeEquivalentTo(tc.expectedConfig.Spec.Replicas))
			g.Expect(config.Spec.ManagementState).To(BeEquivalentTo(tc.expectedConfig.Spec.ManagementState))
		})
	}
}
