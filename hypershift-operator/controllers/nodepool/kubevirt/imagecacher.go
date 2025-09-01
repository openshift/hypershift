package kubevirt

import (
	"context"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apiresource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"kubevirt.io/containerized-data-importer-api/pkg/apis/core/v1beta1"
)

const (
	containerImagePrefix      = "docker://"
	bootImageDVAnnotationHash = "hypershift.openshift.io/kubevirt-boot-image-hash"
	bootImageDVLabelRoleName  = "hypershift.openshift.io/kubevirt-boot-image-role"
	bootImageDVLabelRoleValue = "kv-boot-image-cache"
	bootImageDVLabelUID       = "hypershift.openshift.io/nodepool-uid"
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
	CacheImage(context.Context, client.Client, *hyperv1.NodePool, string) error
	getDVSourceForVMTemplate() (*v1beta1.DataVolumeSource, error)
	String() string
}

type BootImageNamer interface {
	GetCacheName() string
}

// bootImage is the implementation of the BootImage interface for container images
type bootImage struct {
	name   string
	isHTTP bool
}

func newBootImage(imageName string, isHTTP bool) *bootImage {
	bi := &bootImage{}
	if isHTTP {
		bi.isHTTP = true
		bi.name = imageName
	} else {
		bi.name = containerImagePrefix + imageName
	}
	return bi
}

func (bi bootImage) String() string {
	return strings.TrimPrefix(bi.name, containerImagePrefix)
}

func (bootImage) CacheImage(_ context.Context, _ client.Client, _ *hyperv1.NodePool, _ string) error {
	return nil // no implementation
}

func (bi bootImage) getDVSourceForVMTemplate() (*v1beta1.DataVolumeSource, error) {
	if bi.isHTTP {
		return &v1beta1.DataVolumeSource{
			HTTP: &v1beta1.DataVolumeSourceHTTP{
				URL: bi.name,
			},
		}, nil
	}

	pullMethod := v1beta1.RegistryPullNode
	return &v1beta1.DataVolumeSource{
		Registry: &v1beta1.DataVolumeSourceRegistry{
			URL:        &bi.name,
			PullMethod: &pullMethod,
		},
	}, nil
}

// cachedBootImage is the implementation of the BootImage interface for QCOW images
type cachedBootImage struct {
	name      string
	hash      string
	namespace string
	dvName    string
	isHTTP    bool
}

func newCachedBootImage(name, hash, namespace string, isHTTP bool, nodePool *hyperv1.NodePool) *cachedBootImage {
	cbi := &cachedBootImage{
		hash:      hash,
		namespace: namespace,
	}

	if isHTTP {
		cbi.isHTTP = isHTTP
		cbi.name = name
	} else {
		cbi.name = containerImagePrefix + name
	}

	// default to the current cached image. This will be updated
	// later on if it is out of date.
	if nodePool != nil &&
		nodePool.Status.Platform != nil &&
		nodePool.Status.Platform.KubeVirt != nil &&
		len(nodePool.Status.Platform.KubeVirt.CacheName) > 0 {

		cbi.dvName = nodePool.Status.Platform.KubeVirt.CacheName
	}

	return cbi
}

func (cbi cachedBootImage) String() string {
	return strings.TrimPrefix(cbi.name, containerImagePrefix)
}

func (qi *cachedBootImage) CacheImage(ctx context.Context, cl client.Client, nodePool *hyperv1.NodePool, uid string) error {
	logger := ctrl.LoggerFrom(ctx)

	if nodePool.Spec.Platform.Kubevirt == nil {
		// should never happen; but since CacheImage is exposed, we need to protect it from wrong inputs.
		return fmt.Errorf("nodePool does not contain KubeVirt configurations")
	}

	if nodePool.Status.Platform != nil &&
		nodePool.Status.Platform.KubeVirt != nil &&
		len(nodePool.Status.Platform.KubeVirt.CacheName) > 0 {
		var dv v1beta1.DataVolume
		dvName := nodePool.Status.Platform.KubeVirt.CacheName

		err := cl.Get(ctx, client.ObjectKey{Name: dvName, Namespace: qi.namespace}, &dv)
		if err == nil {
			if annotations := dv.GetAnnotations(); annotations != nil && annotations[bootImageDVAnnotationHash] == qi.hash {
				qi.dvName = dvName
				return nil
			}
		} else {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("can't read DataVolume %s/%s: %w", qi.namespace, dvName, err)
			}
			// cache DV not found - should keep searching, or, if missing, create it
		}
	}

	dvList, err := getCacheDVs(ctx, cl, uid, qi.namespace)
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

	qi.cleanOldCaches(ctx, cl, oldDVs)

	// if no DV with the required hash was found
	if len(dvName) == 0 {
		logger.Info("couldn't find boot image cache DataVolume; creating it...")
		dv, err := qi.createDVForCache(ctx, cl, nodePool, uid)
		if err != nil {
			return err
		}
		dvName = dv.Name
	}

	qi.dvName = dvName

	return nil
}

