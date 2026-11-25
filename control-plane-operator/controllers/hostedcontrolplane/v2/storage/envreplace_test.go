package storage

import (
	"testing"

	. "github.com/onsi/gomega"
)

// fakeReleaseImageProvider is a simple mock for imageprovider.ReleaseImageProvider.
type fakeReleaseImageProvider struct {
	images  map[string]string
	version string
}

func (f *fakeReleaseImageProvider) GetImage(key string) string {
	return f.images[key]
}

func (f *fakeReleaseImageProvider) ImageExist(key string) (string, bool) {
	img, ok := f.images[key]
	return img, ok
}

func (f *fakeReleaseImageProvider) Version() string {
	return f.version
}

func (f *fakeReleaseImageProvider) ComponentVersions() (map[string]string, error) {
	return nil, nil
}

func (f *fakeReleaseImageProvider) ComponentImages() map[string]string {
	return f.images
}

func TestSetOperatorImageReferences(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name               string
		releaseImages      map[string]string
		userReleaseImages  map[string]string
		expectedValues     map[string]string
		expectedAbsentKeys []string
	}{
		{
			name: "When env var is a data plane image ref (NODE_DRIVER_REGISTRAR_IMAGE), it should use userReleaseImageProvider",
			releaseImages: map[string]string{
				"csi-node-driver-registrar": "release-registry.io/csi-node-driver-registrar:v1",
			},
			userReleaseImages: map[string]string{
				"csi-node-driver-registrar": "user-registry.io/csi-node-driver-registrar:v1",
			},
			expectedValues: map[string]string{
				"NODE_DRIVER_REGISTRAR_IMAGE": "user-registry.io/csi-node-driver-registrar:v1",
			},
		},
		{
			name: "When env var ends with _DRIVER_IMAGE, it should use userReleaseImageProvider",
			releaseImages: map[string]string{
				"aws-ebs-csi-driver": "release-registry.io/aws-ebs-csi-driver:v1",
			},
			userReleaseImages: map[string]string{
				"aws-ebs-csi-driver": "user-registry.io/aws-ebs-csi-driver:v1",
			},
			expectedValues: map[string]string{
				"AWS_EBS_DRIVER_IMAGE": "user-registry.io/aws-ebs-csi-driver:v1",
			},
		},
		{
			name: "When env var is a control plane operator image, it should use releaseImageProvider",
			releaseImages: map[string]string{
				"aws-ebs-csi-driver-operator": "release-registry.io/aws-ebs-csi-driver-operator:v1",
			},
			userReleaseImages: map[string]string{
				"aws-ebs-csi-driver-operator": "user-registry.io/aws-ebs-csi-driver-operator:v1",
			},
			expectedValues: map[string]string{
				"AWS_EBS_DRIVER_OPERATOR_IMAGE": "release-registry.io/aws-ebs-csi-driver-operator:v1",
			},
		},
		{
			name:              "When image does not exist in provider, it should not set the value",
			releaseImages:     map[string]string{},
			userReleaseImages: map[string]string{},
			expectedValues:    map[string]string{},
			expectedAbsentKeys: []string{
				"AWS_EBS_DRIVER_OPERATOR_IMAGE",
				"AWS_EBS_DRIVER_IMAGE",
				"NODE_DRIVER_REGISTRAR_IMAGE",
				"VOLUME_DEVICE_EXPORTER_IMAGE",
			},
		},
		{
			name: "When LIVENESS_PROBE_IMAGE is set, it should use userReleaseImageProvider as a data plane ref",
			releaseImages: map[string]string{
				"csi-livenessprobe": "release-registry.io/csi-livenessprobe:v1",
			},
			userReleaseImages: map[string]string{
				"csi-livenessprobe": "user-registry.io/csi-livenessprobe:v1",
			},
			expectedValues: map[string]string{
				"LIVENESS_PROBE_IMAGE": "user-registry.io/csi-livenessprobe:v1",
			},
		},
		{
			name: "When CLUSTER_CLOUD_CONTROLLER_MANAGER_OPERATOR_IMAGE is set, it should use userReleaseImageProvider as a data plane ref",
			releaseImages: map[string]string{
				"cluster-cloud-controller-manager-operator": "release-registry.io/ccm-operator:v1",
			},
			userReleaseImages: map[string]string{
				"cluster-cloud-controller-manager-operator": "user-registry.io/ccm-operator:v1",
			},
			expectedValues: map[string]string{
				"CLUSTER_CLOUD_CONTROLLER_MANAGER_OPERATOR_IMAGE": "user-registry.io/ccm-operator:v1",
			},
		},
		{
			name: "When KUBE_RBAC_PROXY_IMAGE is set, it should use userReleaseImageProvider as a data plane ref",
			releaseImages: map[string]string{
				"kube-rbac-proxy": "release-registry.io/kube-rbac-proxy:v1",
			},
			userReleaseImages: map[string]string{
				"kube-rbac-proxy": "user-registry.io/kube-rbac-proxy:v1",
			},
			expectedValues: map[string]string{
				"KUBE_RBAC_PROXY_IMAGE": "user-registry.io/kube-rbac-proxy:v1",
			},
		},
		{
			// volume-data-source-validator runs in the guest's
			// openshift-cluster-storage-operator namespace, so a control plane
			// release image there may not be pullable by guest nodes.
			name: "When VOLUME_DATA_SOURCE_VALIDATOR_IMAGE is set, it should use userReleaseImageProvider as a data plane ref",
			releaseImages: map[string]string{
				"volume-data-source-validator": "release-registry.io/volume-data-source-validator:v1",
			},
			userReleaseImages: map[string]string{
				"volume-data-source-validator": "user-registry.io/volume-data-source-validator:v1",
			},
			expectedValues: map[string]string{
				"VOLUME_DATA_SOURCE_VALIDATOR_IMAGE": "user-registry.io/volume-data-source-validator:v1",
			},
		},
		{
			name: "When HYPERSHIFT_IMAGE is set, it should use releaseImageProvider as a non-data-plane ref",
			releaseImages: map[string]string{
				"token-minter": "release-registry.io/token-minter:v1",
			},
			userReleaseImages: map[string]string{
				"token-minter": "user-registry.io/token-minter:v1",
			},
			expectedValues: map[string]string{
				"HYPERSHIFT_IMAGE": "release-registry.io/token-minter:v1",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			releaseProvider := &fakeReleaseImageProvider{images: tc.releaseImages}
			userReleaseProvider := &fakeReleaseImageProvider{images: tc.userReleaseImages}

			er := &environmentReplacer{values: map[string]string{}}
			er.setOperatorImageReferences(releaseProvider, userReleaseProvider)

			for key, expectedValue := range tc.expectedValues {
				g.Expect(er.values).To(HaveKeyWithValue(key, expectedValue), "expected %s=%s", key, expectedValue)
			}

			for _, key := range tc.expectedAbsentKeys {
				g.Expect(er.values).NotTo(HaveKey(key), "expected key %s to be absent", key)
			}
		})
	}
}
