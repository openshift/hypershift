package nodepool

import (
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/releaseinfo"

	imagev1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ignitionapi "github.com/coreos/ignition/v2/config/v3_2/types"
	"github.com/vincent-petithory/dataurl"
)

const userCRIODropInMachineConfig = `apiVersion: machineconfiguration.openshift.io/v1
kind: MachineConfig
metadata:
  name: 99-worker-crio-nofile
  labels:
    machineconfiguration.openshift.io/role: worker
spec:
  config:
    ignition:
      version: 3.2.0
    storage:
      files:
      - path: /etc/crio/crio.conf.d/01-ctrcfg-defaultUlimits
        mode: 0644
        overwrite: true
        contents:
          source: data:text/plain;charset=utf-8;base64,W2NyaW8ucnVudGltZV0KZGVmYXVsdF91bGltaXRzID0gWyJub2ZpbGU9NjU1MzYiXQo=
`

func TestDefaultUlimitsMigrationRequired(t *testing.T) {
	testCases := []struct {
		name           string
		currentVersion string
		targetVersion  string
		expected       bool
	}{
		{
			name:           "When upgrading from 4.20 to 4.21, it should require the migration",
			currentVersion: "4.20.5",
			targetVersion:  "4.21.0",
			expected:       true,
		},
		{
			name:           "When upgrading from 4.20 to a 4.21 nightly, it should require the migration",
			currentVersion: "4.20.0",
			targetVersion:  "4.21.0-0.nightly-2026-01-01-000000",
			expected:       true,
		},
		{
			name:           "When skipping minors from 4.19 to 4.22, it should require the migration",
			currentVersion: "4.19.3",
			targetVersion:  "4.22.0",
			expected:       true,
		},
		{
			name:           "When upgrading within 4.20, it should not require the migration",
			currentVersion: "4.20.0",
			targetVersion:  "4.20.1",
			expected:       false,
		},
		{
			name:           "When already on 4.21 and upgrading to 4.22, it should not require the migration",
			currentVersion: "4.21.0",
			targetVersion:  "4.22.0",
			expected:       false,
		},
		{
			name:           "When the current version is empty, it should not require the migration",
			currentVersion: "",
			targetVersion:  "4.21.0",
			expected:       false,
		},
		{
			name:           "When the current version is invalid, it should not require the migration",
			currentVersion: "not-a-version",
			targetVersion:  "4.21.0",
			expected:       false,
		},
		{
			name:           "When the target version is invalid, it should not require the migration",
			currentVersion: "4.20.0",
			targetVersion:  "not-a-version",
			expected:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(defaultUlimitsMigrationRequired(tc.currentVersion, tc.targetVersion)).To(Equal(tc.expected))
		})
	}
}

func TestShouldPreserveCRIODefaultUlimits(t *testing.T) {
	testCases := []struct {
		name           string
		currentVersion string
		targetVersion  string
		annotations    map[string]string
		expected       bool
	}{
		{
			name:           "When crossing 4.21, it should preserve",
			currentVersion: "4.20.0",
			targetVersion:  "4.21.0",
			expected:       true,
		},
		{
			name:           "When the marker is set and the target is still 4.21+, it should preserve",
			currentVersion: "4.21.0",
			targetVersion:  "4.22.0",
			annotations: map[string]string{
				CRIODefaultUlimitsPreserveAnnotation: "true",
			},
			expected: true,
		},
		{
			name:           "When the marker is set but the target rolls back below 4.21, it should not preserve",
			currentVersion: "4.21.0",
			targetVersion:  "4.20.0",
			annotations: map[string]string{
				CRIODefaultUlimitsPreserveAnnotation: "true",
			},
			expected: false,
		},
		{
			name:           "When the target version is invalid, it should not preserve",
			currentVersion: "4.20.0",
			targetVersion:  "not-a-version",
			annotations: map[string]string{
				CRIODefaultUlimitsPreserveAnnotation: "true",
			},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Annotations: tc.annotations},
				Status:     hyperv1.NodePoolStatus{Version: tc.currentVersion},
			}
			g.Expect(shouldPreserveCRIODefaultUlimits(nodePool, tc.targetVersion)).To(Equal(tc.expected))
		})
	}
}

func TestMarkCRIODefaultUlimitsPreserved(t *testing.T) {
	testCases := []struct {
		name              string
		currentVersion    string
		annotations       map[string]string
		expectedAnnotated bool
	}{
		{
			name:              "When crossing 4.21, it should set the marker",
			currentVersion:    "4.20.0",
			expectedAnnotated: true,
		},
		{
			name:              "When not crossing 4.21, it should not set the marker",
			currentVersion:    "4.21.0",
			expectedAnnotated: false,
		},
		{
			name:           "When the marker is already set, it should keep it",
			currentVersion: "4.21.0",
			annotations: map[string]string{
				CRIODefaultUlimitsPreserveAnnotation: "true",
			},
			expectedAnnotated: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{Annotations: tc.annotations},
				Status:     hyperv1.NodePoolStatus{Version: tc.currentVersion},
			}

			MarkCRIODefaultUlimitsPreserved(nodePool, "4.21.0")

			if tc.expectedAnnotated {
				g.Expect(nodePool.Annotations).To(HaveKeyWithValue(
					CRIODefaultUlimitsPreserveAnnotation,
					"true",
				))
			} else {
				g.Expect(nodePool.Annotations).ToNot(HaveKey(CRIODefaultUlimitsPreserveAnnotation))
			}
		})
	}
}