func (qi *cachedBootImage) cleanOldCaches(ctx context.Context, cl client.Client, oldDVs []v1beta1.DataVolume) {
	logger := ctrl.LoggerFrom(ctx)
	for _, oldDV := range oldDVs {
		if oldDV.DeletionTimestamp.IsZero() {
			logger.Info("deleting an old boot image cache DataVolume", "namespace", oldDV.Namespace, "DataVolume name", oldDV.Name)
			err := cl.Delete(ctx, &oldDV)
			if err != nil {
				logger.Error(err, fmt.Sprintf("failed to delete an old DataVolume; namespace: %s, name: %s", oldDV.Namespace, oldDV.Name))
			}
		}
	}
}

func (qi *cachedBootImage) createDVForCache(ctx context.Context, cl client.Client, nodePool *hyperv1.NodePool, uid string) (*v1beta1.DataVolume, error) {
	dv := qi.buildDVForCache(nodePool, uid)

	err := cl.Create(ctx, dv)
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, fmt.Errorf("failed to create a DataVolume for the boot image cache ; %w", err)
	}

	return dv, nil
}

func (qi *cachedBootImage) getDVSourceForVMTemplate() (*v1beta1.DataVolumeSource, error) {

	if qi.dvName == "" {
		return nil, fmt.Errorf("no boot image source found for kubevirt machine")
	}

	return &v1beta1.DataVolumeSource{
		PVC: &v1beta1.DataVolumeSourcePVC{
			Namespace: qi.namespace,
			Name:      qi.dvName,
		},
	}, nil
}

func (qi *cachedBootImage) GetCacheName() string {
	return qi.dvName
}

func (qi *cachedBootImage) buildDVForCache(nodePool *hyperv1.NodePool, uid string) *v1beta1.DataVolume {
	pullMethod := v1beta1.RegistryPullNode
	dv := &v1beta1.DataVolume{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: bootImageNamePrefix,
			Namespace:    qi.namespace,
			Labels: map[string]string{
				bootImageDVLabelRoleName:               bootImageDVLabelRoleValue,
				bootImageDVLabelUID:                    uid,
				hyperv1.IsKubeVirtRHCOSVolumeLabelName: "true",
			},
			Annotations: map[string]string{
				bootImageDVAnnotationHash:          qi.hash,
				cdiImmediateBindingAnnotation:      "true",
				cdiDeleteAfterCompletionAnnotation: "false",
			},
		},
		Spec: v1beta1.DataVolumeSpec{
			Preallocation: ptr.To(true),
		},
	}

	if qi.isHTTP {
		dv.Spec.Source = &v1beta1.DataVolumeSource{
			HTTP: &v1beta1.DataVolumeSourceHTTP{
				URL: qi.name,
			},
		}
	} else {
		dv.Spec.Source = &v1beta1.DataVolumeSource{
			Registry: &v1beta1.DataVolumeSourceRegistry{
				URL:        &qi.name,
				PullMethod: &pullMethod,
			},
		}
	}

	kvPlatform := nodePool.Spec.Platform.Kubevirt
	if kvPlatform.RootVolume != nil && kvPlatform.RootVolume.Persistent != nil {
		storageSpec := &v1beta1.StorageSpec{}
		if kvPlatform.RootVolume.Persistent.Size != nil {
			storageSpec.Resources = corev1.VolumeResourceRequirements{
				Requests: map[corev1.ResourceName]apiresource.Quantity{
					corev1.ResourceStorage: *kvPlatform.RootVolume.Persistent.Size,
				},
			}
		}

		if kvPlatform.RootVolume.Persistent.StorageClass != nil {
			storageSpec.StorageClassName = kvPlatform.RootVolume.Persistent.StorageClass
		}
		if kvPlatform.RootVolume.Persistent.VolumeMode != nil {
			storageSpec.VolumeMode = kvPlatform.RootVolume.Persistent.VolumeMode
		}

		for _, am := range kvPlatform.RootVolume.Persistent.AccessModes {
			storageSpec.AccessModes = append(storageSpec.AccessModes, corev1.PersistentVolumeAccessMode(am))
		}

		dv.Spec.Storage = storageSpec
	}

	return dv
}

func getCacheDVSelector(uid string) client.MatchingLabels {
	return map[string]string{
		bootImageDVLabelRoleName: bootImageDVLabelRoleValue,
		bootImageDVLabelUID:      uid,
	}
}

func getCacheDVs(ctx context.Context, cl client.Client, uid string, namespace string) ([]v1beta1.DataVolume, error) {
	dvs := &v1beta1.DataVolumeList{}

	err := cl.List(ctx, dvs, client.InNamespace(namespace), getCacheDVSelector(uid))

	if err != nil {
		return nil, fmt.Errorf("failed to read DataVolumes; %w", err)
	}

	return dvs.Items, nil
}

// DeleteCache deletes the cache DV
//
// This function is not part of the interface, because it called from the nodePool reconciler Delete() method, that is
// called before getting the cacheImage.
func DeleteCache(ctx context.Context, cl client.Client, name, namespace string) error {
	dv := v1beta1.DataVolume{}
	err := cl.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, &dv)

	if err != nil {
		if errors.IsNotFound(err) {
			return nil // already deleted
		}

		return fmt.Errorf("failed to get DataVolume %s/%s: %w", namespace, name, err)
	}

	if dv.DeletionTimestamp.IsZero() {
		err = cl.Delete(ctx, &dv)
		if err != nil {
			return fmt.Errorf("failed to delete DataVolume %s/%s: %w", namespace, name, err)
		}
	}

	return nil
}
