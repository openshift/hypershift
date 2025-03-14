package nodepool

import (
	"context"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/nodepool/openstack"
	"github.com/openshift/hypershift/support/releaseinfo"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"

	orc "github.com/k-orc/openstack-resource-controller/api/v1alpha1"
)

func (r *NodePoolReconciler) setOpenStackConditions(ctx context.Context, nodePool *hyperv1.NodePool, hcluster *hyperv1.HostedCluster, controlPlaneNamespace string, releaseImage *releaseinfo.ReleaseImage) error {
	if nodePool.Spec.Platform.OpenStack.ImageName == "" {
		_, err := openstack.OpenStackReleaseImage(releaseImage)
		if err != nil {
			SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
				Type:               hyperv1.NodePoolValidPlatformImageType,
				Status:             corev1.ConditionFalse,
				Reason:             hyperv1.NodePoolValidationFailedReason,
				Message:            fmt.Sprintf("Couldn't discover an OpenStack Image for release image %q: %s", nodePool.Spec.Release.Image, err.Error()),
				ObservedGeneration: nodePool.Generation,
			})
			return fmt.Errorf("couldn't discover an OpenStack Image for release image: %w", err)
		}
		imageName, err := r.reconcileOpenStackImageCR(ctx, r.Client, hcluster, releaseImage, nodePool)
		if err != nil {
			return err
		}
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidPlatformImageType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            fmt.Sprintf("Bootstrap OpenStack Image is %q", imageName),
			ObservedGeneration: nodePool.Generation,
		})
	} else {
		SetStatusCondition(&nodePool.Status.Conditions, hyperv1.NodePoolCondition{
			Type:               hyperv1.NodePoolValidPlatformImageType,
			Status:             corev1.ConditionTrue,
			Reason:             hyperv1.AsExpectedReason,
			Message:            fmt.Sprintf("Bootstrap OpenStack Image is %q", nodePool.Spec.Platform.OpenStack.ImageName),
			ObservedGeneration: nodePool.Generation,
		})
	}
	return nil
}

// reconcileOpenStackImageCR reconciles the OpenStack Image CR for the given NodePool.
// An ORC object will be created or updated with the image spec.
// The image name will be returned.
func (r *NodePoolReconciler) reconcileOpenStackImageCR(ctx context.Context, client client.Client, hcluster *hyperv1.HostedCluster, release *releaseinfo.ReleaseImage, nodePool *hyperv1.NodePool) (string, error) {
	releaseVersion, err := openstack.OpenStackReleaseImage(release)
	if err != nil {
		return "", err
	}
	controlPlaneNamespace := fmt.Sprintf("%s-%s", nodePool.Namespace, strings.ReplaceAll(nodePool.Spec.ClusterName, ".", "-"))
	openstackCluster, err := openstack.GetOpenStackClusterForHostedCluster(ctx, client, hcluster, controlPlaneNamespace)
	if err != nil {
		return "", err
	}
	imageName := "rhcos-" + releaseVersion
	openStackImage := orc.Image{
		ObjectMeta: metav1.ObjectMeta{
			Name:      imageName,
			Namespace: controlPlaneNamespace,
			OwnerReferences: []metav1.OwnerReference{
				{
					// Since there is no code that deletes the ORC image object, the only way the OpenStack Glance image
					// can be deleted is when the OpenStackCluster CR is deleted and that the imageRetentionPolicy is set to "Prune".
					// That means Nodepools can share the same image and changing the image of a Nodepool will not affect the other Nodepools.
					APIVersion: openstackCluster.APIVersion,
					Kind:       openstackCluster.Kind,
					Name:       openstackCluster.Name,
					UID:        openstackCluster.UID,
				},
			},
		},
		Spec: orc.ImageSpec{},
	}

	if _, err := r.CreateOrUpdate(ctx, client, &openStackImage, func() error {
		err := openstack.ReconcileOpenStackImageSpec(hcluster, &openStackImage.Spec, release)
		if err != nil {
			return err
		}
		return nil
	}); err != nil {
		return "", err
	}
	return imageName, nil
}
