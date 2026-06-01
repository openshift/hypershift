package storage

import (
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
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

func TestSetVersions(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                         string
		version                      string
		expectedOperatorImageVersion string
		expectedOperandImageVersion  string
	}{
		{
			name:                         "When version is set, it should set OPERATOR_IMAGE_VERSION and OPERAND_IMAGE_VERSION",
			version:                      "4.16.0",
			expectedOperatorImageVersion: "4.16.0",
			expectedOperandImageVersion:  "4.16.0",
		},
		{
			name:                         "When version is empty, it should set both versions to empty string",
			version:                      "",
			expectedOperatorImageVersion: "",
			expectedOperandImageVersion:  "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			er := &environmentReplacer{values: map[string]string{}}
			er.setVersions(tc.version)

			g.Expect(er.values["OPERATOR_IMAGE_VERSION"]).To(Equal(tc.expectedOperatorImageVersion))
			g.Expect(er.values["OPERAND_IMAGE_VERSION"]).To(Equal(tc.expectedOperandImageVersion))
		})
	}
}

func TestReplaceEnvVars(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name            string
		replacerValues  map[string]string
		inputEnvVars    []corev1.EnvVar
		expectedEnvVars []corev1.EnvVar
	}{
		{
			name: "When env var name matches a value in the replacer, it should replace the value",
			replacerValues: map[string]string{
				"MY_VAR": "new-value",
			},
			inputEnvVars: []corev1.EnvVar{
				{Name: "MY_VAR", Value: "old-value"},
			},
			expectedEnvVars: []corev1.EnvVar{
				{Name: "MY_VAR", Value: "new-value"},
			},
		},
		{
			name: "When env var name does not match any value, it should leave the value unchanged",
			replacerValues: map[string]string{
				"OTHER_VAR": "some-value",
			},
			inputEnvVars: []corev1.EnvVar{
				{Name: "MY_VAR", Value: "original-value"},
			},
			expectedEnvVars: []corev1.EnvVar{
				{Name: "MY_VAR", Value: "original-value"},
			},
		},
		{
			name:            "When env vars list is empty, it should not panic",
			replacerValues:  map[string]string{"MY_VAR": "value"},
			inputEnvVars:    []corev1.EnvVar{},
			expectedEnvVars: []corev1.EnvVar{},
		},
		{
			name: "When multiple env vars match, it should replace all matching values",
			replacerValues: map[string]string{
				"VAR_A": "new-a",
				"VAR_B": "new-b",
				"VAR_C": "new-c",
			},
			inputEnvVars: []corev1.EnvVar{
				{Name: "VAR_A", Value: "old-a"},
				{Name: "VAR_B", Value: "old-b"},
				{Name: "VAR_UNMATCHED", Value: "keep-me"},
			},
			expectedEnvVars: []corev1.EnvVar{
				{Name: "VAR_A", Value: "new-a"},
				{Name: "VAR_B", Value: "new-b"},
				{Name: "VAR_UNMATCHED", Value: "keep-me"},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			er := &environmentReplacer{values: tc.replacerValues}
			er.replaceEnvVars(tc.inputEnvVars)

			g.Expect(tc.inputEnvVars).To(Equal(tc.expectedEnvVars))
		})
	}
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

func TestNewEnvironmentReplacer(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	releaseProvider := &fakeReleaseImageProvider{
		images: map[string]string{
			"aws-ebs-csi-driver-operator": "release-registry.io/aws-ebs-csi-driver-operator:v1",
			"token-minter":                "release-registry.io/token-minter:v1",
		},
		version: "4.16.0",
	}
	userReleaseProvider := &fakeReleaseImageProvider{
		images: map[string]string{
			"aws-ebs-csi-driver":        "user-registry.io/aws-ebs-csi-driver:v1",
			"csi-node-driver-registrar": "user-registry.io/csi-node-driver-registrar:v1",
		},
		version: "4.15.0",
	}

	er := newEnvironmentReplacer(releaseProvider, userReleaseProvider)

	// Version comes from userReleaseImageProvider
	g.Expect(er.values["OPERATOR_IMAGE_VERSION"]).To(Equal("4.15.0"))
	g.Expect(er.values["OPERAND_IMAGE_VERSION"]).To(Equal("4.15.0"))

	// Control plane operator image from releaseImageProvider
	g.Expect(er.values["AWS_EBS_DRIVER_OPERATOR_IMAGE"]).To(Equal("release-registry.io/aws-ebs-csi-driver-operator:v1"))
	g.Expect(er.values["HYPERSHIFT_IMAGE"]).To(Equal("release-registry.io/token-minter:v1"))

	// Data plane / _DRIVER_IMAGE from userReleaseImageProvider
	g.Expect(er.values["AWS_EBS_DRIVER_IMAGE"]).To(Equal("user-registry.io/aws-ebs-csi-driver:v1"))
	g.Expect(er.values["NODE_DRIVER_REGISTRAR_IMAGE"]).To(Equal("user-registry.io/csi-node-driver-registrar:v1"))
}
