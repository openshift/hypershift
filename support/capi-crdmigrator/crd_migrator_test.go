//go:build envtest

package capicrdmigrator

import (
	"context"
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// testCRD returns a minimal CRD with two versions for testing migration.
// v1alpha1 is the old storage version, v1beta1 is the new storage version.
func testCRD() *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: "widgets.testing.crdmigrator.io",
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "testing.crdmigrator.io",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     "Widget",
				ListKind: "WidgetList",
				Plural:   "widgets",
				Singular: "widget",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1alpha1",
					Served:  true,
					Storage: false,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:                   "object",
							XPreserveUnknownFields: boolPtr(true),
						},
					},
				},
				{
					Name:    "v1beta1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type:                   "object",
							XPreserveUnknownFields: boolPtr(true),
						},
					},
				},
			},
		},
	}
}

func boolPtr(b bool) *bool { return &b }

// widgetGVK is a synthetic type registered with the scheme so the migrator's setup() can resolve it.
type Widget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
}

func (w *Widget) DeepCopyObject() runtime.Object {
	cp := *w
	return &cp
}

func setupTestEnv(t *testing.T) (client.Client, *envtest.Environment) {
	t.Helper()
	g := NewGomegaWithT(t)

	env := &envtest.Environment{}
	cfg, err := env.Start()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cfg).ToNot(BeNil())

	t.Cleanup(func() {
		g.Expect(env.Stop()).To(Succeed())
	})

	s := runtime.NewScheme()
	g.Expect(apiextensionsv1.AddToScheme(s)).To(Succeed())
	s.AddKnownTypeWithName(schema.GroupVersionKind{
		Group:   "testing.crdmigrator.io",
		Version: "v1beta1",
		Kind:    "Widget",
	}, &Widget{})

	c, err := client.New(cfg, client.Options{Scheme: s})
	g.Expect(err).ToNot(HaveOccurred())

	return c, env
}

func installCRD(t *testing.T, c client.Client, crd *apiextensionsv1.CustomResourceDefinition) {
	t.Helper()
	g := NewGomegaWithT(t)
	ctx := context.Background()

	g.Expect(c.Create(ctx, crd)).To(Succeed())

	g.Eventually(func() bool {
		existing := &apiextensionsv1.CustomResourceDefinition{}
		if err := c.Get(ctx, client.ObjectKeyFromObject(crd), existing); err != nil {
			return false
		}
		for _, cond := range existing.Status.Conditions {
			if cond.Type == apiextensionsv1.Established && cond.Status == apiextensionsv1.ConditionTrue {
				return true
			}
		}
		return false
	}, "30s", "100ms").Should(BeTrue(), "CRD should become established")
}

func createWidget(t *testing.T, c client.Client, name, namespace string) {
	t.Helper()
	g := NewGomegaWithT(t)
	ctx := context.Background()

	ns := &unstructured.Unstructured{}
	ns.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Namespace"})
	ns.SetName(namespace)
	_ = c.Create(ctx, ns)

	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   "testing.crdmigrator.io",
		Version: "v1beta1",
		Kind:    "Widget",
	})
	obj.SetName(name)
	obj.SetNamespace(namespace)
	g.Expect(c.Create(ctx, obj)).To(Succeed())
}

func TestCRDMigratorReconcile_SkipsUnknownCRD(t *testing.T) {
	g := NewGomegaWithT(t)
	c, _ := setupTestEnv(t)

	migrator := &CRDMigrator{
		Client:    c,
		APIReader: c,
		Config: map[client.Object]ByObjectConfig{
			&Widget{}: {},
		},
	}
	s := runtime.NewScheme()
	g.Expect(apiextensionsv1.AddToScheme(s)).To(Succeed())
	s.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "testing.crdmigrator.io", Version: "v1beta1", Kind: "Widget",
	}, &Widget{})
	g.Expect(migrator.setup(s)).To(Succeed())

	result, err := migrator.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "unknown.crd.io"},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(ctrl.Result{}))
}

