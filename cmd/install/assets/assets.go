package assets

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"path/filepath"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed hypershift-operator/*
//go:embed cluster-api/*
//go:embed cluster-api-provider-aws/*
//go:embed cluster-api-provider-ibmcloud/*
//go:embed cluster-api-provider-kubevirt/*
//go:embed cluster-api-provider-agent/*
//go:embed cluster-api-provider-azure/*
var crds embed.FS

//go:embed recordingrules/*
var recordingRules embed.FS

const capiLabel = "cluster.x-k8s.io/v1beta1"

// capiResources specifies which CRDs should get labelled with capiLabel
// to satisfy CAPI contracts. There might be a way to achieve this during CRD
// generation, but for now we're just post-processing at runtime here.
var capiResources = map[string]string{
	"cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsclusters.yaml":                      "v1beta1",
	"cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsmachines.yaml":                      "v1beta1",
	"cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsmachinetemplates.yaml":              "v1beta1",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmpowervsclusters.yaml":          "v1beta1",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmpowervsimages.yaml":            "v1beta1",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmpowervsmachines.yaml":          "v1beta1",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmpowervsmachinetemplates.yaml":  "v1beta1",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmvpcclusters.yaml":              "v1alpha4",
	"hypershift-operator/hypershift.openshift.io_hostedcontrolplanes.yaml":                           "v1beta1",
	"cluster-api-provider-kubevirt/infrastructure.cluster.x-k8s.io_kubevirtclusters.yaml":            "v1alpha1",
	"cluster-api-provider-kubevirt/infrastructure.cluster.x-k8s.io_kubevirtmachines.yaml":            "v1alpha1",
	"cluster-api-provider-kubevirt/infrastructure.cluster.x-k8s.io_kubevirtmachinetemplates.yaml":    "v1alpha1",
	"cluster-api-provider-agent/capi-provider.agent-install.openshift.io_agentclusters.yaml":         "v1alpha1",
	"cluster-api-provider-agent/capi-provider.agent-install.openshift.io_agentmachinetemplates.yaml": "v1alpha1",
	"cluster-api-provider-agent/capi-provider.agent-install.openshift.io_agentmachines.yaml":         "v1alpha1",
	"cluster-api-provider-azure/infrastructure.cluster.x-k8s.io_azureclusters.yaml":                  "v1beta1",
	"cluster-api-provider-azure/infrastructure.cluster.x-k8s.io_azuremachines.yaml":                  "v1beta1",
	"cluster-api-provider-azure/infrastructure.cluster.x-k8s.io_azuremachinetemplates.yaml":          "v1beta1",
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
func CustomResourceDefinitions(include func(path string) bool, transform func(*apiextensionsv1.CustomResourceDefinition)) []crclient.Object {
	var allCrds []crclient.Object
	err := fs.WalkDir(crds, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			panic(err)
		}
		if filepath.Ext(path) != ".yaml" {
			return nil
		}
		if include(path) {
			crd := getCustomResourceDefinition(crds, path)
			if transform != nil {
				transform(crd)
			}
			allCrds = append(allCrds, crd)
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

// recordingRuleSpec is meant to return all prometheus rule groups in a PrometheusRuleSpec.
// At the moment we have only one.
func recordingRuleSpec() prometheusoperatorv1.PrometheusRuleSpec {
	var spec prometheusoperatorv1.PrometheusRuleSpec
	err := fs.WalkDir(recordingRules, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			panic(err)
		}
		if filepath.Ext(path) != ".yaml" {
			return nil
		}
		spec = getRecordingRuleSpec(recordingRules, path)
		return nil
	})
	if err != nil {
		panic(err)
	}

	return spec
}

// getRecordingRuleSpec unmarshals a prometheusoperatorv1.PrometheusRuleSpec from file.
func getRecordingRuleSpec(files embed.FS, file string) prometheusoperatorv1.PrometheusRuleSpec {
	var recordingRuleSpec prometheusoperatorv1.PrometheusRuleSpec
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(getContents(files, file)), 100).Decode(&recordingRuleSpec); err != nil {
		panic(err)
	}

	return recordingRuleSpec
}
