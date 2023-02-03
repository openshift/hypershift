package util

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	homanifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

/*
These utilities will manage the manifests rendering,
backup and restore against a cluster
*/

const (
	e2eSASignKey = "e2e-sa-signing-key"
)

type Manifest struct {
	Name string
	Data []byte
}

type ManifestHandler struct {
	Manifests     []Manifest
	SrcClient     crclient.Client
	DstClient     crclient.Client
	HostedCluster *hyperv1.HostedCluster
	NodePools     *hyperv1.NodePoolList
	Ctx           context.Context
	UploadToS3    bool
}

func (h *ManifestHandler) UploadManifests(s3Details map[string]string) error {
	fmt.Printf("\nUploading Manifests to S3\n")
	hcpNs := homanifests.HostedControlPlaneNamespace(h.HostedCluster.Namespace, h.HostedCluster.Name).Name
	foldPrefix := "/tmp"

	if err := os.MkdirAll(fmt.Sprintf("%s/hc-backup-%s/manifests-%s/", foldPrefix, s3Details["seedName"], hcpNs), os.ModePerm); err != nil {
		return fmt.Errorf("error creating local folder to backup manifests: %v", err)
	}

	sess, err := session.NewSession(&aws.Config{
		Region:      aws.String(s3Details["s3Region"]),
		Credentials: credentials.NewSharedCredentials(s3Details["s3Creds"], "default"),
	})
	if err != nil {
		return fmt.Errorf("error creating s3 session: %v", err)
	}

	uploader := s3manager.NewUploader(sess)

	for _, manifest := range h.Manifests {
		fmt.Println("Uploading Manifest:", manifest.Name)
		s3Key := fmt.Sprintf("hc-backup-%s/manifests-%s/%s-%s.yaml", s3Details["seedName"], hcpNs, manifest.Name, s3Details["seedName"])

		fileName := foldPrefix + "/" + s3Key
		if err := ioutil.WriteFile(fileName, manifest.Data, 0644); err != nil {
			return fmt.Errorf("error writing yaml manifests to filesystem")
		}

		upParams := &s3manager.UploadInput{
			Bucket: aws.String(s3Details["bucketName"]),
			Key:    aws.String(s3Key),
			Body:   bytes.NewReader(manifest.Data),
		}

		result, err := uploader.Upload(upParams)
		if err != nil {
			fmt.Println("Result:", result)
			return fmt.Errorf("error uploading file so S3 bucket: %v", err)
		}
	}
	return nil
}

func (h *ManifestHandler) RestoreResource(object crclient.Object, nsKind string) error {
	kind := object.GetObjectKind().GroupVersionKind().Kind
	fmt.Printf("\nRestoring Objects: %s\n", kind)
	manifestPrefix := strings.ToLower(fmt.Sprintf("%s-%s-", kind, nsKind))
	for _, manifest := range h.Manifests {
		if strings.Contains(manifest.Name, manifestPrefix) {
			obj := object.DeepCopyObject().(crclient.Object)
			if err := util.DeserializeResource(string(manifest.Data), obj, api.Scheme); err != nil {
				return fmt.Errorf("error deserializing manifest: %v", err)
			}

			if obj.GetName() == "" && obj.GetNamespace() == "" {
				// Sometimes it recovers empty objects.
				fmt.Println("empty object, bypassing...")
				continue
			}

			obj.SetResourceVersion("")
			obj.SetOwnerReferences([]metav1.OwnerReference{})
			if kind == "NodePool" {
				np := obj.(*hyperv1.NodePool)
				np.Spec.PausedUntil = nil
				obj = np
			}

			if err := h.DstClient.Create(h.Ctx, obj); err != nil {
				if !errors.IsAlreadyExists(err) {
					return fmt.Errorf("error creating manifest: %v", err)
				} else {
					fmt.Println("Object already exists:", manifest.Name)
				}
			}

			fmt.Printf("%s created: %v\n", kind, obj.GetName())
		}
	}
	return nil
}

func (h *ManifestHandler) PackResources(objectList crclient.ObjectList, nsKind string) error {
	var bckManifest Manifest
	var bckManifests []Manifest

	if err := meta.EachListItem(objectList, func(item runtime.Object) error {
		var err error
		var dataS string

		if dataS, err = util.SerializeResource(item, api.Scheme); err != nil {
			return fmt.Errorf("error backing up object %T: %v", item, err)
		}

		bckManifest.Data = []byte(dataS)
		accessor := meta.NewAccessor()
		itemName, err := accessor.Name(item)
		if err != nil {
			return fmt.Errorf("error backing up object %T: %v", item, err)
		}
		fmt.Printf("Backing up %v: %v\n", item.GetObjectKind().GroupVersionKind().Kind, itemName)
		bckManifest.Name = strings.ToLower(item.GetObjectKind().GroupVersionKind().Kind + "-" + nsKind + "-" + itemName)
		bckManifests = append(bckManifests, bckManifest)

		return nil
	}); err != nil {
		return fmt.Errorf("error backing up object: %v", err)
	}

	h.Manifests = append(h.Manifests, bckManifests...)
	return nil
}

func (h *ManifestHandler) GetResources(objectList crclient.ObjectList, ns string) error {
	if err := h.SrcClient.List(h.Ctx, objectList, crclient.InNamespace(ns)); err != nil {
		return fmt.Errorf("failed getting objectList: %v", err)
	}
	return nil
}

