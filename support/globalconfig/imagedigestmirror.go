package globalconfig

import (
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ImageDigestMirrorSet() *configv1.ImageDigestMirrorSet {
	return &configv1.ImageDigestMirrorSet{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageDigestMirrorSet",
			APIVersion: configv1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ReconcileImageDigestMirrorSet(idms *configv1.ImageDigestMirrorSet, hcp *hyperv1.HostedControlPlane) error {
	idms.Spec.ImageDigestMirrors = []configv1.ImageDigestMirrors{}
	for _, imageDigestSourceEntry := range hcp.Spec.ImageDigestSources {
		mirrors := []configv1.ImageMirror{}
		for _, mirror := range imageDigestSourceEntry.Mirrors {
			mirrors = append(mirrors, configv1.ImageMirror(mirror))
		}
		idms.Spec.ImageDigestMirrors = append(idms.Spec.ImageDigestMirrors, configv1.ImageDigestMirrors{
			Source:  imageDigestSourceEntry.Source,
			Mirrors: mirrors,
		})
	}
	return nil
}
