//go:build envtest

package envtest

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	yamlpatch "github.com/vmware-archive/yaml-patch"

	crdassets "github.com/openshift/hypershift/cmd/install/assets/crds"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	kyaml "sigs.k8s.io/yaml"
)

// LoadTestSuiteSpecs recursively walks the given paths looking for any .yaml file
// inside a tests/ directory structure (matching openshift/api conventions).
func LoadTestSuiteSpecs(paths ...string) ([]SuiteSpec, error) {
	suiteFiles := make(map[string]struct{})

	for _, path := range paths {
		if err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			dirPath := filepath.Base(filepath.Dir(filepath.Dir(path)))
			if !info.IsDir() && strings.HasSuffix(path, ".yaml") && dirPath == "tests" {
				suiteFiles[path] = struct{}{}
			}

			return nil
		}); err != nil {
			return nil, fmt.Errorf("could not load files from path %q: %w", path, err)
		}
	}

	out := []SuiteSpec{}
	for path := range suiteFiles {
		suite, err := loadSuiteFile(path)
		if err != nil {
			return nil, fmt.Errorf("could not set up test suite: %w", err)
		}

		out = append(out, suite)
	}

	return out, nil
}

func loadSuiteFile(path string) (SuiteSpec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return SuiteSpec{}, fmt.Errorf("could not read file %q: %w", path, err)
	}

	s := SuiteSpec{}
	if err := kyaml.Unmarshal(raw, &s); err != nil {
		return SuiteSpec{}, fmt.Errorf("could not unmarshal YAML file %q: %w", path, err)
	}

	if len(s.CRDName) == 0 {
		return SuiteSpec{}, fmt.Errorf("test suite spec %q is invalid: missing required field `crdName`", path)
	}

	s.PerTestRuntimeInfo, err = perTestRuntimeInfo(filepath.Dir(path), s.CRDName, s.FeatureGates)
	if err != nil {
		return SuiteSpec{}, fmt.Errorf("unable to determine which CRD files to use: %w", err)
	}
	if len(s.PerTestRuntimeInfo.CRDFilenames) == 0 {
		return SuiteSpec{}, fmt.Errorf("missing CRD files to use for test %v", path)
	}

	if s.Version == "" {
		version, err := getSuiteSpecTestVersion(s)
		if err != nil {
			return SuiteSpec{}, fmt.Errorf("could not determine test suite CRD version for %q: %w", path, err)
		}
		s.Version = version
	}

	return s, nil
}

func getSuiteSpecTestVersion(suiteSpec SuiteSpec) (string, error) {
	version := ""
	for _, file := range suiteSpec.PerTestRuntimeInfo.CRDFilenames {
		crd, err := loadCRDFromFile(file)
		if err != nil {
			return "", err
		}
		if len(crd.Spec.Versions) > 1 {
			return "", fmt.Errorf("too many versions, specify one in the suite")
		}
		if len(version) == 0 {
			version = crd.Spec.Versions[0].Name
			continue
		}
		if version != crd.Spec.Versions[0].Name {
			return "", fmt.Errorf("too many versions, specify one in the suite. Saw %v and %v", version, crd.Spec.Versions[0].Name)
		}
	}
	return version, nil
}

// loadCRDFromFile loads a CRD from a YAML file.
func loadCRDFromFile(filename string) (*apiextensionsv1.CustomResourceDefinition, error) {
	raw, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("could not load CRD: %w", err)
	}

	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := kyaml.Unmarshal(raw, crd); err != nil {
		return nil, fmt.Errorf("could not unmarshal CRD: %w", err)
	}

	return crd, nil
}

// allCRDsForFeatureSet returns all HyperShift CRDs (hypershift-operator, cluster-api,
// all providers) for the given feature set, using the same CustomResourceDefinitions
// function as setupCRDs in cmd/install/install.go.
func allCRDsForFeatureSet(featureSet string) []*apiextensionsv1.CustomResourceDefinition {
	crdObjects := crdassets.CustomResourceDefinitions(
		func(path string, crd *apiextensionsv1.CustomResourceDefinition) bool {
			// Exclude non-CRD files (featuregates, test suites).
			if strings.Contains(path, "payload-manifests") || strings.Contains(path, "tests/") {
				return false
			}
			if strings.Contains(path, "zz_generated.crd-manifests") {
				if annotationFS, ok := crd.Annotations["release.openshift.io/feature-set"]; ok {
					return annotationFS == featureSet
				}
			}
			return true
		},
		nil,
	)

	crds := make([]*apiextensionsv1.CustomResourceDefinition, len(crdObjects))
	for i, obj := range crdObjects {
		crds[i] = obj.(*apiextensionsv1.CustomResourceDefinition)
	}
	return crds
}

