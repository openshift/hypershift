package certificaterevocationcontroller

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"sort"
	"strings"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/hypershift/v1beta1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	hypershiftinformers "github.com/openshift/hypershift/client/informers/externalversions"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"
	"github.com/openshift/hypershift/control-plane-pki-operator/manifests"
	"github.com/openshift/library-go/pkg/certs/cert-inspection/certgraphanalysis"
	"github.com/openshift/library-go/pkg/certs/cert-inspection/certgraphapi"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1applyconfigurations "k8s.io/client-go/applyconfigurations/core/v1"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	certutil "k8s.io/client-go/util/cert"
	"k8s.io/klog/v2"
)

type CertificateRevocationController struct {
	kubeClient       kubernetes.Interface
	hypershiftClient hypershiftclient.Interface

	fieldManager string
	getCRR       func(namespace, name string) (*hypershiftv1beta1.CertificateRevocationRequest, error)
	getSecret    func(namespace, name string) (*corev1.Secret, error)
	listSecrets  func(namespace string) ([]*corev1.Secret, error)
	getConfigMap func(namespace, name string) (*corev1.ConfigMap, error)
}

// TODO: we need some sort of time-based GC for completed CRRs

func NewCertificateRevocationController(
	hostedControlPlane *hypershiftv1beta1.HostedControlPlane,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	hypershiftInformers hypershiftinformers.SharedInformerFactory,
	kubeClient kubernetes.Interface,
	hypershiftClient hypershiftclient.Interface,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &CertificateRevocationController{
		fieldManager:     "certificate-revocation-controller",
		kubeClient:       kubeClient,
		hypershiftClient: hypershiftClient,
		getCRR: func(namespace, name string) (*hypershiftv1beta1.CertificateRevocationRequest, error) {
			return hypershiftInformers.Hypershift().V1beta1().CertificateRevocationRequests().Lister().CertificateRevocationRequests(namespace).Get(name)
		},
		getSecret: func(namespace, name string) (*corev1.Secret, error) {
			return kubeInformersForNamespaces.InformersFor(namespace).Core().V1().Secrets().Lister().Secrets(namespace).Get(name)
		},
		listSecrets: func(namespace string) ([]*corev1.Secret, error) {
			return kubeInformersForNamespaces.InformersFor(namespace).Core().V1().Secrets().Lister().Secrets(namespace).List(labels.Everything())
		},
		getConfigMap: func(namespace, name string) (*corev1.ConfigMap, error) {
			return kubeInformersForNamespaces.InformersFor(namespace).Core().V1().ConfigMaps().Lister().ConfigMaps(namespace).Get(name)
		},
	}

	crrInformer := hypershiftInformers.Hypershift().V1beta1().CertificateRevocationRequests().Informer()
	secretInformer := kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets().Informer()
	configMapInformer := kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().ConfigMaps().Informer()
	listCRRs := func(namespace string) ([]*hypershiftv1beta1.CertificateRevocationRequest, error) {
		return hypershiftInformers.Hypershift().V1beta1().CertificateRevocationRequests().Lister().CertificateRevocationRequests(hostedControlPlane.Namespace).List(labels.Everything())
	}

	return factory.New().
		WithInformersQueueKeysFunc(enqueueCertificateRevocationRequest, crrInformer).
		WithInformersQueueKeysFunc(enqueueSecret(listCRRs), secretInformer).
		WithInformersQueueKeysFunc(enqueueConfigMap(listCRRs), configMapInformer).
		WithSync(c.syncCertificateRevocationRequest).
		ResyncEvery(time.Minute).
		ToController("CertificateRevocationController", eventRecorder.WithComponentSuffix(c.fieldManager))
}

func enqueueCertificateRevocationRequest(obj runtime.Object) []string {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.ErrorS(err, "could not determine queue key")
		return nil
	}
	return []string{key}
}

