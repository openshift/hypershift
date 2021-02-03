package kubeadminpwd

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"

	"github.com/go-logr/logr"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	corelisters "k8s.io/client-go/listers/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	OAuthDeploymentName  = "oauth-openshift"
	SecretHashAnnotation = "hypershift.openshift.io/kubeadmin-secret-hash"
)

type OAuthRestarter struct {
	// Client is a client that allows access to the management cluster
	Client kubeclient.Interface

	// Log is the logger for this controller
	Log logr.Logger

	// Namespace is the namespace where the control plane of the cluster
	// lives on the management server
	Namespace string

	// SecretLister is a lister for target cluster secrets
	SecretLister corelisters.SecretLister
}

func (o *OAuthRestarter) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	controllerLog := o.Log.WithValues("secret", req.NamespacedName.String())

	// Ignore any secret that is not kube-system/kubeadmin
	if req.Namespace != metav1.NamespaceSystem || req.Name != KubeAdminSecret {
		return ctrl.Result{}, nil
	}

	controllerLog.Info("Begin reconciling")

	secret, err := o.SecretLister.Secrets(metav1.NamespaceSystem).Get(KubeAdminSecret)
	if err != nil && !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	hash := ""
	if err == nil {
		hash, err = calculateHash(secret.Data)
		if err != nil {
			return ctrl.Result{}, err
		}
	}
	oauthDeployment, err := o.Client.AppsV1().Deployments(o.Namespace).Get(ctx, OAuthDeploymentName, metav1.GetOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}
	updateNeeded := false
	if hash == "" {
		_, hasAnnotation := oauthDeployment.Spec.Template.ObjectMeta.Annotations[SecretHashAnnotation]
		if hasAnnotation {
			delete(oauthDeployment.Spec.Template.ObjectMeta.Annotations, SecretHashAnnotation)
			updateNeeded = true
		}
	} else {
		if oauthDeployment.Spec.Template.ObjectMeta.Annotations == nil {
			oauthDeployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
		}
		currentValue := oauthDeployment.Spec.Template.ObjectMeta.Annotations[SecretHashAnnotation]
		if currentValue != hash {
			oauthDeployment.Spec.Template.ObjectMeta.Annotations[SecretHashAnnotation] = hash
			updateNeeded = true
		}
	}
	if !updateNeeded {
		return ctrl.Result{}, nil
	}
	controllerLog.Info("Updating Outh Server deployment")
	_, err = o.Client.AppsV1().Deployments(o.Namespace).Update(ctx, oauthDeployment, metav1.UpdateOptions{})
	return ctrl.Result{}, err
}

func calculateHash(data map[string][]byte) (string, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", md5.Sum(b)), nil
}
