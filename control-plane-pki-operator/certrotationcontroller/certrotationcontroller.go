package certrotationcontroller

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1client "github.com/openshift/hypershift/client/clientset/clientset/typed/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-pki-operator/certificates"
	"github.com/openshift/hypershift/control-plane-pki-operator/clienthelpers"
	pkimanifests "github.com/openshift/hypershift/control-plane-pki-operator/manifests"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

type CertRotationController struct {
	certRotators []factory.Controller

	recorder events.Recorder

	cachesToSync []cache.InformerSynced
}

func NewCertRotationController(
	hostedControlPlane *hypershiftv1beta1.HostedControlPlane,
	kubeClient kubernetes.Interface,
	hypershiftClient hypershiftv1beta1client.HostedControlPlanesGetter,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	eventRecorder events.Recorder,
	rotationDay time.Duration,
) (*CertRotationController, error) {
	ret := &CertRotationController{
		recorder: eventRecorder,
		cachesToSync: []cache.InformerSynced{
			kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets().Informer().HasSynced,
			kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().ConfigMaps().Informer().HasSynced,
		},
	}

	ownerRef := &metav1.OwnerReference{
		APIVersion: hypershiftv1beta1.GroupVersion.String(),
		Kind:       "HostedControlPlane",
		Name:       hostedControlPlane.Name,
		UID:        hostedControlPlane.UID,
	}

	// we need the user info we're creating certificates for to be discernible as coming from us,
	// but not something that can be predicted by anyone - so, use a human-readable prefix and
	// crypto/rand for the rest
	randomString := func(bytes int) (string, error) {
		var data = make([]byte, bytes)
		if _, err := rand.Read(data); err != nil {
			return "", err
		}
		var i big.Int
		i.SetBytes(data)
		return i.Text(62), nil
	}
	userNameSuffix, err := randomString(32)
	if err != nil {
		return nil, err
	}
	uid, err := randomString(128)
	if err != nil {
		return nil, err
	}

	rotatorName := "CustomerAdminKubeconfigSigner"
	certRotator := certrotation.NewCertRotationController(
		rotatorName,
		certrotation.RotatedSigningCASecret{
			Namespace:     hostedControlPlane.Namespace,
			Name:          pkimanifests.CustomerSystemAdminSigner(hostedControlPlane.Namespace).Name,
			Validity:      7 * rotationDay,
			Refresh:       2 * rotationDay,
			Informer:      kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
			Owner:         ownerRef,
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "HOSTEDCP",
				Description:   "Root signer for customer break-glass credentials.",
			},
		},
		certrotation.CABundleConfigMap{
			Namespace:     hostedControlPlane.Namespace,
			Name:          pkimanifests.CustomerSystemAdminSignerCA(hostedControlPlane.Namespace).Name,
			Informer:      kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
			Owner:         ownerRef,
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "HOSTEDCP",
				Description:   "Trust bundle for customer break-glass credentials.",
			},
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: hostedControlPlane.Namespace,
			Name:      pkimanifests.CustomerSystemAdminClientCertSecret(hostedControlPlane.Namespace).Name,
			Validity:  36 * rotationDay / 24,
			Refresh:   6 * rotationDay / 24,
			CertCreator: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{
					Name:   certificates.CommonNamePrefix(certificates.CustomerBreakGlassSigner) + userNameSuffix,
					UID:    uid,
					Groups: []string{"system:masters"},
				},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
			Owner:         ownerRef,
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "HOSTEDCP",
				Description:   "Client certificate for customer break-glass credentials.",
			},
		},
		eventRecorder,
		clienthelpers.NewHostedControlPlaneStatusReporter(hostedControlPlane.Name, hostedControlPlane.Namespace, hypershiftClient),
	)
	ret.certRotators = append(ret.certRotators, certRotator)

	sreRotatorName := "SREAdminKubeconfigSigner"
	sreCertRotator := certrotation.NewCertRotationController(
		sreRotatorName,
		certrotation.RotatedSigningCASecret{
			Namespace:     hostedControlPlane.Namespace,
			Name:          pkimanifests.SRESystemAdminSigner(hostedControlPlane.Namespace).Name,
			Validity:      7 * rotationDay,
			Refresh:       2 * rotationDay,
			Informer:      kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
			Owner:         ownerRef,
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "HOSTEDCP",
				Description:   "Root signer for SRE break-glass credentials.",
			},
		},
		certrotation.CABundleConfigMap{
			Namespace:     hostedControlPlane.Namespace,
			Name:          pkimanifests.SRESystemAdminSignerCA(hostedControlPlane.Namespace).Name,
			Informer:      kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
			Owner:         ownerRef,
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "HOSTEDCP",
				Description:   "Trust bundle for SRE break-glass credentials.",
			},
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: hostedControlPlane.Namespace,
			Name:      pkimanifests.SRESystemAdminClientCertSecret(hostedControlPlane.Namespace).Name,
			Validity:  36 * rotationDay / 24,
			Refresh:   6 * rotationDay / 24,
			CertCreator: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{
					Name:   certificates.CommonNamePrefix(certificates.SREBreakGlassSigner) + userNameSuffix,
					UID:    uid,
					Groups: []string{"system:masters"},
				},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
			Owner:         ownerRef,
			AdditionalAnnotations: certrotation.AdditionalAnnotations{
				JiraComponent: "HOSTEDCP",
				Description:   "Client certificate for SRE break-glass credentials.",
			},
		},
		eventRecorder,
		clienthelpers.NewHostedControlPlaneStatusReporter(hostedControlPlane.Name, hostedControlPlane.Namespace, hypershiftClient),
	)
	ret.certRotators = append(ret.certRotators, sreCertRotator)

	return ret, nil
}

func (c *CertRotationController) WaitForReady(stopCh <-chan struct{}) {
	klog.Infof("Waiting for CertRotation")
	defer klog.Infof("Finished waiting for CertRotation")

	if !cache.WaitForCacheSync(stopCh, c.cachesToSync...) {
		utilruntime.HandleError(fmt.Errorf("caches did not sync"))
		return
	}
}

func (c *CertRotationController) Run(ctx context.Context, workers int) {
	klog.Infof("Starting CertRotation")
	defer klog.Infof("Shutting down CertRotation")
	c.WaitForReady(ctx.Done())

	for _, certRotator := range c.certRotators {
		go certRotator.Run(ctx, workers)
	}

	<-ctx.Done()
}