func enqueueSecret(listCRRs func(namespace string) ([]*hypershiftv1beta1.CertificateRevocationRequest, error)) func(obj runtime.Object) []string {
	return func(obj runtime.Object) []string {
		secret, ok := obj.(*corev1.Secret)
		if !ok {
			klog.ErrorS(fmt.Errorf("unexpected object of type %T, wanted %T", obj, &corev1.Secret{}), "could not determine queue key")
			return nil
		}
		signer, ok := signerClassForSecret(secret)
		if !ok {
			return nil
		}
		return enqueueForSigner(secret.Namespace, signer, listCRRs)
	}
}

func enqueueForSigner(namespace string, signer certificates.SignerClass, listCRRs func(namespace string) ([]*hypershiftv1beta1.CertificateRevocationRequest, error)) []string {
	crrs, err := listCRRs(namespace)
	if err != nil {
		klog.ErrorS(err, "could not determine queue key")
		return nil
	}
	var keys []string
	for _, crr := range crrs {
		if crr.Spec.SignerClass == string(signer) {
			key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(crr)
			if err != nil {
				klog.ErrorS(err, "could not determine queue key")
				return nil
			}
			keys = append(keys, key)
		}
	}
	return keys
}

// signerClassForSecret determines the signer classes that the secret contains data for.
// We could use this transformation to create an index, but we expect the scale of resource
// counts for this controller to be very small (maybe O(10)) and the rate of change to be
// low, so the extra memory cost in indices is not valuable.
func signerClassForSecret(secret *corev1.Secret) (certificates.SignerClass, bool) {
	switch secret.Name {
	case manifests.CustomerSystemAdminSigner(secret.Namespace).Name:
		return certificates.CustomerBreakGlassSigner, true
	default:
		return "", false
	}
}

func secretForSignerClass(namespace string, signer certificates.SignerClass) (*corev1.Secret, bool) {
	switch signer {
	case certificates.CustomerBreakGlassSigner:
		return manifests.CustomerSystemAdminSigner(namespace), true
	default:
		return nil, false
	}
}

func clientCertSecretForSignerClass(namespace string, signer certificates.SignerClass) (*corev1.Secret, bool) {
	switch signer {
	case certificates.CustomerBreakGlassSigner:
		return manifests.CustomerSystemAdminClientCertSecret(namespace), true
	default:
		return nil, false
	}
}

func enqueueConfigMap(listCRRs func(namespace string) ([]*hypershiftv1beta1.CertificateRevocationRequest, error)) func(obj runtime.Object) []string {
	return func(obj runtime.Object) []string {
		configMap, ok := obj.(*corev1.ConfigMap)
		if !ok {
			klog.ErrorS(fmt.Errorf("unexpected object of type %T, wanted %T", obj, &corev1.ConfigMap{}), "could not determine queue key")
			return nil
		}
		signer, ok := signerClassForConfigMap(configMap)
		if !ok {
			return nil
		}
		return enqueueForSigner(configMap.Namespace, signer, listCRRs)
	}
}

// signerClassForConfigMap determines the signer classes that the configmap contains data for.
// We could use this transformation to create an index, but we expect the scale of resource
// counts for this controller to be very small (maybe O(10)) and the rate of change to be
// low, so the extra memory cost in indices is not valuable.
func signerClassForConfigMap(configMap *corev1.ConfigMap) (certificates.SignerClass, bool) {
	switch configMap.Name {
	case manifests.CustomerSystemAdminSignerCA(configMap.Namespace).Name:
		return certificates.CustomerBreakGlassSigner, true
	default:
		return "", false
	}
}

func configMapForSignerClass(namespace string, signer certificates.SignerClass) (*corev1.ConfigMap, bool) {
	switch signer {
	case certificates.CustomerBreakGlassSigner:
		return manifests.CustomerSystemAdminSignerCA(namespace), true
	default:
		return nil, false
	}
}