func TestCRDMigratorReconcile_StorageVersionMigration(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx := context.Background()
	c, _ := setupTestEnv(t)

	crd := testCRD()
	installCRD(t, c, crd)

	createWidget(t, c, "widget-1", "default")
	createWidget(t, c, "widget-2", "default")

	// Simulate storedVersions containing old version (apiserver sets this)
	existing := &apiextensionsv1.CustomResourceDefinition{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(crd), existing)).To(Succeed())

	// Verify storedVersions includes v1beta1 (apiserver should set this since v1beta1 is storage)
	g.Expect(existing.Status.StoredVersions).To(ContainElement("v1beta1"))

	// Patch storedVersions to simulate pre-migration state with both versions
	existing.Status.StoredVersions = []string{"v1alpha1", "v1beta1"}
	g.Expect(c.Status().Update(ctx, existing)).To(Succeed())

	s := runtime.NewScheme()
	g.Expect(apiextensionsv1.AddToScheme(s)).To(Succeed())
	s.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "testing.crdmigrator.io", Version: "v1beta1", Kind: "Widget",
	}, &Widget{})

	migrator := &CRDMigrator{
		Client:    c,
		APIReader: c,
		Config: map[client.Object]ByObjectConfig{
			&Widget{}: {},
		},
	}
	g.Expect(migrator.setup(s)).To(Succeed())

	result, err := migrator.Reconcile(ctx, ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "widgets.testing.crdmigrator.io"},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(ctrl.Result{}))

	// Verify storedVersions was updated to only contain the storage version
	updated := &apiextensionsv1.CustomResourceDefinition{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(crd), updated)).To(Succeed())
	g.Expect(updated.Status.StoredVersions).To(Equal([]string{"v1beta1"}))

	// Verify observed generation annotation was set
	g.Expect(updated.Annotations).To(HaveKey(CRDMigrationObservedGenerationAnnotation))
}

func TestCRDMigratorReconcile_AlreadyMigrated(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx := context.Background()
	c, _ := setupTestEnv(t)

	crd := testCRD()
	installCRD(t, c, crd)

	// storedVersions should already be [v1beta1] from apiserver
	existing := &apiextensionsv1.CustomResourceDefinition{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(crd), existing)).To(Succeed())

	// Ensure storedVersions is exactly [v1beta1] (already migrated state)
	existing.Status.StoredVersions = []string{"v1beta1"}
	g.Expect(c.Status().Update(ctx, existing)).To(Succeed())

	s := runtime.NewScheme()
	g.Expect(apiextensionsv1.AddToScheme(s)).To(Succeed())
	s.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "testing.crdmigrator.io", Version: "v1beta1", Kind: "Widget",
	}, &Widget{})

	migrator := &CRDMigrator{
		Client:    c,
		APIReader: c,
		Config: map[client.Object]ByObjectConfig{
			&Widget{}: {},
		},
	}
	g.Expect(migrator.setup(s)).To(Succeed())

	result, err := migrator.Reconcile(ctx, ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "widgets.testing.crdmigrator.io"},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(ctrl.Result{}))

	// Verify storedVersions unchanged
	updated := &apiextensionsv1.CustomResourceDefinition{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(crd), updated)).To(Succeed())
	g.Expect(updated.Status.StoredVersions).To(Equal([]string{"v1beta1"}))

	// Verify observed generation annotation was still set (reconcile completed successfully)
	g.Expect(updated.Annotations).To(HaveKey(CRDMigrationObservedGenerationAnnotation))
}

func TestCRDMigratorReconcile_SkipsOnMatchingObservedGeneration(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx := context.Background()
	c, _ := setupTestEnv(t)

	crd := testCRD()
	installCRD(t, c, crd)

	// Set the observed generation annotation to match current generation
	existing := &apiextensionsv1.CustomResourceDefinition{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(crd), existing)).To(Succeed())

	gen := existing.Generation
	if existing.Annotations == nil {
		existing.Annotations = map[string]string{}
	}
	existing.Annotations[CRDMigrationObservedGenerationAnnotation] = fmt.Sprintf("%d", gen)
	g.Expect(c.Update(ctx, existing)).To(Succeed())

	// Set storedVersions to something that would trigger migration if reconcile ran
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(crd), existing)).To(Succeed())
	existing.Status.StoredVersions = []string{"v1alpha1", "v1beta1"}
	g.Expect(c.Status().Update(ctx, existing)).To(Succeed())

	s := runtime.NewScheme()
	g.Expect(apiextensionsv1.AddToScheme(s)).To(Succeed())
	s.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "testing.crdmigrator.io", Version: "v1beta1", Kind: "Widget",
	}, &Widget{})

	migrator := &CRDMigrator{
		Client:    c,
		APIReader: c,
		Config: map[client.Object]ByObjectConfig{
			&Widget{}: {},
		},
	}
	g.Expect(migrator.setup(s)).To(Succeed())

	result, err := migrator.Reconcile(ctx, ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "widgets.testing.crdmigrator.io"},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(ctrl.Result{}))

	// storedVersions should NOT have been changed (reconcile was skipped)
	updated := &apiextensionsv1.CustomResourceDefinition{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(crd), updated)).To(Succeed())
	g.Expect(updated.Status.StoredVersions).To(Equal([]string{"v1alpha1", "v1beta1"}))
}

