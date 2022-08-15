package globalconfig

import (
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
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
