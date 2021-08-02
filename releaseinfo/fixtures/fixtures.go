package fixtures

import (
	_ "embed"
)

//go:embed 4.8-image-references.json
var ImageReferencesJSON_4_8 []byte

//go:embed 4.8-installer-coreos-bootimages.yaml
var CoreOSBootImagesYAML_4_8 []byte
