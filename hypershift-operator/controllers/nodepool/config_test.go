package nodepool

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	api "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	supportutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/google/go-cmp/cmp"
)

// coreConfigMaps is a fake list of configMaps to match default expectation of 3.
var coreConfigMaps = []crclient.Object{
	&corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-ignition-config-1",
			Namespace: "test-test",
			Labels: map[string]string{
				nodePoolCoreIgnitionConfigLabel: "true",
			},
		},
	},
	&corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-ignition-config-2",
			Namespace: "test-test",
			Labels: map[string]string{
				nodePoolCoreIgnitionConfigLabel: "true",
			},
		},
	},
	&corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "core-ignition-config-3",
			Namespace: "test-test",
			Labels: map[string]string{
				nodePoolCoreIgnitionConfigLabel: "true",
			},
		},
	},
}

func TestNewConfigGenerator(t *testing.T) {
	machineConfig := `
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: config-1
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
        source: "[Service]\nType=oneshot\nExecStart=/usr/bin/echo Hello World\n\n[Install]\nWantedBy=multi-user.target"
        filesystem: root
        mode: 493
        path: /usr/local/bin/file1.sh
`
	machineConfigDefaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: config-1
spec:
  baseOSExtensionsContainerImage: ""
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents: null
        filesystem: root
        mode: 493
        path: /usr/local/bin/file1.sh
        source: |-
          [Service]
          Type=oneshot
          ExecStart=/usr/bin/echo Hello World

          [Install]
          WantedBy=multi-user.target
  extensions: null
  fips: false
  kernelArguments: null
  kernelType: ""
  osImageURL: ""
`
	globalConfig := hyperv1.ClusterConfiguration{
		Authentication: &configv1.AuthenticationSpec{},
		Image:          &configv1.ImageSpec{},
		Proxy:          &configv1.ProxySpec{},
	}
	// Validating against this ensure that if marshaling of the config APIs ever produces a different output
	// because new fields are added, this test will fail.
	expectedGlobalConfigString := `{"metadata":{"name":"cluster","creationTimestamp":null},"spec":{"trustedCA":{"name":""}},"status":{}}
{"metadata":{"name":"cluster","creationTimestamp":null},"spec":{"additionalTrustedCA":{"name":""},"registrySources":{}},"status":{}}
`

	hostedCluster := &hyperv1.HostedCluster{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test",
		},
		Spec: hyperv1.HostedClusterSpec{
			PullSecret: corev1.LocalObjectReference{
				Name: "pull-secret",
			},
			Configuration: &globalConfig,
		},
	}

	testCases := []struct {
		name                       string
		nodePool                   *hyperv1.NodePool
		releaseImage               *releaseinfo.ReleaseImage
		hostedCluster              *hyperv1.HostedCluster
		config                     []crclient.Object
		expectedMCORawConfig       string
		client                     bool
		expectedHash               string
		expectedHashWithoutVersion string
		error                      error
	}{
		{
			name:                       "When all input is given it should not return an error",
			expectedHash:               "e1d8d58e",
			expectedHashWithoutVersion: "0db5756d",
			nodePool:                   &hyperv1.NodePool{},
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name: "latest",
					},
				},
			},
			hostedCluster: hostedCluster,
			client:        true,
			error:         nil,
		},
		{
			name:          "When client is missing it should return an error",
			nodePool:      &hyperv1.NodePool{},
			releaseImage:  &releaseinfo.ReleaseImage{},
			hostedCluster: hostedCluster,
			client:        false,
			error:         fmt.Errorf("client can't be nil"),
		},
		{
			name:          "When release image is missing it should return an error",
			nodePool:      &hyperv1.NodePool{},
			releaseImage:  nil,
			hostedCluster: hostedCluster,
			client:        true,
			error:         fmt.Errorf("release image can't be nil"),
		},
		{
			name:                       "When nodepool has configs it should populate mcoRawConfig ",
			expectedHash:               "801aff6a",
			expectedHashWithoutVersion: "fef02451",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "config-1",
						},
					},
				},
			},
			config: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "config-1",
						Namespace: "test",
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig,
					},
				},
			},
			expectedMCORawConfig: machineConfigDefaulted,
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name: "latest",
					},
				},
			},
			hostedCluster: hostedCluster,
			client:        true,
			error:         nil,
		},
		{
			name: "When nodepool has invalid config it should fail ",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "does-not-exist",
						},
					},
				},
			},
			expectedMCORawConfig: machineConfigDefaulted,
			releaseImage:         &releaseinfo.ReleaseImage{},
			hostedCluster:        hostedCluster,
			client:               true,
			error:                fmt.Errorf("configmaps \"does-not-exist\" not found"),
		},
		{
			name:                       "When additionalTrustBundle is specified it should be included in rolloutConfig",
			expectedHash:               "dc74976e",
			expectedHashWithoutVersion: "71375893",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test",
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "config-1",
						},
					},
				},
			},
			config: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "config-1",
						Namespace: "test",
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig,
					},
				},
			},
			expectedMCORawConfig: machineConfigDefaulted,
			releaseImage: &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name: "latest",
					},
				},
			},
			client: true,
			error:  nil,
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "test",
				},
				Spec: hyperv1.HostedClusterSpec{
					PullSecret: corev1.LocalObjectReference{
						Name: "pull-secret-2",
					},
					AdditionalTrustBundle: &corev1.LocalObjectReference{
						Name: "additional-trust-bundle",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			var client crclient.Client
			if tc.client {
				fakeObjects := append(tc.config, coreConfigMaps...)
				client = fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(fakeObjects...).Build()
			}

			cg, err := NewConfigGenerator(context.Background(), client, tc.hostedCluster, tc.nodePool, tc.releaseImage, "")
			if tc.error != nil {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.error.Error()))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cg.controlplaneNamespace).To(Equal("test-test"))
			if tc.expectedMCORawConfig != "" {
				if diff := cmp.Diff(cg.mcoRawConfig, tc.expectedMCORawConfig); diff != "" {
					t.Errorf("actual config differs from expected: %s", diff)
				}
			}
			g.Expect(cg.pullSecretName).To(Equal(tc.hostedCluster.Spec.PullSecret.Name))
			if tc.hostedCluster.Spec.AdditionalTrustBundle != nil {
				g.Expect(cg.additionalTrustBundleName).To(Equal("additional-trust-bundle"))
			}

			if tc.hostedCluster.Spec.Configuration != nil {
				if diff := cmp.Diff(cg.globalConfig, expectedGlobalConfigString); diff != "" {
					t.Errorf("actual config differs from expected: %s", diff)
				}
			}

			g.Expect(cg.Hash()).To(Equal(tc.expectedHash))
			g.Expect(cg.HashWithoutVersion()).To(Equal(tc.expectedHashWithoutVersion))
		})
	}
}

func TestCompressedAndEncoded(t *testing.T) {
	testCases := []struct {
		name         string
		mcoRawConfig string
	}{
		{
			name:         "When mcoRawConfig has content it should be possible to decode and decompress",
			mcoRawConfig: "test config",
		},
		{
			name:         "When mcoRawConfig is empty it should be possible to decode and decompress",
			mcoRawConfig: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cg := &ConfigGenerator{
				rolloutConfig: &rolloutConfig{
					mcoRawConfig: tc.mcoRawConfig,
				},
			}

			compressedAndEncoded, err := cg.CompressedAndEncoded()
			g.Expect(err).ToNot(HaveOccurred())

			decodedAndDecompressed, err := supportutil.DecodeAndDecompress(compressedAndEncoded.Bytes())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(decodedAndDecompressed.String()).To(Equal(tc.mcoRawConfig))
		})
	}
}

func TestCompressed(t *testing.T) {
	testCases := []struct {
		name         string
		mcoRawConfig string
	}{
		{
			name:         "When mcoRawConfig has content it should be possible to decompress",
			mcoRawConfig: "test config",
		},
		{
			name:         "When mcoRawConfig is empty it should be possible to decompress",
			mcoRawConfig: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cg := &ConfigGenerator{
				rolloutConfig: &rolloutConfig{
					mcoRawConfig: tc.mcoRawConfig,
				},
			}

			compressed, err := cg.Compressed()
			g.Expect(err).ToNot(HaveOccurred())

			decompressed, err := decompress(compressed.Bytes())
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(string(decompressed)).To(Equal(tc.mcoRawConfig))
		})
	}
}

func decompress(content []byte) ([]byte, error) {
	if len(content) == 0 {
		return nil, nil
	}
	gr, err := gzip.NewReader(bytes.NewBuffer(content))
	if err != nil {
		return nil, fmt.Errorf("failed to uncompress content: %w", err)
	}
	defer gr.Close()
	data, err := io.ReadAll(gr)
	if err != nil {
		return nil, fmt.Errorf("failed to read content: %w", err)
	}
	return data, nil
}

func TestHash(t *testing.T) {
	baseCaseMCORawConfig := "test config"
	baseCaseReleaseVersion := "4.7.0"
	baseCasePullSecretName := "pull-secret"
	baseCaseAdditionalTrustBundleName := "trust-bundle"
	baseCaseGlobalConfig := "global config"
	baseCaseHash := "bb196408"

	testCases := []struct {
		name                      string
		mcoRawConfig              string
		releaseVersion            string
		pullSecretName            string
		additionalTrustBundleName string
		globalConfig              string
		expected                  string
	}{
		{
			name:                      "Base case",
			mcoRawConfig:              baseCaseMCORawConfig,
			releaseVersion:            baseCaseReleaseVersion,
			pullSecretName:            baseCasePullSecretName,
			additionalTrustBundleName: baseCaseAdditionalTrustBundleName,
			globalConfig:              baseCaseGlobalConfig,
			expected:                  baseCaseHash,
		},
		{
			name:                      "A different version should change the hash",
			mcoRawConfig:              baseCaseMCORawConfig,
			releaseVersion:            "4.8.0",
			pullSecretName:            baseCasePullSecretName,
			additionalTrustBundleName: baseCaseAdditionalTrustBundleName,
			globalConfig:              baseCaseGlobalConfig,
			expected:                  "27bb7699",
		},
		{
			name:                      "A different mcoRawConfig should change the hash",
			mcoRawConfig:              "different",
			releaseVersion:            baseCaseReleaseVersion,
			pullSecretName:            baseCasePullSecretName,
			additionalTrustBundleName: baseCaseAdditionalTrustBundleName,
			globalConfig:              baseCaseGlobalConfig,
			expected:                  "25f99ac5",
		},
		{
			name:                      "A different pullSecretName should change the hash",
			mcoRawConfig:              baseCaseMCORawConfig,
			releaseVersion:            baseCaseReleaseVersion,
			pullSecretName:            "different",
			additionalTrustBundleName: baseCaseAdditionalTrustBundleName,
			globalConfig:              baseCaseGlobalConfig,
			expected:                  "d0d6f6e9",
		},
		{
			name:                      "A different trust-bundle should change the hash",
			mcoRawConfig:              baseCaseMCORawConfig,
			releaseVersion:            baseCaseReleaseVersion,
			pullSecretName:            baseCasePullSecretName,
			additionalTrustBundleName: "different",
			globalConfig:              baseCaseGlobalConfig,
			expected:                  "42d42744",
		},
		{
			name:                      "A different globalConfig should change the hash",
			mcoRawConfig:              baseCaseMCORawConfig,
			releaseVersion:            baseCaseReleaseVersion,
			pullSecretName:            baseCasePullSecretName,
			additionalTrustBundleName: baseCaseAdditionalTrustBundleName,
			globalConfig:              "different",
			expected:                  "e916ddfe",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			releaseImage := &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name: tc.releaseVersion,
					},
				},
			}
			cg := &ConfigGenerator{
				rolloutConfig: &rolloutConfig{
					mcoRawConfig:              tc.mcoRawConfig,
					pullSecretName:            tc.pullSecretName,
					additionalTrustBundleName: tc.additionalTrustBundleName,
					globalConfig:              tc.globalConfig,
					releaseImage:              releaseImage,
				},
			}

			hash := cg.Hash()
			g.Expect(hash).ToNot(BeEmpty())
			g.Expect(hash).To(Equal(tc.expected))
			if tc.name != "Base case" {
				g.Expect(hash).ToNot(Equal(baseCaseHash))
			}
		})
	}
}

func TestHashWithoutVersion(t *testing.T) {
	baseCaseMCORawConfig := "test config"
	baseCaseReleaseVersion := "4.7.0"
	baseCasePullSecretName := "pull-secret"
	baseCaseAdditionalTrustBundleName := "trust-bundle"
	baseCaseGlobalConfig := "global config"
	baseCaseHash := "85234650"
	testCases := []struct {
		name                      string
		mcoRawConfig              string
		releaseVersion            string
		pullSecretName            string
		additionalTrustBundleName string
		globalConfig              string
		expected                  string
	}{
		{
			name:                      "Base case",
			mcoRawConfig:              baseCaseMCORawConfig,
			releaseVersion:            baseCaseReleaseVersion,
			pullSecretName:            baseCasePullSecretName,
			additionalTrustBundleName: baseCaseAdditionalTrustBundleName,
			globalConfig:              baseCaseGlobalConfig,
			expected:                  baseCaseHash,
		},
		{
			name:                      "A different version should not change the hash",
			mcoRawConfig:              baseCaseMCORawConfig,
			releaseVersion:            "4.8.0",
			pullSecretName:            baseCasePullSecretName,
			additionalTrustBundleName: baseCaseAdditionalTrustBundleName,
			globalConfig:              baseCaseGlobalConfig,
			expected:                  baseCaseHash,
		},
		{
			name:                      "A different mcoRawConfig should change the hash",
			mcoRawConfig:              "different",
			releaseVersion:            baseCaseReleaseVersion,
			pullSecretName:            baseCasePullSecretName,
			additionalTrustBundleName: baseCaseAdditionalTrustBundleName,
			globalConfig:              baseCaseGlobalConfig,
			expected:                  "5ea671c5",
		},
		{
			name:                      "A different pullSecretName should change the hash",
			mcoRawConfig:              baseCaseMCORawConfig,
			releaseVersion:            baseCaseReleaseVersion,
			pullSecretName:            "different",
			additionalTrustBundleName: baseCaseAdditionalTrustBundleName,
			globalConfig:              baseCaseGlobalConfig,
			expected:                  "f6e82eb7",
		},
		{
			name:                      "A different trust-bundle should change the hash",
			mcoRawConfig:              baseCaseMCORawConfig,
			releaseVersion:            baseCaseReleaseVersion,
			pullSecretName:            baseCasePullSecretName,
			additionalTrustBundleName: "different",
			globalConfig:              baseCaseGlobalConfig,
			expected:                  "935c3492",
		},
		{
			// TODO(alberto): This was left inconsistent in https://github.com/openshift/hypershift/pull/3795/files. It should also contain cg.globalConfig.
			// This is kept like this for now to contain the scope of the refactor and avoid backward compatibility issues.
			name:                      "A different globalConfig should NOT change the hash",
			mcoRawConfig:              baseCaseMCORawConfig,
			releaseVersion:            baseCaseReleaseVersion,
			pullSecretName:            baseCasePullSecretName,
			additionalTrustBundleName: baseCaseAdditionalTrustBundleName,
			globalConfig:              "different",
			expected:                  baseCaseHash,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			releaseImage := &releaseinfo.ReleaseImage{
				ImageStream: &imageapi.ImageStream{
					ObjectMeta: metav1.ObjectMeta{
						Name: tc.releaseVersion,
					},
				},
			}
			cg := &ConfigGenerator{
				rolloutConfig: &rolloutConfig{
					mcoRawConfig:              tc.mcoRawConfig,
					pullSecretName:            tc.pullSecretName,
					additionalTrustBundleName: tc.additionalTrustBundleName,
					globalConfig:              tc.globalConfig,
					releaseImage:              releaseImage,
				},
			}

			hash := cg.HashWithoutVersion()
			g.Expect(hash).ToNot(BeEmpty())
			g.Expect(hash).To(Equal(tc.expected))
		})
	}
}

func TestGenerateMCORawConfig(t *testing.T) {
	coreMachineConfig1 := `
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: config-1
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
        source: "[Service]\nType=oneshot\nExecStart=/usr/bin/echo Hello Core\n\n[Install]\nWantedBy=multi-user.target"
        filesystem: root
        mode: 493
        path: /usr/local/bin/core.sh
`
	coreMachineConfig1Defaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: config-1
spec:
  baseOSExtensionsContainerImage: ""
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents: null
        filesystem: root
        mode: 493
        path: /usr/local/bin/core.sh
        source: |-
          [Service]
          Type=oneshot
          ExecStart=/usr/bin/echo Hello Core

          [Install]
          WantedBy=multi-user.target
  extensions: null
  fips: false
  kernelArguments: null
  kernelType: ""
  osImageURL: ""
`

	machineConfig1 := `
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: config-1
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
        source: "[Service]\nType=oneshot\nExecStart=/usr/bin/echo Hello World\n\n[Install]\nWantedBy=multi-user.target"
        filesystem: root
        mode: 493
        path: /usr/local/bin/file1.sh
`
	machineConfig1Defaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: config-1
spec:
  baseOSExtensionsContainerImage: ""
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents: null
        filesystem: root
        mode: 493
        path: /usr/local/bin/file1.sh
        source: |-
          [Service]
          Type=oneshot
          ExecStart=/usr/bin/echo Hello World

          [Install]
          WantedBy=multi-user.target
  extensions: null
  fips: false
  kernelArguments: null
  kernelType: ""
  osImageURL: ""
`
	machineConfig23 := `
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: config-2
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
        source: "[Service]\nType=oneshot\nExecStart=/usr/bin/echo Hello World 2\n\n[Install]\nWantedBy=multi-user.target"
        filesystem: root
        mode: 493
        path: /usr/local/bin/file2.sh
--- # empty yamls should be ignored

---
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  labels:
    machineconfiguration.openshift.io/role: master
  name: config-3
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
        source: "[Service]\nType=oneshot\nExecStart=/usr/bin/echo Hello World 3\n\n[Install]\nWantedBy=multi-user.target"
        filesystem: root
        mode: 493
        path: /usr/local/bin/file3.sh
`
	machineConfig23Defaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: config-2
spec:
  baseOSExtensionsContainerImage: ""
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents: null
        filesystem: root
        mode: 493
        path: /usr/local/bin/file2.sh
        source: |-
          [Service]
          Type=oneshot
          ExecStart=/usr/bin/echo Hello World 2

          [Install]
          WantedBy=multi-user.target
  extensions: null
  fips: false
  kernelArguments: null
  kernelType: ""
  osImageURL: ""

---
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: config-3
spec:
  baseOSExtensionsContainerImage: ""
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents: null
        filesystem: root
        mode: 493
        path: /usr/local/bin/file3.sh
        source: |-
          [Service]
          Type=oneshot
          ExecStart=/usr/bin/echo Hello World 3

          [Install]
          WantedBy=multi-user.target
  extensions: null
  fips: false
  kernelArguments: null
  kernelType: ""
  osImageURL: ""
`

	kubeletConfig1 := `
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: set-max-pods
spec:
  kubeletConfig:
    maxPods: 100
`
	kubeletConfig1Defaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  creationTimestamp: null
  name: set-max-pods
spec:
  kubeletConfig:
    maxPods: 100
  machineConfigPoolSelector:
    matchLabels:
      machineconfiguration.openshift.io/mco-built-in: ""
status:
  conditions: null
`
	kubeletConfig2 := `
apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  name: set-max-pods-2
spec:
  kubeletConfig:
    maxPods: 200
`
	kubeletConfig2Defaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: KubeletConfig
metadata:
  creationTimestamp: null
  name: set-max-pods-2
spec:
  kubeletConfig:
    maxPods: 200
  machineConfigPoolSelector:
    matchLabels:
      machineconfiguration.openshift.io/mco-built-in: ""
status:
  conditions: null
`

	haproxyIgnititionConfig := `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: 20-apiserver-haproxy
spec:
  baseOSExtensionsContainerImage: ""
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - contents:
          source: data:text/plain;charset=utf-8;base64,IyEvdXNyL2Jpbi9lbnYgYmFzaApzZXQgLXgKaXAgYWRkciBhZGQgMTcyLjIwLjAuMS8zMiBicmQgMTcyLjIwLjAuMSBzY29wZSBob3N0IGRldiBsbwppcCByb3V0ZSBhZGQgMTcyLjIwLjAuMS8zMiBkZXYgbG8gc2NvcGUgbGluayBzcmMgMTcyLjIwLjAuMQo=
        mode: 493
        overwrite: true
        path: /usr/local/bin/setup-apiserver-ip.sh
      - contents:
          source: data:text/plain;charset=utf-8;base64,IyEvdXNyL2Jpbi9lbnYgYmFzaApzZXQgLXgKaXAgYWRkciBkZWxldGUgMTcyLjIwLjAuMS8zMiBkZXYgbG8KaXAgcm91dGUgZGVsIDE3Mi4yMC4wLjEvMzIgZGV2IGxvIHNjb3BlIGxpbmsgc3JjIDE3Mi4yMC4wLjEK
        mode: 493
        overwrite: true
        path: /usr/local/bin/teardown-apiserver-ip.sh
      - contents:
          source: data:text/plain;charset=utf-8;base64,Z2xvYmFsCiAgbWF4Y29ubiA3MDAwCiAgbG9nIHN0ZG91dCBsb2NhbDAKICBsb2cgc3Rkb3V0IGxvY2FsMSBub3RpY2UKCmRlZmF1bHRzCiAgbW9kZSB0Y3AKICB0aW1lb3V0IGNsaWVudCAxMG0KICB0aW1lb3V0IHNlcnZlciAxMG0KICB0aW1lb3V0IGNvbm5lY3QgMTBzCiAgdGltZW91dCBjbGllbnQtZmluIDVzCiAgdGltZW91dCBzZXJ2ZXItZmluIDVzCiAgdGltZW91dCBxdWV1ZSA1cwogIHJldHJpZXMgMwoKZnJvbnRlbmQgbG9jYWxfYXBpc2VydmVyCiAgYmluZCAxNzIuMjAuMC4xOjY0NDMKICBsb2cgZ2xvYmFsCiAgbW9kZSB0Y3AKICBvcHRpb24gdGNwbG9nCiAgZGVmYXVsdF9iYWNrZW5kIHJlbW90ZV9hcGlzZXJ2ZXIKCmJhY2tlbmQgcmVtb3RlX2FwaXNlcnZlcgogIG1vZGUgdGNwCiAgbG9nIGdsb2JhbAogIG9wdGlvbiBodHRwY2hrIEdFVCAvdmVyc2lvbgogIG9wdGlvbiBsb2ctaGVhbHRoLWNoZWNrcwogIGRlZmF1bHQtc2VydmVyIGludGVyIDEwcyBmYWxsIDMgcmlzZSAzCiAgc2VydmVyIGNvbnRyb2xwbGFuZSBsb2NhbGhvc3Q6NjQ0Mwo=
        mode: 420
        overwrite: true
        path: /etc/kubernetes/apiserver-proxy-config/haproxy.cfg
      - contents:
          source: data:text/plain;charset=utf-8;base64,YXBpVmVyc2lvbjogdjEKa2luZDogUG9kCm1ldGFkYXRhOgogIGNyZWF0aW9uVGltZXN0YW1wOiBudWxsCiAgbGFiZWxzOgogICAgazhzLWFwcDoga3ViZS1hcGlzZXJ2ZXItcHJveHkKICBuYW1lOiBrdWJlLWFwaXNlcnZlci1wcm94eQogIG5hbWVzcGFjZToga3ViZS1zeXN0ZW0Kc3BlYzoKICBjb250YWluZXJzOgogIC0gY29tbWFuZDoKICAgIC0gaGFwcm94eQogICAgLSAtZgogICAgLSAvdXNyL2xvY2FsL2V0Yy9oYXByb3h5CiAgICBsaXZlbmVzc1Byb2JlOgogICAgICBmYWlsdXJlVGhyZXNob2xkOiAzCiAgICAgIGh0dHBHZXQ6CiAgICAgICAgaG9zdDogMTcyLjIwLjAuMQogICAgICAgIHBhdGg6IC92ZXJzaW9uCiAgICAgICAgcG9ydDogNjQ0MwogICAgICAgIHNjaGVtZTogSFRUUFMKICAgICAgaW5pdGlhbERlbGF5U2Vjb25kczogMTIwCiAgICAgIHBlcmlvZFNlY29uZHM6IDEyMAogICAgICBzdWNjZXNzVGhyZXNob2xkOiAxCiAgICBuYW1lOiBoYXByb3h5CiAgICBwb3J0czoKICAgIC0gY29udGFpbmVyUG9ydDogNjQ0MwogICAgICBob3N0UG9ydDogNjQ0MwogICAgICBuYW1lOiBhcGlzZXJ2ZXIKICAgICAgcHJvdG9jb2w6IFRDUAogICAgcmVzb3VyY2VzOgogICAgICByZXF1ZXN0czoKICAgICAgICBjcHU6IDEzbQogICAgICAgIG1lbW9yeTogMTZNaQogICAgc2VjdXJpdHlDb250ZXh0OgogICAgICBydW5Bc1VzZXI6IDEwMDEKICAgIHZvbHVtZU1vdW50czoKICAgIC0gbW91bnRQYXRoOiAvdXNyL2xvY2FsL2V0Yy9oYXByb3h5CiAgICAgIG5hbWU6IGNvbmZpZwogIGhvc3ROZXR3b3JrOiB0cnVlCiAgcHJpb3JpdHlDbGFzc05hbWU6IHN5c3RlbS1ub2RlLWNyaXRpY2FsCiAgdm9sdW1lczoKICAtIGhvc3RQYXRoOgogICAgICBwYXRoOiAvZXRjL2t1YmVybmV0ZXMvYXBpc2VydmVyLXByb3h5LWNvbmZpZwogICAgbmFtZTogY29uZmlnCnN0YXR1czoge30K
        mode: 420
        overwrite: true
        path: /etc/kubernetes/manifests/kube-apiserver-proxy.yaml
    systemd:
      units:
      - contents: |
          [Unit]
          Description=Sets up local IP to proxy API server requests
          Wants=network-online.target
          After=network-online.target

          [Service]
          Type=oneshot
          ExecStart=/usr/local/bin/setup-apiserver-ip.sh
          ExecStop=/usr/local/bin/teardown-apiserver-ip.sh
          RemainAfterExit=yes

          [Install]
          WantedBy=multi-user.target
        enabled: true
        name: apiserver-ip.service
  extensions: null
  fips: false
  kernelArguments: null
  kernelType: ""
  osImageURL: ""
`
	containerRuntimeConfig1 := `apiVersion: machineconfiguration.openshift.io/v1
kind: ContainerRuntimeConfig
metadata:
  name: set-pids-limit
spec:
  containerRuntimeConfig:
    logSizeMax: "0"
    overlaySize: "0"
    pidsLimit: 2048
`

	containerRuntimeConfig1Defaulted := `apiVersion: machineconfiguration.openshift.io/v1
kind: ContainerRuntimeConfig
metadata:
  creationTimestamp: null
  name: set-pids-limit
spec:
  containerRuntimeConfig:
    logSizeMax: "0"
    overlaySize: "0"
    pidsLimit: 2048
  machineConfigPoolSelector:
    matchLabels:
      machineconfiguration.openshift.io/mco-built-in: ""
status:
  conditions: null
`

	namespace := "test"
	testCases := []struct {
		name              string
		nodePool          *hyperv1.NodePool
		config            []crclient.Object
		coreConfig        []crclient.Object
		expect            string
		renderHAProxy     bool
		missingCoreConfig bool
		error             bool
	}{
		{
			name: "gets a single valid MachineConfig",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "machineconfig-1",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			config: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machineconfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig1,
					},
					BinaryData: nil,
				},
			},
			expect: machineConfig1Defaulted,
			error:  false,
		},
		{
			name: "gets three valid MachineConfig, two of them in a single config-map",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "machineconfig-1",
						},
						{
							Name: "machineconfig-2",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			config: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machineconfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig1,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machineconfig-2",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig23,
					},
				},
			},
			expect: machineConfig1Defaulted + "\n---\n" + machineConfig23Defaulted,
			error:  false,
		},
		{
			name: "fails if a non existent config is referenced",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "does-not-exist",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			config: []crclient.Object{},
			expect: "",
			error:  true,
		},
		{
			name: "gets a single valid ContainerRuntimeConfig",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "containerRuntimeConfig-1",
						},
					},
				},
			},
			config: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "containerRuntimeConfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: containerRuntimeConfig1,
					},
				},
			},
			expect: containerRuntimeConfig1Defaulted,
			error:  false,
		},
		{
			name: "gets a single valid MachineConfig with a core MachineConfig",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "machineconfig-1",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			config: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machineconfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig1,
					},
					BinaryData: nil,
				},
			},
			coreConfig: []crclient.Object{
				&corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-ignition-config-1",
						Namespace: "test-test",
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
					Data: map[string]string{
						TokenSecretConfigKey: coreMachineConfig1,
					},
				},
				&corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-ignition-config-2",
						Namespace: "test-test",
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
				},
				&corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-ignition-config-3",
						Namespace: "test-test",
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
				},
			},
			expect: coreMachineConfig1Defaulted + "\n---\n" + machineConfig1Defaulted,
			error:  false,
		},
		{
			name: "gets a single valid MachineConfig with a core MachineConfig and ignores independent namespace",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "machineconfig-1",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			config: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machineconfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig1,
					},
					BinaryData: nil,
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-machineconfig",
						Namespace: "separatenamespace",
					},
					Data: map[string]string{
						TokenSecretConfigKey: coreMachineConfig1,
					},
				},
			},
			coreConfig: []crclient.Object{
				&corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-ignition-config-1",
						Namespace: "test-test",
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
					Data: map[string]string{
						TokenSecretConfigKey: coreMachineConfig1,
					},
				},
				&corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-ignition-config-2",
						Namespace: "test-test",
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
				},
				&corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-ignition-config-3",
						Namespace: "test-test",
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
				},
			},
			expect: coreMachineConfig1Defaulted + "\n---\n" + machineConfig1Defaulted,
			error:  false,
		},
		{
			name: "No configs, missingConfigs error is returned",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
			},
			missingCoreConfig: true,
			error:             true,
		},
		{
			name: "Nodepool controller generates HAProxy config",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "machineconfig-1",
						},
					},
				},
			},
			config: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "machineconfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: machineConfig1,
					},
					BinaryData: nil,
				},
			},
			// Have only 2 core configs as setting rolloutConfig.haproxyRawConfig will decrease the requirement of number of core configs.
			renderHAProxy: true,
			coreConfig: []crclient.Object{
				&corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-ignition-config-1",
						Namespace: "test-test",
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
				},
				&corev1.ConfigMap{
					TypeMeta: metav1.TypeMeta{},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "core-ignition-config-2",
						Namespace: "test-test",
						Labels: map[string]string{
							nodePoolCoreIgnitionConfigLabel: "true",
						},
					},
				},
			},
			expect: haproxyIgnititionConfig + "\n---\n" + machineConfig1Defaulted, // + "\n---\n" + machineConfig1Defaulted,
		},
		{
			name: "gets a single valid KubeletConfig",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "kubeletconfig-1",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			config: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubeletconfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: kubeletConfig1,
					},
					BinaryData: nil,
				},
			},
			expect: kubeletConfig1Defaulted,
			error:  false,
		},
		{
			name: "gets two valid KubeletConfig",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "kubeletconfig-1",
						},
						{
							Name: "kubeletconfig-2",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			config: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubeletconfig-1",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: kubeletConfig1,
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "kubeletconfig-2",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: kubeletConfig2,
					},
				},
			},
			expect: kubeletConfig1Defaulted + "\n---\n" + kubeletConfig2Defaulted,
			error:  false,
		},
		{
			name: "It should fail if spec.Configs has unsupported content",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
				},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{
							Name: "unsupported",
						},
					},
				},
				Status: hyperv1.NodePoolStatus{},
			},
			config: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "unsupported",
						Namespace: namespace,
					},
					Data: map[string]string{
						TokenSecretConfigKey: "unsupported",
					},
				},
			},
			error: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			tc.config = append(tc.config, &corev1.Secret{
				Data: map[string][]byte{".dockerconfigjson": nil},
			})

			hc := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test",
					Namespace:   "test",
					Annotations: map[string]string{"hypershift.openshift.io/control-plane-operator-image": "cpo-image"},
				},
				Status: hyperv1.HostedClusterStatus{KubeConfig: &corev1.LocalObjectReference{Name: "kubeconfig"}},
				Spec: hyperv1.HostedClusterSpec{
					Networking: hyperv1.ClusterNetworking{
						ServiceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("192.168.1.0/24")}},
					},
				},
			}

			fakeObjects := append(tc.config, coreConfigMaps...)
			if tc.coreConfig != nil {
				fakeObjects = append(tc.config, tc.coreConfig...)
			}
			if tc.missingCoreConfig {
				fakeObjects = tc.config
			}

			cg := ConfigGenerator{
				Client:                fake.NewClientBuilder().WithObjects(fakeObjects...).Build(),
				hostedCluster:         hc,
				nodePool:              tc.nodePool,
				controlplaneNamespace: "test-test",
				rolloutConfig: &rolloutConfig{
					haproxyRawConfig: "",
				},
			}
			if tc.renderHAProxy {
				cg.haproxyRawConfig = haproxyIgnititionConfig
			}

			got, err := cg.generateMCORawConfig(context.Background())
			if tc.error {
				g.Expect(err).To(HaveOccurred())
				if tc.missingCoreConfig {
					var missingCoreConfigError *MissingCoreConfigError
					g.Expect(errors.As(err, &missingCoreConfigError)).To(BeTrue())
				}
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			if diff := cmp.Diff(got, tc.expect); diff != "" {
				t.Errorf("actual config differs from expected: %s", diff)
			}
		})
	}
}

