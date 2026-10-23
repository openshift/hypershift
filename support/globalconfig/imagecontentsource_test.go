package globalconfig

import (
	"slices"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/capabilities"
	"github.com/openshift/hypershift/support/imageregistry"

	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"

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
		expectedResult    map[string][]string
		hasICSPCapability bool
		hasIDMSCapability bool
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

func TestReconcileMgmtImageRegistryOverrides(t *testing.T) {
	ctx := t.Context()
	g := NewGomegaWithT(t)

	// Define test cases
	testCases := []struct {
		name                    string
		capChecker              capabilities.CapabiltyChecker
		registryOverrides       map[string]string
		idms                    *configv1.ImageDigestMirrorSetList
		icsp                    *operatorv1alpha1.ImageContentSourcePolicyList
		expectedOverrides       map[string][]string
		expectedRegistryOverrides map[string]string
		expectedError           error
		expectedErrorMatch      string
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
			expectedOverrides: map[string][]string{
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
			expectedRegistryOverrides: map[string]string{
				"registry1": "override1",
				"registry2": "override2",
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
			expectedOverrides: nil,
			expectedRegistryOverrides: map[string]string{
				"registry1": "override1",
				"registry2": "override2",
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

			// Check release provider overrides via getter
			releaseProvider := provider.GetReleaseProvider()
			g.Expect(releaseProvider.GetOpenShiftImageRegistryOverrides()).To(Equal(tc.expectedOverrides))
			g.Expect(releaseProvider.GetRegistryOverrides()).To(Equal(tc.expectedRegistryOverrides))

			// Check image metadata provider overrides via getter
			metadataProvider := provider.GetMetadataProvider()
			metadataImpl, ok := metadataProvider.(*imageregistry.RegistryClientImageMetadataProvider)
			g.Expect(ok).To(BeTrue())
			g.Expect(metadataImpl.GetOpenShiftImageRegistryOverrides()).To(Equal(tc.expectedOverrides))
		})
	}
}

func TestCommonRegistryProviderConcurrentReconcileAndRead(t *testing.T) {
	g := NewGomegaWithT(t)

	capChecker := &capabilities.MockCapabilityChecker{
		MockHas: func(capability ...capabilities.CapabilityType) bool {
			return slices.Contains(capability, capabilities.CapabilityIDMS)
		},
	}

	idms := createFakeIDMS()
	idmsObjs := make([]client.Object, len(idms.Items))
	for i, item := range idms.Items {
		idmsObjs[i] = &item
	}
	fakeClient := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(idmsObjs...).Build()

	provider, err := NewCommonRegistryProvider(t.Context(), capChecker, fakeClient, map[string]string{
		"registry1": "override1",
	})
	g.Expect(err).ToNot(HaveOccurred())

	done := make(chan struct{})
	reconcileErr := make(chan error, 1)

	// Writer: continuously reconcile mirror data
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			if err := provider.Reconcile(t.Context(), fakeClient); err != nil {
				reconcileErr <- err
				return
			}
		}
	}()

	// Readers: concurrently read overrides from both providers
	for i := 0; i < 4; i++ {
		go func() {
			for {
				select {
				case <-done:
					return
				default:
					overrides := provider.GetReleaseProvider().GetOpenShiftImageRegistryOverrides()
					_ = overrides

					metadataImpl := provider.GetMetadataProvider().(*imageregistry.RegistryClientImageMetadataProvider)
					metaOverrides := metadataImpl.GetOpenShiftImageRegistryOverrides()
					_ = metaOverrides
				}
			}
		}()
	}

	<-done

	select {
	case err := <-reconcileErr:
		t.Fatalf("Reconcile failed: %v", err)
	default:
	}
}
