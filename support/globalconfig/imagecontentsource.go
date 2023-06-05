package globalconfig

import (
	"context"
	configv1 "github.com/openshift/api/config/v1"
	operatorv1alpha1 "github.com/openshift/api/operator/v1alpha1"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

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

// GetAllImageRegistryMirrors returns all image registry mirrors from any ImageContentSourcePolicy and
// ImageDigestMirrorSet in an OpenShift management cluster (other management cluster types will not have these policies)
// ImageContentSourcePolicy will be deprecated and removed in 4.17 according to the following Jira ticket and PR
// description in favor of ImageDigestMirrorSet. We will need to look for both policy types in the cluster.
//
//		https://issues.redhat.com/browse/OCPNODE-1258
//	    https://github.com/openshift/hypershift/pull/1776
func GetAllImageRegistryMirrors(ctx context.Context, client client.Client, mgmtClusterHasIDMSCapability bool) (map[string][]string, error) {
	var mgmtClusterRegistryOverrides = make(map[string][]string)

	// Retrieve any ImageContentSourcePolicy from the management cluster
	var imageContentSourcePolicies = ImageContentSourcePolicyList()
	err := client.List(ctx, imageContentSourcePolicies)
	if err != nil {
		return nil, err
	}

	// For each image content source policy in the management cluster, map the source with each of its mirrors
	for _, item := range imageContentSourcePolicies.Items {
		for _, mirror := range item.Spec.RepositoryDigestMirrors {
			source := mirror.Source

			for n := range mirror.Mirrors {
				mgmtClusterRegistryOverrides[source] = append(mgmtClusterRegistryOverrides[source], mirror.Mirrors[n])
			}
		}
	}

	if mgmtClusterHasIDMSCapability {
		// Retrieve any ImageDigestMirrorSet from the management cluster
		var imageDigestMirrorSets = ImageDigestMirrorSetList()
		err = client.List(ctx, imageDigestMirrorSets)
		if err != nil {
			return nil, err
		}

		// For each image digest mirror set in the management cluster, map the source with each of its mirrors
		for _, item := range imageDigestMirrorSets.Items {
			for _, imageDigestMirror := range item.Spec.ImageDigestMirrors {
				source := imageDigestMirror.Source

				for n := range imageDigestMirror.Mirrors {
					mgmtClusterRegistryOverrides[source] = append(mgmtClusterRegistryOverrides[source], string(imageDigestMirror.Mirrors[n]))
				}
			}
		}
	}

	return mgmtClusterRegistryOverrides, nil
}
