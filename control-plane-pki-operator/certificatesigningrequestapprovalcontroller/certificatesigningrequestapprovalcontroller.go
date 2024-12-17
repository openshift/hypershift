package certificatesigningrequestapprovalcontroller

import (
	"context"
	"time"

	certificatesv1alpha1 "github.com/openshift/hypershift/api/certificates/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftinformers "github.com/openshift/hypershift/client/informers/externalversions"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

type CertificateSigningRequestApprovalController struct {
	kubeClient kubernetes.Interface

	namespace, signerName string
	getCSR                func(name string) (*certificatesv1.CertificateSigningRequest, error)
	getCSRA               func(namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error)
}

func NewCertificateSigningRequestApprovalController(
	hostedControlPlane *hypershiftv1beta1.HostedControlPlane,
	signer certificates.SignerClass,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	hypershiftInformers hypershiftinformers.SharedInformerFactory,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &CertificateSigningRequestApprovalController{
		kubeClient: kubeClient,
		namespace:  hostedControlPlane.Namespace,
		signerName: certificates.SignerNameForHCP(hostedControlPlane, signer),
		getCSR: func(name string) (*certificatesv1.CertificateSigningRequest, error) {
			return kubeInformersForNamespaces.InformersFor(corev1.NamespaceAll).Certificates().V1().CertificateSigningRequests().Lister().Get(name)
		},
		getCSRA: func(namespace, name string) (*certificatesv1alpha1.CertificateSigningRequestApproval, error) {
			return hypershiftInformers.Certificates().V1alpha1().CertificateSigningRequestApprovals().Lister().CertificateSigningRequestApprovals(namespace).Get(name)
		},
	}
	csrInformer := kubeInformersForNamespaces.InformersFor(corev1.NamespaceAll).Certificates().V1().CertificateSigningRequests().Informer()
	csraInformer := hypershiftInformers.Certificates().V1alpha1().CertificateSigningRequestApprovals().Informer()

	return factory.New().
		WithInformersQueueKeysFunc(enqueueCertificateSigningRequest, csrInformer).
		WithInformersQueueKeysFunc(enqueueCertificateSigningRequestApproval, csraInformer).
		WithSync(c.syncCertificateSigningRequest).
		ResyncEvery(time.Minute).
		ToController(string(signer)+"-CertificateSigningRequestApprovalController", eventRecorder.WithComponentSuffix(string(signer)+"-certificate-signing-request-approval-controller"))
}

func enqueueCertificateSigningRequest(obj runtime.Object) []string {
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.ErrorS(err, "could not determine queue key")
		return nil
	}
	return []string{key}
}

func enqueueCertificateSigningRequestApproval(obj runtime.Object) []string {
	// by convention, an approval is tied to a CertificateSingingRequest by name only
	// we're OK to just use the full queue key since the sync will throw away the namespace
	key, err := cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		klog.ErrorS(err, "could not determine queue key")
		return nil
	}
	return []string{key}
}

func (c *CertificateSigningRequestApprovalController) syncCertificateSigningRequest(ctx context.Context, syncContext factory.SyncContext) error {
	_, name, err := cache.SplitMetaNamespaceKey(syncContext.QueueKey())
	if err != nil {
		return err
	}

	csr, requeue, err := c.processCertificateSigningRequest(name)
	if err != nil {
		return err
	}
	if requeue {
		return factory.SyntheticRequeueError
	}
	if csr != nil {
		syncContext.Recorder().Eventf("CertificateSigningRequestApproved", "%q in is approved", csr.Name)
		_, err = c.kubeClient.CertificatesV1().CertificateSigningRequests().UpdateApproval(ctx, name, csr, metav1.UpdateOptions{})
		return err
	}

	return nil
}

func (c *CertificateSigningRequestApprovalController) processCertificateSigningRequest(name string) (*certificatesv1.CertificateSigningRequest, bool, error) {
	csr, err := c.getCSR(name)
	if apierrors.IsNotFound(err) {
		return nil, false, nil // nothing to do
	}
	if err != nil {
		return nil, false, err
	}

	if csr.Spec.SignerName != c.signerName {
		return nil, false, nil
	}

	if approved, denied := certificates.GetCertApprovalCondition(&csr.Status); approved || denied {
		return nil, false, nil
	}

	_, approvalGetErr := c.getCSRA(c.namespace, name)
	if approvalGetErr != nil && !apierrors.IsNotFound(approvalGetErr) {
		return nil, false, approvalGetErr
	}
	if apierrors.IsNotFound(approvalGetErr) {
		return nil, false, nil
	}

	// a CertificateSigningRequestApproval resource exists and matches the CertificateSigningRequest, so we can approve it
	csr = csr.DeepCopy()
	csr.Status.Conditions = append(csr.Status.Conditions, certificatesv1.CertificateSigningRequestCondition{
		Type:           certificatesv1.CertificateApproved,
		Status:         corev1.ConditionTrue,
		Reason:         "ApprovalPresent",
		Message:        "The requisite approval resource exists.",
		LastUpdateTime: metav1.Now(),
	})
	return csr, false, nil
}
