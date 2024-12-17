package storage

import (
	"strings"
	"testing"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/storage/assets"
	assets2 "github.com/openshift/hypershift/support/assets"

	"k8s.io/apimachinery/pkg/util/rand"
)

func TestEnvironmentReplacer(t *testing.T) {
	// Test that EnvironmentReplaces replaces **all** env. vars in the operator Deployment.
	// This protects us from adding a new image to assets/10_deployment-hypershift.yaml
	// and not adding it to envreplace.go.

	// All image URLs will point to string "REPLACED"
	replaced := "REPLACED"
	images := map[string]string{}
	for _, payloadName := range operatorImageRefs {
		images[payloadName] = replaced
	}

	imageProviver := imageprovider.NewFromImages(images)

	er := newEnvironmentReplacer()
	er.setOperatorImageReferences(imageProviver, imageProviver)
	version := rand.String(10)
	er.setVersions(version)

	deployment := assets2.MustDeployment(assets.ReadFile, "10_deployment-hypershift.yaml")
	er.replaceEnvVars(deployment.Spec.Template.Spec.Containers[0].Env)

	// Check that all env. vars were replaced.
	// Feel free to add exceptions in the future, but right now *all* env. vars of the operator
	// refer either to version or image name.
	for _, env := range deployment.Spec.Template.Spec.Containers[0].Env {
		if strings.HasSuffix(env.Name, "_VERSION") {
			if env.Value != version {
				t.Errorf("Environment variable %q in assets/10_deployment-hypershift.yaml was not replaced by the operator. Please update envreplace.go!", env.Name)
			}
			continue
		}
		// Not version -> it must be an image name
		if env.Value != replaced {
			t.Errorf("Environment variable %q in assets/10_deployment-hypershift.yaml was not replaced by the operator. Please update envreplace.go!", env.Name)
		}
	}
}
