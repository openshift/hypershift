package globalconfig

import (
	"context"
	"slices"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/releaseinfo"
	hyperutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetAllImageRegistryMirrors(t *testing.T) {
	ctx := t.Context()
	g := NewGomegaWithT(t)
	testsCases := []struct {
		name              string
		icsp              *operatorv1alpha1.ImageContentSourcePolicyList
		idms              *configv1.ImageDigestMirrorSetList
		itms              *configv1.ImageTagMirrorSetList
		expectedResult    map[string][]string
		hasICSPCapability bool
		hasIDMSCapability bool
		hasITMSCapability bool
	}{
		{
			name: "validate ImageRegistryMirrors with only ICSP",
			icsp: createFakeICSP(),
			expectedResult: map[string][]string{
				"registry1": {"icsp-registry-mirrors-2/mirror1", "icsp-registry-mirrors-2/mirror2", "icsp-registry-mirrors-1/mirror1", "icsp-registry-mirrors-1/mirror2"},
				"registry2": {"mirror1", "mirror2"},
				"registry3.sample.com/samplens/sampleimage@sha256:123456": {
					"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
					"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
				},
			},
			hasICSPCapability: true,
			hasIDMSCapability: false,
			hasITMSCapability: false,
		},
		{
			name: "validate ImageRegistryMirrors with only IDMS",
			idms: createFakeIDMS(),
			expectedResult: map[string][]string{
				"registry1.sample.com/samplens/sampleimage@sha256:123456": {"mirror1.sample.com/samplens/sampleimage@sha256:123456", "mirror1.sample.com/samplens/sampleimage@sha256:123456"},
				"registry2.sample.com/samplens/sampleimage@sha256:123456": {"mirror2.sample.com/samplens/sampleimage@sha256:123456", "mirror2.sample.com/samplens/sampleimage@sha256:123456"},
				"registry3.sample.com/samplens/sampleimage@sha256:123456": {"mirror3.sample.com/samplens/sampleimage@sha256:123456", "mirror3.sample.com/samplens/sampleimage@sha256:123456"},
			},
			hasICSPCapability: false,
			hasIDMSCapability: true,
			hasITMSCapability: false,
		},
		{
			name: "validate ImageRegistryMirrors with only ITMS",
			itms: createFakeITMS(),
			expectedResult: map[string][]string{
				"registry1.sample.com/samplens/sampleimage:latest": {"mirror1.sample.com/samplens/sampleimage:latest", "mirror1.sample.com/samplens/sampleimage:latest"},
				"registry2.sample.com/samplens/sampleimage:v1.0":   {"mirror2.sample.com/samplens/sampleimage:v1.0", "mirror2.sample.com/samplens/sampleimage:v1.0"},
				"registry4.sample.com/samplens/sampleimage:stable": {"mirror4.sample.com/samplens/sampleimage:stable", "mirror4.sample.com/samplens/sampleimage:stable"},
			},
			hasICSPCapability: false,
			hasIDMSCapability: false,
			hasITMSCapability: true,
		},
		{
			name: "validate ImageRegistryMirrors with ICSP and IDMS",
			idms: createFakeIDMS(),
			icsp: createFakeICSP(),
			expectedResult: map[string][]string{
				"registry1.sample.com/samplens/sampleimage@sha256:123456": {"mirror1.sample.com/samplens/sampleimage@sha256:123456", "mirror1.sample.com/samplens/sampleimage@sha256:123456"},
				"registry2.sample.com/samplens/sampleimage@sha256:123456": {"mirror2.sample.com/samplens/sampleimage@sha256:123456", "mirror2.sample.com/samplens/sampleimage@sha256:123456"},
				"registry1": {"icsp-registry-mirrors-2/mirror1", "icsp-registry-mirrors-2/mirror2", "icsp-registry-mirrors-1/mirror1", "icsp-registry-mirrors-1/mirror2"},
				"registry2": {"mirror1", "mirror2"},
				"registry3.sample.com/samplens/sampleimage@sha256:123456": {
					"mirror3.sample.com/samplens/sampleimage@sha256:123456",
					"mirror3.sample.com/samplens/sampleimage@sha256:123456",
					"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
					"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
				},
			},
			hasICSPCapability: true,
			hasIDMSCapability: true,
			hasITMSCapability: false,
		},
		{
			name: "validate ImageRegistryMirrors with IDMS, ITMS and ICSP",
			idms: createFakeIDMS(),
			itms: createFakeITMS(),
			icsp: createFakeICSP(),
			expectedResult: map[string][]string{
				"registry1.sample.com/samplens/sampleimage@sha256:123456": {"mirror1.sample.com/samplens/sampleimage@sha256:123456", "mirror1.sample.com/samplens/sampleimage@sha256:123456"},
				"registry2.sample.com/samplens/sampleimage@sha256:123456": {"mirror2.sample.com/samplens/sampleimage@sha256:123456", "mirror2.sample.com/samplens/sampleimage@sha256:123456"},
				"registry1.sample.com/samplens/sampleimage:latest":        {"mirror1.sample.com/samplens/sampleimage:latest", "mirror1.sample.com/samplens/sampleimage:latest"},
				"registry2.sample.com/samplens/sampleimage:v1.0":          {"mirror2.sample.com/samplens/sampleimage:v1.0", "mirror2.sample.com/samplens/sampleimage:v1.0"},
				"registry4.sample.com/samplens/sampleimage:stable":        {"mirror4.sample.com/samplens/sampleimage:stable", "mirror4.sample.com/samplens/sampleimage:stable"},
				"registry1": {"icsp-registry-mirrors-2/mirror1", "icsp-registry-mirrors-2/mirror2", "icsp-registry-mirrors-1/mirror1", "icsp-registry-mirrors-1/mirror2"},
				"registry2": {"mirror1", "mirror2"},
				"registry3.sample.com/samplens/sampleimage@sha256:123456": {
					"mirror3.sample.com/samplens/sampleimage@sha256:123456",
					"mirror3.sample.com/samplens/sampleimage@sha256:123456",
					"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
					"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
				},
			},
			hasICSPCapability: true,
			hasIDMSCapability: true,
			hasITMSCapability: true,
		},
		{
			name:              "validate empty ImageRegistryMirrors",
			idms:              nil,
			icsp:              nil,
			itms:              nil,
			expectedResult:    map[string][]string{},
			hasICSPCapability: true,
			hasIDMSCapability: true,
			hasITMSCapability: true,
		},
	}

	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			var objs []client.Object

			testScheme := runtime.NewScheme()
			_ = operatorv1alpha1.AddToScheme(testScheme)
			_ = configv1.AddToScheme(testScheme)

			if tc.idms != nil {
				idmsObjs := make([]client.Object, len(tc.idms.Items))
				for i := range tc.idms.Items {
					idmsObjs[i] = &tc.idms.Items[i]
				}
				objs = append(objs, idmsObjs...)
			}

			if tc.icsp != nil {
				icspObjs := make([]client.Object, len(tc.icsp.Items))
				for i := range tc.icsp.Items {
					icspObjs[i] = &tc.icsp.Items[i]
				}
				objs = append(objs, icspObjs...)
			}

			if tc.itms != nil {
				itmsObjs := make([]client.Object, len(tc.itms.Items))
				for i := range tc.itms.Items {
					itmsObjs[i] = &tc.itms.Items[i]
				}
				objs = append(objs, itmsObjs...)
			}

			client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(objs...).Build()

			result, err := GetAllImageRegistryMirrors(ctx, client, tc.hasIDMSCapability, tc.hasITMSCapability, tc.hasICSPCapability)
			g.Expect(err).To(BeNil())
			g.Expect(result).To(Equal(tc.expectedResult))
		})

	}
}

