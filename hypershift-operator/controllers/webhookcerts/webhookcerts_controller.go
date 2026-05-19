/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package webhookcerts

import (
	"bytes"
	"context"
	"crypto/x509"
	"fmt"
	"time"

	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/upsert"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/go-logr/logr"
)

const (
	// CASecretName is the name of the Secret containing the self-signed CA used to sign webhook serving certs.
	CASecretName = "webhook-serving-ca"
	// ServingCertSecretName is the name of the Secret containing the serving cert for the webhook server.
	ServingCertSecretName = "manager-serving-cert"

	requeueInterval = 12 * time.Hour

	// service-ca annotations used on the operator Service to trigger cert generation.
	serviceCABetaAnnotation  = "service.beta.openshift.io/serving-cert-secret-name"
	serviceCAAlphaAnnotation = "service.alpha.openshift.io/serving-cert-secret-name"

	// service-ca annotations placed on secrets it creates.
	originatingServiceBetaAnnotation  = "service.beta.openshift.io/originating-service-name"
	originatingServiceAlphaAnnotation = "service.alpha.openshift.io/originating-service-name"

	// service-ca annotation on webhook configs that triggers CA bundle injection.
	injectCABundleAnnotation = "service.beta.openshift.io/inject-cabundle"

	webhookConfigName = "hypershift.openshift.io"
)

// WebhookCertReconciler reconciles the self-managed webhook CA and serving cert.
// It is used on non-OpenShift clusters where the service-ca operator is not available.
type WebhookCertReconciler struct {
	Client         client.Client
	Namespace      string
	ServiceName    string
	createOrUpdate upsert.CreateOrUpdateFN
}

func (r *WebhookCertReconciler) SetupWithManager(mgr ctrl.Manager, createOrUpdate upsert.CreateOrUpdateProvider) error {
	r.Client = mgr.GetClient()
	r.createOrUpdate = createOrUpdate.CreateOrUpdate

	return ctrl.NewControllerManagedBy(mgr).
		Named("webhookcerts").
		For(&corev1.Secret{}, builder.WithPredicates(predicate.NewPredicateFuncs(func(o client.Object) bool {
			return o.GetNamespace() == r.Namespace &&
				(o.GetName() == CASecretName || o.GetName() == ServingCertSecretName)
		}))).
		Complete(r)
}

func (r *WebhookCertReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	// 0. Handle upgrade from service-ca managed certs.
	// On existing OpenShift clusters, the service-ca operator may have created the
	// serving cert secret and annotated the Service. We must remove these before
	// reconciling our own certs, otherwise service-ca will keep overwriting the secret
	// with a cert signed by a different CA than the one we inject into webhook configs.
	if err := r.removeServiceCAResources(ctx, log); err != nil {
		return ctrl.Result{}, err
	}

	// 1. Reconcile the self-signed CA.
	caSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CASecretName,
			Namespace: r.Namespace,
		},
	}
	if _, err := r.createOrUpdate(ctx, r.Client, caSecret, func() error {
		caSecret.Type = corev1.SecretTypeOpaque
		return certs.ReconcileSelfSignedCA(caSecret, "hypershift-webhook-ca", "openshift")
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile webhook CA secret: %w", err)
	}

	// 2. Reconcile the serving cert signed by the CA.
	dnsNames := webhookDNSNames(r.ServiceName, r.Namespace)
	servingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ServingCertSecretName,
			Namespace: r.Namespace,
		},
	}
	if _, err := r.createOrUpdate(ctx, r.Client, servingSecret, func() error {
		servingSecret.Type = corev1.SecretTypeTLS
		return certs.ReconcileSignedCert(
			servingSecret,
			caSecret,
			"hypershift-operator",
			[]string{"openshift"},
			[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			corev1.TLSCertKey,
			corev1.TLSPrivateKeyKey,
			"",
			dnsNames,
			nil,
		)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to reconcile webhook serving cert: %w", err)
	}

	// 3. Patch caBundle on CRDs with conversion webhooks.
	caBundle := caSecret.Data[certs.CASignerCertMapKey]
	if err := r.patchCRDsCABundle(ctx, caBundle); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch CRD caBundle: %w", err)
	}

	// 4. Patch caBundle on webhook configurations.
	if err := r.patchWebhookConfigsCABundle(ctx, caBundle); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to patch webhook config caBundle: %w", err)
	}

	log.Info("Webhook certs reconciled", "requeueAfter", requeueInterval)
	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

// patchCRDsCABundle patches the caBundle on all CRDs whose conversion webhook points to our service.
func (r *WebhookCertReconciler) patchCRDsCABundle(ctx context.Context, caBundle []byte) error {
	crdList := &apiextensionsv1.CustomResourceDefinitionList{}
	if err := r.Client.List(ctx, crdList); err != nil {
		return fmt.Errorf("failed to list CRDs: %w", err)
	}

	for i := range crdList.Items {
		crd := &crdList.Items[i]
		if crd.Spec.Conversion == nil || crd.Spec.Conversion.Webhook == nil || crd.Spec.Conversion.Webhook.ClientConfig == nil {
			continue
		}
		svc := crd.Spec.Conversion.Webhook.ClientConfig.Service
		if svc == nil || svc.Name != r.ServiceName || svc.Namespace != r.Namespace {
			continue
		}
		if bytes.Equal(crd.Spec.Conversion.Webhook.ClientConfig.CABundle, caBundle) {
			continue
		}
		patch := client.MergeFrom(crd.DeepCopy())
		crd.Spec.Conversion.Webhook.ClientConfig.CABundle = caBundle
		if err := r.Client.Patch(ctx, crd, patch); err != nil {
			return fmt.Errorf("failed to patch CRD %s caBundle: %w", crd.Name, err)
		}
	}
	return nil
}

