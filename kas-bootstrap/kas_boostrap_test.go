package kasbootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"go.uber.org/zap/zapcore"
	yaml "gopkg.in/yaml.v3"
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

			managementClient := builder.Build()

			// Set the KAS_BOOTSTRAP_NAMESPACE environment variable for the test.
			// This should come from the downward API in the real deployment.
			namespace := "test-namespace"
			t.Setenv("KAS_BOOTSTRAP_NAMESPACE", namespace)

			err := reconcileFeatureGate(context.TODO(), c, managementClient, &tc.renderedFeatureGate)
			g.Expect(err).ToNot(HaveOccurred())

			var updatedFeatureGate configv1.FeatureGate
			err = c.Get(context.TODO(), client.ObjectKey{Name: "cluster"}, &updatedFeatureGate)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(updatedFeatureGate.Status.FeatureGates).To(ConsistOf(tc.expectedFeatureGates))

			// Check that the ConfigMap with feature gate details was created.
			hostedClusterGates := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hostedcluster-gates",
					Namespace: namespace,
				},
			}
			err = managementClient.Get(context.TODO(), client.ObjectKeyFromObject(hostedClusterGates), hostedClusterGates)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(hostedClusterGates.Data).To(HaveKey("featureGates"))

			var featureGateDetails configv1.FeatureGateDetails
			err = yaml.Unmarshal([]byte(hostedClusterGates.Data["featureGates"]), &featureGateDetails)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(featureGateDetails).To(Equal(tc.renderedFeatureGate.Status.FeatureGates[0]))
		})
	}
}

func TestApplyBootstrapResources(t *testing.T) {
	testCases := []struct {
		name      string
		filesPath string
		expectErr bool
	}{
		{
			name:      "when the files path does not exist it should return an error",
			filesPath: filepath.Join(".", "testdata-not-exist"),
			expectErr: true,
		},
		{
			name:      "when the files path exist it should return create the resources",
			filesPath: filepath.Join(".", "testdata"),
		},
	}

	logger := zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))
	ctrl.SetLogger(logger)

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			builder := fake.NewClientBuilder().WithScheme(configScheme)
			c := builder.Build()

			err := applyBootstrapResources(context.TODO(), c, tc.filesPath)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			crdList := &apiextensionsv1.CustomResourceDefinitionList{}
			err = c.List(context.TODO(), crdList)
			g.Expect(err).ToNot(HaveOccurred())

			// we expect 2 CRDs to be created.
			g.Expect(crdList.Items).To(HaveLen(2))

			// we expect the feature gate to be created.
			featureGate := &configv1.FeatureGate{}
			err = c.Get(context.TODO(), client.ObjectKey{Name: "cluster"}, featureGate)
			g.Expect(err).ToNot(HaveOccurred())

			// we expect the hcco-role-binding to be created.
			roleBinding := &rbacv1.ClusterRoleBinding{}
			err = c.Get(context.TODO(), client.ObjectKey{Name: "hcco-cluster-admin"}, roleBinding)
			g.Expect(err).ToNot(HaveOccurred())
		})
	}
}
