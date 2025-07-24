package globalconfig

import (
	"slices"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/releaseinfo"
	hyperutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"

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
		expectedResult    map[string][]string
		hasICSPCapability bool
		hasIDMSCapability bool
	}{
		{
			name: "validate ImageRegistryMirrors with only ICSP",
			icsp: createFakeICSP(),
			expectedResult: map[string][]string{
				"registry1": {"mirror1", "mirror2"},
				"registry2": {"mirror1", "mirror2"},
				"registry3.sample.com/samplens/sampleimage@sha256:123456": {
					"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
					"mirroricsp3.sample.com/samplens/sampleimage@sha256:123456",
				},
			},
			hasICSPCapability: true,
			hasIDMSCapability: false,
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
		},
		{
			name: "validate ImageRegistryMirrors with ICSP and IDMS",
			idms: createFakeIDMS(),
			icsp: createFakeICSP(),
			expectedResult: map[string][]string{
				"registry1.sample.com/samplens/sampleimage@sha256:123456": {"mirror1.sample.com/samplens/sampleimage@sha256:123456", "mirror1.sample.com/samplens/sampleimage@sha256:123456"},
				"registry2.sample.com/samplens/sampleimage@sha256:123456": {"mirror2.sample.com/samplens/sampleimage@sha256:123456", "mirror2.sample.com/samplens/sampleimage@sha256:123456"},
				"registry1": {"mirror1", "mirror2"},
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
		},
		{
			name:              "validate empty ImageRegistryMirrors",
			idms:              nil,
			icsp:              nil,
			expectedResult:    map[string][]string{},
			hasICSPCapability: true,
			hasIDMSCapability: true,
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

			client := fake.NewClientBuilder().WithScheme(testScheme).WithObjects(objs...).Build()

			result, err := GetAllImageRegistryMirrors(ctx, client, tc.hasIDMSCapability, tc.hasICSPCapability)
			g.Expect(err).To(BeNil())
			g.Expect(result).To(Equal(tc.expectedResult))
		})

	}
}

func createFakeICSP() *operatorv1alpha1.ImageContentSourcePolicyList {
	return &operatorv1alpha1.ImageContentSourcePolicyList{
		Items: []operatorv1alpha1.ImageContentSourcePolicy{
			{
				Spec: operatorv1alpha1.ImageContentSourcePolicySpec{
					RepositoryDigestMirrors: []operatorv1alpha1.RepositoryDigestMirrors{
						{
							Source:  "registry1",
							Mirrors: []string{"mirror1", "mirror2"},
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
					"registry1": {"mirror1", "mirror2"},
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
					"registry1": {"mirror1", "mirror2"},
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
