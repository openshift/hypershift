package api

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func TestEncode(t *testing.T) {
	tests := []struct {
		name                string
		obj                 runtime.Object
		expectNullTimestamp bool
		expectContains      string
	}{
		{
			name: "When encoding a MachineConfig with zero creationTimestamp it should replace with null",
			obj: &mcfgv1.MachineConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: mcfgv1.SchemeGroupVersion.String(),
					Kind:       "MachineConfig",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-config",
					Namespace: "test-namespace",
					// CreationTimestamp is zero by default
				},
			},
			expectNullTimestamp: true,
			expectContains:      "creationTimestamp: null",
		},
		{
			name: "When encoding a MachineConfig with non-zero creationTimestamp it should preserve the timestamp",
			obj: &mcfgv1.MachineConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: mcfgv1.SchemeGroupVersion.String(),
					Kind:       "MachineConfig",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-config",
					Namespace:         "test-namespace",
					CreationTimestamp: metav1.NewTime(metav1.Now().Time),
				},
			},
			expectNullTimestamp: false,
			expectContains:      "creationTimestamp:",
		},
		{
			name: "When encoding a ConfigMap with zero creationTimestamp it should replace with null",
			obj: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					APIVersion: corev1.SchemeGroupVersion.String(),
					Kind:       "ConfigMap",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-configmap",
					Namespace: "test-namespace",
				},
				Data: map[string]string{
					"key": "value",
				},
			},
			expectNullTimestamp: true,
			expectContains:      "creationTimestamp: null",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			encoded, err := CompatibleYAMLEncode(tt.obj, YamlSerializer)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(encoded).ToNot(BeEmpty())

			encodedStr := string(encoded)
			g.Expect(encodedStr).To(ContainSubstring(tt.expectContains))

			if tt.expectNullTimestamp {
				g.Expect(encodedStr).To(ContainSubstring("creationTimestamp: null"))
				g.Expect(encodedStr).ToNot(ContainSubstring(`creationTimestamp: "1970-01-01T00:00:00Z"`))
			} else {
				// Should contain a timestamp but not the epoch timestamp
				g.Expect(encodedStr).ToNot(ContainSubstring(`creationTimestamp: "1970-01-01T00:00:00Z"`))
			}
		})
	}
}

func TestEncodeJSON(t *testing.T) {
	tests := []struct {
		name                string
		obj                 runtime.Object
		expectNullTimestamp bool
		expectContains      string
	}{
		{
			name: "When encoding a MachineConfig to JSON with zero creationTimestamp it should replace with null",
			obj: &mcfgv1.MachineConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: mcfgv1.SchemeGroupVersion.String(),
					Kind:       "MachineConfig",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-config",
					Namespace: "test-namespace",
				},
			},
			expectNullTimestamp: true,
			expectContains:      `"creationTimestamp":null`,
		},
		{
			name: "When encoding a MachineConfig to JSON with non-zero creationTimestamp it should preserve the timestamp",
			obj: &mcfgv1.MachineConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: mcfgv1.SchemeGroupVersion.String(),
					Kind:       "MachineConfig",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:              "test-config",
					Namespace:         "test-namespace",
					CreationTimestamp: metav1.NewTime(metav1.Now().Time),
				},
			},
			expectNullTimestamp: false,
			expectContains:      `"creationTimestamp":`,
		},
		{
			name: "When encoding a ConfigMap to JSON with zero creationTimestamp it should replace with null",
			obj: &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					APIVersion: corev1.SchemeGroupVersion.String(),
					Kind:       "ConfigMap",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-configmap",
					Namespace: "test-namespace",
				},
				Data: map[string]string{
					"key": "value",
				},
			},
			expectNullTimestamp: true,
			expectContains:      `"creationTimestamp":null`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			encoded, err := CompatibleJSONEncode(tt.obj)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(encoded).ToNot(BeEmpty())

			encodedStr := string(encoded)
			g.Expect(encodedStr).To(ContainSubstring(tt.expectContains))

			if tt.expectNullTimestamp {
				g.Expect(encodedStr).To(ContainSubstring(`"creationTimestamp":null`))
				g.Expect(encodedStr).ToNot(ContainSubstring(`"creationTimestamp":"1970-01-01T00:00:00Z"`))
			} else {
				// Should contain a timestamp but not the epoch timestamp
				g.Expect(encodedStr).ToNot(ContainSubstring(`"creationTimestamp":"1970-01-01T00:00:00Z"`))
			}
		})
	}
}

