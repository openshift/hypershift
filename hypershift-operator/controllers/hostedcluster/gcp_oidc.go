package hostedcluster

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/oidc"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/go-logr/logr"
)

const (
	gcpOIDCDocumentsFinalizer = "hypershift.io/gcp-oidc-discovery"
)

func (r *HostedClusterReconciler) reconcileGCPOIDCDocuments(ctx context.Context, log logr.Logger, hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane) error {
	if hcluster.Spec.ServiceAccountSigningKey != nil && hcluster.Spec.ServiceAccountSigningKey.Name != "" {
		return nil
	}

	if controllerutil.ContainsFinalizer(hcluster, gcpOIDCDocumentsFinalizer) {
		return nil
	}

	// Defense-in-depth: the caller guards on bucket/client, but this
	// protects against future callsites that may omit the check.
	if r.GCPOIDCStorageBucketName == "" || r.GCSClient == nil {
		return fmt.Errorf("hypershift operator was not configured with a GCS bucket or credentials for OIDC document storage; set --gcp-oidc-storage-bucket-name")
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: hcp.Namespace,
			Name:      serviceAccountSigningKeySecret,
		},
	}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("sa-signing-key secret not yet available, will retry", "secret", client.ObjectKeyFromObject(secret))
			return nil
		}
		return fmt.Errorf("failed to get controlplane service account signing key %q: %w", client.ObjectKeyFromObject(secret), err)
	}

	if _, ok := secret.Data[serviceSignerPublicKey]; !ok {
		return fmt.Errorf("controlplane service account signing key secret %q missing required key %s", client.ObjectKeyFromObject(secret), serviceSignerPublicKey)
	}

	params := oidc.OIDCGeneratorParams{
		IssuerURL: hcp.Spec.IssuerURL,
		PubKey:    secret.Data[serviceSignerPublicKey],
	}

	for path, generator := range oidcDocumentGenerators() {
		bodyReader, err := generator(params)
		if err != nil {
			return fmt.Errorf("failed to generate OIDC document %s: %w", path, err)
		}
		objectName := hcluster.Spec.InfraID + path
		if err := r.GCSClient.UploadObject(ctx, r.GCPOIDCStorageBucketName, objectName, bodyReader); err != nil {
			return fmt.Errorf("failed to upload %s to gs://%s/%s: %w", path, r.GCPOIDCStorageBucketName, objectName, err)
		}
		log.Info("Uploaded OIDC document to GCS", "path", path, "bucket", r.GCPOIDCStorageBucketName, "object", objectName)
	}

	if !controllerutil.ContainsFinalizer(hcluster, gcpOIDCDocumentsFinalizer) {
		controllerutil.AddFinalizer(hcluster, gcpOIDCDocumentsFinalizer)
		if err := r.Client.Update(ctx, hcluster); err != nil {
			return fmt.Errorf("failed to update the hosted cluster after adding the %s finalizer: %w", gcpOIDCDocumentsFinalizer, err)
		}
	}

	log.Info("Successfully uploaded GCP OIDC documents to GCS bucket", "bucket", r.GCPOIDCStorageBucketName)
	return nil
}

func (r *HostedClusterReconciler) cleanupGCPOIDCBucketData(ctx context.Context, log logr.Logger, hcluster *hyperv1.HostedCluster) error {
	if !controllerutil.ContainsFinalizer(hcluster, gcpOIDCDocumentsFinalizer) {
		return nil
	}

	if r.GCPOIDCStorageBucketName == "" || r.GCSClient == nil {
		return fmt.Errorf("hypershift operator was not configured with a GCS bucket; cannot clean up OIDC documents. Please set up the bucket or clean up manually and remove the %s finalizer", gcpOIDCDocumentsFinalizer)
	}

	var deleteErrors []error
	for path := range oidcDocumentGenerators() {
		objectName := hcluster.Spec.InfraID + path
		if err := r.GCSClient.DeleteObject(ctx, r.GCPOIDCStorageBucketName, objectName); err != nil {
			log.Error(err, "Failed to delete OIDC document from GCS", "object", objectName)
			deleteErrors = append(deleteErrors, err)
		}
	}

	if len(deleteErrors) > 0 {
		return fmt.Errorf("failed to delete %d OIDC documents from GCS bucket %s", len(deleteErrors), r.GCPOIDCStorageBucketName)
	}

	controllerutil.RemoveFinalizer(hcluster, gcpOIDCDocumentsFinalizer)
	if err := r.Client.Update(ctx, hcluster); err != nil {
		return fmt.Errorf("failed to update hostedcluster after removing %s finalizer: %w", gcpOIDCDocumentsFinalizer, err)
	}

	log.Info("Cleaned up GCP OIDC documents from GCS bucket", "bucket", r.GCPOIDCStorageBucketName)
	return nil
}