func (c *CertificateRevocationController) syncCertificateRevocationRequest(ctx context.Context, syncContext factory.SyncContext) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(syncContext.QueueKey())
	if err != nil {
		return err
	}

	action, requeue, err := c.processCertificateRevocationRequest(namespace, name, nil)
	if err != nil {
		return err
	}
	if requeue {
		return factory.SyntheticRequeueError
	}
	if action != nil {
		if err := action.validate(); err != nil {
			panic(err)
		}
		if action.event != nil {
			syncContext.Recorder().Eventf(action.event.reason, action.event.messageFmt, action.event.args...)
		}

		if action.crr != nil {
			_, err := c.hypershiftClient.HypershiftV1beta1().CertificateRevocationRequests(*action.crr.Namespace).ApplyStatus(ctx, action.crr, metav1.ApplyOptions{FieldManager: c.fieldManager})
			return err
		} else if action.cm != nil {
			_, err := c.kubeClient.CoreV1().Secrets(*action.secret.Namespace).Apply(ctx, action.secret, metav1.ApplyOptions{FieldManager: c.fieldManager})
			return err
		} else if action.secret != nil {
			_, err := c.kubeClient.CoreV1().ConfigMaps(*action.cm.Namespace).Apply(ctx, action.cm, metav1.ApplyOptions{FieldManager: c.fieldManager})
			return err
		}
	}

	return nil
}

type actions struct {
	event  *eventInfo
	crr    *hypershiftv1beta1applyconfigurations.CertificateRevocationRequestApplyConfiguration
	secret *corev1applyconfigurations.SecretApplyConfiguration
	cm     *corev1applyconfigurations.ConfigMapApplyConfiguration
}

func (a *actions) validate() error {
	var set int
	if a.crr != nil {
		set += 1
	}
	if a.cm != nil {
		set += 1
	}
	if a.secret != nil {
		set += 1
	}
	if set != 1 {
		return errors.New("programmer error: more than one action set")
	}
	return nil
}

type eventInfo struct {
	reason, messageFmt string
	args               []interface{}
}

func event(reason, messageFmt string, args ...interface{}) *eventInfo {
	return &eventInfo{
		reason:     reason,
		messageFmt: messageFmt,
		args:       args,
	}
}

