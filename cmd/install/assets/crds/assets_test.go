package crds

import (
	"testing"

	. "github.com/onsi/gomega"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestCAPICRDOverridesWithStorageVersion(t *testing.T) {
	t.Run("When requesting v1beta1 overrides, it should return all CRDs with v1beta1 storage", func(t *testing.T) {
		g := NewGomegaWithT(t)
		overrides := CAPICRDOverridesWithStorageVersion("v1beta1")
		g.Expect(overrides).To(HaveLen(len(capiCRDNames)))
		for _, name := range capiCRDNames {
			entry, ok := overrides[name]
			g.Expect(ok).To(BeTrue(), "missing CRD %s", name)
			g.Expect(entry.StorageVersion).To(Equal("v1beta1"))
			g.Expect(entry.NeedsConversion).To(BeTrue())
		}
	})

	t.Run("When requesting v1beta2 overrides, it should return all CRDs with v1beta2 storage", func(t *testing.T) {
		g := NewGomegaWithT(t)
		overrides := CAPICRDOverridesWithStorageVersion("v1beta2")
		g.Expect(overrides).To(HaveLen(len(capiCRDNames)))
		for _, name := range capiCRDNames {
			entry, ok := overrides[name]
			g.Expect(ok).To(BeTrue(), "missing CRD %s", name)
			g.Expect(entry.StorageVersion).To(Equal("v1beta2"))
			g.Expect(entry.NeedsConversion).To(BeTrue())
		}
	})

	t.Run("When using default CAPICRDOverrides, it should return v1beta1 storage", func(t *testing.T) {
		g := NewGomegaWithT(t)
		overrides := CAPICRDOverrides()
		for _, name := range capiCRDNames {
			g.Expect(overrides[name].StorageVersion).To(Equal("v1beta1"))
		}
	})
}

func TestCustomResourceDefinitionsStorageVersion(t *testing.T) {
	t.Run("When selecting v1beta2 storage version, it should set v1beta2 as storage and v1beta1 as non-storage", func(t *testing.T) {
		g := NewGomegaWithT(t)
		capiNames := make(map[string]bool, len(capiCRDNames))
		for _, name := range capiCRDNames {
			capiNames[name] = true
		}

		crds := CustomResourceDefinitions("v1beta2",
			func(path string, crd *apiextensionsv1.CustomResourceDefinition) bool { return true },
			nil,
		)
		for _, obj := range crds {
			crd := obj.(*apiextensionsv1.CustomResourceDefinition)
			if !capiNames[crd.Name] {
				continue
			}
			for _, v := range crd.Spec.Versions {
				switch v.Name {
				case "v1beta2":
					g.Expect(v.Storage).To(BeTrue(), "CRD %s version v1beta2 should be storage", crd.Name)
				case "v1beta1":
					g.Expect(v.Storage).To(BeFalse(), "CRD %s version v1beta1 should not be storage when v1beta2 is selected", crd.Name)
				}
			}
		}
	})

	t.Run("When selecting v1beta1 storage version, it should set v1beta1 as storage and v1beta2 as non-storage", func(t *testing.T) {
		g := NewGomegaWithT(t)
		capiNames := make(map[string]bool, len(capiCRDNames))
		for _, name := range capiCRDNames {
			capiNames[name] = true
		}

		crds := CustomResourceDefinitions("v1beta1",
			func(path string, crd *apiextensionsv1.CustomResourceDefinition) bool { return true },
			nil,
		)
		for _, obj := range crds {
			crd := obj.(*apiextensionsv1.CustomResourceDefinition)
			if !capiNames[crd.Name] {
				continue
			}
			for _, v := range crd.Spec.Versions {
				switch v.Name {
				case "v1beta1":
					g.Expect(v.Storage).To(BeTrue(), "CRD %s version v1beta1 should be storage", crd.Name)
				case "v1beta2":
					g.Expect(v.Storage).To(BeFalse(), "CRD %s version v1beta2 should not be storage when v1beta1 is selected", crd.Name)
				}
			}
		}
	})
}

func TestCapiResources(t *testing.T) {
	t.Run("When loading all capiResources paths they should exist and parse correctly", func(t *testing.T) {
		g := NewGomegaWithT(t)
		for path := range capiResources {
			// getCustomResourceDefinition panics if file doesn't exist or can't be parsed
			g.Expect(func() {
				crd := getCustomResourceDefinition(CRDS, path, CAPICRDOverrides())
				g.Expect(crd).ToNot(BeNil())
			}).ToNot(Panic(), "path %s should exist and parse", path)
		}
	})

	t.Run("When loading a non-existent path it should panic", func(t *testing.T) {
		g := NewGomegaWithT(t)
		g.Expect(func() {
			getCustomResourceDefinition(CRDS, "cluster-api-provider-fake/nonexistent.yaml", CAPICRDOverrides())
		}).To(Panic())
	})
}
