package imageresolution

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestResolverConfig_RegistryOverridesFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   ResolverConfig
		expected string
	}{
		{
			name:     "When config is zero-value, it should return empty string",
			config:   ResolverConfig{},
			expected: "",
		},
		{
			name: "When RegistryOverrides is nil, it should return empty string",
			config: ResolverConfig{
				RegistryOverrides: nil,
			},
			expected: "",
		},
		{
			name: "When RegistryOverrides is empty map, it should return empty string",
			config: ResolverConfig{
				RegistryOverrides: map[string]string{},
			},
			expected: "",
		},
		{
			name: "When single override, it should serialize as source=dest",
			config: ResolverConfig{
				RegistryOverrides: map[string]string{
					"quay.io": "mirror.azurecr.io",
				},
			},
			expected: "quay.io=mirror.azurecr.io",
		},
		{
			name: "When multiple overrides, it should sort alphabetically by source",
			config: ResolverConfig{
				RegistryOverrides: map[string]string{
					"quay.io": "mirror1.io",
					"gcr.io":  "mirror2.io",
				},
			},
			expected: "gcr.io=mirror2.io,quay.io=mirror1.io",
		},
		{
			name: "When three overrides, it should match old ConvertRegistryOverridesToCommandLineFlag format",
			config: ResolverConfig{
				RegistryOverrides: map[string]string{
					"registry1": "mirror1.1",
					"registry2": "mirror2.1",
					"registry3": "mirror3.1",
				},
			},
			expected: "registry1=mirror1.1,registry2=mirror2.1,registry3=mirror3.1",
		},
		{
			name: "When registry contains path components, it should preserve them",
			config: ResolverConfig{
				RegistryOverrides: map[string]string{
					"quay.io/openshift-release-dev": "myregistry.example.com/openshift-release-dev",
				},
			},
			expected: "quay.io/openshift-release-dev=myregistry.example.com/openshift-release-dev",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(tt.config.RegistryOverridesFlag()).To(Equal(tt.expected))
		})
	}
}

func TestResolverConfig_ImageRegistryMirrorsEnvVar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   ResolverConfig
		expected string
	}{
		{
			name:     "When config is zero-value, it should return empty string",
			config:   ResolverConfig{},
			expected: "",
		},
		{
			name: "When ImageRegistryMirrors is nil, it should return empty string",
			config: ResolverConfig{
				ImageRegistryMirrors: nil,
			},
			expected: "",
		},
		{
			name: "When ImageRegistryMirrors is empty map, it should return empty string",
			config: ResolverConfig{
				ImageRegistryMirrors: map[string][]string{},
			},
			expected: "",
		},
		{
			name: "When single source with single mirror, it should serialize as source=mirror",
			config: ResolverConfig{
				ImageRegistryMirrors: map[string][]string{
					"quay.io/openshift": {"mirror1.io/openshift"},
				},
			},
			expected: "quay.io/openshift=mirror1.io/openshift",
		},
		{
			name: "When single source with multiple mirrors, it should emit one pair per mirror",
			config: ResolverConfig{
				ImageRegistryMirrors: map[string][]string{
					"quay.io/openshift": {"mirror1.io/openshift", "mirror2.io/openshift"},
				},
			},
			expected: "quay.io/openshift=mirror1.io/openshift,quay.io/openshift=mirror2.io/openshift",
		},
		{
			name: "When multiple sources with multiple mirrors, it should match old ConvertOpenShiftImageRegistryOverridesToCommandLineFlag format",
			config: ResolverConfig{
				ImageRegistryMirrors: map[string][]string{
					"registry1": {"mirror1.1", "mirror1.2", "mirror1.3"},
					"registry2": {"mirror2.1", "mirror2.2"},
					"registry3": {"mirror3.1"},
				},
			},
			expected: "registry1=mirror1.1,registry1=mirror1.2,registry1=mirror1.3,registry2=mirror2.1,registry2=mirror2.2,registry3=mirror3.1",
		},
		{
			name: "When sources have path components, it should preserve them",
			config: ResolverConfig{
				ImageRegistryMirrors: map[string][]string{
					"registry.redhat.io/redhat": {"myregistry.example.com/redhat"},
				},
			},
			expected: "registry.redhat.io/redhat=myregistry.example.com/redhat",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(tt.config.ImageRegistryMirrorsEnvVar()).To(Equal(tt.expected))
		})
	}
}