func TestMissingCoreConfigError(t *testing.T) {
	g := NewWithT(t)
	err := &MissingCoreConfigError{
		Got:      3,
		Expected: 1,
	}

	g.Expect(err.Error()).To(Equal("expected 1 core ignition configs, found 3"))

}

func TestGetCoreConfigs(t *testing.T) {
	namespace := "test"
	testCases := []struct {
		name                   string
		hostedCluster          *hyperv1.HostedCluster
		haproxyRawConfig       string
		existingConfigMaps     []crclient.Object
		expectedConfigMapCount int
		expectedError          error
	}{
		{
			name:          "When there are 3 core configs it should return them",
			hostedCluster: &hyperv1.HostedCluster{},
			existingConfigMaps: []crclient.Object{
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config1", Namespace: namespace, Labels: map[string]string{nodePoolCoreIgnitionConfigLabel: "true"}}},
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config2", Namespace: namespace, Labels: map[string]string{nodePoolCoreIgnitionConfigLabel: "true"}}},
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config3", Namespace: namespace, Labels: map[string]string{nodePoolCoreIgnitionConfigLabel: "true"}}},
			},
			expectedConfigMapCount: 3,
			expectedError:          nil,
		},
		{
			name: "When there are ImageContentSources in HC it should return 4 core configs",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					ImageContentSources: []hyperv1.ImageContentSource{{Source: "test"}},
				},
			},
			existingConfigMaps: []crclient.Object{
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config1", Namespace: namespace, Labels: map[string]string{nodePoolCoreIgnitionConfigLabel: "true"}}},
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config2", Namespace: namespace, Labels: map[string]string{nodePoolCoreIgnitionConfigLabel: "true"}}},
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config3", Namespace: namespace, Labels: map[string]string{nodePoolCoreIgnitionConfigLabel: "true"}}},
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config4", Namespace: namespace, Labels: map[string]string{nodePoolCoreIgnitionConfigLabel: "true"}}},
			},
			expectedConfigMapCount: 4,
			expectedError:          nil,
		},
		{
			name:             "When there is haproxy content set it should return only 2 core configs",
			hostedCluster:    &hyperv1.HostedCluster{},
			haproxyRawConfig: "some-config",
			existingConfigMaps: []crclient.Object{
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config1", Namespace: namespace, Labels: map[string]string{nodePoolCoreIgnitionConfigLabel: "true"}}},
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config2", Namespace: namespace, Labels: map[string]string{nodePoolCoreIgnitionConfigLabel: "true"}}},
			},
			expectedConfigMapCount: 2,
			expectedError:          nil,
		},
		{
			name:          "When there are not enough core configs it should fail",
			hostedCluster: &hyperv1.HostedCluster{},
			existingConfigMaps: []crclient.Object{
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config1", Namespace: namespace, Labels: map[string]string{nodePoolCoreIgnitionConfigLabel: "true"}}},
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "config2", Namespace: namespace, Labels: map[string]string{nodePoolCoreIgnitionConfigLabel: "true"}}},
			},
			expectedConfigMapCount: 2,
			expectedError:          &MissingCoreConfigError{Got: 2, Expected: 3},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().WithObjects(tc.existingConfigMaps...).Build()
			cg := &ConfigGenerator{
				Client:                fakeClient,
				hostedCluster:         tc.hostedCluster,
				controlplaneNamespace: namespace,
				rolloutConfig: &rolloutConfig{
					haproxyRawConfig: tc.haproxyRawConfig,
				},
			}

			configs, err := cg.getCoreConfigs(context.Background())
			if tc.expectedError != nil {
				g.Expect(err).To(HaveOccurred())
				g.Expect(configs).To(HaveLen(tc.expectedConfigMapCount))
				g.Expect(err).To(MatchError(tc.expectedError))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(configs).To(HaveLen(tc.expectedConfigMapCount))
		})
	}
}

