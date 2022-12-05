package kubevirt

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

const (
	containerImagePrefix      = "docker://"
	bootImageDVAnnotationHash = "hypershift.openshift.io/kubevirt-boot-image-hash"
	bootImageDVLabelRoleName  = "hypershift.openshift.io/kubevirt-boot-image-role"
	bootImageDVLabelRoleValue = "kv-boot-image-cache"
	bootImageDVLabelInfraId   = "hypershift.openshift.io/infra-id"
	bootImageNamePrefix       = bootImageDVLabelRoleValue + "-"

	// A CDI annotation for DataVolume, to not wait to first customer, but start importing immediately.
	// originally defined in CDI.
	cdiImmediateBindingAnnotation = "cdi.kubevirt.io/storage.bind.immediate.requested"
	// A CDI annotation for [not] deleting the DataVolume after the PVC population is completed.
	// originally defined in CDI.
	cdiDeleteAfterCompletionAnnotation = "cdi.kubevirt.io/storage.deleteAfterCompletion"
)

// BootImage represents the KubeVirt boot image. It responsible to hold cache the boot image and to generate its
// reference to be used by the node templates.
type BootImage interface {
	// CacheImage creates a PVC to cache the node image.
	CacheImage(ctx context.Context, cl client.Client, nodePool *hyperv1.NodePool, infraId string) error
	getDVSourceForVMTemplate() *v1beta1.DataVolumeSource
}

// containerImage is the implementation of the BootImage interface for container images
type containerImage struct {
	name string
}

func newContainerBootImage(imageName string) *containerImage {
	return &containerImage{
		name: containerImagePrefix + imageName,
	}
}

func (containerImage) CacheImage(_ context.Context, _ client.Client, _ *hyperv1.NodePool, _ string) error {
	return nil // no implementation
}

func (ci containerImage) getDVSourceForVMTemplate() *v1beta1.DataVolumeSource {
	pullMethod := v1beta1.RegistryPullNode
	return &v1beta1.DataVolumeSource{
		Registry: &v1beta1.DataVolumeSourceRegistry{
			URL:        &ci.name,
			PullMethod: &pullMethod,
		},
	}
}

// containerImage is the implementation of the BootImage interface for container images
type qcowImage struct {
	name string
}

func newQCOWBootImage(imageName string) *qcowImage {
	return &qcowImage{
		name: imageName,
	}
}

func (qcowImage) CacheImage(_ context.Context, _ client.Client, _ *hyperv1.NodePool, _ string) error {
	return nil // no implementation
}

func (qi qcowImage) getDVSourceForVMTemplate() *v1beta1.DataVolumeSource {
	return &v1beta1.DataVolumeSource{
		HTTP: &v1beta1.DataVolumeSourceHTTP{
			URL: qi.name,
		},
	}
}

// cachedQCOWImage is the implementation of the BootImage interface for QCOW images
type cachedQCOWImage struct {
	name      string
	hash      string
	namespace string
	dvName    string
}

func newCachedQCOWBootImage(name, hash, namespace string) *cachedQCOWImage {
	return &cachedQCOWImage{
		name:      name,
		hash:      hash,
		namespace: namespace,
	}
}

func (qi *cachedQCOWImage) CacheImage(ctx context.Context, cl client.Client, nodePool *hyperv1.NodePool, infraId string) error {
	logger := ctrl.LoggerFrom(ctx)

	if nodePool.Spec.Platform.Kubevirt == nil {
		// should never happen; but since CacheImage is exposed, we need to protect it from wrong inputs.
		return fmt.Errorf("nodePool does not contain KubeVirt configurations")
	}

	dvList, err := getCacheDVs(ctx, cl, infraId, qi.namespace)
	if err != nil {
		return err
	}

	oldDVs := make([]v1beta1.DataVolume, 0)
	var dvName string
	for _, dv := range dvList {
		if (len(dvName) == 0) && (dv.Annotations[bootImageDVAnnotationHash] == qi.hash) {
			dvName = dv.Name
		} else {
			oldDVs = append(oldDVs, dv)
		}
	}

	// if no DV with the required hash was found
	if len(dvName) == 0 {
		logger.Info("couldn't find boot image cache DataVolume; creating it...")
		dv, err := qi.createDVForCache(ctx, cl, nodePool, infraId)
		if err != nil {
			return err
		}
		dvName = dv.Name
	}

	if len(dvName) == 0 {
		return fmt.Errorf("can't get the name of the boot image cache DataVolume")
	}
	qi.dvName = dvName
	qi.cleanOldCaches(ctx, cl, oldDVs)

	return nil
}

