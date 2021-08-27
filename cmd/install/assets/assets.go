package assets

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"path/filepath"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed hypershift-operator/*
//go:embed cluster-api/*
//go:embed cluster-api-provider-aws/*
//go:embed cluster-api-provider-ibmcloud/*
var crds embed.FS

//go:embed recordingrules/*.promql
var recordingRules embed.FS

var recordingRulesByName = map[string]string{
	"hypershift:controlplane:component_memory_usage":       "recordingrules/controlplane_memory_usage.promql",
	"hypershift:controlplane:component_cpu_usage_seconds":  "recordingrules/controlplane_cpu_usage.promql",
	"hypershift:controlplane:component_api_requests_total": "recordingrules/controlplane_api_requests.promql",
	"hypershift:operator:component_api_requests_total":     "recordingrules/operator_api_requests.promql",
}

const capiLabel = "cluster.x-k8s.io/v1beta1"

// capiResources specifies which CRDs should get labelled with capiLabel
// to satisfy CAPI contracts. There might be a way to achieve this during CRD
// generation, but for now we're just post-processing at runtime here.
var capiResources = map[string]string{
	"cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsclusters.yaml":         "v1alpha4",
	"cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsmachines.yaml":         "v1alpha4",
	"cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsmachinetemplates.yaml": "v1alpha4",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmvpcclusters.yaml": "v1alpha4",
	"hypershift-operator/hypershift.openshift.io_hostedcontrolplanes.yaml":              "v1alpha1",
}

func getContents(fs embed.FS, file string) []byte {
	f, err := fs.Open(file)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := f.Close(); err != nil {
			panic(err)
		}
	}()
	b, err := io.ReadAll(f)
	if err != nil {
		panic(err)
	}
	return b
}

// CustomResourceDefinitions returns all existing CRDs as controller-runtime objects
func CustomResourceDefinitions(include func(path string) bool) []crclient.Object {
	var allCrds []crclient.Object
	err := fs.WalkDir(crds, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			panic(err)
		}
		if filepath.Ext(path) != ".yaml" {
			return nil
		}
		if include(path) {
			allCrds = append(allCrds, getCustomResourceDefinition(crds, path))
		}
		return nil
	})
	if err != nil {
		panic(err)
	}
	return allCrds
}

// getCustomResourceDefinition unmarshals a CRD from file. Note there's a hack
// here to strip leading YAML document separator which controller-gen creates
// even though there's only one object in the document.
func getCustomResourceDefinition(files embed.FS, file string) *apiextensionsv1.CustomResourceDefinition {
	b := getContents(files, file)
	repaired := bytes.Replace(b, []byte("\n---\n"), []byte(""), 1)
	crd := apiextensionsv1.CustomResourceDefinition{}
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(repaired), 100).Decode(&crd); err != nil {
		panic(err)
	}
	if label, hasLabel := capiResources[file]; hasLabel {
		if crd.Labels == nil {
			crd.Labels = map[string]string{}
		}
		crd.Labels[capiLabel] = label
	}
	return &crd
}