func TestGetUserConfigs(t *testing.T) {
	namespace := "test"
	testCases := []struct {
		name            string
		nodePool        *hyperv1.NodePool
		existingConfigs []crclient.Object
		expectedConfigs []crclient.Object
		expectedError   bool
	}{
		{
			name: "When no configs in NodePool it should return none",
			nodePool: &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{},
				},
			},
			existingConfigs: []crclient.Object{},
			expectedConfigs: nil,
			expectedError:   false,
		},
		{
			name: "When there is a single config in NodePool it should return it",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{Name: "config1"},
					},
				},
			},
			existingConfigs: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "config1", Namespace: namespace},
					Data:       map[string]string{"key": "value"},
				},
			},
			expectedConfigs: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "config1", Namespace: namespace},
					Data:       map[string]string{"key": "value"},
				},
			},
			expectedError: false,
		},
		{
			name: "When there are multiple configs in NodePool it should return them",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{Name: "config1"},
						{Name: "config2"},
					},
				},
			},
			existingConfigs: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "config1", Namespace: namespace},
					Data:       map[string]string{"key1": "value1"},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "config2", Namespace: namespace},
					Data:       map[string]string{"key2": "value2"},
				},
			},
			expectedConfigs: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "config1", Namespace: namespace},
					Data:       map[string]string{"key1": "value1"},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "config2", Namespace: namespace},
					Data:       map[string]string{"key2": "value2"},
				},
			},
			expectedError: false,
		},
		{
			name: "When the config not found it should fail",
			nodePool: &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
				Spec: hyperv1.NodePoolSpec{
					Config: []corev1.LocalObjectReference{
						{Name: "non-existent-config"},
					},
				},
			},
			existingConfigs: []crclient.Object{},
			expectedConfigs: []crclient.Object{},
			expectedError:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			fakeClient := fake.NewClientBuilder().WithObjects(tc.existingConfigs...).Build()
			cg := &ConfigGenerator{
				Client:   fakeClient,
				nodePool: tc.nodePool,
			}

			configs, err := cg.getUserConfigs(context.Background())
			if tc.expectedError {
				g.Expect(err).To(HaveOccurred(), "Expected an error, but got none")
				return
			}
			g.Expect(err).ToNot(HaveOccurred(), "Unexpected error")
			g.Expect(len(configs)).To(Equal(len(tc.expectedConfigs)))
		})
	}
}