func (qi *cachedQCOWImage) cleanOldCaches(ctx context.Context, cl client.Client, oldDVs []v1beta1.DataVolume) {
	logger := ctrl.LoggerFrom(ctx)
	for _, oldDV := range oldDVs {
		if oldDV.DeletionTimestamp == nil {
			logger.Info("deleting an old boot image cache DataVolume", "namespace", oldDV.Namespace, "DataVolume name", oldDV.Name)
			err := cl.Delete(ctx, &oldDV)
			if err != nil {
				logger.Error(err, fmt.Sprintf("failed to delete an old DataVolume; namespace: %s, name: %s", oldDV.Namespace, oldDV.Name))
			}
		}
	}
}

func (qi *cachedQCOWImage) createDVForCache(ctx context.Context, cl client.Client, nodePool *hyperv1.NodePool, infraId string) (*v1beta1.DataVolume, error) {
	dv := qi.buildDVForCache(nodePool, infraId)

	err := cl.Create(ctx, dv)
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("failed to create a DataVolume for the boot image cache ; %w", err)
	}

	return dv, nil
}

func (qi *cachedQCOWImage) getDVSourceForVMTemplate() *v1beta1.DataVolumeSource {
	return &v1beta1.DataVolumeSource{
		PVC: &v1beta1.DataVolumeSourcePVC{
			Namespace: qi.namespace,
			Name:      qi.dvName,
		},
	}
}

func (qi *cachedQCOWImage) buildDVForCache(nodePool *hyperv1.NodePool, infraId string) *v1beta1.DataVolume {
	dv := &v1beta1.DataVolume{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: bootImageNamePrefix,
			Namespace:    qi.namespace,
			Labels: map[string]string{
				bootImageDVLabelRoleName: bootImageDVLabelRoleValue,
				bootImageDVLabelInfraId:  infraId,
			},
			Annotations: map[string]string{
				bootImageDVAnnotationHash:          qi.hash,
				cdiImmediateBindingAnnotation:      "true",
				cdiDeleteAfterCompletionAnnotation: "false",
			},
		},
		Spec: v1beta1.DataVolumeSpec{
			Source: &v1beta1.DataVolumeSource{
				HTTP: &v1beta1.DataVolumeSourceHTTP{
					URL: qi.name,
				},
			},
			Preallocation: pointer.Bool(true),
		},
	}

	kvPlatform := nodePool.Spec.Platform.Kubevirt
	if kvPlatform.RootVolume != nil && kvPlatform.RootVolume.Persistent != nil {
		storageSpec := &v1beta1.StorageSpec{}
		if kvPlatform.RootVolume.Persistent.Size != nil {
			storageSpec.Resources = corev1.ResourceRequirements{
				Requests: map[corev1.ResourceName]apiresource.Quantity{
					corev1.ResourceStorage: *kvPlatform.RootVolume.Persistent.Size,
				},
			}
		}

		if kvPlatform.RootVolume.Persistent.StorageClass != nil {
			storageSpec.StorageClassName = kvPlatform.RootVolume.Persistent.StorageClass
		}

		for _, am := range kvPlatform.RootVolume.Persistent.AccessModes {
			storageSpec.AccessModes = append(storageSpec.AccessModes, corev1.PersistentVolumeAccessMode(am))
		}

		dv.Spec.Storage = storageSpec
	}

	return dv
}

func getCacheDVSelector(infraId string) client.MatchingLabels {
	return map[string]string{
		bootImageDVLabelRoleName: bootImageDVLabelRoleValue,
		bootImageDVLabelInfraId:  infraId,
	}
}

func getCacheDVs(ctx context.Context, cl client.Client, infraId string, namespace string) ([]v1beta1.DataVolume, error) {
	dvs := &v1beta1.DataVolumeList{}

	err := cl.List(ctx, dvs, client.InNamespace(namespace), getCacheDVSelector(infraId))

	if err != nil {
		return nil, fmt.Errorf("failed to read DataVolumes; %w", err)
	}

	return dvs.Items, nil
}