func TestCrioDefaultUlimitsMachineConfig(t *testing.T) {
	g := NewWithT(t)

	machineConfig, err := crioDefaultUlimitsMachineConfig()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(machineConfig.Name).To(Equal(crioDefaultUlimitsMachineConfigName))
	g.Expect(machineConfig.Labels).To(HaveKeyWithValue("machineconfiguration.openshift.io/role", "worker"))

	var ignitionConfig ignitionapi.Config
	g.Expect(json.Unmarshal(machineConfig.Spec.Config.Raw, &ignitionConfig)).To(Succeed())
	g.Expect(ignitionConfig.Storage.Files).To(HaveLen(1))

	file := ignitionConfig.Storage.Files[0]
	g.Expect(file.Path).To(Equal(crioDefaultUlimitsDropInPath))
	g.Expect(file.Mode).ToNot(BeNil())
	g.Expect(*file.Mode).To(Equal(int(0o644)))
	g.Expect(file.Overwrite).ToNot(BeNil())
	g.Expect(*file.Overwrite).To(BeTrue())
	g.Expect(file.Contents.Source).ToNot(BeNil())

	decoded, err := dataurl.DecodeString(*file.Contents.Source)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(string(decoded.Data)).To(Equal(crioDefaultUlimitsDropInContents))
}

func testGenerateMCORawConfigCRIODefaultUlimits(t *testing.T) {
	testCases := []struct {
		name           string
		currentVersion string
		targetVersion  string
		annotations    map[string]string
		userConfig     bool
		expected       bool
	}{
		{
			name:           "When crossing 4.21, it should include the drop-in",
			currentVersion: "4.20.0",
			targetVersion:  "4.21.0",
			expected:       true,
		},
		{
			name:           "When the marker is set, it should keep the drop-in on later upgrades",
			currentVersion: "4.21.0",
			targetVersion:  "4.22.0",
			annotations: map[string]string{
				CRIODefaultUlimitsPreserveAnnotation: "true",
			},
			expected: true,
		},
		{
			name:           "When the NodePool starts on 4.21, it should not include the drop-in",
			currentVersion: "",
			targetVersion:  "4.21.0",
			expected:       false,
		},
		{
			name:           "When staying on 4.21, it should not include the drop-in",
			currentVersion: "4.21.0",
			targetVersion:  "4.21.0",
			expected:       false,
		},
		{
			name:           "When the target rolls back below 4.21, it should not include the drop-in",
			currentVersion: "4.21.0",
			targetVersion:  "4.20.0",
			annotations: map[string]string{
				CRIODefaultUlimitsPreserveAnnotation: "true",
			},
			expected: false,
		},
		{
			name:           "When user config writes the drop-in path, it should not include the generated drop-in",
			currentVersion: "4.20.0",
			targetVersion:  "4.21.0",
			userConfig:     true,
			expected:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			nodePool := &hyperv1.NodePool{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "nodepool-name",
					Namespace:   "test",
					Annotations: tc.annotations,
				},
				Status: hyperv1.NodePoolStatus{Version: tc.currentVersion},
			}
			releaseImage := &releaseinfo.ReleaseImage{
				ImageStream: &imagev1.ImageStream{
					ObjectMeta: metav1.ObjectMeta{Name: tc.targetVersion},
				},
			}

			fakeObjects := append([]crclient.Object{}, coreConfigMaps...)
			if tc.userConfig {
				nodePool.Spec.Config = []corev1.LocalObjectReference{{Name: "user-crio"}}
				fakeObjects = append(fakeObjects, &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Name: "user-crio", Namespace: "test"},
					Data: map[string]string{
						TokenSecretConfigKey: userCRIODropInMachineConfig,
					},
				})
			}

			cg := ConfigGenerator{
				Client:                fake.NewClientBuilder().WithObjects(fakeObjects...).Build(),
				hostedCluster:         &hyperv1.HostedCluster{},
				nodePool:              nodePool,
				controlplaneNamespace: "test-test",
				rolloutConfig: &rolloutConfig{
					releaseImage: releaseImage,
				},
			}

			fullConfig, rolloutConfig, err := cg.generateMCORawConfig(t.Context(), nil)
			g.Expect(err).ToNot(HaveOccurred())
			for _, got := range []string{fullConfig, rolloutConfig} {
				if tc.expected {
					g.Expect(got).To(ContainSubstring(crioDefaultUlimitsMachineConfigName))
					g.Expect(got).To(ContainSubstring(crioDefaultUlimitsDropInPath))
				} else {
					g.Expect(got).ToNot(ContainSubstring(crioDefaultUlimitsMachineConfigName))
				}
			}
		})
	}
}
