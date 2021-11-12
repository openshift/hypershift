package render

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
)

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
