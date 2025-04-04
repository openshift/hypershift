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

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

//go:embed hypershift-operator/*
//go:embed cluster-api/*
//go:embed cluster-api-provider-aws/*
//go:embed cluster-api-provider-ibmcloud/*
//go:embed cluster-api-provider-kubevirt/*
//go:embed cluster-api-provider-agent/*
//go:embed cluster-api-provider-azure/*
//go:embed cluster-api-provider-openstack/*
var CRDS embed.FS

//go:embed recordingrules/*
var recordingRules embed.FS

const capiLabel = "cluster.x-k8s.io/v1beta1"

// capiResources specifies which CRDs should get labelled with capiLabel
// to satisfy CAPI contracts. There might be a way to achieve this during CRD
// generation, but for now we're just post-processing at runtime here.
var capiResources = map[string]string{
	"cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsclusters.yaml":                        "v1beta2",
	"cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsmachines.yaml":                        "v1beta2",
	"cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsmachinetemplates.yaml":                "v1beta2",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmpowervsclusters.yaml":            "v1beta2",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmpowervsimages.yaml":              "v1beta2",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmpowervsmachines.yaml":            "v1beta2",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmpowervsmachinetemplates.yaml":    "v1beta2",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmvpcclusters.yaml":                "v1beta2",
	"hypershift-operator/zz_generated.crd-manifests/hostedcontrolplanes-Default.crd.yaml":              "v1beta1",
	"hypershift-operator/zz_generated.crd-manifests/hostedcontrolplanes-TechPreviewNoUpgrade.crd.yaml": "v1beta1",
	"cluster-api-provider-kubevirt/infrastructure.cluster.x-k8s.io_kubevirtclusters.yaml":              "v1alpha1",
	"cluster-api-provider-kubevirt/infrastructure.cluster.x-k8s.io_kubevirtmachines.yaml":              "v1alpha1",
	"cluster-api-provider-kubevirt/infrastructure.cluster.x-k8s.io_kubevirtmachinetemplates.yaml":      "v1alpha1",
	"cluster-api-provider-agent/capi-provider.agent-install.openshift.io_agentclusters.yaml":           "v1beta1",
	"cluster-api-provider-agent/capi-provider.agent-install.openshift.io_agentmachinetemplates.yaml":   "v1beta1",
	"cluster-api-provider-agent/capi-provider.agent-install.openshift.io_agentmachines.yaml":           "v1beta1",
	"cluster-api-provider-azure/infrastructure.cluster.x-k8s.io_azureclusters.yaml":                    "v1beta1",
	"cluster-api-provider-azure/infrastructure.cluster.x-k8s.io_azuremachines.yaml":                    "v1beta1",
	"cluster-api-provider-azure/infrastructure.cluster.x-k8s.io_azuremachinetemplates.yaml":            "v1beta1",
	"cluster-api-provider-openstack/openstack.k-orc.cloud_images.yaml":                                 "v1alpha1",
	"cluster-api-provider-openstack/infrastructure.cluster.x-k8s.io_openstackclustertemplates.yaml":    "v1beta1",
	"cluster-api-provider-openstack/infrastructure.cluster.x-k8s.io_openstackclusters.yaml":            "v1beta1",
	"cluster-api-provider-openstack/infrastructure.cluster.x-k8s.io_openstackmachines.yaml":            "v1beta1",
	"cluster-api-provider-openstack/infrastructure.cluster.x-k8s.io_openstackmachinetemplates.yaml":    "v1beta1",
	"cluster-api-provider-openstack/infrastructure.cluster.x-k8s.io_openstackfloatingippools.yaml":     "v1alpha1",
	"cluster-api-provider-openstack/infrastructure.cluster.x-k8s.io_openstackservers.yaml":             "v1alpha1",
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
func CustomResourceDefinitions(include func(path string, crd *apiextensionsv1.CustomResourceDefinition) bool, transform func(*apiextensionsv1.CustomResourceDefinition)) []crclient.Object {
	var allCrds []crclient.Object
	err := fs.WalkDir(CRDS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			panic(err)
		}
		if filepath.Ext(path) != ".yaml" {
			return nil
		}
		crd := getCustomResourceDefinition(CRDS, path)
		if include(path, crd) {

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

// prometheusRuleSpec is meant to return all prometheus rule groups in a PrometheusRuleSpec.
// At the moment we have only one.
func prometheusRuleSpec() prometheusoperatorv1.PrometheusRuleSpec {
	var spec prometheusoperatorv1.PrometheusRuleSpec
	err := fs.WalkDir(recordingRules, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			panic(err)
		}
		if filepath.Ext(path) != ".yaml" {
			return nil
		}
		spec = getPrometheusRuleSpec(recordingRules, path)
		return nil
	})
	if err != nil {
		panic(err)
	}

	return spec
}

// getPrometheusRuleSpec unmarshals a prometheusoperatorv1.PrometheusRuleSpec from file.
func getPrometheusRuleSpec(files embed.FS, file string) prometheusoperatorv1.PrometheusRuleSpec {
	var prometheusRuleSpec prometheusoperatorv1.PrometheusRuleSpec
	if err := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(getContents(files, file)), 100).Decode(&prometheusRuleSpec); err != nil {
		panic(err)
	}

	return prometheusRuleSpec
}