// removeInjectCABundleAnnotation removes the service-ca inject-cabundle annotation from
// webhook configurations so the service-ca operator stops overwriting our self-managed caBundle.
func (r *WebhookCertReconciler) removeInjectCABundleAnnotation(ctx context.Context, log logr.Logger) error {
	mwc := &admissionregistrationv1.MutatingWebhookConfiguration{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: webhookConfigName}, mwc); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get MutatingWebhookConfiguration: %w", err)
		}
	} else if _, ok := mwc.Annotations[injectCABundleAnnotation]; ok {
		delete(mwc.Annotations, injectCABundleAnnotation)
		if err := r.Client.Update(ctx, mwc); err != nil {
			return fmt.Errorf("failed to remove %s annotation from MutatingWebhookConfiguration: %w", injectCABundleAnnotation, err)
		}
		log.Info("Removed inject-cabundle annotation from MutatingWebhookConfiguration")
	}

	vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: webhookConfigName}, vwc); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get ValidatingWebhookConfiguration: %w", err)
		}
	} else if _, ok := vwc.Annotations[injectCABundleAnnotation]; ok {
		delete(vwc.Annotations, injectCABundleAnnotation)
		if err := r.Client.Update(ctx, vwc); err != nil {
			return fmt.Errorf("failed to remove %s annotation from ValidatingWebhookConfiguration: %w", injectCABundleAnnotation, err)
		}
		log.Info("Removed inject-cabundle annotation from ValidatingWebhookConfiguration")
	}

	return nil
}

// patchWebhookConfigsCABundle patches the caBundle on MutatingWebhookConfiguration and ValidatingWebhookConfiguration
// resources named hypershift.openshift.io.
func (r *WebhookCertReconciler) patchWebhookConfigsCABundle(ctx context.Context, caBundle []byte) error {
	// Patch MutatingWebhookConfiguration
	mwc := &admissionregistrationv1.MutatingWebhookConfiguration{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: webhookConfigName}, mwc); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get MutatingWebhookConfiguration: %w", err)
		}
	} else {
		needsPatch := false
		for i := range mwc.Webhooks {
			if !bytes.Equal(mwc.Webhooks[i].ClientConfig.CABundle, caBundle) {
				needsPatch = true
				mwc.Webhooks[i].ClientConfig.CABundle = caBundle
			}
		}
		if needsPatch {
			if err := r.Client.Update(ctx, mwc); err != nil {
				return fmt.Errorf("failed to update MutatingWebhookConfiguration: %w", err)
			}
		}
	}

	// Patch ValidatingWebhookConfiguration
	vwc := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: webhookConfigName}, vwc); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get ValidatingWebhookConfiguration: %w", err)
		}
	} else {
		needsPatch := false
		for i := range vwc.Webhooks {
			if !bytes.Equal(vwc.Webhooks[i].ClientConfig.CABundle, caBundle) {
				needsPatch = true
				vwc.Webhooks[i].ClientConfig.CABundle = caBundle
			}
		}
		if needsPatch {
			if err := r.Client.Update(ctx, vwc); err != nil {
				return fmt.Errorf("failed to update ValidatingWebhookConfiguration: %w", err)
			}
		}
	}

	return nil
}

// removeServiceCAResources handles the upgrade from service-ca managed certs to self-managed certs.
// It removes the service-ca annotations from the operator Service and deletes the serving cert
// secret if it was created by service-ca, so the reconciler can recreate it with the self-managed CA.
func (r *WebhookCertReconciler) removeServiceCAResources(ctx context.Context, log logr.Logger) error {
	svc := &corev1.Service{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.ServiceName}, svc); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get operator service: %w", err)
		}
	} else {
		changed := false
		for _, annotation := range []string{serviceCABetaAnnotation, serviceCAAlphaAnnotation} {
			if _, ok := svc.Annotations[annotation]; ok {
				delete(svc.Annotations, annotation)
				changed = true
			}
		}
		if changed {
			if err := r.Client.Update(ctx, svc); err != nil {
				return fmt.Errorf("failed to remove service-ca annotations from operator service: %w", err)
			}
			log.Info("Removed service-ca annotations from operator service")
		}
	}

	// Remove the inject-cabundle annotation from webhook configurations so the
	// service-ca operator stops overwriting the caBundle we manage ourselves.
	if err := r.removeInjectCABundleAnnotation(ctx, log); err != nil {
		return err
	}

	// If the existing serving cert secret was created by service-ca, delete it
	// so we can recreate it signed by our self-managed CA.
	existingSecret := &corev1.Secret{}
	if err := r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: ServingCertSecretName}, existingSecret); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get existing serving cert secret: %w", err)
		}
	} else if isServiceCAManaged(existingSecret) {
		if err := r.Client.Delete(ctx, existingSecret); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete service-ca managed serving cert secret: %w", err)
		}
		log.Info("Deleted service-ca managed serving cert secret")
	}

	return nil
}

