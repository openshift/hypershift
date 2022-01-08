package nodepool

import (
	"context"
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capikubevirt "sigs.k8s.io/cluster-api-provider-kubevirt/api/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func (r NodePoolReconciler) reconcileKubevirtMachineTemplate(ctx context.Context,
	nodePool *hyperv1.NodePool,
	controlPlaneNamespace string,
) (*capikubevirt.KubevirtMachineTemplate, error) {

	log := ctrl.LoggerFrom(ctx)
	// Get target template and hash.
	targetKubevirtMachineTemplate, targetTemplateHash := kubevirtMachineTemplate(nodePool, controlPlaneNamespace)

	// Get current template and hash.
	currentTemplateHash := nodePool.GetAnnotations()[nodePoolAnnotationCurrentProviderConfig]
	currentKubevirtMachineTemplate := &capikubevirt.KubevirtMachineTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s", nodePool.GetName(), currentTemplateHash),
			Namespace: controlPlaneNamespace,
		},
	}
	if err := r.Get(ctx, ctrlclient.ObjectKeyFromObject(currentKubevirtMachineTemplate), currentKubevirtMachineTemplate); err != nil && !apierrors.IsNotFound(err) {
		return nil, fmt.Errorf("error getting existing KubevirtMachineTemplate: %w", err)
	}

	if equality.Semantic.DeepEqual(currentKubevirtMachineTemplate.Spec.Template.Spec, targetKubevirtMachineTemplate.Spec.Template.Spec) {
		return currentKubevirtMachineTemplate, nil
	}

	// Otherwise create new template.
	log.Info("The KubevirtMachineTemplate referenced by this NodePool has changed. Creating a new one")
	if err := r.Create(ctx, targetKubevirtMachineTemplate); err != nil {
		return nil, fmt.Errorf("error creating new KubevirtMachineTemplate: %w", err)
	}

	// Store new template hash.
	if nodePool.Annotations == nil {
		nodePool.Annotations = make(map[string]string)
	}
	nodePool.Annotations[nodePoolAnnotationCurrentProviderConfig] = targetTemplateHash

	return targetKubevirtMachineTemplate, nil
}

func kubevirtMachineTemplate(nodePool *hyperv1.NodePool, controlPlaneNamespace string) (*capikubevirt.KubevirtMachineTemplate, string) {
	kubevirtPlatform := nodePool.Spec.Platform.Kubevirt
	kubevirtMachineTemplate := &capikubevirt.KubevirtMachineTemplate{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				nodePoolAnnotation: ctrlclient.ObjectKeyFromObject(nodePool).String(),
			},
			Namespace: controlPlaneNamespace,
		},
		Spec: capikubevirt.KubevirtMachineTemplateSpec{
			Template: capikubevirt.KubevirtMachineTemplateResource{
				Spec: capikubevirt.KubevirtMachineSpec{
					VirtualMachineTemplate: *kubevirtPlatform.NodeTemplate,
				},
			},
		},
	}
	specHash := hashStruct(kubevirtMachineTemplate.Spec.Template.Spec)
	kubevirtMachineTemplate.SetName(fmt.Sprintf("%s-%s", nodePool.GetName(), specHash))

	return kubevirtMachineTemplate, specHash
}
