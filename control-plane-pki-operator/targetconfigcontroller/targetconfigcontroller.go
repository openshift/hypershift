package targetconfigcontroller

import (
	"context"
	"fmt"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftv1beta1client "github.com/openshift/hypershift/client/clientset/clientset/typed/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-pki-operator/clienthelpers"
	pkimanifests "github.com/openshift/hypershift/control-plane-pki-operator/manifests"

	"github.com/openshift/library-go/pkg/controller/factory"
	"github.com/openshift/library-go/pkg/operator/certrotation"
	"github.com/openshift/library-go/pkg/operator/events"
	"github.com/openshift/library-go/pkg/operator/resource/resourceapply"
	"github.com/openshift/library-go/pkg/operator/resourcesynccontroller"
	"github.com/openshift/library-go/pkg/operator/v1helpers"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	coreclientv1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corev1listers "k8s.io/client-go/listers/core/v1"
)

type TargetConfigController struct {
	hostedControlPlane *hypershiftv1beta1.HostedControlPlane

	hypershiftClient hypershiftv1beta1client.HostedControlPlanesGetter

	kubeClient      kubernetes.Interface
	configMapLister corev1listers.ConfigMapLister
}

func NewTargetConfigController(
	hostedControlPlane *hypershiftv1beta1.HostedControlPlane,
	hypershiftClient hypershiftv1beta1client.HostedControlPlanesGetter,
	kubeInformersForNamespaces v1helpers.KubeInformersForNamespaces,
	kubeClient kubernetes.Interface,
	eventRecorder events.Recorder,
) factory.Controller {
	c := &TargetConfigController{
		hostedControlPlane: hostedControlPlane,
		hypershiftClient:   hypershiftClient,
		kubeClient:         kubeClient,
		configMapLister:    kubeInformersForNamespaces.ConfigMapLister(),
	}

	return factory.New().WithInformers(
		kubeInformersForNamespaces.InformersFor(hostedControlPlane.Namespace).Core().V1().ConfigMaps().Informer(),
	).WithSync(c.sync).ResyncEvery(time.Minute).ToController("TargetConfigController", eventRecorder.WithComponentSuffix("target-config-controller"))
}

func (c TargetConfigController) sync(ctx context.Context, syncContext factory.SyncContext) error {
	requeue, err := createTargetConfig(ctx, c, syncContext.Recorder())
	if err != nil {
		return err
	}
	if requeue {
		return factory.SyntheticRequeueError
	}

	return nil
}

// createTargetConfig takes care of creation of valid resources in a fixed name.  These are inputs to other control loops.
// returns whether or not requeue and if an error happened when updating status.  Normally it updates status itself.
func createTargetConfig(ctx context.Context, c TargetConfigController, recorder events.Recorder) (bool, error) {
	var errors []error

	_, _, err := ManageClientCABundle(ctx, c.configMapLister, c.kubeClient.CoreV1(), recorder, c.hostedControlPlane)
	if err != nil {
		errors = append(errors, fmt.Errorf("%q: %v", "configmap/"+pkimanifests.TotalKASClientCABundle("placeholder").Name, err))
	}

	if len(errors) > 0 {
		condition := metav1.Condition{
			Type:    "TargetConfigControllerDegraded",
			Status:  metav1.ConditionTrue,
			Reason:  "SynchronizationError",
			Message: v1helpers.NewMultiLineAggregate(errors).Error(),
		}

		if _, err := clienthelpers.UpdateHostedControlPlaneStatusCondition(ctx, condition, c.hostedControlPlane.Namespace, c.hostedControlPlane.Name, "target-config-controller", c.hypershiftClient); err != nil {
			return true, err
		}
		return true, nil
	}

	condition := metav1.Condition{
		Type:   "TargetConfigControllerDegraded",
		Status: metav1.ConditionFalse,
		Reason: hypershiftv1beta1.AsExpectedReason,
	}
	if _, err := clienthelpers.UpdateHostedControlPlaneStatusCondition(ctx, condition, c.hostedControlPlane.Namespace, c.hostedControlPlane.Name, "target-config-controller", c.hypershiftClient); err != nil {
		return true, err
	}

	return false, nil
}

func ManageClientCABundle(ctx context.Context, lister corev1listers.ConfigMapLister, client coreclientv1.ConfigMapsGetter, recorder events.Recorder, owner *hypershiftv1beta1.HostedControlPlane) (*corev1.ConfigMap, bool, error) {
	requiredConfigMap, err := resourcesynccontroller.CombineCABundleConfigMaps(
		resourcesynccontroller.ResourceLocation{Namespace: owner.Namespace, Name: pkimanifests.TotalKASClientCABundle(owner.Namespace).Name},
		lister,
		certrotation.AdditionalAnnotations{
			JiraComponent: "HOSTEDCP",
			Description:   "Kubernetes total client CA bundle.",
		},
		// this bundle is what this operator uses to mint new customer client certs it directly manages
		resourcesynccontroller.ResourceLocation{Namespace: owner.Namespace, Name: pkimanifests.CustomerSystemAdminSignerCA(owner.Namespace).Name},
		// this bundle is what this operator uses to mint new SRE client certs it directly manages
		resourcesynccontroller.ResourceLocation{Namespace: owner.Namespace, Name: pkimanifests.SRESystemAdminSignerCA(owner.Namespace).Name},
	)
	if err != nil {
		return nil, false, err
	}

	if requiredConfigMap.OwnerReferences == nil {
		requiredConfigMap.OwnerReferences = []metav1.OwnerReference{}
	}
	requiredConfigMap.OwnerReferences = append(requiredConfigMap.OwnerReferences, metav1.OwnerReference{
		APIVersion: hypershiftv1beta1.GroupVersion.String(),
		Kind:       "HostedControlPlane",
		Name:       owner.Name,
		UID:        owner.UID,
	})

	return resourceapply.ApplyConfigMap(ctx, client, recorder, requiredConfigMap)
}