func (c *CertificateRevocationController) processCertificateRevocationRequest(namespace, name string, now func() time.Time) (*actions, bool, error) {
	if now == nil {
		now = time.Now
	}

	crr, err := c.getCRR(namespace, name)
	if apierrors.IsNotFound(err) {
		return nil, false, nil // nothing to be done, CSR is gone
	}
	if err != nil {
		return nil, false, err
	}

	switch crr.Status.Phase {
	case hypershiftv1beta1.CertificateRevocationRequestPhaseUnknown, "":
		// validate the signer class - we've got a validation rule on the object, but system:masters can bypass that
		if !certificates.ValidSignerClass(crr.Spec.SignerClass) {
			cfg := hypershiftv1beta1applyconfigurations.CertificateRevocationRequest(name, namespace)
			cfg.Status = hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatus().
				WithPhase(hypershiftv1beta1.CertificateRevocationRequestPhaseUnknown).
				WithConditions(metav1applyconfigurations.Condition().
					WithType(hypershiftv1beta1.SignerClassValidType).
					WithStatus(metav1.ConditionFalse).
					WithLastTransitionTime(metav1.NewTime(now())).
					WithReason(hypershiftv1beta1.SignerClassUnknownReason).
					WithMessage(fmt.Sprintf("Signer class %q unknown.", crr.Spec.SignerClass)),
				)
			return &actions{crr: cfg}, false, nil
		}

		// we haven't seen this CRR before, so choose a revocation timestamp
		revocationTimestamp := now()
		cfg := hypershiftv1beta1applyconfigurations.CertificateRevocationRequest(name, namespace)
		cfg.Status = hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatus().
			WithRevocationTimestamp(metav1.NewTime(revocationTimestamp)).
			WithPhase(hypershiftv1beta1.CertificateRevocationRequestPhaseRegenerating).
			WithConditions(metav1applyconfigurations.Condition().
				WithType(hypershiftv1beta1.SignerClassValidType).
				WithStatus(metav1.ConditionTrue).
				WithLastTransitionTime(metav1.NewTime(now())).
				WithReason(hypershiftv1beta1.AsExpectedReason).
				WithMessage(fmt.Sprintf("Signer class %q known.", crr.Spec.SignerClass)),
			)
		e := event("CertificateRevocationStarted", "%q certificates valid before %s will be revoked.", crr.Spec.SignerClass, revocationTimestamp)
		return &actions{event: e, crr: cfg}, false, nil
	case hypershiftv1beta1.CertificateRevocationRequestPhaseRegenerating:
		// a revocation timestamp exists, so we need to ensure new certs are generated after that point
		signer, ok := secretForSignerClass(namespace, certificates.SignerClass(crr.Spec.SignerClass))
		if !ok {
			// we should never reach this case as we validate the class before transitioning states, and it's immutable
			return nil, false, nil
		}

		certs, err := c.loadCertificateSecret(signer.Namespace, signer.Name)
		if err != nil {
			return nil, false, err
		}

		var certificateNeedsRegeneration bool
		if len(certs) > 0 {
			onlyAfter, beforeAndDuring := partitionCertificatesByValidity(certs, crr.Status.RevocationTimestamp.Time)
			certificateNeedsRegeneration = len(beforeAndDuring) > 0 || len(onlyAfter) == 0
		} else {
			// there's no certificate there, regenerate it
			certificateNeedsRegeneration = true
		}

		if certificateNeedsRegeneration {
			// when we revoke a signer, we need to keep a copy of a previous leaf to verify that it's
			// invalid when we're done revoking it
			current, err := c.getSecret(signer.Namespace, signer.Name)
			if err != nil {
				return nil, false, err
			}

			// base36(sha224(value)) produces a useful, deterministic value that fits the requirements to be
			// a Kubernetes object name (honoring length requirement, is a valid DNS subdomain, etc)
			hash := sha256.Sum224([]byte(crr.Name))
			var i big.Int
			i.SetBytes(hash[:])
			previousSignerName := i.Text(36)
			copied, err := c.getSecret(namespace, previousSignerName)
			if err != nil && !apierrors.IsNotFound(err) {
				return nil, false, err
			}
			if apierrors.IsNotFound(err) ||
				!reflect.DeepEqual(copied.Data[corev1.TLSCertKey], current.Data[corev1.TLSCertKey]) ||
				!reflect.DeepEqual(copied.Data[corev1.TLSPrivateKeyKey], current.Data[corev1.TLSPrivateKeyKey]) {
				secretCfg := corev1applyconfigurations.Secret(previousSignerName, signer.Namespace).
					WithOwnerReferences(metav1applyconfigurations.OwnerReference().
						WithAPIVersion(hypershiftv1beta1.GroupVersion.String()).
						WithKind("CertificateRevocationRequest").
						WithName(crr.Name).
						WithUID(crr.UID)).
					WithType(corev1.SecretTypeTLS).
					WithData(map[string][]byte{
						corev1.TLSCertKey:       current.Data[corev1.TLSCertKey],
						corev1.TLSPrivateKeyKey: current.Data[corev1.TLSPrivateKeyKey],
					})
				return &actions{secret: secretCfg}, false, nil
			}

			if crr.Status.PreviousSigner == nil {
				cfg := hypershiftv1beta1applyconfigurations.CertificateRevocationRequest(name, namespace)
				cfg.Status = hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatus().
					WithPreviousSigner(corev1.LocalObjectReference{Name: previousSignerName})
				return &actions{crr: cfg}, false, nil
			}

			secretCfg := corev1applyconfigurations.Secret(signer.Name, signer.Namespace).
				WithAnnotations(map[string]string{
					certrotation.CertificateNotAfterAnnotation: "force-regeneration",
				})
			return &actions{secret: secretCfg}, false, nil
		}

		cfg := hypershiftv1beta1applyconfigurations.CertificateRevocationRequest(name, namespace)
		cfg.Status = hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatus().
			WithPhase(hypershiftv1beta1.CertificateRevocationRequestPhasePropagating)
		e := event("CertificateRevocationProgressing", "New %q signer certificates generated.", crr.Spec.SignerClass)
		return &actions{event: e, crr: cfg}, false, nil
	case hypershiftv1beta1.CertificateRevocationRequestPhasePropagating:
		// new certificates exist, we need to ensure they work against the API server
		signer, ok := secretForSignerClass(namespace, certificates.SignerClass(crr.Spec.SignerClass))
		if !ok {
			// we should never reach this case as we validate the class before transitioning states, and it's immutable
			return nil, false, nil
		}

		signers, err := c.loadCertificateSecret(signer.Namespace, signer.Name)
		if err != nil {
			return nil, false, err
		}
		if signers == nil {
			return nil, false, nil
		}

		totalClientCA := manifests.TotalKASClientCABundle(namespace)
		totalClientTrustBundle, err := c.loadTrustBundleConfigMap(totalClientCA.Namespace, totalClientCA.Name)
		if err != nil {
			return nil, false, err
		}
		if totalClientTrustBundle == nil {
			return nil, false, nil
		}

		// TODO: technically, this will race - it takes time for the trust bundle to be re-loaded by KAS.
		// however, using a real client to send a SSR is very hard to test ...
		if len(trustedCertificates(totalClientTrustBundle, []*certificateSecret{{cert: signers[0]}}, now)) == 0 {
			return nil, false, nil
		}

		// we need to re-queue as KAS loading this won't trigger any k8s api events

		cfg := hypershiftv1beta1applyconfigurations.CertificateRevocationRequest(name, namespace)
		cfg.Status = hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatus().
			WithPhase(hypershiftv1beta1.CertificateRevocationRequestPhaseRevoking)
		return &actions{crr: cfg}, false, nil
	case hypershiftv1beta1.CertificateRevocationRequestPhaseRevoking:
		// new certificates propagated, time to remove all previous certificates from trust bundle

		trustBundleCA, ok := configMapForSignerClass(namespace, certificates.SignerClass(crr.Spec.SignerClass))
		if !ok {
			// we should never reach this case as we validate the class before transitioning states, and it's immutable
			return nil, false, nil
		}

		currentTrustBundle, err := c.loadTrustBundleConfigMap(trustBundleCA.Namespace, trustBundleCA.Name)
		if err != nil {
			return nil, false, err
		}
		if currentTrustBundle == nil {
			return nil, false, nil
		}

		onlyAfter, _ := partitionCertificatesByValidity(currentTrustBundle, crr.Status.RevocationTimestamp.Time)
		if len(onlyAfter) != len(currentTrustBundle) {
			// we need to prune the trust bundle, but first, we need to ensure that all leaf certificates
			// trusted by the current bundle continue to be trusted by the filtered bundle; all leaves must
			// have been regenerated for us to revoke the old certificates
			secrets, err := c.listSecrets(namespace)
			if err != nil {
				return nil, false, fmt.Errorf("failed to list secrets: %w", err)
			}

			var existingLeafCerts []*certificateSecret
			for _, secret := range secrets {
				certKeyInfo, err := certgraphanalysis.InspectSecret(secret)
				if err != nil {
					return nil, false, fmt.Errorf("failed to load cert/key pair from secret %s/%s: %w", secret.Namespace, secret.Name, err)
				}
				if certKeyInfo == nil {
					continue
				}

				certs, err := certutil.ParseCertsPEM(secret.Data[corev1.TLSCertKey])
				if err != nil {
					return nil, false, fmt.Errorf("could not parse certificate in secret %s/%s: %w", secret.Namespace, secret.Name, err)
				}

				if isLeafCertificate(certKeyInfo) {
					existingLeafCerts = append(existingLeafCerts, &certificateSecret{
						namespace: secret.Namespace,
						name:      secret.Name,
						cert:      certs[0],
					})
				}
			}

			currentlyTrustedLeaves := trustedCertificates(currentTrustBundle, existingLeafCerts, now)
			futureTrustedLeaves := trustedCertificates(onlyAfter, existingLeafCerts, now)

			if diff := certificateSecretNames(currentlyTrustedLeaves).Difference(certificateSecretNames(futureTrustedLeaves)); diff.Len() != 0 {
				list := diff.UnsortedList()
				sort.Strings(list)
				cfg := hypershiftv1beta1applyconfigurations.CertificateRevocationRequest(name, namespace)
				cfg.Status = hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatus().
					WithConditions(metav1applyconfigurations.Condition().
						WithType(hypershiftv1beta1.LeafCertificatesRegeneratedType).
						WithStatus(metav1.ConditionFalse).
						WithLastTransitionTime(metav1.NewTime(now())).
						WithReason(hypershiftv1beta1.LeafCertificatesStaleReason).
						WithMessage(fmt.Sprintf("Revocation would lose trust for leaf certificates: %v.", strings.Join(list, ", "))),
					)
				return &actions{crr: cfg}, false, nil
			}

			newBundlePEM, err := certutil.EncodeCertificates(onlyAfter...)
			if err != nil {
				return nil, false, fmt.Errorf("failed to encode new cert bundle for configmap %s/%s: %w", trustBundleCA.Name, trustBundleCA.Namespace, err)
			}

			caCfg := corev1applyconfigurations.ConfigMap(trustBundleCA.Name, trustBundleCA.Namespace)
			caCfg.WithData(map[string]string{
				"ca-bundle.crt": string(newBundlePEM),
			})
			return &actions{cm: caCfg}, false, nil
		}

		// we're already pruned, we can continue
		cfg := hypershiftv1beta1applyconfigurations.CertificateRevocationRequest(name, namespace)
		cfg.Status = hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatus().
			WithPhase(hypershiftv1beta1.CertificateRevocationRequestPhaseValidating).
			WithConditions(metav1applyconfigurations.Condition().
				WithType(hypershiftv1beta1.LeafCertificatesRegeneratedType).
				WithStatus(metav1.ConditionTrue).
				WithLastTransitionTime(metav1.NewTime(now())).
				WithReason(hypershiftv1beta1.AsExpectedReason).
				WithMessage("All leaf certificates are re-generated."),
			)
		e := event("CertificateRevocationProgressing", "Previous %q signer certificates revoked.", crr.Spec.SignerClass)
		return &actions{event: e, crr: cfg}, false, nil
	case hypershiftv1beta1.CertificateRevocationRequestPhaseValidating:
		// old certificates removed, time to ensure old certificate is rejected
		oldCertSecret, err := c.getSecret(namespace, crr.Status.PreviousSigner.Name)
		if err != nil {
			return nil, false, err
		}

		oldCertPEM, ok := oldCertSecret.Data[corev1.TLSCertKey]
		if !ok || len(oldCertPEM) == 0 {
			return nil, false, err
		}

		oldCerts, err := certutil.ParseCertsPEM(oldCertPEM)
		if err != nil {
			return nil, false, err
		}

		totalClientCA := manifests.TotalKASClientCABundle(namespace)
		totalClientTrustBundle, err := c.loadTrustBundleConfigMap(totalClientCA.Namespace, totalClientCA.Name)
		if err != nil {
			return nil, false, err
		}
		if totalClientTrustBundle == nil {
			return nil, false, nil
		}
		// TODO: technically, this will race - it takes time for the trust bundle to be re-loaded by KAS.
		// however, using a real client to send a SSR is very hard to test ...
		if len(trustedCertificates(totalClientTrustBundle, []*certificateSecret{{cert: oldCerts[0]}}, now)) != 0 {
			return nil, false, nil
		}

		// we need to re-queue as KAS loading this won't trigger any k8s api events

		cfg := hypershiftv1beta1applyconfigurations.CertificateRevocationRequest(name, namespace)
		cfg.Status = hypershiftv1beta1applyconfigurations.CertificateRevocationRequestStatus().
			WithPhase(hypershiftv1beta1.CertificateRevocationRequestPhaseComplete)
		e := event("CertificateRevocationComplete", "%q signer certificates revoked.", crr.Spec.SignerClass)
		return &actions{event: e, crr: cfg}, false, nil
	default:
		// we should never get here ... ? add a status condition?
		return nil, false, nil
	}
}