// isServiceCAManaged returns true if the secret was created by the service-ca operator.
func isServiceCAManaged(secret *corev1.Secret) bool {
	if secret.Annotations == nil {
		return false
	}
	_, hasBeta := secret.Annotations[originatingServiceBetaAnnotation]
	_, hasAlpha := secret.Annotations[originatingServiceAlphaAnnotation]
	return hasBeta || hasAlpha
}

// webhookDNSNames returns the DNS names for the webhook serving cert.
func webhookDNSNames(serviceName, namespace string) []string {
	return []string{
		fmt.Sprintf("%s.%s.svc", serviceName, namespace),
		fmt.Sprintf("%s.%s.svc.cluster.local", serviceName, namespace),
	}
}

// GenerateInitialWebhookCerts generates the CA and serving cert secrets for use at install time.
// It also returns the CA bundle bytes for injection into CRDs and webhook configs.
func GenerateInitialWebhookCerts(namespace, serviceName string) (*corev1.Secret, *corev1.Secret, []byte, error) {
	caSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CASecretName,
			Namespace: namespace,
		},
		Data: map[string][]byte{},
	}
	if err := certs.ReconcileSelfSignedCA(caSecret, "hypershift-webhook-ca", "openshift"); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate webhook CA: %w", err)
	}

	dnsNames := webhookDNSNames(serviceName, namespace)
	servingSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ServingCertSecretName,
			Namespace: namespace,
		},
		Type: corev1.SecretTypeTLS,
		Data: map[string][]byte{},
	}
	if err := certs.ReconcileSignedCert(
		servingSecret,
		caSecret,
		"hypershift-operator",
		[]string{"openshift"},
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		corev1.TLSCertKey,
		corev1.TLSPrivateKeyKey,
		"",
		dnsNames,
		nil,
	); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate webhook serving cert: %w", err)
	}

	caBundle := caSecret.Data[certs.CASignerCertMapKey]
	return caSecret, servingSecret, caBundle, nil
}

// EnsureWebhookCerts ensures that webhook cert secrets exist so the webhook
// server can start. If the serving cert secret already exists with valid data,
// this is a no-op (the volume mount handles file delivery). If the secret is
// missing or has empty data, new certs are generated and persisted as secrets.
func EnsureWebhookCerts(ctx context.Context, c client.Client, namespace, serviceName string) error {
	log := ctrl.LoggerFrom(ctx).WithName("webhook-cert-bootstrap")

	existingSecret := &corev1.Secret{}
	err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ServingCertSecretName}, existingSecret)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to check for existing serving cert secret: %w", err)
	}

	// if secret exists check it is not empty
	if err == nil {
		if len(existingSecret.Data[corev1.TLSCertKey]) > 0 && len(existingSecret.Data[corev1.TLSPrivateKeyKey]) > 0 {
			log.Info("Serving cert secret already exists, volume mount will provide certs")
			return nil
		}
		log.Info("Serving cert secret exists but has empty data, regenerating")
	}

	log.Info("Generating webhook certificates")
	caSecret, servingSecret, _, err := GenerateInitialWebhookCerts(namespace, serviceName)
	if err != nil {
		return fmt.Errorf("failed to generate webhook certs: %w", err)
	}

	if createErr := c.Create(ctx, caSecret); createErr != nil {
		if !apierrors.IsAlreadyExists(createErr) {
			return fmt.Errorf("failed to create CA secret: %w", createErr)
		}
		// CA already exists — fetch it and regenerate the serving cert so it
		// is signed by the persisted CA, not the transient one we just created.
		persistedCA := &corev1.Secret{}
		if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: CASecretName}, persistedCA); err != nil {
			return fmt.Errorf("failed to get existing CA secret: %w", err)
		}
		servingSecret.Data = map[string][]byte{}
		dnsNames := webhookDNSNames(serviceName, namespace)
		if err := certs.ReconcileSignedCert(
			servingSecret,
			persistedCA,
			"hypershift-operator",
			[]string{"openshift"},
			[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			corev1.TLSCertKey,
			corev1.TLSPrivateKeyKey,
			"",
			dnsNames,
			nil,
		); err != nil {
			return fmt.Errorf("failed to regenerate serving cert from persisted CA: %w", err)
		}
	}

	if createErr := c.Create(ctx, servingSecret); createErr != nil {
		if !apierrors.IsAlreadyExists(createErr) {
			return fmt.Errorf("failed to create serving cert secret: %w", createErr)
		}
		existingSecret.Data = servingSecret.Data
		if err := c.Update(ctx, existingSecret); err != nil {
			return fmt.Errorf("failed to update serving cert secret: %w", err)
		}
	}

	log.Info("Webhook certificates bootstrapped")
	return nil
}