// GenerateTestSuite generates a Ginkgo test suite from the provided SuiteSpec.
// For each CRD file variant, the CRD under test is installed, all tests are run,
// and the CRD is uninstalled. Only the suite's own CRD is installed/uninstalled
// (not all CRDs) to keep test execution fast.
func GenerateTestSuite(suiteSpec SuiteSpec) {
	for i := range suiteSpec.PerTestRuntimeInfo.CRDFilenames {
		crdFilename := suiteSpec.PerTestRuntimeInfo.CRDFilenames[i]

		baseCRD, err := loadVersionedCRD(suiteSpec, crdFilename)
		Expect(err).ToNot(HaveOccurred())

		suiteName := generateSuiteName(suiteSpec, crdFilename)

		Describe(suiteName, Ordered, func() {
			var crd *apiextensionsv1.CustomResourceDefinition

			BeforeEach(OncePerOrdered, func() {
				Expect(k8sClient).ToNot(BeNil(), "Kubernetes client is not initialised")

				// Retry CRD installation — a previous suite may still be deleting the same CRD.
				var crds []*apiextensionsv1.CustomResourceDefinition
				Eventually(func() error {
					var err error
					crds, err = envtest.InstallCRDs(cfg, envtest.CRDInstallOptions{
						CRDs: []*apiextensionsv1.CustomResourceDefinition{
							baseCRD.DeepCopy(),
						},
					})
					return err
				}, "30s", "1s").Should(Succeed(), "CRD should install successfully")
				Expect(crds).To(HaveLen(1), "Only one CRD should have been installed")
				crd = crds[0]

				Expect(envtest.WaitForCRDs(cfg, crds, envtest.CRDInstallOptions{})).To(Succeed())
			})

			AfterEach(func() {
				// Clean up CRs created during the test.
				for _, u := range newUnstructuredsFor(crd) {
					_ = k8sClient.DeleteAllOf(ctx, u, client.InNamespace("default"))
				}
			})

			AfterEach(OncePerOrdered, func() {
				// Uninstall the CRD and wait for removal.
				Expect(envtest.UninstallCRDs(cfg, envtest.CRDInstallOptions{
					CRDs: []*apiextensionsv1.CustomResourceDefinition{crd},
				})).ToNot(HaveOccurred())
				Eventually(func() error {
					return k8sClient.Get(ctx, client.ObjectKeyFromObject(crd), crd)
				}, "30s").ShouldNot(Succeed())
			})

			generateOnCreateTable(suiteSpec.Tests.OnCreate)
			generateOnUpdateTable(suiteSpec.Tests.OnUpdate, crdFilename)
		})
	}
}

// GenerateCRDInstallTest generates a test that validates all CRDs for a given
// feature set can be installed successfully. This tests CRD schema validity
// across Kubernetes versions without running individual validation tests.
func GenerateCRDInstallTest(featureSet string) {
	allCRDs := allCRDsForFeatureSet(featureSet)

	It(fmt.Sprintf("should install all CRDs for feature set %q", featureSet), func() {
		Expect(k8sClient).ToNot(BeNil(), "Kubernetes client is not initialised")

		// Retry — a previous suite may still be deleting CRDs.
		// Deep copy inside the loop so that resourceVersion set by a failed
		// attempt does not poison the next retry.
		var crds []*apiextensionsv1.CustomResourceDefinition
		Eventually(func() error {
			crdsToInstall := make([]*apiextensionsv1.CustomResourceDefinition, len(allCRDs))
			for i, c := range allCRDs {
				crdsToInstall[i] = c.DeepCopy()
			}
			var err error
			crds, err = envtest.InstallCRDs(cfg, envtest.CRDInstallOptions{
				CRDs: crdsToInstall,
			})
			return err
		}, "60s", "1s").Should(Succeed(), "all CRDs should install without error")
		Expect(crds).To(HaveLen(len(allCRDs)), "all CRDs should have been installed")
		Expect(envtest.WaitForCRDs(cfg, crds, envtest.CRDInstallOptions{})).To(Succeed())

		// Uninstall after validation.
		Expect(envtest.UninstallCRDs(cfg, envtest.CRDInstallOptions{
			CRDs: crds,
		})).ToNot(HaveOccurred())
	})
}

