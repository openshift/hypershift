package kasbootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"

	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"go.uber.org/zap/zapcore"
)

func TestParseFeatureGateV1(t *testing.T) {
	g := NewGomegaWithT(t)

	testFilePath := filepath.Join(t.TempDir(), "99_feature-gate.yaml")
	err := os.WriteFile(testFilePath, []byte(`
apiVersion: config.openshift.io/v1
kind: FeatureGate
metadata:
  name: cluster
spec:
  featureSet: TechPreviewNoUpgrade
status:
  featureGates:
  - version: "4.7.0"
    enabled:
    - name: foo
    disabled: []
`), 0644)
	g.Expect(err).ToNot(HaveOccurred())

	objBytes, err := os.ReadFile(testFilePath)
	g.Expect(err).ToNot(HaveOccurred())

	result, err := parseFeatureGateV1(objBytes)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(result).ToNot(BeNil())

	expectedFeatureSet := configv1.TechPreviewNoUpgrade
	g.Expect(result.Spec.FeatureSet).To(Equal(expectedFeatureSet))

	expectedFeatureGates := []configv1.FeatureGateDetails{
		{
			Version:  "4.7.0",
			Enabled:  []configv1.FeatureGateAttributes{{Name: "foo"}},
			Disabled: []configv1.FeatureGateAttributes{},
		},
	}
	g.Expect(result.Status.FeatureGates).To(Equal(expectedFeatureGates))
}

func TestReconcileFeatureGate(t *testing.T) {
	testCases := []struct {
		name                 string
		clusterVersion       configv1.ClusterVersion
		existingFeatureGate  configv1.FeatureGate
		renderedFeatureGate  configv1.FeatureGate
		expectedFeatureGates []configv1.FeatureGateDetails
	}{
		{
			name: "when the rendered feature gate is the same as the existing feature gate it should not update",
			clusterVersion: configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Status: configv1.ClusterVersionStatus{
					History: []configv1.UpdateHistory{
						{Version: "4.6.0"},
					},
				},
			},
			existingFeatureGate: configv1.FeatureGate{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version:  "4.7.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "foo"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
					},
				},
			},
			renderedFeatureGate: configv1.FeatureGate{
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version:  "4.7.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "foo"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
					},
				},
			},
			expectedFeatureGates: []configv1.FeatureGateDetails{
				{
					Version:  "4.7.0",
					Enabled:  []configv1.FeatureGateAttributes{{Name: "foo"}},
					Disabled: []configv1.FeatureGateAttributes{},
				},
			},
		},
		{
			name: "when the rendered feature gate is different from the existing feature gate it should update appending to the status",
			clusterVersion: configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Status: configv1.ClusterVersionStatus{
					History: []configv1.UpdateHistory{
						{Version: "4.6.0"},
					},
				},
			},
			existingFeatureGate: configv1.FeatureGate{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version:  "4.6.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "bar"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
					},
				},
			},
			renderedFeatureGate: configv1.FeatureGate{
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version:  "4.7.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "foo"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
					},
				},
			},
			expectedFeatureGates: []configv1.FeatureGateDetails{
				{
					Version:  "4.6.0",
					Enabled:  []configv1.FeatureGateAttributes{{Name: "bar"}},
					Disabled: []configv1.FeatureGateAttributes{},
				},
				{
					Version:  "4.7.0",
					Enabled:  []configv1.FeatureGateAttributes{{Name: "foo"}},
					Disabled: []configv1.FeatureGateAttributes{},
				},
			},
		},
		{
			name: "when the existing feature gate version is not in the clusterVersion it should be dropped from the status",
			clusterVersion: configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Status: configv1.ClusterVersionStatus{
					History: []configv1.UpdateHistory{
						{Version: "4.7.0"},
						{Version: "4.6.0"},
						{Version: "4.4.0"},
					},
				},
			},
			existingFeatureGate: configv1.FeatureGate{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version:  "4.6.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "bar"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
						{
							Version:  "4.5.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "bar"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
					},
				},
			},
			renderedFeatureGate: configv1.FeatureGate{
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version:  "4.7.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "foo"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
					},
				},
			},
			expectedFeatureGates: []configv1.FeatureGateDetails{
				{
					Version:  "4.7.0",
					Enabled:  []configv1.FeatureGateAttributes{{Name: "foo"}},
					Disabled: []configv1.FeatureGateAttributes{},
				},
				{
					Version:  "4.6.0",
					Enabled:  []configv1.FeatureGateAttributes{{Name: "bar"}},
					Disabled: []configv1.FeatureGateAttributes{},
				},
			},
		},
		{
			name: "when the clusterVersion does not exist it should not fail and append everything to the status",
			existingFeatureGate: configv1.FeatureGate{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version:  "4.5.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "bar"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
						{
							Version:  "4.6.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "bar"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
					},
				},
			},
			renderedFeatureGate: configv1.FeatureGate{
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version:  "4.7.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "foo"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
					},
				},
			},
			expectedFeatureGates: []configv1.FeatureGateDetails{
				{
					Version:  "4.7.0",
					Enabled:  []configv1.FeatureGateAttributes{{Name: "foo"}},
					Disabled: []configv1.FeatureGateAttributes{},
				},
				{
					Version:  "4.6.0",
					Enabled:  []configv1.FeatureGateAttributes{{Name: "bar"}},
					Disabled: []configv1.FeatureGateAttributes{},
				},
				{
					Version:  "4.5.0",
					Enabled:  []configv1.FeatureGateAttributes{{Name: "bar"}},
					Disabled: []configv1.FeatureGateAttributes{},
				},
			},
		},
		{
			name: "when clusterVersion has a completed entry, it should only keep feature gates for versions after the completed entry",
			clusterVersion: configv1.ClusterVersion{
				ObjectMeta: metav1.ObjectMeta{
					Name: "version",
				},
				Status: configv1.ClusterVersionStatus{
					History: []configv1.UpdateHistory{
						{Version: "4.8.0", State: configv1.PartialUpdate},
						{Version: "4.7.0", State: configv1.CompletedUpdate},
						{Version: "4.6.0", State: configv1.CompletedUpdate},
						{Version: "4.5.0", State: configv1.PartialUpdate},
					},
				},
			},
			existingFeatureGate: configv1.FeatureGate{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version:  "4.5.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "oldFeature"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
						{
							Version:  "4.6.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "oldFeature"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
						{
							Version:  "4.7.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "lastCompletedFeature"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
						{
							Version:  "4.8.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "newFeature"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
					},
				},
			},
			renderedFeatureGate: configv1.FeatureGate{
				Status: configv1.FeatureGateStatus{
					FeatureGates: []configv1.FeatureGateDetails{
						{
							Version:  "4.9.0",
							Enabled:  []configv1.FeatureGateAttributes{{Name: "newFeature"}},
							Disabled: []configv1.FeatureGateAttributes{},
						},
					},
				},
			},
			expectedFeatureGates: []configv1.FeatureGateDetails{
				{
					Version:  "4.9.0",
					Enabled:  []configv1.FeatureGateAttributes{{Name: "newFeature"}},
					Disabled: []configv1.FeatureGateAttributes{},
				},
				{
					Version:  "4.8.0",
					Enabled:  []configv1.FeatureGateAttributes{{Name: "newFeature"}},
					Disabled: []configv1.FeatureGateAttributes{},
				},
				{
					Version:  "4.7.0",
					Enabled:  []configv1.FeatureGateAttributes{{Name: "lastCompletedFeature"}},
					Disabled: []configv1.FeatureGateAttributes{},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			logger := zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
				o.EncodeTime = zapcore.RFC3339TimeEncoder
			}))
			ctrl.SetLogger(logger)

			builder := fake.NewClientBuilder().WithScheme(configScheme)
			c := builder.WithObjects([]client.Object{&tc.clusterVersion, &tc.existingFeatureGate}...).
				WithStatusSubresource(&tc.existingFeatureGate).Build()

			err := reconcileFeatureGate(t.Context(), c, &tc.renderedFeatureGate)
			g.Expect(err).ToNot(HaveOccurred())

			var updatedFeatureGate configv1.FeatureGate
			err = c.Get(t.Context(), client.ObjectKey{Name: "cluster"}, &updatedFeatureGate)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(updatedFeatureGate.Status.FeatureGates).To(ConsistOf(tc.expectedFeatureGates))
		})
	}
}