func (c *CertificateRevocationController) loadCertificateSecret(namespace, name string) ([]*x509.Certificate, error) {
	secret, err := c.getSecret(namespace, name)
	if apierrors.IsNotFound(err) {
		return nil, nil // try again later
	}
	if err != nil {
		return nil, fmt.Errorf("could not fetch client cert secret %s/%s: %w", namespace, name, err)
	}

	clientCertPEM, ok := secret.Data[corev1.TLSCertKey]
	if !ok || len(clientCertPEM) == 0 {
		return nil, fmt.Errorf("found no certificate in secret %s/%s: %w", namespace, name, err)
	}

	clientCertificates, err := certutil.ParseCertsPEM(clientCertPEM)
	if err != nil {
		return nil, fmt.Errorf("could not parse certificate in secret %s/%s: %w", namespace, name, err)
	}

	return clientCertificates, nil
}

func (c *CertificateRevocationController) loadTrustBundleConfigMap(namespace, name string) ([]*x509.Certificate, error) {
	configMap, err := c.getConfigMap(namespace, name)
	if apierrors.IsNotFound(err) {
		return nil, nil // try again later
	}
	if err != nil {
		return nil, fmt.Errorf("could not fetch configmap %s/%s: %w", namespace, name, err)
	}

	caPEM, ok := configMap.Data["ca-bundle.crt"]
	if !ok || len(caPEM) == 0 {
		return nil, fmt.Errorf("found no trust bundle in configmap %s/%s: %w", namespace, name, err)
	}

	trustBundle, err := certutil.ParseCertsPEM([]byte(caPEM))
	if err != nil {
		return nil, fmt.Errorf("could not parse trust bundle in configmap %s/%s: %w", namespace, name, err)
	}

	return trustBundle, nil
}