// loadVersionedCRD loads the CRD and keeps only the version matching the suite spec.
func loadVersionedCRD(suiteSpec SuiteSpec, crdFilename string) (*apiextensionsv1.CustomResourceDefinition, error) {
	crd, err := loadCRDFromFile(crdFilename)
	if err != nil {
		return nil, fmt.Errorf("could not load CRD: %w", err)
	}

	if suiteSpec.Version == "" {
		return crd, nil
	}

	crdVersions := []apiextensionsv1.CustomResourceDefinitionVersion{}
	for _, version := range crd.Spec.Versions {
		if version.Name != suiteSpec.Version {
			continue
		}
		version.Storage = true
		version.Served = true
		crdVersions = append(crdVersions, version)
	}

	if len(crdVersions) == 0 {
		return nil, fmt.Errorf("could not find CRD version matching version %s", suiteSpec.Version)
	}

	crd.Spec.Versions = crdVersions
	return crd, nil
}

// generateSuiteName creates a descriptive suite name including the GVR, feature set, and filename.
func generateSuiteName(suiteSpec SuiteSpec, crdFilename string) string {
	crd, err := loadCRDFromFile(crdFilename)
	if err != nil {
		return suiteSpec.Name
	}

	featureSet := crd.Annotations["release.openshift.io/feature-set"]
	filename := filepath.Base(crdFilename)

	gvr := schema.GroupVersionResource{
		Group:    crd.Spec.Group,
		Resource: crd.Spec.Names.Plural,
		Version:  suiteSpec.Version,
	}

	return fmt.Sprintf("[%s][FeatureSet=%q][File=%v] %s",
		gvr.String(), featureSet, filename, suiteSpec.Name)
}

// newUnstructuredsFor creates unstructured objects for each version of the CRD, for cleanup.
func newUnstructuredsFor(crd *apiextensionsv1.CustomResourceDefinition) []*unstructured.Unstructured {
	out := []*unstructured.Unstructured{}
	for _, version := range crd.Spec.Versions {
		u := &unstructured.Unstructured{}
		u.SetAPIVersion(fmt.Sprintf("%s/%s", crd.Spec.Group, version.Name))
		u.SetKind(crd.Spec.Names.Kind)
		out = append(out, u)
	}
	return out
}

// generateOnCreateTable generates a table of tests from the defined OnCreate tests.
func generateOnCreateTable(onCreateTests []OnCreateTestSpec) {
	type onCreateTableInput struct {
		initial       []byte
		expected      []byte
		expectedError string
	}

	var assertOnCreate interface{} = func(in onCreateTableInput) {
		initialObj, err := newUnstructuredFrom(in.initial)
		Expect(err).ToNot(HaveOccurred(), "initial data should be a valid Kubernetes YAML resource")

		err = k8sClient.Create(ctx, initialObj)
		if in.expectedError != "" {
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(in.expectedError))
			return
		}
		Expect(err).ToNot(HaveOccurred())

		if len(in.expected) > 0 {
			gotObj := newEmptyUnstructuredFrom(initialObj)
			Expect(k8sClient.Get(ctx, objectKey(initialObj), gotObj)).To(Succeed())

			expectedObj, err := newUnstructuredFrom(in.expected)
			Expect(err).ToNot(HaveOccurred(), "expected data should be a valid Kubernetes YAML resource when no expected error is provided")

			expectedObj.SetName(gotObj.GetName())
			expectedObj.SetNamespace(gotObj.GetNamespace())

			assertObjectMatch(gotObj, expectedObj)
		}
	}

	tableEntries := []interface{}{assertOnCreate}

	for _, testEntry := range onCreateTests {
		tableEntries = append(tableEntries, Entry(testEntry.Name, onCreateTableInput{
			initial:       []byte(testEntry.Initial),
			expected:      []byte(testEntry.Expected),
			expectedError: testEntry.ExpectedError,
		}))
	}

	if len(tableEntries) > 1 {
		DescribeTable("On Create", tableEntries...)
	}
}

