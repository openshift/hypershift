package render

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

func TestIgnitionConfigRendering(t *testing.T) {
	testCases := []struct {
		name   string
		params *ClusterParams
	}{
		{
			name:   "No ssh key",
			params: &ClusterParams{},
		},
		{
			name:   "Single ssh key",
			params: &ClusterParams{SSHKey: "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC7xaGqJaFd51jCl+MjZzgH1WfgKbNmn+AbvRXOabeNYNRTZiRNcFlWHQPxL/fFWiJ5rDkyTRm6dI49TflU5lMSOcKwoO0sZlMbrDrUeDf2cy/7KffpAto+Te8vB4udAERMJHY89v9/RF6GgMLpW+lbIT3Gyj+MbIF8aAz0vt6VJA8Ptwq2SlxWSPLbxoe5nNP1JaOubG4Arm6t75smJ+wvexV8d9duvFWig2MW5lMTAa6QpSAp6Gd03dWSUiH5++dk3vlNMR9hZMv7/DWqyauGi0MYtuywQqVWr3YMQve72VJTo/qVhvfFylKEFTKA0h5Cl3ziL0DbgM/RDsUqaLynB7b6jAJkhXd02wv6+IkHly02SEnLHGJs50uK7J7GdAWWbKfRByVGg5kP5DwiTEln357ukT7OH8Ys6PNd0Lzzy/oA4Gv+uDzI1RMMBsTcv3SwASuht+EZzQ5hoSCkM6QoEtpruSCEdCtvTEq9idcrVijKbYURtrDdH5WAN9ZYUF13s94870srbG3uavvT2G1IcWjBjiVVoJM8cifYnTHllHX/oPw9iZxhjlrC5Uc+dgRhnpoRYMar30Kg/No1GYj2EPEZgvHVde6KqActTFnD0K5xJEAUzKutu7TDUePm+MYREt4HMeT4LxsVUar9Aak5pgmUKLqKHLY8NeQxWtKMbQ== alvaro@localhost.localdomain"},
		},
		{
			name: "Multiple ssh keys",
			params: &ClusterParams{
				SSHKey: `ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC7xaGqJaFd51jCl+MjZzgH1WfgKbNmn+AbvRXOabeNYNRTZiRNcFlWHQPxL/fFWiJ5rDkyTRm6dI49TflU5lMSOcKwoO0sZlMbrDrUeDf2cy/7KffpAto+Te8vB4udAERMJHY89v9/RF6GgMLpW+lbIT3Gyj+MbIF8aAz0vt6VJA8Ptwq2SlxWSPLbxoe5nNP1JaOubG4Arm6t75smJ+wvexV8d9duvFWig2MW5lMTAa6QpSAp6Gd03dWSUiH5++dk3vlNMR9hZMv7/DWqyauGi0MYtuywQqVWr3YMQve72VJTo/qVhvfFylKEFTKA0h5Cl3ziL0DbgM/RDsUqaLynB7b6jAJkhXd02wv6+IkHly02SEnLHGJs50uK7J7GdAWWbKfRByVGg5kP5DwiTEln357ukT7OH8Ys6PNd0Lzzy/oA4Gv+uDzI1RMMBsTcv3SwASuht+EZzQ5hoSCkM6QoEtpruSCEdCtvTEq9idcrVijKbYURtrDdH5WAN9ZYUF13s94870srbG3uavvT2G1IcWjBjiVVoJM8cifYnTHllHX/oPw9iZxhjlrC5Uc+dgRhnpoRYMar30Kg/No1GYj2EPEZgvHVde6KqActTFnD0K5xJEAUzKutu7TDUePm+MYREt4HMeT4LxsVUar9Aak5pgmUKLqKHLY8NeQxWtKMbQ== alvaro@localhost.localdomain
ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAACAQC7xaGqJaFd51jCl+MjZzgH1WfgKbNmn+AbvRXOabeNYNRTZiRNcFlWHQPxL/fFWiJ5rDkyTRm6dI49TflU5lMSOcKwoO0sZlMbrDrUeDf2cy/7KffpAto+Te8vB4udAERMJHY89v9/RF6GgMLpW+lbIT3Gyj+MbIF8aAz0vt6VJA8Ptwq2SlxWSPLbxoe5nNP1JaOubG4Arm6t75smJ+wvexV8d9duvFWig2MW5lMTAa6QpSAp6Gd03dWSUiH5++dk3vlNMR9hZMv7/DWqyauGi0MYtuywQqVWr3YMQve72VJTo/qVhvfFylKEFTKA0h5Cl3ziL0DbgM/RDsUqaLynB7b6jAJkhXd02wv6+IkHly02SEnLHGJs50uK7J7GdAWWbKfRByVGg5kP5DwiTEln357ukT7OH8Ys6PNd0Lzzy/oA4Gv+uDzI1RMMBsTcv3SwASuht+EZzQ5hoSCkM6QoEtpruSCEdCtvTEq9idcrVijKbYURtrDdH5WAN9ZYUF13s94870srbG3uavvT2G1IcWjBjiVVoJM8cifYnTHllHX/oPw9iZxhjlrC5Uc+dgRhnpoRYMar30Kg/No1GYj2EPEZgvHVde6KqActTFnD0K5xJEAUzKutu7TDUePm+MYREt4HMeT4LxsVUar9Aak5pgmUKLqKHLY8NeQxWtKMbQ== alvaro@localhost.localdomain`,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newClusterManifestContext(nil, map[string]string{"release": "1.2.3"}, tc.params, nil, nil, nil)
			ctx.ignitionConfigs()
			for name, value := range ctx.manifests {
				CompareWithFixture(t, value, WithExtension(name))
			}
		})
	}
}