func TestDefaultAndValidateConfigManifest(t *testing.T) {
	testCases := []struct {
		name           string
		input          []byte
		expectedOutput []byte
		error          error
	}{
		{
			name: "Valid MachineConfig",
			input: []byte(`
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: test-config
`),
			expectedOutput: []byte(`
apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  creationTimestamp: null
  labels:
    machineconfiguration.openshift.io/role: worker
  name: test-config
spec:
  baseOSExtensionsContainerImage: ""
  config: null
  extensions: null
  fips: false
  kernelArguments: null
  kernelType: ""
  osImageURL: ""
`),
			error: nil,
		},
		{
			name: "When the manifest is not valid it should fail to decode",
			input: []byte(`
invalid: yaml
  - content
`),
			expectedOutput: nil,
			error:          fmt.Errorf("error decoding config: Object 'Kind' is missing in '\ninvalid: yaml\n  - content\n'"),
		},
		{
			name: "When the API is not supported config it should fail with unsupported type",
			input: []byte(`
apiVersion: hypershift.openshift.io/v1beta1
kind: HostedCluster
metadata:
  name: example-cluster
spec:
  release:
    image: quay.io/openshift-release-dev/ocp-release:4.12.0-rc.3-x86_64
`),
			expectedOutput: nil,
			error:          fmt.Errorf("error decoding config: no kind \"HostedCluster\" is registered for version \"hypershift.openshift.io/v1beta1\" in scheme"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cg := &ConfigGenerator{}
			output, err := cg.defaultAndValidateConfigManifest(tc.input)
			if tc.error != nil {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.error.Error()))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(output).To(MatchYAML(tc.expectedOutput))

		})
	}
}