// fakeApplyClient wraps a fake client.Client and implements the Apply interface
// by using Create instead of Patch with client.Apply, because the current
// controller-runtime version has a bug that prevents server-side apply with fake.Client.
// Once controller-runtime is upgraded to >= 0.22, this can be replaced.
type fakeApplyClient struct {
	client.Client
}

func newFakeApplyClient() *fakeApplyClient {
	c := fake.NewClientBuilder().WithScheme(configScheme).Build()
	return &fakeApplyClient{Client: c}
}

func (c *fakeApplyClient) Apply(ctx context.Context, obj *unstructured.Unstructured, opts ...client.PatchOption) error {
	return c.Client.Create(ctx, obj)
}

// errorApplyClient is an Apply implementation that always returns an error.
type errorApplyClient struct {
	err error
}

func (c *errorApplyClient) Apply(ctx context.Context, obj *unstructured.Unstructured, opts ...client.PatchOption) error {
	return c.err
}

func TestApplyManifest(t *testing.T) {
	logger := zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))
	ctrl.SetLogger(logger)

	testCases := []struct {
		name      string
		setup     func(t *testing.T, g Gomega) (Apply, string)
		expectErr string
	}{
		{
			name: "when the manifest file does not exist it should return an error",
			setup: func(t *testing.T, g Gomega) (Apply, string) {
				return newFakeApplyClient(), filepath.Join(t.TempDir(), "nonexistent.yaml")
			},
			expectErr: "failed to read file",
		},
		{
			name: "when the manifest file contains invalid YAML it should return a decode error",
			setup: func(t *testing.T, g Gomega) (Apply, string) {
				invalidPath := filepath.Join(t.TempDir(), "invalid.yaml")
				g.Expect(os.WriteFile(invalidPath, []byte("not: a: valid: k8s: resource"), 0644)).To(Succeed())
				return newFakeApplyClient(), invalidPath
			},
			expectErr: "failed to decode file",
		},
		{
			name: "when the apply client returns an error it should propagate",
			setup: func(t *testing.T, g Gomega) (Apply, string) {
				return &errorApplyClient{err: fmt.Errorf("connection refused")}, filepath.Join(".", "testdata", kasBootstrapContainerRolebindingManifest)
			},
			expectErr: "failed to apply file",
		},
		{
			name: "when the manifest is valid it should apply successfully",
			setup: func(t *testing.T, g Gomega) (Apply, string) {
				return newFakeApplyClient(), "./testdata/0000_10_config-operator_01_featuregates.crd.yaml"
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			applyClient, path := tc.setup(t, g)
			err := applyManifest(t.Context(), applyClient, path)
			if tc.expectErr != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectErr))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
		})
	}
}