func createFakeICSP() *operatorv1alpha1.ImageContentSourcePolicyList {
	return &operatorv1alpha1.ImageContentSourcePolicyList{
		Items: []operatorv1alpha1.ImageContentSourcePolicy{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "icsp-registry-mirrors-1",
				},
				Spec: operatorv1alpha1.ImageContentSourcePolicySpec{
					RepositoryDigestMirrors: []operatorv1alpha1.RepositoryDigestMirrors{
						{
							Source:  "registry1",
							Mirrors: []string{"icsp-registry-mirrors-1/mirror1", "icsp-registry-mirrors-1/mirror2"},
						},
						{
							Source:  "registry2",
							Mirrors: []string{"mirror1", "mirror2"},
						},
						{
							Source: "registry3.sample.com/samplens/sampleimage@sha256:123456",
							Mirrors: []string{
								"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
								"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
							},
						},
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "icsp-registry-mirrors-2",
				},
				Spec: operatorv1alpha1.ImageContentSourcePolicySpec{
					RepositoryDigestMirrors: []operatorv1alpha1.RepositoryDigestMirrors{
						{
							Source:  "registry1",
							Mirrors: []string{"icsp-registry-mirrors-2/mirror1", "icsp-registry-mirrors-2/mirror2"},
						},
					},
				},
			},
		},
	}
}

