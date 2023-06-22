package globalconfig

import (
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ImageContentSourcePolicy() *operatorv1alpha1.ImageContentSourcePolicy {
	return &operatorv1alpha1.ImageContentSourcePolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageContentSourcePolicy",
			APIVersion: operatorv1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

// ImageContentSourcePolicyList returns an initialized ImageContentSourcePolicyList pointer
func ImageContentSourcePolicyList() *operatorv1alpha1.ImageContentSourcePolicyList {
	return &operatorv1alpha1.ImageContentSourcePolicyList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageContentSourcePolicyList",
			APIVersion: configv1.GroupVersion.String(),
		},
	}
}

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

func ImageDigestMirrorSetList() *configv1.ImageDigestMirrorSetList {
	return &configv1.ImageDigestMirrorSetList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageDigestMirrorSetList",
			APIVersion: configv1.GroupVersion.String(),
		},
	}
}

// ReconcileImageContentSourcePolicy reconciles the ImageContentSources from the HCP spec into an ImageContentSourcePolicy
func ReconcileImageContentSourcePolicy(icsp *operatorv1alpha1.ImageContentSourcePolicy, hcp *hyperv1.HostedControlPlane) error {
	if icsp.Labels == nil {
		icsp.Labels = map[string]string{}
	}
	icsp.Labels["machineconfiguration.openshift.io/role"] = "worker"
	icsp.Spec.RepositoryDigestMirrors = []operatorv1alpha1.RepositoryDigestMirrors{}
	for _, imageContentSourceEntry := range hcp.Spec.ImageContentSources {
		icsp.Spec.RepositoryDigestMirrors = append(icsp.Spec.RepositoryDigestMirrors, operatorv1alpha1.RepositoryDigestMirrors{
			Source:  imageContentSourceEntry.Source,
			Mirrors: imageContentSourceEntry.Mirrors,
		})
	}
	return nil
}

// ReconcileImageDigestMirrors reconciles the ImageContentSources from the HCP spec into an ImageDigestMirrorSet
func ReconcileImageDigestMirrors(idms *configv1.ImageDigestMirrorSet, hcp *hyperv1.HostedControlPlane) error {
	if idms.Labels == nil {
		idms.Labels = map[string]string{}
	}

	idms.Labels["machineconfiguration.openshift.io/role"] = "worker"
	idms.Spec.ImageDigestMirrors = []configv1.ImageDigestMirrors{}
	for _, source := range hcp.Spec.ImageContentSources {
		var mirrors []configv1.ImageMirror

		for _, mirror := range source.Mirrors {
			mirrors = append(mirrors, configv1.ImageMirror(mirror))
		}

		idms.Spec.ImageDigestMirrors = append(idms.Spec.ImageDigestMirrors, configv1.ImageDigestMirrors{
			Source:  source.Source,
			Mirrors: mirrors,
		})
	}
	return nil
}