func TestApplyBootstrapManifests(t *testing.T) {
	logger := zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))
	ctrl.SetLogger(logger)

	t.Run("when the files path does not exist it should return an error", func(t *testing.T) {
		g := NewGomegaWithT(t)
		c := newFakeApplyClient()
		err := applyBootstrapManifests(t.Context(), c, filepath.Join(t.TempDir(), "testdata-not-exist"))
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("does not exist"))
	})

	t.Run("when the apply client returns an error it should propagate", func(t *testing.T) {
		g := NewGomegaWithT(t)
		c := &errorApplyClient{err: fmt.Errorf("connection refused")}
		err := applyBootstrapManifests(t.Context(), c, "./testdata")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to apply file"))
	})

	t.Run("it should skip the kas-bootstrap-container-rolebinding manifest", func(t *testing.T) {
		g := NewGomegaWithT(t)
		c := newFakeApplyClient()
		err := applyBootstrapManifests(t.Context(), c, "./testdata")
		g.Expect(err).ToNot(HaveOccurred())

		// Verify the rolebinding is not retrievable from the client.
		roleBinding := &rbacv1.ClusterRoleBinding{}
		err = c.Get(t.Context(), client.ObjectKey{Name: "kas-bootstrap-container-cluster-admin"}, roleBinding)
		g.Expect(err).To(HaveOccurred())
	})

	t.Run("it should skip non-yaml files", func(t *testing.T) {
		g := NewGomegaWithT(t)

		tmpDir := t.TempDir()
		// Copy a valid yaml manifest so applyBootstrapManifests has something to apply.
		crdContent, err := os.ReadFile("./testdata/0000_10_config-operator_01_apiservers.crd.yaml")
		g.Expect(err).ToNot(HaveOccurred())
		err = os.WriteFile(filepath.Join(tmpDir, "0000_10_config-operator_01_apiservers.crd.yaml"), crdContent, 0644)
		g.Expect(err).ToNot(HaveOccurred())

		// Write a non-yaml file that would fail if decoded.
		err = os.WriteFile(filepath.Join(tmpDir, "README.md"), []byte("# not a k8s resource"), 0644)
		g.Expect(err).ToNot(HaveOccurred())

		c := newFakeApplyClient()
		err = applyBootstrapManifests(t.Context(), c, tmpDir)
		g.Expect(err).ToNot(HaveOccurred())

		// Verify only the CRD was created and the non-yaml file was skipped.
		crdList := &apiextensionsv1.CustomResourceDefinitionList{}
		err = c.List(t.Context(), crdList)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(crdList.Items).To(HaveLen(1))
		g.Expect(crdList.Items[0].Name).To(Equal("apiservers.config.openshift.io"))
	})

	t.Run("it should apply all yaml manifests except the rolebinding", func(t *testing.T) {
		g := NewGomegaWithT(t)
		c := newFakeApplyClient()
		err := applyBootstrapManifests(t.Context(), c, "./testdata")
		g.Expect(err).ToNot(HaveOccurred())

		// Verify 2 CRDs were created.
		crdList := &apiextensionsv1.CustomResourceDefinitionList{}
		err = c.List(t.Context(), crdList)
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(crdList.Items).To(HaveLen(2))

		// Verify the FeatureGate was created.
		featureGate := &configv1.FeatureGate{}
		err = c.Get(t.Context(), client.ObjectKey{Name: "cluster"}, featureGate)
		g.Expect(err).ToNot(HaveOccurred())

		// Verify the hcco ClusterRoleBinding was created.
		hccoRoleBinding := &rbacv1.ClusterRoleBinding{}
		err = c.Get(t.Context(), client.ObjectKey{Name: "hcco-cluster-admin"}, hccoRoleBinding)
		g.Expect(err).ToNot(HaveOccurred())

		// Verify the kas-bootstrap-container rolebinding was NOT created.
		kasRoleBinding := &rbacv1.ClusterRoleBinding{}
		err = c.Get(t.Context(), client.ObjectKey{Name: "kas-bootstrap-container-cluster-admin"}, kasRoleBinding)
		g.Expect(err).To(HaveOccurred())
	})
}
