package certificaterevocationcontroller

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	certificatesv1alpha1applyconfigurations "github.com/openshift/hypershift/client/applyconfiguration/certificates/v1alpha1"
	hypershiftclient "github.com/openshift/hypershift/client/clientset/clientset"
	hypershiftinformers "github.com/openshift/hypershift/client/informers/externalversions"
	hcpmanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"
	"github.com/openshift/hypershift/control-plane-pki-operator/manifests"

	"github.com/openshift/library-go/pkg/certs/cert-inspection/certgraphanalysis"
	"github.com/openshift/library-go/pkg/certs/cert-inspection/certgraphapi"
	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	corev1applyconfigurations "k8s.io/client-go/applyconfigurations/core/v1"
	metav1applyconfigurations "k8s.io/client-go/applyconfigurations/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	certutil "k8s.io/client-go/util/cert"
	"k8s.io/klog/v2"
)

type CertificateRevocationController struct {
	kubeClient       kubernetes.Interface
	hypershiftClient hypershiftclient.Interface

	fieldManager string
	getCRR       func(namespace, name string) (*certificatesv1alpha1.CertificateRevocationRequest, error)
	getSecret    func(namespace, name string) (*corev1.Secret, error)
	listSecrets  func(namespace string) ([]*corev1.Secret, error)
	getConfigMap func(namespace, name string) (*corev1.ConfigMap, error)

	// for unit testing only
	skipKASConnections bool
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
		getCRR: func(namespace, name string) (*certificatesv1alpha1.CertificateRevocationRequest, error) {
			return hypershiftInformers.Certificates().V1alpha1().CertificateRevocationRequests().Lister().CertificateRevocationRequests(namespace).Get(name)
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

	crrInformer := hypershiftInformers.Certificates().V1alpha1().CertificateRevocationRequests().Informer()
	secretInformer := kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets().Informer()
	configMapInformer := kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().ConfigMaps().Informer()
	listCRRs := func(namespace string) ([]*certificatesv1alpha1.CertificateRevocationRequest, error) {
		return hypershiftInformers.Certificates().V1alpha1().CertificateRevocationRequests().Lister().CertificateRevocationRequests(hostedControlPlane.Namespace).List(labels.Everything())
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

func enqueueSecret(listCRRs func(namespace string) ([]*certificatesv1alpha1.CertificateRevocationRequest, error)) func(obj runtime.Object) []string {
	return func(obj runtime.Object) []string {
		secret, ok := obj.(*corev1.Secret)
		if !ok {
			klog.ErrorS(fmt.Errorf("unexpected object of type %T, wanted %T", obj, &corev1.Secret{}), "could not determine queue key")
			return nil
		}
		// if this is a copied signer, queue the CRR that copied it
		for _, owner := range secret.OwnerReferences {
			if owner.Kind == "CertificateRevocationRequest" {
				key, err := cache.MetaNamespaceKeyFunc(&metav1.ObjectMeta{
					Namespace: secret.Namespace,
					Name:      owner.Name,
				})
				if err != nil {
					klog.ErrorS(err, "could not determine queue key")
					return nil
				}
				return []string{key}
			}
		}
		// if this is a leaf certificate, requeue any CRRs revoking the issuer
		if signer, ok := signerClassForLeafCertificateSecret(secret); ok {
			return enqueueForSigner(secret.Namespace, signer, listCRRs)
		}

		// if this is a signer, requeue any CRRs revoking it
		if signer, ok := signerClassForSecret(secret); ok {
			return enqueueForSigner(secret.Namespace, signer, listCRRs)
		}
		return nil
	}
}

func enqueueForSigner(namespace string, signer certificates.SignerClass, listCRRs func(namespace string) ([]*certificatesv1alpha1.CertificateRevocationRequest, error)) []string {
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

func enqueueAll(namespace string, listCRRs func(namespace string) ([]*certificatesv1alpha1.CertificateRevocationRequest, error)) []string {
	crrs, err := listCRRs(namespace)
	if err != nil {
		klog.ErrorS(err, "could not determine queue key")
		return nil
	}
	var keys []string
	for _, crr := range crrs {
		key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(crr)
		if err != nil {
			klog.ErrorS(err, "could not determine queue key")
			return nil
		}
		keys = append(keys, key)
	}
	return keys
}

// TODO: we *could* store the signer -> signer class, trust bundle -> certificate, and leaf -> trunk associations
// on the objects and use lookups or indices for this.

// signerClassForSecret determines the signer classes that the secret contains data for.
// We could use this transformation to create an index, but we expect the scale of resource
// counts for this controller to be very small (maybe O(10)) and the rate of change to be
// low, so the extra memory cost in indices is not valuable.
func signerClassForSecret(secret *corev1.Secret) (certificates.SignerClass, bool) {
	return signerClassForSecretName(secret.Name)
}

func signerClassForSecretName(name string) (certificates.SignerClass, bool) {
	switch name {
	case manifests.CustomerSystemAdminSigner("").Name:
		return certificates.CustomerBreakGlassSigner, true
	case manifests.SRESystemAdminSigner("").Name:
		return certificates.SREBreakGlassSigner, true
	default:
		return "", false
	}
}

func secretForSignerClass(namespace string, signer certificates.SignerClass) (*corev1.Secret, bool) {
	switch signer {
	case certificates.CustomerBreakGlassSigner:
		return manifests.CustomerSystemAdminSigner(namespace), true
	case certificates.SREBreakGlassSigner:
		return manifests.SRESystemAdminSigner(namespace), true
	default:
		return nil, false
	}
}

func signerClassForLeafCertificateSecret(secret *corev1.Secret) (certificates.SignerClass, bool) {
	switch secret.Name {
	case manifests.CustomerSystemAdminClientCertSecret(secret.Namespace).Name:
		return certificates.CustomerBreakGlassSigner, true
	case manifests.SRESystemAdminClientCertSecret(secret.Namespace).Name:
		return certificates.SREBreakGlassSigner, true
	default:
		return "", false
	}
}

func enqueueConfigMap(listCRRs func(namespace string) ([]*certificatesv1alpha1.CertificateRevocationRequest, error)) func(obj runtime.Object) []string {
	return func(obj runtime.Object) []string {
		configMap, ok := obj.(*corev1.ConfigMap)
		if !ok {
			klog.ErrorS(fmt.Errorf("unexpected object of type %T, wanted %T", obj, &corev1.ConfigMap{}), "could not determine queue key")
			return nil
		}
		totalClientCACM := manifests.TotalKASClientCABundle(configMap.Namespace)
		if configMap.Name == totalClientCACM.Name {
			return enqueueAll(configMap.Namespace, listCRRs)
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
	case manifests.SRESystemAdminSignerCA(configMap.Namespace).Name:
		return certificates.SREBreakGlassSigner, true
	default:
		return "", false
	}
}

func configMapForSignerClass(namespace string, signer certificates.SignerClass) (*corev1.ConfigMap, bool) {
	switch signer {
	case certificates.CustomerBreakGlassSigner:
		return manifests.CustomerSystemAdminSignerCA(namespace), true
	case certificates.SREBreakGlassSigner:
		return manifests.SRESystemAdminSignerCA(namespace), true
	default:
		return nil, false
	}
}

func (c *CertificateRevocationController) syncCertificateRevocationRequest(ctx context.Context, syncContext factory.SyncContext) error {
	namespace, name, err := cache.SplitMetaNamespaceKey(syncContext.QueueKey())
	if err != nil {
		return err
	}

	action, requeue, err := c.processCertificateRevocationRequest(ctx, namespace, name, nil)
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

		// TODO: using force on secrets & CM since we're a different field manager - maybe collapse?
		switch {
		case action.crr != nil:
			_, err := c.hypershiftClient.CertificatesV1alpha1().CertificateRevocationRequests(*action.crr.Namespace).ApplyStatus(ctx, action.crr, metav1.ApplyOptions{FieldManager: c.fieldManager})
			return err
		case action.secret != nil:
			_, err := c.kubeClient.CoreV1().Secrets(*action.secret.Namespace).Apply(ctx, action.secret, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
			return err
		case action.cm != nil:
			_, err := c.kubeClient.CoreV1().ConfigMaps(*action.cm.Namespace).Apply(ctx, action.cm, metav1.ApplyOptions{FieldManager: c.fieldManager, Force: true})
			return err
		}
	}

	return nil
}

type actions struct {
	event  *eventInfo
	crr    *certificatesv1alpha1applyconfigurations.CertificateRevocationRequestApplyConfiguration
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
	if set > 1 {
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

func (c *CertificateRevocationController) processCertificateRevocationRequest(ctx context.Context, namespace, name string, now func() time.Time) (*actions, bool, error) {
	if now == nil {
		now = time.Now
	}

	crr, err := c.getCRR(namespace, name)
	if apierrors.IsNotFound(err) {
		return nil, false, nil // nothing to be done, CRR is gone
	}
	if err != nil {
		return nil, false, err
	}

	for _, step := range []revocationStep{
		// we haven't seen this CRR before, so choose a revocation timestamp
		c.commitRevocationTimestamp,
		// a revocation timestamp exists, so we need to ensure new certs are generated after that point
		c.generateNewSignerCertificate,
		// new certificates exist, we need to ensure they work against the API server
		c.ensureNewSignerCertificatePropagated,
		// new certificates exist and are accepted by the API server, we need to re-generate leaf certificates
		c.generateNewLeafCertificates,
		// new certificates propagated, time to remove all previous certificates from trust bundle
		c.prunePreviousSignerCertificates,
		// old certificates removed, time to ensure old certificate is rejected
		c.ensureOldSignerCertificateRevoked,
	} {
		// each step either handles the current step or hands off to the next one
		done, action, requeue, err := step(ctx, namespace, name, now, crr)
		if done {
			return action, requeue, err
		}
	}
	// nothing to do
	return nil, false, nil
}

type revocationStep func(ctx context.Context, namespace string, name string, now func() time.Time, crr *certificatesv1alpha1.CertificateRevocationRequest) (bool, *actions, bool, error)

func (c *CertificateRevocationController) commitRevocationTimestamp(ctx context.Context, namespace string, name string, now func() time.Time, crr *certificatesv1alpha1.CertificateRevocationRequest) (bool, *actions, bool, error) {
	if !certificates.ValidSignerClass(crr.Spec.SignerClass) {
		cfg := certificatesv1alpha1applyconfigurations.CertificateRevocationRequest(name, namespace)
		cfg.Status = certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatus().
			WithConditions(conditions(crr.Status.Conditions, metav1applyconfigurations.Condition().
				WithType(certificatesv1alpha1.SignerClassValidType).
				WithStatus(metav1.ConditionFalse).
				WithLastTransitionTime(metav1.NewTime(now())).
				WithReason(certificatesv1alpha1.SignerClassUnknownReason).
				WithMessage(fmt.Sprintf("Signer class %q unknown.", crr.Spec.SignerClass)),
			)...)
		e := event("CertificateRevocationInvalid", "Signer class %q unknown.", crr.Spec.SignerClass)
		return true, &actions{event: e, crr: cfg}, false, nil
	}

	if crr.Status.RevocationTimestamp == nil {
		revocationTimestamp := now()
		cfg := certificatesv1alpha1applyconfigurations.CertificateRevocationRequest(name, namespace)
		cfg.Status = certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatus().
			WithRevocationTimestamp(metav1.NewTime(revocationTimestamp)).
			WithConditions(conditions(crr.Status.Conditions, metav1applyconfigurations.Condition().
				WithType(certificatesv1alpha1.SignerClassValidType).
				WithStatus(metav1.ConditionTrue).
				WithLastTransitionTime(metav1.NewTime(now())).
				WithReason(hypershiftv1beta1.AsExpectedReason).
				WithMessage(fmt.Sprintf("Signer class %q known.", crr.Spec.SignerClass)),
			)...)
		e := event("CertificateRevocationStarted", "%q certificates valid before %s will be revoked.", crr.Spec.SignerClass, revocationTimestamp)
		return true, &actions{event: e, crr: cfg}, false, nil
	}

	return false, nil, false, nil
}

func (c *CertificateRevocationController) generateNewSignerCertificate(ctx context.Context, namespace string, name string, now func() time.Time, crr *certificatesv1alpha1.CertificateRevocationRequest) (bool, *actions, bool, error) {
	signer, ok := secretForSignerClass(namespace, certificates.SignerClass(crr.Spec.SignerClass))
	if !ok {
		// we should never reach this case as we validate the class before transitioning states, and it's immutable
		return true, nil, false, nil
	}

	current, certs, err := c.loadCertificateSecret(signer.Namespace, signer.Name)
	if err != nil {
		return true, nil, false, err
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

		// base36(sha224(value)) produces a useful, deterministic value that fits the requirements to be
		// a Kubernetes object name (honoring length requirement, is a valid DNS subdomain, etc)
		hash := sha256.Sum224([]byte(crr.Name))
		var i big.Int
		i.SetBytes(hash[:])
		previousSignerName := i.Text(36)
		_, err = c.getSecret(namespace, previousSignerName)
		if err != nil && !apierrors.IsNotFound(err) {
			return true, nil, false, err
		}
		if apierrors.IsNotFound(err) {
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
			e := event("CertificateRevocationProgressing", "Copying previous signer %s/%s to %s/%s.", signer.Namespace, signer.Name, namespace, previousSignerName)
			return true, &actions{event: e, secret: secretCfg}, false, nil
		}

		if crr.Status.PreviousSigner == nil {
			cfg := certificatesv1alpha1applyconfigurations.CertificateRevocationRequest(name, namespace)
			cfg.Status = certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatus().
				WithRevocationTimestamp(*crr.Status.RevocationTimestamp).
				WithPreviousSigner(corev1.LocalObjectReference{Name: previousSignerName}).
				WithConditions(conditions(crr.Status.Conditions, metav1applyconfigurations.Condition().
					WithType(certificatesv1alpha1.RootCertificatesRegeneratedType).
					WithStatus(metav1.ConditionFalse).
					WithLastTransitionTime(metav1.NewTime(now())).
					WithReason(certificatesv1alpha1.RootCertificatesStaleReason).
					WithMessage(fmt.Sprintf("Signer certificate %s/%s needs to be regenerated.", signer.Namespace, signer.Name)),
				)...)
			e := event("CertificateRevocationProgressing", "Recording reference to copied previous signer %s/%s.", namespace, previousSignerName)
			return true, &actions{event: e, crr: cfg}, false, nil
		}

		// SSA would allow us to simply send this indiscriminately, but regeneration takes time, and if we're
		// reconciling after we've sent this annotation once and send it again, all we do is kick another round
		// of regeneration, which is not helpful
		if val, ok := current.Annotations[certrotation.CertificateNotAfterAnnotation]; !ok || val != "force-regeneration" {
			secretCfg := corev1applyconfigurations.Secret(signer.Name, signer.Namespace).
				WithAnnotations(map[string]string{
					certrotation.CertificateNotAfterAnnotation: "force-regeneration",
				})
			e := event("CertificateRevocationProgressing", "Marking signer %s/%s for regeneration.", signer.Namespace, signer.Name)
			return true, &actions{event: e, secret: secretCfg}, false, nil
		}
		return true, nil, false, nil
	}

	var recorded bool
	for _, condition := range crr.Status.Conditions {
		if condition.Type == certificatesv1alpha1.RootCertificatesRegeneratedType && condition.Status == metav1.ConditionTrue {
			recorded = true
			break
		}
	}
	if !recorded {
		cfg := certificatesv1alpha1applyconfigurations.CertificateRevocationRequest(name, namespace)
		cfg.Status = certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatus().
			WithRevocationTimestamp(*crr.Status.RevocationTimestamp).
			WithPreviousSigner(*crr.Status.PreviousSigner).
			WithConditions(conditions(crr.Status.Conditions,
				metav1applyconfigurations.Condition().
					WithType(certificatesv1alpha1.RootCertificatesRegeneratedType).
					WithStatus(metav1.ConditionTrue).
					WithLastTransitionTime(metav1.NewTime(now())).
					WithReason(hypershiftv1beta1.AsExpectedReason).
					WithMessage(fmt.Sprintf("Signer certificate %s/%s regenerated.", signer.Namespace, signer.Name)),
				metav1applyconfigurations.Condition().
					WithType(certificatesv1alpha1.NewCertificatesTrustedType).
					WithStatus(metav1.ConditionFalse).
					WithLastTransitionTime(metav1.NewTime(now())).
					WithReason(hypershiftv1beta1.WaitingForAvailableReason).
					WithMessage(fmt.Sprintf("New signer certificate %s/%s not yet trusted.", signer.Namespace, signer.Name)),
			)...)
		e := event("CertificateRevocationProgressing", "New %q signer certificates generated.", crr.Spec.SignerClass)
		return true, &actions{event: e, crr: cfg}, false, nil
	}

	return false, nil, false, nil
}

func (c *CertificateRevocationController) ensureNewSignerCertificatePropagated(ctx context.Context, namespace string, name string, now func() time.Time, crr *certificatesv1alpha1.CertificateRevocationRequest) (bool, *actions, bool, error) {
	signer, ok := secretForSignerClass(namespace, certificates.SignerClass(crr.Spec.SignerClass))
	if !ok {
		// we should never reach this case as we validate the class before transitioning states, and it's immutable
		return true, nil, false, nil
	}

	signerSecert, signers, err := c.loadCertificateSecret(signer.Namespace, signer.Name)
	if err != nil {
		return true, nil, false, err
	}
	if signers == nil {
		return true, nil, false, nil
	}

	currentCertPEM, ok := signerSecert.Data[corev1.TLSCertKey]
	if !ok || len(currentCertPEM) == 0 {
		return true, nil, false, fmt.Errorf("signer certificate %s/%s had no data for %s", signerSecert.Namespace, signerSecert.Name, corev1.TLSCertKey)
	}

	currentKeyPEM, ok := signerSecert.Data[corev1.TLSPrivateKeyKey]
	if !ok || len(currentKeyPEM) == 0 {
		return true, nil, false, fmt.Errorf("signer certificate %s/%s had no data for %s", signerSecert.Namespace, signerSecert.Name, corev1.TLSPrivateKeyKey)
	}

	totalClientCA := manifests.TotalKASClientCABundle(namespace)
	totalClientTrustBundle, err := c.loadTrustBundleConfigMap(totalClientCA.Namespace, totalClientCA.Name)
	if err != nil {
		return true, nil, false, err
	}
	if totalClientTrustBundle == nil {
		return true, nil, false, nil
	}

	// the real gate for this phase is that KAS has loaded the updated trust bundle and now
	// authorizes clients using certificates signed by the new signer - it is difficult to unit-test
	// that, though, and it's always valid to first check that our certificates have propagated as far
	// as we can tell in the system before asking the KAS, since that's expensive
	if len(trustedCertificates(totalClientTrustBundle, []*certificateSecret{{cert: signers[0]}}, now)) == 0 {
		return true, nil, false, nil
	}

	// if the updated trust bundle has propagated as far as we can tell, let's go ahead and ask
	// KAS to detect when it trusts the new signer
	if !c.skipKASConnections {
		kubeconfig := hcpmanifests.KASServiceKubeconfigSecret(namespace)
		kubeconfigSecret, err := c.getSecret(kubeconfig.Namespace, kubeconfig.Name)
		if err != nil {
			return true, nil, false, fmt.Errorf("couldn't fetch guest cluster service network kubeconfig: %w", err)
		}
		adminClientCfg, err := clientcmd.NewClientConfigFromBytes(kubeconfigSecret.Data["kubeconfig"])
		if err != nil {
			return true, nil, false, fmt.Errorf("couldn't load guest cluster service network kubeconfig: %w", err)
		}
		adminCfg, err := adminClientCfg.ClientConfig()
		if err != nil {
			return true, nil, false, fmt.Errorf("couldn't load guest cluster service network kubeconfig: %w", err)
		}
		certCfg := rest.AnonymousClientConfig(adminCfg)
		certCfg.CertData = currentCertPEM
		certCfg.KeyData = currentKeyPEM

		testClient, err := kubernetes.NewForConfig(certCfg)
		if err != nil {
			return true, nil, false, fmt.Errorf("couldn't create guest cluster client using old certificate: %w", err)
		}

		_, err = testClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
		if apierrors.IsUnauthorized(err) {
			// this is OK, things are just propagating still
			return true, nil, true, nil // we need to synthetically re-queue since nothing about KAS loading will trigger us
		}
		if err != nil {
			return true, nil, false, fmt.Errorf("couldn't send SSR to guest cluster: %w", err)
		}
	}

	var recorded bool
	for _, condition := range crr.Status.Conditions {
		if condition.Type == certificatesv1alpha1.NewCertificatesTrustedType && condition.Status == metav1.ConditionTrue {
			recorded = true
			break
		}
	}
	if !recorded {
		cfg := certificatesv1alpha1applyconfigurations.CertificateRevocationRequest(name, namespace)
		cfg.Status = certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatus().
			WithRevocationTimestamp(*crr.Status.RevocationTimestamp).
			WithPreviousSigner(*crr.Status.PreviousSigner).
			WithConditions(conditions(crr.Status.Conditions, metav1applyconfigurations.Condition().
				WithType(certificatesv1alpha1.NewCertificatesTrustedType).
				WithStatus(metav1.ConditionTrue).
				WithLastTransitionTime(metav1.NewTime(now())).
				WithReason(hypershiftv1beta1.AsExpectedReason).
				WithMessage(fmt.Sprintf("New signer certificate %s/%s trusted.", signer.Namespace, signer.Name)),
			)...)
		e := event("CertificateRevocationProgressing", "New %q signer certificates valid.", crr.Spec.SignerClass)
		return true, &actions{event: e, crr: cfg}, false, nil
	}

	return false, nil, false, nil
}

func (c *CertificateRevocationController) generateNewLeafCertificates(ctx context.Context, namespace string, name string, now func() time.Time, crr *certificatesv1alpha1.CertificateRevocationRequest) (bool, *actions, bool, error) {
	signer, ok := secretForSignerClass(namespace, certificates.SignerClass(crr.Spec.SignerClass))
	if !ok {
		// we should never reach this case as we validate the class before transitioning states, and it's immutable
		return true, nil, false, nil
	}
	secrets, err := c.listSecrets(signer.Namespace)
	if err != nil {
		return true, nil, false, err
	}

	var currentIssuer string
	for _, secret := range secrets {
		if secret.Name == signer.Name {
			currentIssuer = secret.Annotations[certrotation.CertificateIssuer]
			break
		}
	}

	currentIssuerName, currentIssuerTimestamp, err := parseIssuer(currentIssuer)
	if err != nil {
		return true, nil, false, fmt.Errorf("signer %s/%s metadata.annotations[%s] malformed: %w", signer.Namespace, signer.Name, certrotation.CertificateIssuer, err)
	}

	for _, secret := range secrets {
		issuer, set := secret.Annotations[certrotation.CertificateIssuer]
		if !set {
			continue
		}
		issuerName, issuerTimestamp, err := parseIssuer(issuer)
		if err != nil {
			return true, nil, false, fmt.Errorf("certificate %s/%s metadata.annotations[%s] malformed: %w", secret.Namespace, secret.Name, certrotation.CertificateIssuer, err)
		}
		if issuerName == currentIssuerName && issuerTimestamp.Before(currentIssuerTimestamp) {
			secretCfg := corev1applyconfigurations.Secret(secret.Name, secret.Namespace).
				WithAnnotations(map[string]string{
					certrotation.CertificateNotAfterAnnotation: "force-regeneration",
				})
			e := event("CertificateRevocationProgressing", "Marking certificate %s/%s for regeneration.", secret.Namespace, secret.Name)
			return true, &actions{event: e, secret: secretCfg}, false, nil
		}
	}

	return false, nil, false, nil
}

func (c *CertificateRevocationController) prunePreviousSignerCertificates(ctx context.Context, namespace string, name string, now func() time.Time, crr *certificatesv1alpha1.CertificateRevocationRequest) (bool, *actions, bool, error) {
	trustBundleCA, ok := configMapForSignerClass(namespace, certificates.SignerClass(crr.Spec.SignerClass))
	if !ok {
		// we should never reach this case as we validate the class before transitioning states, and it's immutable
		return true, nil, false, nil
	}

	currentTrustBundle, err := c.loadTrustBundleConfigMap(trustBundleCA.Namespace, trustBundleCA.Name)
	if err != nil {
		return true, nil, false, err
	}
	if currentTrustBundle == nil {
		return true, nil, false, nil
	}

	onlyAfter, _ := partitionCertificatesByValidity(currentTrustBundle, crr.Status.RevocationTimestamp.Time)
	if len(onlyAfter) != len(currentTrustBundle) {
		// we need to prune the trust bundle, but first, we need to ensure that all leaf certificates
		// trusted by the current bundle continue to be trusted by the filtered bundle; all leaves must
		// have been regenerated for us to revoke the old certificates
		secrets, err := c.listSecrets(namespace)
		if err != nil {
			return true, nil, false, fmt.Errorf("failed to list secrets: %w", err)
		}

		var existingLeafCerts []*certificateSecret
		for _, secret := range secrets {
			if _, hasTLSCertKey := secret.Data[corev1.TLSCertKey]; !hasTLSCertKey {
				continue
			}

			certKeyInfo, err := certgraphanalysis.InspectSecret(secret)
			if err != nil {
				klog.Warningf("failed to load cert/key pair from secret %s/%s: %v", secret.Namespace, secret.Name, err)
				continue
			}
			if certKeyInfo == nil {
				continue
			}

			certs, err := certutil.ParseCertsPEM(secret.Data[corev1.TLSCertKey])
			if err != nil {
				return true, nil, false, fmt.Errorf("could not parse certificate in secret %s/%s: %w", secret.Namespace, secret.Name, err)
			}

			for _, cert := range certKeyInfo {
				if isLeafCertificate(cert) {
					existingLeafCerts = append(existingLeafCerts, &certificateSecret{
						namespace: secret.Namespace,
						name:      secret.Name,
						cert:      certs[0],
					})
				}
			}
		}

		currentlyTrustedLeaves := trustedCertificates(currentTrustBundle, existingLeafCerts, now)
		futureTrustedLeaves := trustedCertificates(onlyAfter, existingLeafCerts, now)

		if diff := certificateSecretNames(currentlyTrustedLeaves).Difference(certificateSecretNames(futureTrustedLeaves)); diff.Len() != 0 {
			list := diff.UnsortedList()
			sort.Strings(list)
			cfg := certificatesv1alpha1applyconfigurations.CertificateRevocationRequest(name, namespace)
			cfg.Status = certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatus().
				WithRevocationTimestamp(*crr.Status.RevocationTimestamp).
				WithPreviousSigner(*crr.Status.PreviousSigner).
				WithConditions(
					conditions(crr.Status.Conditions, metav1applyconfigurations.Condition().
						WithType(certificatesv1alpha1.LeafCertificatesRegeneratedType).
						WithStatus(metav1.ConditionFalse).
						WithLastTransitionTime(metav1.NewTime(now())).
						WithReason(certificatesv1alpha1.LeafCertificatesStaleReason).
						WithMessage(fmt.Sprintf("Revocation would lose trust for leaf certificates: %v.", strings.Join(list, ", "))),
					)...,
				)
			e := event("CertificateRevocationProgressing", "Waiting for leaf certificates %v to regenerate.", strings.Join(list, ", "))
			return true, &actions{event: e, crr: cfg}, false, nil
		}

		newBundlePEM, err := certutil.EncodeCertificates(onlyAfter...)
		if err != nil {
			return true, nil, false, fmt.Errorf("failed to encode new cert bundle for configmap %s/%s: %w", trustBundleCA.Name, trustBundleCA.Namespace, err)
		}

		caCfg := corev1applyconfigurations.ConfigMap(trustBundleCA.Name, trustBundleCA.Namespace)
		caCfg.WithData(map[string]string{
			"ca-bundle.crt": string(newBundlePEM),
		})
		e := event("CertificateRevocationProgressing", "Pruning previous %q signer certificates from CA bundle.", crr.Spec.SignerClass)
		return true, &actions{event: e, cm: caCfg}, false, nil
	}

	var recorded bool
	for _, condition := range crr.Status.Conditions {
		if condition.Type == certificatesv1alpha1.LeafCertificatesRegeneratedType && condition.Status == metav1.ConditionTrue {
			recorded = true
			break
		}
	}
	if !recorded {
		// we're already pruned, we can continue
		cfg := certificatesv1alpha1applyconfigurations.CertificateRevocationRequest(name, namespace)
		cfg.Status = certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatus().
			WithRevocationTimestamp(*crr.Status.RevocationTimestamp).
			WithPreviousSigner(*crr.Status.PreviousSigner).
			WithConditions(
				conditions(crr.Status.Conditions,
					metav1applyconfigurations.Condition().
						WithType(certificatesv1alpha1.LeafCertificatesRegeneratedType).
						WithStatus(metav1.ConditionTrue).
						WithLastTransitionTime(metav1.NewTime(now())).
						WithReason(hypershiftv1beta1.AsExpectedReason).
						WithMessage("All leaf certificates are re-generated."),
					metav1applyconfigurations.Condition().
						WithType(certificatesv1alpha1.PreviousCertificatesRevokedType).
						WithStatus(metav1.ConditionFalse).
						WithLastTransitionTime(metav1.NewTime(now())).
						WithReason(hypershiftv1beta1.WaitingForAvailableReason).
						WithMessage("Previous signer certificate not yet revoked."),
				)...,
			)
		e := event("CertificateRevocationProgressing", "Previous %q signer certificates pruned.", crr.Spec.SignerClass)
		return true, &actions{event: e, crr: cfg}, false, nil
	}

	return false, nil, false, nil
}

func (c *CertificateRevocationController) ensureOldSignerCertificateRevoked(ctx context.Context, namespace string, name string, now func() time.Time, crr *certificatesv1alpha1.CertificateRevocationRequest) (bool, *actions, bool, error) {
	oldCertSecret, err := c.getSecret(namespace, crr.Status.PreviousSigner.Name)
	if err != nil {
		return true, nil, false, err
	}

	oldCertPEM, ok := oldCertSecret.Data[corev1.TLSCertKey]
	if !ok || len(oldCertPEM) == 0 {
		return true, nil, false, fmt.Errorf("signer certificate %s/%s had no data for %s", oldCertSecret.Namespace, oldCertSecret.Name, corev1.TLSCertKey)
	}

	oldCerts, err := certutil.ParseCertsPEM(oldCertPEM)
	if err != nil {
		return true, nil, false, err
	}

	oldKeyPEM, ok := oldCertSecret.Data[corev1.TLSPrivateKeyKey]
	if !ok || len(oldKeyPEM) == 0 {
		return true, nil, false, fmt.Errorf("signer certificate %s/%s had no data for %s", oldCertSecret.Namespace, oldCertSecret.Name, corev1.TLSPrivateKeyKey)
	}

	totalClientCA := manifests.TotalKASClientCABundle(namespace)
	totalClientTrustBundle, err := c.loadTrustBundleConfigMap(totalClientCA.Namespace, totalClientCA.Name)
	if err != nil {
		return true, nil, false, err
	}
	if totalClientTrustBundle == nil {
		return true, nil, false, nil
	}
	// the real gate for this phase is that KAS has loaded the updated trust bundle and no longer
	// authorizes clients using certificates signed by the revoked signer - it is difficult to unit-test
	// that, though, and it's always valid to first check that our certificates have propagated as far
	// as we can tell in the system before asking the KAS, since that's expensive
	if len(trustedCertificates(totalClientTrustBundle, []*certificateSecret{{cert: oldCerts[0]}}, now)) != 0 {
		return true, nil, false, nil
	}

	// if the updated trust bundle has propagated as far as we can tell, let's go ahead and ask
	// KAS to ensure it no longer trusts the old signer
	if !c.skipKASConnections {
		kubeconfig := hcpmanifests.KASServiceKubeconfigSecret(namespace)
		kubeconfigSecret, err := c.getSecret(kubeconfig.Namespace, kubeconfig.Name)
		if err != nil {
			return true, nil, false, fmt.Errorf("couldn't fetch guest cluster service network kubeconfig: %w", err)
		}
		adminClientCfg, err := clientcmd.NewClientConfigFromBytes(kubeconfigSecret.Data["kubeconfig"])
		if err != nil {
			return true, nil, false, fmt.Errorf("couldn't load guest cluster service network kubeconfig: %w", err)
		}
		adminCfg, err := adminClientCfg.ClientConfig()
		if err != nil {
			return true, nil, false, fmt.Errorf("couldn't load guest cluster service network kubeconfig: %w", err)
		}
		certCfg := rest.AnonymousClientConfig(adminCfg)
		certCfg.CertData = oldCertPEM
		certCfg.KeyData = oldKeyPEM

		testClient, err := kubernetes.NewForConfig(certCfg)
		if err != nil {
			return true, nil, false, fmt.Errorf("couldn't create guest cluster client using old certificate: %w", err)
		}

		_, err = testClient.AuthenticationV1().SelfSubjectReviews().Create(ctx, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
		if err == nil {
			// this is OK, things are just propagating still
			return true, nil, true, nil // we need to synthetically re-queue since nothing about KAS loading will trigger us
		}
		if !apierrors.IsUnauthorized(err) {
			return true, nil, false, fmt.Errorf("couldn't send SSR to guest cluster: %w", err)
		}
	}

	var recorded bool
	for _, condition := range crr.Status.Conditions {
		if condition.Type == certificatesv1alpha1.PreviousCertificatesRevokedType && condition.Status == metav1.ConditionTrue {
			recorded = true
			break
		}
	}
	if !recorded {
		cfg := certificatesv1alpha1applyconfigurations.CertificateRevocationRequest(name, namespace)
		cfg.Status = certificatesv1alpha1applyconfigurations.CertificateRevocationRequestStatus().
			WithRevocationTimestamp(*crr.Status.RevocationTimestamp).
			WithPreviousSigner(*crr.Status.PreviousSigner).
			WithConditions(conditions(crr.Status.Conditions,
				metav1applyconfigurations.Condition().
					WithType(certificatesv1alpha1.PreviousCertificatesRevokedType).
					WithStatus(metav1.ConditionTrue).
					WithLastTransitionTime(metav1.NewTime(now())).
					WithReason(hypershiftv1beta1.AsExpectedReason).
					WithMessage("Previous signer certificate revoked."),
			)...)
		e := event("CertificateRevocationComplete", "%q signer certificates revoked.", crr.Spec.SignerClass)
		return true, &actions{event: e, crr: cfg}, false, nil
	}
	return false, nil, false, nil
}

func (c *CertificateRevocationController) loadCertificateSecret(namespace, name string) (*corev1.Secret, []*x509.Certificate, error) {
	secret, err := c.getSecret(namespace, name)
	if apierrors.IsNotFound(err) {
		return nil, nil, nil // try again later
	}
	if err != nil {
		return nil, nil, fmt.Errorf("could not fetch client cert secret %s/%s: %w", namespace, name, err)
	}

	clientCertPEM, ok := secret.Data[corev1.TLSCertKey]
	if !ok || len(clientCertPEM) == 0 {
		return nil, nil, fmt.Errorf("found no certificate in secret %s/%s: %w", namespace, name, err)
	}

	clientCertificates, err := certutil.ParseCertsPEM(clientCertPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("could not parse certificate in secret %s/%s: %w", namespace, name, err)
	}

	return secret, clientCertificates, nil
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

// conditions provides the full list of conditions that we need to send with each SSA call -
// if one field manager sets some conditions in one call, and another set in a second, any conditions
// provided in the first but not the second will be removed. Therefore, we need to provide the whole
// list of conditions on each call. Since we are the only actor to add conditions to this resource,
// we can accumulate all current conditions and simply append the new one, or overwrite a current
// condition if we're updating the content for that type.
func conditions(existing []metav1.Condition, updated ...*metav1applyconfigurations.ConditionApplyConfiguration) []*metav1applyconfigurations.ConditionApplyConfiguration {
	updatedTypes := sets.New[string]()
	for _, condition := range updated {
		if condition.Type == nil {
			panic(fmt.Errorf("programmer error: must set a type for condition: %#v", condition))
		}
		updatedTypes.Insert(*condition.Type)
	}
	conditions := updated
	for _, condition := range existing {
		if !updatedTypes.Has(condition.Type) {
			conditions = append(conditions, metav1applyconfigurations.Condition().
				WithType(condition.Type).
				WithStatus(condition.Status).
				WithObservedGeneration(condition.ObservedGeneration).
				WithLastTransitionTime(condition.LastTransitionTime).
				WithReason(condition.Reason).
				WithMessage(condition.Message),
			)
		}
	}
	return conditions
}

// parseIssuer parses an issuer identifier like "namespace_name-signer@1705510729"
// into the issuer name (namespace_name-issuer) and the timestamp (as unix seconds).
// These are created in library-go with:
// signerName := fmt.Sprintf("%s-signer@%d", c.componentName, time.Now().Unix())
func parseIssuer(issuer string) (string, time.Time, error) {
	issuerParts := strings.Split(issuer, "@")
	if len(issuerParts) != 2 {
		return "", time.Time{}, fmt.Errorf("issuer %q malformed: splitting by '@' resulted in %d parts, not 2", issuer, len(issuerParts))
	}
	issuerName, issuerTimestamp := issuerParts[0], issuerParts[1]
	issuerTimestampSeconds, err := strconv.ParseInt(issuerTimestamp, 10, 64)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("issuer timestamp %q malformed: %w", issuerTimestamp, err)
	}
	return issuerName, time.Unix(issuerTimestampSeconds, 0), nil
}
