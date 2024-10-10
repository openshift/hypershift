package crds

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

//go:embed hypershift.openshift.io_controlplanecomponents.yaml
var controlplanecomponentCRD []byte

func InstallCRDs(ctx context.Context, client crclient.Client) error {
	obj := &apiextensionsv1.CustomResourceDefinition{}
	if _, _, err := api.YamlSerializer.Decode(controlplanecomponentCRD, nil, obj); err != nil {
		return fmt.Errorf("cannot decode controlplanecomponent CRD: %v", err)
	}

	createOrUpdate := upsert.NewV2(false)
	_, err := createOrUpdate.CreateOrUpdateV2(ctx, client, obj)
	return err
}
