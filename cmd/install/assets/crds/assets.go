package crds

import (
	"bytes"
	"embed"
	"io"
	"io/fs"
	"path/filepath"
	"slices"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/yaml"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed hypershift-operator/*
//go:embed cluster-api/*
//go:embed cluster-api-provider-aws/*
//go:embed cluster-api-provider-gcp/*
//go:embed cluster-api-provider-ibmcloud/*
//go:embed cluster-api-provider-kubevirt/*
//go:embed cluster-api-provider-agent/*
//go:embed cluster-api-provider-azure/*
//go:embed cluster-api-provider-openstack/*
var CRDS embed.FS

const capiLabel = "cluster.x-k8s.io/v1beta1"

// CAPICRDOverrideEntry configures a CAPI CRD that has both v1beta1 and v1beta2 versions.
type CAPICRDOverrideEntry struct {
	StorageVersion  string
	NeedsConversion bool
}

// capiCRDNames lists all CAPI CRDs that need storage version overrides and conversion webhooks.
var capiCRDNames = []string{
	"clusterclasses.cluster.x-k8s.io",
	"clusters.cluster.x-k8s.io",
	"machinedeployments.cluster.x-k8s.io",
	"machinedrainrules.cluster.x-k8s.io",
	"machinehealthchecks.cluster.x-k8s.io",
	"machinepools.cluster.x-k8s.io",
	"machines.cluster.x-k8s.io",
	"machinesets.cluster.x-k8s.io",
	"ipaddressclaims.ipam.cluster.x-k8s.io",
	"ipaddresses.ipam.cluster.x-k8s.io",
	"clusterresourcesetbindings.addons.cluster.x-k8s.io",
	"clusterresourcesets.addons.cluster.x-k8s.io",
}

// CAPICRDNames returns the list of CAPI CRD names that are managed by HyperShift.
func CAPICRDNames() []string {
	return slices.Clone(capiCRDNames)
}

// CAPICRDOverrides returns the override map for CAPI CRDs with the default v1beta1 storage version.
func CAPICRDOverrides() map[string]CAPICRDOverrideEntry {
	return CAPICRDOverridesWithStorageVersion("v1beta1")
}

// CAPICRDOverridesWithStorageVersion returns the override map with the specified storage version.
func CAPICRDOverridesWithStorageVersion(storageVersion string) map[string]CAPICRDOverrideEntry {
	overrides := make(map[string]CAPICRDOverrideEntry, len(capiCRDNames))
	for _, name := range capiCRDNames {
		overrides[name] = CAPICRDOverrideEntry{StorageVersion: storageVersion, NeedsConversion: true}
	}
	return overrides
}

// capiResources specifies which CRDs should get labeled with capiLabel
// to satisfy CAPI contracts. There might be a way to achieve this during CRD
// generation, but for now we're just post-processing at runtime here.
var capiResources = map[string]string{
	"cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsclusters.yaml":                                   "v1beta2",
	"cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsmachines.yaml":                                   "v1beta2",
	"cluster-api-provider-aws/infrastructure.cluster.x-k8s.io_awsmachinetemplates.yaml":                           "v1beta2",
	"cluster-api-provider-gcp/infrastructure.cluster.x-k8s.io_gcpclusters.yaml":                                   "v1beta1",
	"cluster-api-provider-gcp/infrastructure.cluster.x-k8s.io_gcpmachines.yaml":                                   "v1beta1",
	"cluster-api-provider-gcp/infrastructure.cluster.x-k8s.io_gcpmachinetemplates.yaml":                           "v1beta1",
	"cluster-api-provider-gcp/infrastructure.cluster.x-k8s.io_gcpclustertemplates.yaml":                           "v1beta1",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmpowervsclusters.yaml":                       "v1beta2",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmpowervsimages.yaml":                         "v1beta2",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmpowervsmachines.yaml":                       "v1beta2",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmpowervsmachinetemplates.yaml":               "v1beta2",
	"cluster-api-provider-ibmcloud/infrastructure.cluster.x-k8s.io_ibmvpcclusters.yaml":                           "v1beta2",
	"hypershift-operator/zz_generated.crd-manifests/hostedcontrolplanes-Hypershift-Default.crd.yaml":              "v1beta1",
	"hypershift-operator/zz_generated.crd-manifests/hostedcontrolplanes-Hypershift-TechPreviewNoUpgrade.crd.yaml": "v1beta1",
	"cluster-api-provider-kubevirt/infrastructure.cluster.x-k8s.io_kubevirtclusters.yaml":                         "v1alpha1",
	"cluster-api-provider-kubevirt/infrastructure.cluster.x-k8s.io_kubevirtmachines.yaml":                         "v1alpha1",
	"cluster-api-provider-kubevirt/infrastructure.cluster.x-k8s.io_kubevirtmachinetemplates.yaml":                 "v1alpha1",
	"cluster-api-provider-agent/capi-provider.agent-install.openshift.io_agentclusters.yaml":                      "v1beta1",
	"cluster-api-provider-agent/capi-provider.agent-install.openshift.io_agentmachinetemplates.yaml":              "v1beta1",
	"cluster-api-provider-agent/capi-provider.agent-install.openshift.io_agentmachines.yaml":                      "v1beta1",
	"cluster-api-provider-azure/infrastructure.cluster.x-k8s.io_azureclusters.yaml":                               "v1beta1",
	"cluster-api-provider-azure/infrastructure.cluster.x-k8s.io_azuremachines.yaml":                               "v1beta1",
	"cluster-api-provider-azure/infrastructure.cluster.x-k8s.io_azuremachinetemplates.yaml":                       "v1beta1",
	"cluster-api-provider-openstack/openstack.k-orc.cloud_images.yaml":                                            "v1alpha1",
	"cluster-api-provider-openstack/infrastructure.cluster.x-k8s.io_openstackclustertemplates.yaml":               "v1beta1",
	"cluster-api-provider-openstack/infrastructure.cluster.x-k8s.io_openstackclusters.yaml":                       "v1beta1",
	"cluster-api-provider-openstack/infrastructure.cluster.x-k8s.io_openstackmachines.yaml":                       "v1beta1",
	"cluster-api-provider-openstack/infrastructure.cluster.x-k8s.io_openstackmachinetemplates.yaml":               "v1beta1",
	"cluster-api-provider-openstack/infrastructure.cluster.x-k8s.io_openstackfloatingippools.yaml":                "v1alpha1",
	"cluster-api-provider-openstack/infrastructure.cluster.x-k8s.io_openstackclusteridentities.yaml":              "v1alpha1",
	"cluster-api-provider-openstack/infrastructure.cluster.x-k8s.io_openstackservers.yaml":                        "v1alpha1",
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

// CustomResourceDefinitions returns all existing CRDs as controller-runtime objects.
// capiStorageVersion controls which version is set as storage for CAPI CRDs (e.g., "v1beta1" or "v1beta2").
func CustomResourceDefinitions(capiStorageVersion string, include func(path string, crd *apiextensionsv1.CustomResourceDefinition) bool, transform func(*apiextensionsv1.CustomResourceDefinition)) []crclient.Object {
	overrides := CAPICRDOverridesWithStorageVersion(capiStorageVersion)
	var allCrds []crclient.Object
	err := fs.WalkDir(CRDS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			panic(err)
		}
		if filepath.Ext(path) != ".yaml" {
			return nil
		}
		crd := getCustomResourceDefinition(CRDS, path, overrides)
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
func getCustomResourceDefinition(files embed.FS, file string, overrides map[string]CAPICRDOverrideEntry) *apiextensionsv1.CustomResourceDefinition {
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

	if override, ok := overrides[crd.Name]; ok && override.StorageVersion != "" {
		for i := range crd.Spec.Versions {
			crd.Spec.Versions[i].Storage = crd.Spec.Versions[i].Name == override.StorageVersion
		}
	}

	return &crd
}
