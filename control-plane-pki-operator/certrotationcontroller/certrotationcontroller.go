package certrotationcontroller

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1client "github.com/openshift/hypershift/client/clientset/clientset/typed/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-pki-operator/clienthelpers"
	pkimanifests "github.com/openshift/hypershift/control-plane-pki-operator/manifests"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apiserver/pkg/authentication/user"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	"github.com/openshift/library-go/pkg/controller/factory"

	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/v1helpers"
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

	// we need the user info we're creating certificates for to be discernable as coming from us,
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
	userName := "customer-break-glass-" + userNameSuffix
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
			Validity:      36 * rotationDay / 24,
			Refresh:       7 * rotationDay / 24,
			Informer:      kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
			Owner:         ownerRef,
		},
		certrotation.CABundleConfigMap{
			Namespace:     hostedControlPlane.Namespace,
			Name:          pkimanifests.CustomerSystemAdminSignerCA(hostedControlPlane.Namespace).Name,
			Informer:      kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().ConfigMaps(),
			Lister:        kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().ConfigMaps().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
			Owner:         ownerRef,
		},
		certrotation.RotatedSelfSignedCertKeySecret{
			Namespace: hostedControlPlane.Namespace,
			Name:      pkimanifests.CustomerSystemAdminClientCertSecret(hostedControlPlane.Namespace).Name,
			Validity:  29 * rotationDay / 24,
			Refresh:   5 * rotationDay / 24,
			CertCreator: &certrotation.ClientRotation{
				UserInfo: &user.DefaultInfo{Name: userName, UID: uid, Groups: []string{"system:masters"}},
			},
			Informer:      kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets(),
			Lister:        kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().Secrets().Lister(),
			Client:        kubeClient.CoreV1(),
			EventRecorder: eventRecorder,
			Owner:         ownerRef,
		},
		eventRecorder,
		clienthelpers.NewHostedControlPlaneStatusReporter(hostedControlPlane.Name, hostedControlPlane.Namespace, hypershiftClient),
	)
	ret.certRotators = append(ret.certRotators, certRotator)

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

// RunOnce will run the cert rotation logic, but will not try to update the static pod status.
// This eliminates the need to pass an OperatorClient and avoids dubious writes and status.
func (c *CertRotationController) RunOnce() error {
	errlist := []error{}
	runOnceCtx := context.WithValue(context.Background(), certrotation.RunOnceContextKey, true)
	for _, certRotator := range c.certRotators {
		if err := certRotator.Sync(runOnceCtx, factory.NewSyncContext("CertRotationController", c.recorder)); err != nil {
			errlist = append(errlist, err)
		}
	}

	return utilerrors.NewAggregate(errlist)
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
