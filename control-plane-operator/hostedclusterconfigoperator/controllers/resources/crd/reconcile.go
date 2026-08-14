package crd

import (
	_ "embed"
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

var (
	//go:embed assets/apiserver.openshift.io_apirequestcount-crd.yaml
	requestCountCRDBytes []byte

	requestCountCRD = mustCRD(requestCountCRDBytes)
)

func ReconcileRequestCountCRD(crd *apiextensionsv1.CustomResourceDefinition) error {
	crd.Spec = requestCountCRD.Spec
	return nil
}

func mustCRD(content []byte) *apiextensionsv1.CustomResourceDefinition {
	crd := &apiextensionsv1.CustomResourceDefinition{}
	deserializeResource(content, crd)
	return crd
}

func deserializeResource(data []byte, obj runtime.Object) {
	gvks, _, err := api.Scheme.ObjectKinds(obj)
	if err != nil || len(gvks) == 0 {
		panic(fmt.Sprintf("cannot determine gvk of resource: %v", err))
	}
	if _, _, err = api.YamlSerializer.Decode(data, &gvks[0], obj); err != nil {
		panic(fmt.Sprintf("cannot decode resource: %v", err))
	}
}