// generateOnUpdateTable generates a table of tests from the defined OnUpdate tests.
func generateOnUpdateTable(onUpdateTests []OnUpdateTestSpec, crdFileName string) {
	type onUpdateTableInput struct {
		crdPatches          []Patch
		initial             []byte
		updated             []byte
		expected            []byte
		expectedError       string
		expectedStatusError string
	}

	var assertOnUpdate interface{} = func(in onUpdateTableInput) {
		var originalCRDObjectKey client.ObjectKey
		var originalCRDSpec apiextensionsv1.CustomResourceDefinitionSpec

		initialObj, err := newUnstructuredFrom(in.initial)
		Expect(err).ToNot(HaveOccurred(), "initial data should be a valid Kubernetes YAML resource")

		if len(in.crdPatches) > 0 {
			patchedCRD, err := getPatchedCRD(crdFileName, in.crdPatches)
			Expect(err).ToNot(HaveOccurred(), "could not load patched crd")

			originalCRDObjectKey = objectKey(patchedCRD)

			originalCRD := &apiextensionsv1.CustomResourceDefinition{}
			Expect(k8sClient.Get(ctx, originalCRDObjectKey, originalCRD)).To(Succeed())

			originalCRDSpec = *originalCRD.Spec.DeepCopy()
			originalCRD.Spec = patchedCRD.Spec

			// Add a sentinel field so that we can check that the schema update has persisted.
			originalCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["sentinel"] = apiextensionsv1.JSONSchemaProps{
				Type: "string",
				Enum: []apiextensionsv1.JSON{
					{Raw: []byte(fmt.Sprintf(`"%s+patched"`, initialObj.GetUID()))},
				},
			}
			initialObj.Object["sentinel"] = initialObj.GetUID() + "+patched"

			Expect(k8sClient.Update(ctx, originalCRD)).To(Succeed(), "failed updating patched CRD schema")
		}

		initialStatus, hasStatus, err := unstructured.NestedFieldNoCopy(initialObj.Object, "status")
		Expect(err).ToNot(HaveOccurred())

		// Use an eventually here, so that we retry until the sentinel correctly applies.
		Eventually(func() error {
			return k8sClient.Create(ctx, initialObj)
		}, "5s").Should(Succeed(), "initial object should create successfully")

		if hasStatus && initialStatus != nil {
			Expect(unstructured.SetNestedField(initialObj.Object, initialStatus, "status")).To(Succeed())
			Expect(k8sClient.Status().Update(ctx, initialObj)).ToNot(HaveOccurred(), "initial object status should update successfully")
		}

		if len(in.crdPatches) > 0 {
			originalCRD := &apiextensionsv1.CustomResourceDefinition{}
			Expect(k8sClient.Get(ctx, originalCRDObjectKey, originalCRD)).To(Succeed())

			originalCRD.Spec = originalCRDSpec

			// Add a sentinel field so that we can check that the schema update has persisted.
			originalCRD.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["sentinel"] = apiextensionsv1.JSONSchemaProps{
				Type: "string",
				Enum: []apiextensionsv1.JSON{
					{Raw: []byte(fmt.Sprintf(`"%s+restored"`, initialObj.GetUID()))},
				},
			}

			Expect(k8sClient.Update(ctx, originalCRD)).To(Succeed())

			Eventually(func() error {
				updatedObj := initialObj.DeepCopy()
				updatedObj.Object["sentinel"] = initialObj.GetUID() + "+restored"

				return k8sClient.Update(ctx, updatedObj)
			}, "5s").Should(Succeed(), "Sentinel should be persisted")

			// Drop the sentinel field now we know the rest of the CRD schema is up to date.
			originalCRD.Spec = originalCRDSpec
			Expect(k8sClient.Update(ctx, originalCRD)).To(Succeed())
		}

		gotObj := newEmptyUnstructuredFrom(initialObj)
		Expect(k8sClient.Get(ctx, objectKey(initialObj), gotObj)).To(Succeed())

		updatedObj, err := newUnstructuredFrom(in.updated)
		Expect(err).ToNot(HaveOccurred(), "updated data should be a valid Kubernetes YAML resource")

		updatedObjStatus, hasUpdatedStatus, err := unstructured.NestedFieldNoCopy(updatedObj.Object, "status")
		Expect(err).ToNot(HaveOccurred())

		updatedObj.SetName(gotObj.GetName())
		updatedObj.SetNamespace(gotObj.GetNamespace())
		updatedObj.SetResourceVersion(gotObj.GetResourceVersion())

		err = k8sClient.Update(ctx, updatedObj)
		if in.expectedError != "" {
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(in.expectedError))
			return
		}
		Expect(err).ToNot(HaveOccurred(), "unexpected error updating spec")

		if hasUpdatedStatus && updatedObjStatus != nil {
			Expect(unstructured.SetNestedField(updatedObj.Object, updatedObjStatus, "status")).To(Succeed())

			err := k8sClient.Status().Update(ctx, updatedObj)
			if in.expectedStatusError != "" {
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(in.expectedStatusError))
				return
			}
			Expect(err).ToNot(HaveOccurred(), "unexpected error updating status")
		}

		if len(in.expected) > 0 {
			Expect(k8sClient.Get(ctx, objectKey(initialObj), gotObj)).To(Succeed())

			expectedObj, err := newUnstructuredFrom(in.expected)
			Expect(err).ToNot(HaveOccurred(), "expected data should be a valid Kubernetes YAML resource when no expected error is provided")

			expectedObj.SetName(gotObj.GetName())
			expectedObj.SetNamespace(gotObj.GetNamespace())

			assertObjectMatch(gotObj, expectedObj)
		}
	}

	tableEntries := []interface{}{assertOnUpdate}

	for _, testEntry := range onUpdateTests {
		tableEntries = append(tableEntries, Entry(testEntry.Name, onUpdateTableInput{
			crdPatches:          testEntry.InitialCRDPatches,
			initial:             []byte(testEntry.Initial),
			updated:             []byte(testEntry.Updated),
			expected:            []byte(testEntry.Expected),
			expectedError:       testEntry.ExpectedError,
			expectedStatusError: testEntry.ExpectedStatusError,
		}))
	}

	if len(tableEntries) > 1 {
		DescribeTable("On Update", tableEntries...)
	}
}

