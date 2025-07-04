package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/yaml"

	"github.com/google/go-cmp/cmp"
	"github.com/onsi/gomega/types"
)

// CompareWithFixture will compare output with a test fixture and allows to automatically update them
// by setting the UPDATE env var.
// If output is not a []byte or string, it will get serialized as yaml prior to the comparison.
// The fixtures are stored in $PWD/testdata/prefix${testName}.yaml
func CompareWithFixture(t *testing.T, output interface{}, opts ...option) {
	t.Helper()
	options := &options{
		Extension: ".yaml",
	}
	for _, opt := range opts {
		opt(options)
	}

	var serializedOutput []byte
	switch v := output.(type) {
	case []byte:
		serializedOutput = v
	case string:
		serializedOutput = []byte(v)
	default:
		serialized, err := yaml.Marshal(v)
		if err != nil {
			t.Fatalf("failed to yaml marshal output of type %T: %v", output, err)
		}
		serializedOutput = serialized
	}

	golden, err := golden(t, options)
	if err != nil {
		t.Fatalf("failed to get absolute path to testdata file: %v", err)
	}
	if os.Getenv("UPDATE") != "" {
		if err := os.MkdirAll(filepath.Dir(golden), 0755); err != nil {
			t.Fatalf("failed to create fixture directory: %v", err)
		}
		if err := os.WriteFile(golden, serializedOutput, 0644); err != nil {
			t.Fatalf("failed to write updated fixture: %v", err)
		}
	}
	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("failed to read testdata file: %v", err)
	}

	if diff := cmp.Diff(string(expected), string(serializedOutput)); diff != "" {
		t.Errorf("got diff between expected and actual result:\nfile: %s\ndiff:\n%s\n\nIf this is expected, re-run the test with `UPDATE=true go test ./...` to update the fixtures.", golden, diff)
	}
}

type options struct {
	Prefix    string
	Suffix    string
	Extension string

	SubDir string
}

type option func(*options)

func WithExtension(extension string) option {
	return func(opts *options) {
		opts.Extension = extension
	}
}

func WithSuffix(suffix string) option {
	return func(opts *options) {
		opts.Suffix = suffix
	}
}

func WithSubDir(subDir string) option {
	return func(opts *options) {
		opts.SubDir = subDir
	}
}

// golden determines the golden file to use
func golden(t *testing.T, opts *options) (string, error) {
	if opts.Extension == "" {
		opts.Extension = ".yaml"
	}
	return filepath.Abs(filepath.Join("testdata", opts.SubDir, sanitizeFilename(opts.Prefix+t.Name()+opts.Suffix)) + opts.Extension)
}

func sanitizeFilename(s string) string {
	result := strings.Builder{}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || r == '.' || (r >= '0' && r <= '9') {
			// The thing is documented as returning a nil error so lets just drop it
			_, _ = result.WriteRune(r)
			continue
		}
		if !strings.HasSuffix(result.String(), "_") {
			result.WriteRune('_')
		}
	}
	return "zz_fixture_" + result.String()
}

// RuntimeObjectIgnoreRvTypeMeta compares two kubernetes objects, ignoring their resource
// version and TypeMeta. It is what you want 99% of the time.
var RuntimeObjectIgnoreRvTypeMeta = cmp.Comparer(func(x, y runtime.Object) bool {
	xCopy := x.DeepCopyObject()
	yCopy := y.DeepCopyObject()
	CleanRVAndTypeMeta(xCopy)
	CleanRVAndTypeMeta(yCopy)
	return cmp.Diff(xCopy, yCopy) == ""
})

func CleanRVAndTypeMeta(r runtime.Object) {
	if metaObject, ok := r.(metav1.Object); ok {
		metaObject.SetResourceVersion("")
	}
	if typeObject, ok := r.(interface{ SetGroupVersionKind(schema.GroupVersionKind) }); ok {
		typeObject.SetGroupVersionKind(schema.GroupVersionKind{})
	}
	if _, isList := r.(metav1.ListInterface); isList {
		objects, err := apimeta.ExtractList(r)
		// ExtractList only errors if the list is not a list, so this
		// should never error.
		if err != nil {
			panic(fmt.Sprintf("extract list failed: %v", err))
		}
		for _, item := range objects {
			CleanRVAndTypeMeta(item)
		}
	}
}

// MarshalYamlAndDiff diffs the yaml representation of two runtime.Objects,
// useful for getting a human-readable diff for bigger objects.
func MarshalYamlAndDiff(a, b runtime.Object, t *testing.T) string {
	t.Helper()

	CleanRVAndTypeMeta(a)
	CleanRVAndTypeMeta(b)
	aYAML, err := yaml.Marshal(a)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	bYAML, err := yaml.Marshal(b)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	return cmp.Diff(string(aYAML), string(bYAML))
}

// MatchExpected ensures that `cmp.Diff(actual, expected)` returns nothing.
// Usage looks like:
//
//	Expect(actual).To(MatchExpected(expected))
func MatchExpected(expected any, opts ...cmp.Option) types.GomegaMatcher {
	return WithTransform(func(actual any) string {
		return cmp.Diff(actual, expected, opts...)
	}, BeEmpty())

}

// CreateSupportedVersionsConfigMap creates a ConfigMap with supported OpenShift versions for testing
func CreateSupportedVersionsConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "supported-versions",
			Namespace: "test",
			Labels:    map[string]string{"hypershift.openshift.io/supported-versions": "true"},
		},
		Data: map[string]string{
			"server-version":     "test-server",
			"supported-versions": `{"versions":["4.19", "4.18", "4.17", "4.16", "4.15", "4.14"]}`,
		},
	}
}