func createFakeIDMS() *configv1.ImageDigestMirrorSetList {
	return &configv1.ImageDigestMirrorSetList{
		Items: []configv1.ImageDigestMirrorSet{
			{
				Spec: configv1.ImageDigestMirrorSetSpec{
					ImageDigestMirrors: []configv1.ImageDigestMirrors{
						{
							Source: "registry1.sample.com/samplens/sampleimage@sha256:123456",
							Mirrors: []configv1.ImageMirror{
								"mirror1.sample.com/samplens/sampleimage@sha256:123456",
								"mirror1.sample.com/samplens/sampleimage@sha256:123456",
							},
						},
						{
							Source: "registry2.sample.com/samplens/sampleimage@sha256:123456",
							Mirrors: []configv1.ImageMirror{
								"mirror2.sample.com/samplens/sampleimage@sha256:123456",
								"mirror2.sample.com/samplens/sampleimage@sha256:123456",
							},
						},
						{
							Source: "registry3.sample.com/samplens/sampleimage@sha256:123456",
							Mirrors: []configv1.ImageMirror{
								"mirror3.sample.com/samplens/sampleimage@sha256:123456",
								"mirror3.sample.com/samplens/sampleimage@sha256:123456",
							},
						},
					},
				},
			},
		},
	}
}

func createFakeITMS() *configv1.ImageTagMirrorSetList {
	return &configv1.ImageTagMirrorSetList{
		Items: []configv1.ImageTagMirrorSet{
			{
				Spec: configv1.ImageTagMirrorSetSpec{
					ImageTagMirrors: []configv1.ImageTagMirrors{
						{
							Source: "registry1.sample.com/samplens/sampleimage:latest",
							Mirrors: []configv1.ImageMirror{
								"mirror1.sample.com/samplens/sampleimage:latest",
								"mirror1.sample.com/samplens/sampleimage:latest",
							},
						},
						{
							Source: "registry2.sample.com/samplens/sampleimage:v1.0",
							Mirrors: []configv1.ImageMirror{
								"mirror2.sample.com/samplens/sampleimage:v1.0",
								"mirror2.sample.com/samplens/sampleimage:v1.0",
							},
						},
						{
							Source: "registry4.sample.com/samplens/sampleimage:stable",
							Mirrors: []configv1.ImageMirror{
								"mirror4.sample.com/samplens/sampleimage:stable",
								"mirror4.sample.com/samplens/sampleimage:stable",
							},
						},
					},
				},
			},
		},
	}
}