func (h *ManifestHandler) ManageReconciliation(object crclient.Object, stopped string) error {
	//Flaky stopped param, receive bool and convert to string pointer
	switch obj := object.(type) {
	case *hyperv1.HostedCluster:
		fmt.Printf("Managing HostedCluster reconciliation: %s\n", obj.Name)
		hcOrig := obj.DeepCopy()
		obj.Spec.PausedUntil = &stopped
		if err := h.SrcClient.Patch(h.Ctx, obj, crclient.MergeFrom(hcOrig)); err != nil {
			return fmt.Errorf("failed pausing hostedcluster %s reconciliation: %v", obj.Name, err)
		}
	case *hyperv1.NodePool:
		fmt.Printf("Stopping NodePool reconciliation: %s\n", obj.Name)
		npOrig := obj.DeepCopy()
		obj.Spec.PausedUntil = &stopped
		if err := h.SrcClient.Patch(h.Ctx, obj, crclient.MergeFrom(npOrig)); err != nil {
			return fmt.Errorf("failed pausing nodepool %s reconciliation: %v", obj.Name, err)
		}
	default:
		return fmt.Errorf("only HostedCluster and NodePools are allowed to stop reconciliation. Object: %s", object.GetName())
	}
	return nil
}

func (h *ManifestHandler) GetReferencedSecrets() (*corev1.SecretList, error) {
	secretList := &corev1.SecretList{}
	tmpList := &corev1.SecretList{}

	if err := h.SrcClient.List(h.Ctx, tmpList, crclient.InNamespace(h.HostedCluster.Namespace)); err != nil {
		return &corev1.SecretList{}, fmt.Errorf("failed to get the list of secrets from %s namespace: %w", h.HostedCluster.Namespace, err)
	}

	for _, secretRef := range tmpList.Items {
		if secretRef.Type == corev1.SecretTypeOpaque {
			secretList.Items = append(secretList.Items, secretRef)
		}
	}

	if len(secretList.Items) <= 0 {
		return &corev1.SecretList{}, fmt.Errorf("empty secret list recovered from namespace %s namespace", h.HostedCluster.Namespace)
	}

	return secretList, nil
}

func (h *ManifestHandler) GetReferencedConfigMaps() (*corev1.ConfigMapList, error) {
	configMapList := &corev1.ConfigMapList{}
	configMapRefs := make([]string, 0)
	nodePoolNames := make([]string, 0)

	for _, nodePool := range h.NodePools.Items {
		nodePoolNames = append(nodePoolNames, nodePool.Name)
		if nodePool.Spec.TuningConfig != nil {
			for _, tuningCM := range nodePool.Spec.TuningConfig {
				configMapRefs = append(configMapRefs, tuningCM.Name)
			}
		}

		if nodePool.Spec.Config != nil {
			for _, config := range nodePool.Spec.Config {
				configMapRefs = append(configMapRefs, config.Name)
			}
		}
	}

	// No ConfigMaps to backup
	if len(configMapRefs) <= 0 {
		fmt.Printf("there is not configMaps to backup referenced in the nodePools %v at %s namespace\n", nodePoolNames, h.HostedCluster.Namespace)
		return &corev1.ConfigMapList{}, nil
	}

	for _, configMapRef := range configMapRefs {
		sourceCM := &corev1.ConfigMap{}
		if err := h.SrcClient.Get(h.Ctx, crclient.ObjectKey{Namespace: h.HostedCluster.Namespace, Name: configMapRef}, sourceCM); err != nil {
			return &corev1.ConfigMapList{}, fmt.Errorf("failed to get referenced configmap %s/%s: %w", h.HostedCluster.Namespace, configMapRef, err)
		}

		configMapList.Items = append(configMapList.Items, *sourceCM)
	}

	return configMapList, nil
}

func (h *ManifestHandler) GetNamespace(object crclient.ObjectList, ns string) error {
	matchLabel := make(map[string]string)
	matchLabel[corev1.LabelMetadataName] = ns

	if err := h.SrcClient.List(h.Ctx, object, crclient.MatchingLabels(matchLabel)); err != nil {
		return fmt.Errorf("failed getting Namespace: %v", err)
	}
	return nil
}

func (h *ManifestHandler) RestoreETCD(hc *hyperv1.HostedCluster, s3Details map[string]string) error {
	fmt.Printf("\nRestoring ETCD\n")
	manifestPrefix := strings.ToLower(fmt.Sprintf("%s-%s-%s", hc.Kind, HcType, h.HostedCluster.Name))
	for _, manifest := range h.Manifests {
		if strings.Contains(manifest.Name, manifestPrefix) {
			if err := util.DeserializeResource(string(manifest.Data), hc, api.Scheme); err != nil {
				return fmt.Errorf("error deserializing manifest: %v", err)
			}
			hc.ResourceVersion = ""
			hc.Spec.PausedUntil = nil
			hc.Annotations[hyperv1.CleanupCloudResourcesAnnotation] = "true"
			hc.Spec.Etcd.Managed.Storage.Type = hyperv1.PersistentVolumeEtcdStorage
			hc.Spec.Etcd.Managed.Storage.RestoreSnapshotURL = append(hc.Spec.Etcd.Managed.Storage.RestoreSnapshotURL, s3Details["s3EtcdSnapshotURL"])

			dataS, err := util.SerializeResource(hc, api.Scheme)
			if err != nil {
				return fmt.Errorf("error serializing manifest: %v", err)
			}

			manifest.Data = []byte(dataS)

			if err := h.DstClient.Create(h.Ctx, hc); err != nil {
				if !errors.IsAlreadyExists(err) {
					return fmt.Errorf("error creating manifest: %v", err)
				} else {
					fmt.Println("HostedCluster object already exists...")
				}
			}
			fmt.Printf("HostedCluster Restored: %v\n", hc.Name)
			break
		}
	}
	return nil
}