func TestCRDMigratorReconcile_NoResourcesMigration(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx := context.Background()
	c, _ := setupTestEnv(t)

	crd := testCRD()
	installCRD(t, c, crd)

	existing := &apiextensionsv1.CustomResourceDefinition{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(crd), existing)).To(Succeed())
	existing.Status.StoredVersions = []string{"v1alpha1", "v1beta1"}
	g.Expect(c.Status().Update(ctx, existing)).To(Succeed())

	s := runtime.NewScheme()
	g.Expect(apiextensionsv1.AddToScheme(s)).To(Succeed())
	s.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "testing.crdmigrator.io", Version: "v1beta1", Kind: "Widget",
	}, &Widget{})

	migrator := &CRDMigrator{
		Client:    c,
		APIReader: c,
		Config: map[client.Object]ByObjectConfig{
			&Widget{}: {},
		},
	}
	g.Expect(migrator.setup(s)).To(Succeed())

	result, err := migrator.Reconcile(ctx, ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "widgets.testing.crdmigrator.io"},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(ctrl.Result{}))

	// storedVersions should be updated even with no CRs to migrate
	updated := &apiextensionsv1.CustomResourceDefinition{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(crd), updated)).To(Succeed())
	g.Expect(updated.Status.StoredVersions).To(Equal([]string{"v1beta1"}))
}

func TestCRDMigratorReconcile_CleanupManagedFields(t *testing.T) {
	g := NewGomegaWithT(t)
	ctx := context.Background()
	c, _ := setupTestEnv(t)

	crd := testCRD()
	installCRD(t, c, crd)

	createWidget(t, c, "widget-mf", "default")

	// Add a managedFields entry with a no-longer-served version
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(schema.GroupVersionKind{
		Group: "testing.crdmigrator.io", Version: "v1beta1", Kind: "Widget",
	})
	g.Expect(c.Get(ctx, client.ObjectKey{Name: "widget-mf", Namespace: "default"}, obj)).To(Succeed())

	staleMF := metav1.ManagedFieldsEntry{
		Manager:    "stale-manager",
		Operation:  metav1.ManagedFieldsOperationApply,
		APIVersion: "testing.crdmigrator.io/v1alpha0",
		FieldsType: "FieldsV1",
		FieldsV1:   &metav1.FieldsV1{Raw: []byte("{}")},
	}
	obj.SetManagedFields(append(obj.GetManagedFields(), staleMF))
	g.Expect(c.Update(ctx, obj)).To(Succeed())

	// Verify stale entry exists
	g.Expect(c.Get(ctx, client.ObjectKey{Name: "widget-mf", Namespace: "default"}, obj)).To(Succeed())
	hasStaleMF := false
	for _, mf := range obj.GetManagedFields() {
		if mf.APIVersion == "testing.crdmigrator.io/v1alpha0" {
			hasStaleMF = true
		}
	}
	g.Expect(hasStaleMF).To(BeTrue(), "stale managedFields entry should exist before cleanup")

	// Set storedVersions to already migrated so only cleanup phase runs
	existing := &apiextensionsv1.CustomResourceDefinition{}
	g.Expect(c.Get(ctx, client.ObjectKeyFromObject(crd), existing)).To(Succeed())
	existing.Status.StoredVersions = []string{"v1beta1"}
	g.Expect(c.Status().Update(ctx, existing)).To(Succeed())

	s := runtime.NewScheme()
	g.Expect(apiextensionsv1.AddToScheme(s)).To(Succeed())
	s.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "testing.crdmigrator.io", Version: "v1beta1", Kind: "Widget",
	}, &Widget{})

	migrator := &CRDMigrator{
		Client:    c,
		APIReader: c,
		Config: map[client.Object]ByObjectConfig{
			&Widget{}: {},
		},
	}
	g.Expect(migrator.setup(s)).To(Succeed())

	result, err := migrator.Reconcile(ctx, ctrl.Request{
		NamespacedName: client.ObjectKey{Name: "widgets.testing.crdmigrator.io"},
	})
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).To(Equal(ctrl.Result{}))

	// Verify stale managedFields entry was removed
	g.Expect(c.Get(ctx, client.ObjectKey{Name: "widget-mf", Namespace: "default"}, obj)).To(Succeed())
	for _, mf := range obj.GetManagedFields() {
		g.Expect(mf.APIVersion).ToNot(Equal("testing.crdmigrator.io/v1alpha0"),
			"stale managedFields entry should have been cleaned up")
	}
}