func certificateSecretNames(leaves []*certificateSecret) sets.Set[string] {
	names := sets.Set[string]{}
	for _, leaf := range leaves {
		names.Insert(fmt.Sprintf("%s/%s", leaf.namespace, leaf.name))
	}
	return names
}

type certificateSecret struct {
	namespace, name string
	cert            *x509.Certificate
}

func isLeafCertificate(certKeyPair *certgraphapi.CertKeyPair) bool {
	if certKeyPair.Spec.Details.SignerDetails != nil {
		return false
	}

	issuerInfo := certKeyPair.Spec.CertMetadata.CertIdentifier.Issuer
	if issuerInfo == nil {
		return false
	}

	// a certificate that's not self-signed is a leaf
	return issuerInfo.CommonName != certKeyPair.Spec.CertMetadata.CertIdentifier.CommonName
}

func trustedCertificates(trustBundle []*x509.Certificate, secrets []*certificateSecret, now func() time.Time) []*certificateSecret {
	trustPool := x509.NewCertPool()

	for i := range trustBundle {
		trustPool.AddCert(trustBundle[i])
	}

	verifyOpts := x509.VerifyOptions{
		Roots:       trustPool,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		CurrentTime: now(),
	}

	var trusted []*certificateSecret
	for i := range secrets {
		if _, err := secrets[i].cert.Verify(verifyOpts); err == nil {
			trusted = append(trusted, secrets[i])
		}
	}
	return trusted
}

// partitionCertificatesByValidity partitions certsPEM into two disjoint sets of certificates:
// those only valid after the cutoff and those valid at or before the cutoff
func partitionCertificatesByValidity(certs []*x509.Certificate, cutoff time.Time) ([]*x509.Certificate, []*x509.Certificate) {
	var onlyAfter, beforeAndDuring []*x509.Certificate
	for _, cert := range certs {
		if cert.NotBefore.After(cutoff) {
			onlyAfter = append(onlyAfter, cert)
		} else {
			beforeAndDuring = append(beforeAndDuring, cert)
		}
	}

	return onlyAfter, beforeAndDuring
}
