package syncfgconfigmap

import (
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/spf13/pflag"
)

func TestNewRunCommand(t *testing.T) {
	t.Parallel()

	t.Run("When sync-fg-configmap command is created, it should have 'sync-fg-configmap' as use", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		cmd := NewRunCommand()
		g.Expect(cmd.Use).To(Equal("sync-fg-configmap"))
	})

	t.Run("When sync-fg-configmap command is created, it should register exactly the expected flags", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		cmd := NewRunCommand()

		var flagNames []string
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			flagNames = append(flagNames, f.Name)
		})
		sort.Strings(flagNames)

		g.Expect(flagNames).To(Equal([]string{"file", "name", "namespace", "payload-version"}))
	})

	t.Run("When sync-fg-configmap command is created, it should default file to /manifests/99_feature-gate.yaml", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		cmd := NewRunCommand()
		fileFlag := cmd.Flags().Lookup("file")

		g.Expect(fileFlag).ToNot(BeNil())
		g.Expect(fileFlag.DefValue).To(Equal("/manifests/99_feature-gate.yaml"))
	})

	t.Run("When sync-fg-configmap command is created, it should default name to feature-gate", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		cmd := NewRunCommand()
		nameFlag := cmd.Flags().Lookup("name")

		g.Expect(nameFlag).ToNot(BeNil())
		g.Expect(nameFlag.DefValue).To(Equal("feature-gate"))
	})
}

func TestReconcile(t *testing.T) {
	t.Parallel()

	t.Run("When reconcile is called with valid content, it should create the configmap", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		scheme := runtime.NewScheme()
		g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		opts := &syncFGConfigMapOptions{
			Name:           "feature-gate",
			Namespace:      "test-namespace",
			PayloadVersion: "4.17.0",
		}
		content := []byte("apiVersion: config.openshift.io/v1\nkind: FeatureGate")

		err := opts.reconcile(t.Context(), fakeClient, content)
		g.Expect(err).ToNot(HaveOccurred())

		cm := &corev1.ConfigMap{}
		err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "feature-gate", Namespace: "test-namespace"}, cm)
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When reconcile is called with valid content, it should set the payload-version annotation", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		scheme := runtime.NewScheme()
		g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		opts := &syncFGConfigMapOptions{
			Name:           "feature-gate",
			Namespace:      "test-namespace",
			PayloadVersion: "4.17.0",
		}
		content := []byte("apiVersion: config.openshift.io/v1\nkind: FeatureGate")

		err := opts.reconcile(t.Context(), fakeClient, content)
		g.Expect(err).ToNot(HaveOccurred())

		cm := &corev1.ConfigMap{}
		err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "feature-gate", Namespace: "test-namespace"}, cm)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cm.Annotations).To(HaveKeyWithValue("hypershift.openshift.io/payload-version", "4.17.0"))
	})

	t.Run("When reconcile is called with valid content, it should set the feature-gate.yaml data key", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		scheme := runtime.NewScheme()
		g.Expect(corev1.AddToScheme(scheme)).To(Succeed())
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

		opts := &syncFGConfigMapOptions{
			Name:           "feature-gate",
			Namespace:      "test-namespace",
			PayloadVersion: "4.17.0",
		}
		content := []byte("apiVersion: config.openshift.io/v1\nkind: FeatureGate")

		err := opts.reconcile(t.Context(), fakeClient, content)
		g.Expect(err).ToNot(HaveOccurred())

		cm := &corev1.ConfigMap{}
		err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "feature-gate", Namespace: "test-namespace"}, cm)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cm.Data).To(HaveKeyWithValue("feature-gate.yaml", string(content)))
	})

	t.Run("When the configmap already exists, it should update it with new content", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		scheme := runtime.NewScheme()
		g.Expect(corev1.AddToScheme(scheme)).To(Succeed())

		existingCM := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "feature-gate",
				Namespace: "test-namespace",
				Annotations: map[string]string{
					"hypershift.openshift.io/payload-version": "4.16.0",
				},
			},
			Data: map[string]string{
				"feature-gate.yaml": "old-content",
			},
		}
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingCM).Build()

		opts := &syncFGConfigMapOptions{
			Name:           "feature-gate",
			Namespace:      "test-namespace",
			PayloadVersion: "4.17.0",
		}
		newContent := []byte("apiVersion: config.openshift.io/v1\nkind: FeatureGate\nspec: updated")

		err := opts.reconcile(t.Context(), fakeClient, newContent)
		g.Expect(err).ToNot(HaveOccurred())

		cm := &corev1.ConfigMap{}
		err = fakeClient.Get(t.Context(), client.ObjectKey{Name: "feature-gate", Namespace: "test-namespace"}, cm)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(cm.Annotations).To(HaveKeyWithValue("hypershift.openshift.io/payload-version", "4.17.0"))
		g.Expect(cm.Data).To(HaveKeyWithValue("feature-gate.yaml", string(newContent)))
	})
}