func TestReconcileMgmtImageRegistryOverrides(t *testing.T) {
	ctx := t.Context()
	g := NewGomegaWithT(t)

	// Define test cases
	testCases := []struct {
		name               string
		capChecker         capabilities.CapabiltyChecker
		registryOverrides  map[string]string
		idms               *configv1.ImageDigestMirrorSetList
		icsp               *operatorv1alpha1.ImageContentSourcePolicyList
		expectedRelease    *releaseinfo.ProviderWithOpenShiftImageRegistryOverridesDecorator
		expectedMetadata   *hyperutil.RegistryClientImageMetadataProvider
		expectedError      error
		expectedErrorMatch string
	}{
		{
			name: "Test with ICSP and IDMS capabilities",
			capChecker: &capabilities.MockCapabilityChecker{
				MockHas: func(capability ...capabilities.CapabilityType) bool {
					return slices.Contains(capability, capabilities.CapabilityICSP) || slices.Contains(capability, capabilities.CapabilityIDMS)
				},
			},
			registryOverrides: map[string]string{
				"registry1": "override1",
				"registry2": "override2",
			},
			idms: createFakeIDMS(),
			icsp: createFakeICSP(),
			expectedRelease: &releaseinfo.ProviderWithOpenShiftImageRegistryOverridesDecorator{
				Delegate: &releaseinfo.RegistryMirrorProviderDecorator{
					Delegate: &releaseinfo.CachedProvider{
						Inner: &releaseinfo.RegistryClientProvider{},
						Cache: map[string]*releaseinfo.ReleaseImage{},
					},
					RegistryOverrides: map[string]string{
						"registry1": "override1",
						"registry2": "override2",
					},
				},
				OpenShiftImageRegistryOverrides: map[string][]string{
					"registry1": {"icsp-registry-mirrors-2/mirror1", "icsp-registry-mirrors-2/mirror2", "icsp-registry-mirrors-1/mirror1", "icsp-registry-mirrors-1/mirror2"},
					"registry2": {"mirror1", "mirror2"},
					"registry1.sample.com/samplens/sampleimage@sha256:123456": {
						"mirror1.sample.com/samplens/sampleimage@sha256:123456",
						"mirror1.sample.com/samplens/sampleimage@sha256:123456",
					},
					"registry2.sample.com/samplens/sampleimage@sha256:123456": {
						"mirror2.sample.com/samplens/sampleimage@sha256:123456",
						"mirror2.sample.com/samplens/sampleimage@sha256:123456",
					},
					"registry3.sample.com/samplens/sampleimage@sha256:123456": {
						"mirror3.sample.com/samplens/sampleimage@sha256:123456",
						"mirror3.sample.com/samplens/sampleimage@sha256:123456",
						"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
						"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
					},
				},
			},
			expectedMetadata: &hyperutil.RegistryClientImageMetadataProvider{
				OpenShiftImageRegistryOverrides: map[string][]string{
					"registry1": {"icsp-registry-mirrors-2/mirror1", "icsp-registry-mirrors-2/mirror2", "icsp-registry-mirrors-1/mirror1", "icsp-registry-mirrors-1/mirror2"},
					"registry2": {"mirror1", "mirror2"},
					"registry1.sample.com/samplens/sampleimage@sha256:123456": {
						"mirror1.sample.com/samplens/sampleimage@sha256:123456",
						"mirror1.sample.com/samplens/sampleimage@sha256:123456",
					},
					"registry2.sample.com/samplens/sampleimage@sha256:123456": {
						"mirror2.sample.com/samplens/sampleimage@sha256:123456",
						"mirror2.sample.com/samplens/sampleimage@sha256:123456",
					},
					"registry3.sample.com/samplens/sampleimage@sha256:123456": {
						"mirror3.sample.com/samplens/sampleimage@sha256:123456",
						"mirror3.sample.com/samplens/sampleimage@sha256:123456",
						"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
						"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
					},
				},
			},
			expectedError: nil,
		},
		{
			name: "Test without ICSP and IDMS capabilities",
			capChecker: &capabilities.MockCapabilityChecker{
				MockHas: func(capability ...capabilities.CapabilityType) bool {
					return false
				},
			},
			registryOverrides: map[string]string{
				"registry1": "override1",
				"registry2": "override2",
			},
			expectedRelease: &releaseinfo.ProviderWithOpenShiftImageRegistryOverridesDecorator{
				Delegate: &releaseinfo.RegistryMirrorProviderDecorator{
					Delegate: &releaseinfo.CachedProvider{
						Inner: &releaseinfo.RegistryClientProvider{},
						Cache: map[string]*releaseinfo.ReleaseImage{},
					},
					RegistryOverrides: map[string]string{
						"registry1": "override1",
						"registry2": "override2",
					},
				},
				OpenShiftImageRegistryOverrides: nil,
			},
			expectedMetadata: &hyperutil.RegistryClientImageMetadataProvider{
				OpenShiftImageRegistryOverrides: nil,
			},
			expectedError: nil,
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var objs []client.Object

			if tc.idms != nil {
				idmsObjs := make([]client.Object, len(tc.idms.Items))
				for i, idms := range tc.idms.Items {
					idmsObjs[i] = &idms
				}
				objs = append(objs, idmsObjs...)
			}

			if tc.icsp != nil {
				icspObjs := make([]client.Object, len(tc.icsp.Items))
				for i, icsp := range tc.icsp.Items {
					icspObjs[i] = &icsp
				}
				objs = append(objs, icspObjs...)
			}
			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()

			provider, err := NewCommonRegistryProvider(ctx, tc.capChecker, client, tc.registryOverrides)

			// Check error
			if tc.expectedError != nil {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedErrorMatch))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}

			// Check release provider
			g.Expect(provider.GetReleaseProvider()).To(Equal(tc.expectedRelease))

			// Check image metadata provider
			g.Expect(provider.GetMetadataProvider()).To(Equal(tc.expectedMetadata))
		})
	}
}

