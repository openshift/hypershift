package static

import (
	"encoding/json"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/machineimage"
)

type StaticImageProvider struct {
}

var _ machineimage.Provider = &StaticImageProvider{}

type regionImage struct {
	HVMImage string `json:"hvm"`
}

type staticImages struct {
	AMIs map[string]regionImage `json:"amis"`
}

func (p *StaticImageProvider) Image(cluster *hyperv1.HostedCluster) (string, error) {
	if cluster.Spec.Platform.AWS == nil {
		return "", fmt.Errorf("unsupported platform, only AWS is supported")
	}
	// TODO: Support other versions, other archs. Currently only 4.7 amd64 is supported.
	imageData := MustAsset("4.7/rhcos-amd64.json")
	images := &staticImages{}
	if err := json.Unmarshal(imageData, images); err != nil {
		return "", fmt.Errorf("cannot decode image data: %w", err)
	}
	return images.AMIs[cluster.Spec.Platform.AWS.Region].HVMImage, nil
}