func TestResolverConfig_IsEmpty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   ResolverConfig
		expected bool
	}{
		{
			name:     "When zero-value config, it should be empty",
			config:   ResolverConfig{},
			expected: true,
		},
		{
			name: "When only RegistryOverrides set, it should not be empty",
			config: ResolverConfig{
				RegistryOverrides: map[string]string{"a": "b"},
			},
			expected: false,
		},
		{
			name: "When only ImageRegistryMirrors set, it should not be empty",
			config: ResolverConfig{
				ImageRegistryMirrors: map[string][]string{"a": {"b"}},
			},
			expected: false,
		},
		{
			name: "When both empty maps, it should be empty",
			config: ResolverConfig{
				RegistryOverrides:    map[string]string{},
				ImageRegistryMirrors: map[string][]string{},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(tt.config.IsEmpty()).To(Equal(tt.expected))
		})
	}
}

func TestParseRegistryOverridesFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		flag      string
		expected  map[string]string
		expectErr bool
	}{
		{
			name:     "When empty string, it should return nil",
			flag:     "",
			expected: nil,
		},
		{
			name:     "When legacy sentinel '=', it should return nil for backward compatibility",
			flag:     "=",
			expected: nil,
		},
		{
			name: "When single pair, it should parse correctly",
			flag: "quay.io=mirror.io",
			expected: map[string]string{
				"quay.io": "mirror.io",
			},
		},
		{
			name: "When multiple pairs, it should parse all",
			flag: "quay.io=mirror1.io,gcr.io=mirror2.io",
			expected: map[string]string{
				"quay.io": "mirror1.io",
				"gcr.io":  "mirror2.io",
			},
		},
		{
			name: "When registry has path components, it should preserve them",
			flag: "quay.io/openshift=myregistry.com/openshift",
			expected: map[string]string{
				"quay.io/openshift": "myregistry.com/openshift",
			},
		},
		{
			name:      "When entry has no '=', it should return error",
			flag:      "quay.io",
			expectErr: true,
		},
		{
			name:      "When source is empty, it should return error",
			flag:      "=mirror.io",
			expectErr: true,
		},
		{
			name:      "When destination is empty, it should return error",
			flag:      "quay.io=",
			expectErr: true,
		},
		{
			name:      "When one valid and one malformed pair, it should return error",
			flag:      "quay.io=mirror.io,bad-entry",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			result, err := ParseRegistryOverridesFlag(tt.flag)
			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestParseImageRegistryMirrorsEnvVar(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		envVar    string
		expected  map[string][]string
		expectErr bool
	}{
		{
			name:     "When empty string, it should return nil",
			envVar:   "",
			expected: nil,
		},
		{
			name:     "When legacy sentinel '=', it should return nil for backward compatibility",
			envVar:   "=",
			expected: nil,
		},
		{
			name:   "When single pair, it should parse correctly",
			envVar: "quay.io/openshift=mirror.io/openshift",
			expected: map[string][]string{
				"quay.io/openshift": {"mirror.io/openshift"},
			},
		},
		{
			name:   "When duplicate source, it should collect all mirrors in order",
			envVar: "quay.io=mirror1.io,quay.io=mirror2.io",
			expected: map[string][]string{
				"quay.io": {"mirror1.io", "mirror2.io"},
			},
		},
		{
			name:   "When multiple sources with multiple mirrors, it should parse all",
			envVar: "registry1=mirror1.1,registry1=mirror1.2,registry1=mirror1.3,registry2=mirror2.1,registry2=mirror2.2,registry3=mirror3.1",
			expected: map[string][]string{
				"registry1": {"mirror1.1", "mirror1.2", "mirror1.3"},
				"registry2": {"mirror2.1", "mirror2.2"},
				"registry3": {"mirror3.1"},
			},
		},
		{
			name:      "When entry has no '=', it should return error",
			envVar:    "quay.io",
			expectErr: true,
		},
		{
			name:      "When source is empty, it should return error",
			envVar:    "=mirror.io",
			expectErr: true,
		},
		{
			name:      "When mirror is empty, it should return error",
			envVar:    "quay.io=",
			expectErr: true,
		},
		{
			name:      "When one valid and one malformed pair, it should return error",
			envVar:    "quay.io=mirror.io,bad-entry",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			result, err := ParseImageRegistryMirrorsEnvVar(tt.envVar)
			if tt.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestRegistryOverridesFlag_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		original map[string]string
	}{
		{
			name:     "When nil map round-trips, it should produce nil",
			original: nil,
		},
		{
			name:     "When empty map round-trips, it should produce nil",
			original: map[string]string{},
		},
		{
			name: "When single override round-trips, it should recover original",
			original: map[string]string{
				"quay.io": "mirror.io",
			},
		},
		{
			name: "When multiple overrides round-trip, it should recover all",
			original: map[string]string{
				"quay.io":        "mirror1.io",
				"gcr.io":         "mirror2.io",
				"registry.ci.io": "mirror3.io",
			},
		},
		{
			name: "When registries have path components, round-trip should preserve them",
			original: map[string]string{
				"quay.io/openshift-release-dev":  "myregistry.com/openshift-release-dev",
				"registry.redhat.io/ubi9/ubi-go": "internal.registry.com/ubi9/ubi-go",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			serialized := ResolverConfig{RegistryOverrides: tt.original}.RegistryOverridesFlag()
			parsed, err := ParseRegistryOverridesFlag(serialized)
			g.Expect(err).ToNot(HaveOccurred())

			if len(tt.original) == 0 {
				g.Expect(parsed).To(BeNil())
			} else {
				g.Expect(parsed).To(Equal(tt.original))
			}
		})
	}
}

func TestImageRegistryMirrorsEnvVar_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		original map[string][]string
	}{
		{
			name:     "When nil map round-trips, it should produce nil",
			original: nil,
		},
		{
			name:     "When empty map round-trips, it should produce nil",
			original: map[string][]string{},
		},
		{
			name: "When single source with single mirror round-trips, it should recover original",
			original: map[string][]string{
				"quay.io": {"mirror.io"},
			},
		},
		{
			name: "When single source with multiple mirrors round-trips, it should recover all mirrors in order",
			original: map[string][]string{
				"quay.io": {"mirror1.io", "mirror2.io", "mirror3.io"},
			},
		},
		{
			name: "When multiple sources with multiple mirrors round-trip, it should recover all",
			original: map[string][]string{
				"registry1": {"mirror1.1", "mirror1.2", "mirror1.3"},
				"registry2": {"mirror2.1", "mirror2.2"},
				"registry3": {"mirror3.1"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			serialized := ResolverConfig{ImageRegistryMirrors: tt.original}.ImageRegistryMirrorsEnvVar()
			parsed, err := ParseImageRegistryMirrorsEnvVar(serialized)
			g.Expect(err).ToNot(HaveOccurred())

			if len(tt.original) == 0 {
				g.Expect(parsed).To(BeNil())
			} else {
				g.Expect(parsed).To(Equal(tt.original))
			}
		})
	}
}

func TestResolverConfig_Clone(t *testing.T) {
	t.Parallel()

	t.Run("When cloning a config with data, it should return independent copy", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		original := ResolverConfig{
			RegistryOverrides: map[string]string{
				"quay.io": "mirror.io",
			},
			ImageRegistryMirrors: map[string][]string{
				"registry1": {"mirror1.1", "mirror1.2"},
			},
		}

		cloned := original.Clone()

		g.Expect(cloned.RegistryOverrides).To(Equal(original.RegistryOverrides))
		g.Expect(cloned.ImageRegistryMirrors).To(Equal(original.ImageRegistryMirrors))

		cloned.RegistryOverrides["quay.io"] = "mutated.io"
		cloned.ImageRegistryMirrors["registry1"] = append(cloned.ImageRegistryMirrors["registry1"], "mutated.io")

		g.Expect(original.RegistryOverrides["quay.io"]).To(Equal("mirror.io"), "original should not be mutated")
		g.Expect(original.ImageRegistryMirrors["registry1"]).To(HaveLen(2), "original should not be mutated")
	})

	t.Run("When cloning a zero-value config, it should return zero-value", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		cloned := ResolverConfig{}.Clone()
		g.Expect(cloned.IsEmpty()).To(BeTrue())
	})
}