func TestReconcileImageTagMirrors(t *testing.T) {
	g := NewGomegaWithT(t)

	testCases := []struct {
		name                    string
		additionalITMS          *configv1.ImageTagMirrorSet
		expectedImageTagMirrors []configv1.ImageTagMirrors
		expectedLabels          map[string]string
	}{
		{
			name: "Test with multiple image content sources",
			additionalITMS: &configv1.ImageTagMirrorSet{
				Spec: configv1.ImageTagMirrorSetSpec{
					ImageTagMirrors: []configv1.ImageTagMirrors{
						{
							Source:  "registry1.example.com/repo1:tag1",
							Mirrors: []configv1.ImageMirror{"mirror1.example.com/repo1:tag1", "mirror2.example.com/repo1:tag1"},
						},
						{
							Source:  "registry2.example.com/repo2:tag2",
							Mirrors: []configv1.ImageMirror{"mirror3.example.com/repo2:tag2"},
						},
					},
				},
			},
			expectedImageTagMirrors: []configv1.ImageTagMirrors{
				{
					Source:  "registry1.example.com/repo1:tag1",
					Mirrors: []configv1.ImageMirror{"mirror1.example.com/repo1:tag1", "mirror2.example.com/repo1:tag1"},
				},
				{
					Source:  "registry2.example.com/repo2:tag2",
					Mirrors: []configv1.ImageMirror{"mirror3.example.com/repo2:tag2"},
				},
			},
			expectedLabels: map[string]string{
				"machineconfiguration.openshift.io/role": "worker",
			},
		},
		{
			name:                    "Test with empty image content sources",
			additionalITMS:          &configv1.ImageTagMirrorSet{},
			expectedImageTagMirrors: []configv1.ImageTagMirrors{},
			expectedLabels: map[string]string{
				"machineconfiguration.openshift.io/role": "worker",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			itms := ImageTagMirrorSet()
			hcp := &hyperv1.HostedControlPlane{}

			err := ReconcileImageTagMirrors(itms, hcp, tc.additionalITMS)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(itms.Labels).To(Equal(tc.expectedLabels))
			g.Expect(itms.Spec.ImageTagMirrors).To(Equal(tc.expectedImageTagMirrors))
		})
	}
}

func TestParseImageMirrorConfigMap(t *testing.T) {
	g := NewGomegaWithT(t)

	testCases := []struct {
		name             string
		configMapData    map[string]string
		expectedIDMS     *configv1.ImageDigestMirrorSet
		expectedITMS     *configv1.ImageTagMirrorSet
		expectError      bool
		configMapMissing bool
	}{
		{
			name: "Valid IDMS and ITMS",
			configMapData: map[string]string{
				"idms.yaml": `apiVersion: config.openshift.io/v1
kind: ImageDigestMirrorSet
metadata:
  name: test-idms
spec:
  imageDigestMirrors:
  - source: registry.example.com/repo
    mirrors:
    - mirror.example.com/repo`,
				"itms.yaml": `apiVersion: config.openshift.io/v1
kind: ImageTagMirrorSet
metadata:
  name: test-itms
spec:
  imageTagMirrors:
  - source: registry.example.com/tags
    mirrors:
    - mirror.example.com/tags`,
			},
			expectedIDMS: &configv1.ImageDigestMirrorSet{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ImageDigestMirrorSet",
					APIVersion: "config.openshift.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-idms",
				},
				Spec: configv1.ImageDigestMirrorSetSpec{
					ImageDigestMirrors: []configv1.ImageDigestMirrors{
						{
							Source:  "registry.example.com/repo",
							Mirrors: []configv1.ImageMirror{"mirror.example.com/repo"},
						},
					},
				},
			},
			expectedITMS: &configv1.ImageTagMirrorSet{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ImageTagMirrorSet",
					APIVersion: "config.openshift.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-itms",
				},
				Spec: configv1.ImageTagMirrorSetSpec{
					ImageTagMirrors: []configv1.ImageTagMirrors{
						{
							Source:  "registry.example.com/tags",
							Mirrors: []configv1.ImageMirror{"mirror.example.com/tags"},
						},
					},
				},
			},
			expectError: false,
		},
		{
			name: "Only IDMS",
			configMapData: map[string]string{
				"idms.yaml": `apiVersion: config.openshift.io/v1
kind: ImageDigestMirrorSet
metadata:
  name: test-idms
spec:
  imageDigestMirrors:
  - source: registry.example.com/repo
    mirrors:
    - mirror.example.com/repo`,
			},
			expectedIDMS: &configv1.ImageDigestMirrorSet{
				TypeMeta: metav1.TypeMeta{
					Kind:       "ImageDigestMirrorSet",
					APIVersion: "config.openshift.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-idms",
				},
				Spec: configv1.ImageDigestMirrorSetSpec{
					ImageDigestMirrors: []configv1.ImageDigestMirrors{
						{
							Source:  "registry.example.com/repo",
							Mirrors: []configv1.ImageMirror{"mirror.example.com/repo"},
						},
					},
				},
			},
			expectedITMS: nil,
			expectError:  false,
		},
		{
			name:             "Missing ConfigMap",
			configMapMissing: true,
			expectError:      true,
		},
		{
			name: "Invalid IDMS YAML",
			configMapData: map[string]string{
				"idms.yaml": `invalid yaml content {{{`,
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var objects []client.Object
			configMapRef := &corev1.LocalObjectReference{Name: "test-configmap"}

			if !tc.configMapMissing {
				cm := &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-configmap",
						Namespace: "test-namespace",
					},
					Data: tc.configMapData,
				}
				objects = append(objects, cm)
			}

			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()
			ctx := context.Background()

			idms, itms, err := ParseImageMirrorConfigMap(ctx, client, configMapRef, "test-namespace")

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				if tc.expectedIDMS != nil {
					g.Expect(idms).ToNot(BeNil())
					g.Expect(idms.Spec.ImageDigestMirrors).To(Equal(tc.expectedIDMS.Spec.ImageDigestMirrors))
				} else {
					g.Expect(idms).To(BeNil())
				}
				if tc.expectedITMS != nil {
					g.Expect(itms).ToNot(BeNil())
					g.Expect(itms.Spec.ImageTagMirrors).To(Equal(tc.expectedITMS.Spec.ImageTagMirrors))
				} else {
					g.Expect(itms).To(BeNil())
				}
			}
		})
	}
}