func TestEncode_NonMetaObject(t *testing.T) {
	t.Run("When encoding an object that doesn't implement metav1.Object it should still encode successfully", func(t *testing.T) {
		g := NewGomegaWithT(t)

		// Create a simple runtime.Object that doesn't implement metav1.Object
		// We'll use a minimal object that implements runtime.Object
		obj := &runtime.Unknown{
			TypeMeta: runtime.TypeMeta{
				APIVersion: "v1",
				Kind:       "Unknown",
			},
			Raw: []byte(`{"test": "data"}`),
		}

		encoded, err := CompatibleYAMLEncode(obj, YamlSerializer)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(encoded).ToNot(BeEmpty())
	})
}

func TestEncodeJSON_NonMetaObject(t *testing.T) {
	t.Run("When encoding an object to JSON that doesn't implement metav1.Object it should still encode successfully", func(t *testing.T) {
		g := NewGomegaWithT(t)

		obj := &runtime.Unknown{
			TypeMeta: runtime.TypeMeta{
				APIVersion: "v1",
				Kind:       "Unknown",
			},
			Raw: []byte(`{"test": "data"}`),
		}

		encoded, err := CompatibleJSONEncode(obj)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(encoded).ToNot(BeEmpty())
	})
}

func TestEncode_Consistency(t *testing.T) {
	t.Run("When encoding the same object multiple times it should produce consistent output", func(t *testing.T) {
		g := NewGomegaWithT(t)

		obj := &mcfgv1.MachineConfig{
			TypeMeta: metav1.TypeMeta{
				APIVersion: mcfgv1.SchemeGroupVersion.String(),
				Kind:       "MachineConfig",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-config",
				Namespace: "test-namespace",
			},
		}

		encoded1, err1 := CompatibleYAMLEncode(obj, YamlSerializer)
		g.Expect(err1).ToNot(HaveOccurred())

		// Create a new object with the same data
		obj2 := &mcfgv1.MachineConfig{
			TypeMeta: metav1.TypeMeta{
				APIVersion: mcfgv1.SchemeGroupVersion.String(),
				Kind:       "MachineConfig",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-config",
				Namespace: "test-namespace",
			},
		}

		encoded2, err2 := CompatibleYAMLEncode(obj2, YamlSerializer)
		g.Expect(err2).ToNot(HaveOccurred())

		// Both should have null timestamps
		g.Expect(string(encoded1)).To(ContainSubstring("creationTimestamp: null"))
		g.Expect(string(encoded2)).To(ContainSubstring("creationTimestamp: null"))

		// Remove the creationTimestamp line for comparison since it will be identical
		encoded1Str := strings.ReplaceAll(string(encoded1), "creationTimestamp: null\n", "")
		encoded2Str := strings.ReplaceAll(string(encoded2), "creationTimestamp: null\n", "")
		g.Expect(encoded1Str).To(Equal(encoded2Str))
	})
}

func TestEncodeJSON_Consistency(t *testing.T) {
	t.Run("When encoding the same object to JSON multiple times it should produce consistent output", func(t *testing.T) {
		g := NewGomegaWithT(t)

		obj := &mcfgv1.MachineConfig{
			TypeMeta: metav1.TypeMeta{
				APIVersion: mcfgv1.SchemeGroupVersion.String(),
				Kind:       "MachineConfig",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-config",
				Namespace: "test-namespace",
			},
		}

		encoded1, err1 := CompatibleJSONEncode(obj)
		g.Expect(err1).ToNot(HaveOccurred())

		// Create a new object with the same data
		obj2 := &mcfgv1.MachineConfig{
			TypeMeta: metav1.TypeMeta{
				APIVersion: mcfgv1.SchemeGroupVersion.String(),
				Kind:       "MachineConfig",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-config",
				Namespace: "test-namespace",
			},
		}

		encoded2, err2 := CompatibleJSONEncode(obj2)
		g.Expect(err2).ToNot(HaveOccurred())

		// Both should have null timestamps
		g.Expect(string(encoded1)).To(ContainSubstring(`"creationTimestamp":null`))
		g.Expect(string(encoded2)).To(ContainSubstring(`"creationTimestamp":null`))

		// Remove the creationTimestamp field for comparison since it will be identical
		encoded1Str := strings.ReplaceAll(string(encoded1), `"creationTimestamp":null,`, "")
		encoded1Str = strings.ReplaceAll(encoded1Str, `"creationTimestamp":null`, "")
		encoded2Str := strings.ReplaceAll(string(encoded2), `"creationTimestamp":null,`, "")
		encoded2Str = strings.ReplaceAll(encoded2Str, `"creationTimestamp":null`, "")
		g.Expect(encoded1Str).To(Equal(encoded2Str))
	})
}