// CompareWithFixture will compare output with a test fixture and allows to automatically update them
// by setting the UPDATE env var.
// If output is not a []byte or string, it will get serialized as yaml prior to the comparison.
// The fixtures are stored in $PWD/testdata/prefix${testName}.yaml
func CompareWithFixture(t *testing.T, output interface{}, opts ...Option) {
	t.Helper()
	options := &Options{
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
		if err := ioutil.WriteFile(golden, serializedOutput, 0644); err != nil {
			t.Fatalf("failed to write updated fixture: %v", err)
		}
	}
	expected, err := ioutil.ReadFile(golden)
	if err != nil {
		t.Fatalf("failed to read testdata file: %v", err)
	}

	if diff := cmp.Diff(string(expected), string(serializedOutput)); diff != "" {
		t.Errorf("got diff between expected and actual result:\nfile: %s\ndiff:\n%s\n\nIf this is expected, re-run the test with `UPDATE=true go test ./...` to update the fixtures.", golden, diff)
	}
}

type Options struct {
	Prefix    string
	Suffix    string
	Extension string
}

type Option func(*Options)

func WithPrefix(prefix string) Option {
	return func(o *Options) {
		o.Prefix = prefix
	}
}

func WithSuffix(suffix string) Option {
	return func(o *Options) {
		o.Suffix = suffix
	}
}

func WithExtension(extension string) Option {
	return func(o *Options) {
		o.Extension = extension
	}
}

// golden determines the golden file to use
func golden(t *testing.T, opts *Options) (string, error) {
	if opts.Extension == "" {
		opts.Extension = ".yaml"
	}
	return filepath.Abs(filepath.Join("testdata", sanitizeFilename(opts.Prefix+t.Name()+opts.Suffix)) + opts.Extension)
}

func sanitizeFilename(s string) string {
	result := strings.Builder{}
	for _, r := range s {
		if (r >= 'a' && r < 'z') || (r >= 'A' && r < 'Z') || r == '_' || r == '.' || (r >= '0' && r <= '9') {
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

func clusterParamsSecrets() *corev1.SecretList {
	return &corev1.SecretList{
		Items: []corev1.Secret{
			{ObjectMeta: metav1.ObjectMeta{Name: "root-ca"}, Data: map[string][]byte{"ca.crt": []byte("foo")}},
			{ObjectMeta: metav1.ObjectMeta{Name: "bootstrap-kubeconfig"}, Data: map[string][]byte{"kubeconfig": []byte("kk")}},
		},
	}
}

func clusterParamsConfigMaps() *corev1.ConfigMapList {
	return &corev1.ConfigMapList{
		Items: []corev1.ConfigMap{
			{ObjectMeta: metav1.ObjectMeta{Name: "combined-ca"}, Data: map[string]string{"ca.crt": "foo"}},
		},
	}
}

func TestMachineConfigServerRendering(t *testing.T) {
	testCases := []struct {
		name   string
		params *ClusterParams
	}{
		{
			name:   "No extra AWS tags",
			params: &ClusterParams{PlatformType: "AWS"},
		},
		{
			name: "AWS resource tags are passed on",
			params: &ClusterParams{
				PlatformType: "AWS",
				AWSResourceTags: []hyperv1.AWSResourceTag{
					{Key: "foo", Value: "bar"},
					{Key: "baz", Value: "bar"},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := newClusterManifestContext(nil, map[string]string{"release": "1.2.3"}, tc.params, nil, clusterParamsSecrets(), clusterParamsConfigMaps())
			ctx.machineConfigServer()

			renderedManifests, err := ctx.renderManifests()
			if err != nil {
				t.Fatalf("failed to render manifests: %v", err)
			}

			value, exists := renderedManifests["machine-config-server-configmap.yaml"]
			if !exists {
				t.Fatalf("Bug: machine-config-server/machine-config-server-configmap.yaml manifest does not exist")
			}

			var configMap corev1.ConfigMap
			if err := yaml.Unmarshal(value, &configMap); err != nil {
				t.Fatalf("failed to unmarshal manifest into a configmap: %v, raw manifest: %s", err, string(value))
			}

			configRaw, exists := configMap.Data["cluster-infrastructure-02-config.yaml"]
			if !exists {
				t.Fatalf("configmap %s does not have a 'cluster-infrastructure-02-config.yaml' key", string(value))
			}

			var infrastructure configv1.Infrastructure
			if err := yaml.Unmarshal([]byte(configRaw), &infrastructure); err != nil {
				t.Fatalf("failed to unmarshal 'data' key into a configv1.Infrastructure: %v, raw: %s", err, configRaw)
			}

			if diff := cmp.Diff(configV1RTToHyperV1RT(infrastructure.Status.PlatformStatus.AWS.ResourceTags), tc.params.AWSResourceTags); diff != "" {
				t.Errorf("AWS resource tags in infrastructure differ from input: %s\nRaw manifest: %s", diff, string(value))
			}

		})
	}

}

func configV1RTToHyperV1RT(in []configv1.AWSResourceTag) []hyperv1.AWSResourceTag {
	var result []hyperv1.AWSResourceTag
	for _, entry := range in {
		result = append(result, hyperv1.AWSResourceTag{Key: entry.Key, Value: entry.Value})
	}

	return result
}