func TestGlobalConfigString(t *testing.T) {
	expectedGlobalConfigStringWhenEmpty := `{"metadata":{"name":"cluster","creationTimestamp":null},"spec":{"trustedCA":{"name":""}},"status":{}}
{"metadata":{"name":"cluster","creationTimestamp":null},"spec":{"additionalTrustedCA":{"name":""},"registrySources":{}},"status":{}}
`
	expectedGlobalConfigStringWithValues := `{"metadata":{"name":"cluster","creationTimestamp":null},"spec":{"httpProxy":"proxy","noProxy":"noProxy","trustedCA":{"name":""}},"status":{"httpProxy":"proxy","noProxy":".cluster.local,.local,.svc,127.0.0.1,localhost,noProxy"}}
{"metadata":{"name":"cluster","creationTimestamp":null},"spec":{"externalRegistryHostnames":["external registry"],"additionalTrustedCA":{"name":""},"registrySources":{}},"status":{}}
`

	testCases := []struct {
		name           string
		globalConfig   *hyperv1.ClusterConfiguration
		expectedOutput string
	}{
		// Expected behaviour for backward compatibility for empty values is:
		// return serialized string with empty values for AdditionalTrustedCA and RegistrySources and drop everything else even if it doesn't have a omitempty tag in the API.
		{
			name:           "When Empty GlobalConfig it should return serialized string honouring backward compatibility expectation (see code comment)",
			globalConfig:   &hyperv1.ClusterConfiguration{},
			expectedOutput: expectedGlobalConfigStringWhenEmpty,
		},
		{
			name: "When GlobalConfig is set with empty structs it should return serialized string honouring backward compatibility expectation (see code comment)",
			globalConfig: &hyperv1.ClusterConfiguration{
				APIServer:      &configv1.APIServerSpec{},
				Authentication: &configv1.AuthenticationSpec{},
				FeatureGate:    &configv1.FeatureGateSpec{},
				Image:          &configv1.ImageSpec{},
				Proxy:          &configv1.ProxySpec{},
			},
			expectedOutput: expectedGlobalConfigStringWhenEmpty,
		},
		{
			name: "When GlobalConfig is set with some values GlobalConfig it should keep them and it should honour backward compatibility expectation (see code comment)",
			globalConfig: &hyperv1.ClusterConfiguration{
				APIServer:      &configv1.APIServerSpec{},
				Authentication: &configv1.AuthenticationSpec{},
				FeatureGate:    &configv1.FeatureGateSpec{},
				Image: &configv1.ImageSpec{
					AllowedRegistriesForImport: []configv1.RegistryLocation{},
					ExternalRegistryHostnames:  []string{"external registry"},
					AdditionalTrustedCA:        configv1.ConfigMapNameReference{},
					RegistrySources:            configv1.RegistrySources{},
				},
				Proxy: &configv1.ProxySpec{
					HTTPProxy:          "proxy",
					HTTPSProxy:         "",
					NoProxy:            "noProxy",
					ReadinessEndpoints: []string{},
					TrustedCA:          configv1.ConfigMapNameReference{},
				},
			},
			expectedOutput: expectedGlobalConfigStringWithValues,
		},
	}

	for _, tc := range testCases {
		hcluster := &hyperv1.HostedCluster{
			Spec: hyperv1.HostedClusterSpec{
				Configuration: tc.globalConfig,
			},
		}

		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			output, err := globalConfigString(hcluster)
			g.Expect(err).ToNot(HaveOccurred())
			if diff := cmp.Diff(output, tc.expectedOutput); diff != "" {
				t.Errorf("actual config differs from expected: %s", diff)
			}
		})
	}
}