func TestEncode_EdgeCase_EpochTimestampInAnnotations(t *testing.T) {
	t.Run("When encoding an object with epoch timestamp in annotations it should not replace the annotation value", func(t *testing.T) {
		g := NewGomegaWithT(t)

		obj := &mcfgv1.MachineConfig{
			TypeMeta: metav1.TypeMeta{
				APIVersion: mcfgv1.SchemeGroupVersion.String(),
				Kind:       "MachineConfig",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-config",
				Namespace: "test-namespace",
				Annotations: map[string]string{
					"test-annotation":    `"1970-01-01T00:00:00Z"`,
					"another-annotation": "creationTimestamp: \"1970-01-01T00:00:00Z\"",
				},
			},
		}

		encoded, err := CompatibleYAMLEncode(obj, YamlSerializer)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(encoded).ToNot(BeEmpty())

		encodedStr := string(encoded)
		// The metadata creationTimestamp should be null
		g.Expect(encodedStr).To(ContainSubstring("creationTimestamp: null"))
		// But the annotation values should remain unchanged (YAML serializer quotes the values)
		g.Expect(encodedStr).To(ContainSubstring(`test-annotation: '"1970-01-01T00:00:00Z"'`))
		g.Expect(encodedStr).To(ContainSubstring(`another-annotation: 'creationTimestamp: "1970-01-01T00:00:00Z"'`))
	})
}

func TestEncodeJSON_EdgeCase_EpochTimestampInAnnotations(t *testing.T) {
	t.Run("When encoding an object to JSON with epoch timestamp in annotations it should not replace the annotation value", func(t *testing.T) {
		g := NewGomegaWithT(t)

		obj := &mcfgv1.MachineConfig{
			TypeMeta: metav1.TypeMeta{
				APIVersion: mcfgv1.SchemeGroupVersion.String(),
				Kind:       "MachineConfig",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-config",
				Namespace: "test-namespace",
				Annotations: map[string]string{
					"test-annotation":    `"1970-01-01T00:00:00Z"`,
					"another-annotation": `"creationTimestamp":"1970-01-01T00:00:00Z"`,
				},
			},
		}

		encoded, err := CompatibleJSONEncode(obj)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(encoded).ToNot(BeEmpty())

		encodedStr := string(encoded)
		// The metadata creationTimestamp should be null
		g.Expect(encodedStr).To(ContainSubstring(`"creationTimestamp":null`))
		// But the annotation values should remain unchanged
		g.Expect(encodedStr).To(ContainSubstring(`"test-annotation":"\"1970-01-01T00:00:00Z\""`))
		g.Expect(encodedStr).To(ContainSubstring(`"another-annotation":"\"creationTimestamp\":\"1970-01-01T00:00:00Z\""`))
	})
}

func TestEncode_EdgeCase_EpochTimestampInLabels(t *testing.T) {
	t.Run("When encoding an object with epoch timestamp in labels it should not replace the label value", func(t *testing.T) {
		g := NewGomegaWithT(t)

		obj := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				APIVersion: corev1.SchemeGroupVersion.String(),
				Kind:       "ConfigMap",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-configmap",
				Namespace: "test-namespace",
				Labels: map[string]string{
					"timestamp": "1970-01-01T00:00:00Z",
				},
			},
			Data: map[string]string{
				"key": "value",
			},
		}

		encoded, err := CompatibleYAMLEncode(obj, YamlSerializer)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(encoded).ToNot(BeEmpty())

		encodedStr := string(encoded)
		// The metadata creationTimestamp should be null
		g.Expect(encodedStr).To(ContainSubstring("creationTimestamp: null"))
		// But the label value should remain unchanged (YAML serializer quotes string values)
		g.Expect(encodedStr).To(ContainSubstring(`timestamp: "1970-01-01T00:00:00Z"`))
	})
}

func TestEncodeJSON_EdgeCase_EpochTimestampInData(t *testing.T) {
	t.Run("When encoding a ConfigMap to JSON with epoch timestamp in data it should not replace the data value", func(t *testing.T) {
		g := NewGomegaWithT(t)

		obj := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{
				APIVersion: corev1.SchemeGroupVersion.String(),
				Kind:       "ConfigMap",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-configmap",
				Namespace: "test-namespace",
			},
			Data: map[string]string{
				"timestamp": `"creationTimestamp":"1970-01-01T00:00:00Z"`,
				"other":     `{"field":"1970-01-01T00:00:00Z"}`,
			},
		}

		encoded, err := CompatibleJSONEncode(obj)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(encoded).ToNot(BeEmpty())

		encodedStr := string(encoded)
		// The metadata creationTimestamp should be null
		g.Expect(encodedStr).To(ContainSubstring(`"creationTimestamp":null`))
		// But the data values should remain unchanged
		g.Expect(encodedStr).To(ContainSubstring(`"timestamp":"\"creationTimestamp\":\"1970-01-01T00:00:00Z\""`))
		g.Expect(encodedStr).To(ContainSubstring(`"other":"{\"field\":\"1970-01-01T00:00:00Z\"}"`))
	})
}
