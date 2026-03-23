package envtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
	kyaml "sigs.k8s.io/yaml"
)

// LoadTestSuiteSpecs recursively walks the given paths looking for any file with the suffix `.testsuite.yaml`.
func LoadTestSuiteSpecs(paths ...string) ([]SuiteSpec, error) {
	suiteFiles := make(map[string]struct{})

	for _, path := range paths {
		if err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !info.IsDir() && strings.HasSuffix(path, ".testsuite.yaml") {
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

// loadSuiteFile loads an individual SuiteSpec from the given file name.
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

	return s, nil
}

// GenerateTestSuite generates a Ginkgo test suite from the provided SuiteSpec.
// CRDs are expected to be already installed in the BeforeSuite.
func GenerateTestSuite(suiteSpec SuiteSpec) {
	// Derive the GVK for cleanup from the first onCreate test's initial YAML.
	var cleanupGVK schema.GroupVersionKind
	if len(suiteSpec.Tests.OnCreate) > 0 {
		u, err := newUnstructuredFrom([]byte(suiteSpec.Tests.OnCreate[0].Initial))
		if err == nil {
			cleanupGVK = u.GroupVersionKind()
		}
	} else if len(suiteSpec.Tests.OnUpdate) > 0 {
		u, err := newUnstructuredFrom([]byte(suiteSpec.Tests.OnUpdate[0].Initial))
		if err == nil {
			cleanupGVK = u.GroupVersionKind()
		}
	}

	Describe(suiteSpec.Name, func() {
		AfterEach(func() {
			if cleanupGVK.Kind != "" {
				u := &unstructured.Unstructured{}
				u.SetGroupVersionKind(cleanupGVK)
				Expect(k8sClient.DeleteAllOf(ctx, u, client.InNamespace("default")))
			}
		})

		generateOnCreateTable(suiteSpec.Tests.OnCreate)
		generateOnUpdateTable(suiteSpec.Tests.OnUpdate)
	})
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
func generateOnUpdateTable(onUpdateTests []OnUpdateTestSpec) {
	type onUpdateTableInput struct {
		initial             []byte
		updated             []byte
		expected            []byte
		expectedError       string
		expectedStatusError string
	}

	var assertOnUpdate interface{} = func(in onUpdateTableInput) {
		initialObj, err := newUnstructuredFrom(in.initial)
		Expect(err).ToNot(HaveOccurred(), "initial data should be a valid Kubernetes YAML resource")

		initialStatus, hasStatus, err := unstructured.NestedFieldNoCopy(initialObj.Object, "status")
		Expect(err).ToNot(HaveOccurred())

		Eventually(func() error {
			return k8sClient.Create(ctx, initialObj)
		}, "5s").Should(Succeed(), "initial object should create successfully")

		if hasStatus && initialStatus != nil {
			Expect(unstructured.SetNestedField(initialObj.Object, initialStatus, "status")).To(Succeed())
			Expect(k8sClient.Status().Update(ctx, initialObj)).ToNot(HaveOccurred(), "initial object status should update successfully")
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