// getPatchedCRD loads a CRD from file and applies JSON patches to it.
func getPatchedCRD(crdFileName string, patches []Patch) (*apiextensionsv1.CustomResourceDefinition, error) {
	patch := yamlpatch.Patch{}

	for _, p := range patches {
		patch = append(patch, yamlpatch.Operation{
			Op:    yamlpatch.Op(p.Op),
			Path:  yamlpatch.OpPath(p.Path),
			Value: yamlpatch.NewNode(p.Value),
		})
	}

	baseDoc, err := os.ReadFile(crdFileName)
	if err != nil {
		return nil, fmt.Errorf("could not read file %q: %w", crdFileName, err)
	}

	patchedDoc, err := patch.Apply(baseDoc)
	if err != nil {
		return nil, fmt.Errorf("could not apply patch: %w", err)
	}

	placeholderWrapper := yamlpatch.NewPlaceholderWrapper("{{", "}}")
	patchedData := bytes.NewBuffer(placeholderWrapper.Unwrap(patchedDoc))

	crd := &apiextensionsv1.CustomResourceDefinition{}
	if err := kyaml.Unmarshal(patchedData.Bytes(), crd); err != nil {
		return nil, fmt.Errorf("could not unmarshal CRD: %w", err)
	}

	return crd, nil
}

func newUnstructuredFrom(raw []byte) (*unstructured.Unstructured, error) {
	u := &unstructured.Unstructured{}
	if err := k8syaml.Unmarshal(raw, &u.Object); err != nil {
		return nil, fmt.Errorf("could not unmarshal raw YAML: %w", err)
	}

	u.SetGenerateName("test-")
	u.SetName("")
	u.SetNamespace("default")

	return u, nil
}

func newEmptyUnstructuredFrom(initial *unstructured.Unstructured) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	if initial != nil {
		u.GetObjectKind().SetGroupVersionKind(initial.GetObjectKind().GroupVersionKind())
	}
	return u
}

func objectKey(obj client.Object) client.ObjectKey {
	return client.ObjectKey{Namespace: obj.GetNamespace(), Name: obj.GetName()}
}

// assertObjectMatch compares spec and status fields of two unstructured objects,
// ignoring metadata differences set by the server.
func assertObjectMatch(got, expected *unstructured.Unstructured) {
	expectedSpec, hasExpectedSpec, _ := unstructured.NestedMap(expected.Object, "spec")
	if hasExpectedSpec {
		gotSpec, _, _ := unstructured.NestedMap(got.Object, "spec")
		Expect(gotSpec).To(Equal(expectedSpec))
	}

	expectedStatus, hasExpectedStatus, _ := unstructured.NestedMap(expected.Object, "status")
	if hasExpectedStatus {
		gotStatus, _, _ := unstructured.NestedMap(got.Object, "status")
		Expect(gotStatus).To(Equal(expectedStatus))
	}
}