func TestReconcileImageDigestMirrorsWithAdditional(t *testing.T) {
	g := NewGomegaWithT(t)

	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			ImageContentSources: []hyperv1.ImageContentSource{
				{
					Source:  "source1.example.com",
					Mirrors: []string{"mirror1.example.com"},
				},
			},
		},
	}

	additionalIDMS := &configv1.ImageDigestMirrorSet{
		Spec: configv1.ImageDigestMirrorSetSpec{
			ImageDigestMirrors: []configv1.ImageDigestMirrors{
				{
					Source:  "source2.example.com",
					Mirrors: []configv1.ImageMirror{"mirror2.example.com"},
				},
			},
		},
	}

	idms := ImageDigestMirrorSet()
	err := ReconcileImageDigestMirrors(idms, hcp, additionalIDMS)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify both sources are present
	g.Expect(idms.Spec.ImageDigestMirrors).To(HaveLen(2))
	g.Expect(idms.Spec.ImageDigestMirrors[0].Source).To(Equal("source1.example.com"))
	g.Expect(idms.Spec.ImageDigestMirrors[1].Source).To(Equal("source2.example.com"))
}

func TestReconcileImageTagMirrorsWithAdditional(t *testing.T) {
	g := NewGomegaWithT(t)

	hcp := &hyperv1.HostedControlPlane{
		Spec: hyperv1.HostedControlPlaneSpec{
			ImageContentSources: []hyperv1.ImageContentSource{
				{
					Source:  "source1.example.com",
					Mirrors: []string{"mirror1.example.com"},
				},
			},
		},
	}

	additionalITMS := &configv1.ImageTagMirrorSet{
		Spec: configv1.ImageTagMirrorSetSpec{
			ImageTagMirrors: []configv1.ImageTagMirrors{
				{
					Source:  "source2.example.com",
					Mirrors: []configv1.ImageMirror{"mirror2.example.com"},
				},
			},
		},
	}

	itms := ImageTagMirrorSet()
	err := ReconcileImageTagMirrors(itms, hcp, additionalITMS)
	g.Expect(err).ToNot(HaveOccurred())

	// Verify only the additionalITMS source is present
	g.Expect(itms.Spec.ImageTagMirrors).To(HaveLen(1))
	g.Expect(itms.Spec.ImageTagMirrors[0].Source).To(Equal("source2.example.com"))
}
