package cmca

import (
	"bytes"
	"context"
	"crypto/md5"
	"fmt"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
)

const (
	RouterCAConfigMap  = "router-ca"
	ServiceCAConfigMap = "service-ca"
)

// ManagedCAObserver watches 2 CA configmaps in the target cluster:
// - openshift-managed-config/router-ca
// - openshift-managed-config/service-ca
// It populates a configmap on the management cluster with their content.
// A separate controller uses that content to adjust the configmap for
// the Kube controller manager CA.
type ManagedCAObserver struct {

	// Client is a client that allows access to the management cluster
	Client kubeclient.Interface

	// TargetClient is a Kube client for the target cluster
	TargetClient kubeclient.Interface

	// Namespace is the namespace where the control plane of the cluster
	// lives on the management server
	Namespace string

	// InitialCA is the initial CA for the controller manager
	InitialCA string

	// Log is the logger for this controller
	Log logr.Logger
}

func (r *ManagedCAObserver) managedDeployments() []string {
	return []string{
		manifests.KCMDeployment(r.Namespace).Name,
		manifests.OpenShiftAPIServerDeployment(r.Namespace).Name,
	}
}

// Reconcile periodically watches for changes in the CA configmaps and updates
// the kube-controller-manager-ca configmap in the management cluster with their
// content.
func (r *ManagedCAObserver) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {

	if req.Namespace != ManagedConfigNamespace {
		return ctrl.Result{}, nil
	}

	controllerLog := r.Log.WithValues("configmap", req.NamespacedName)

	controllerLog.Info("syncing configmap")

	additionalCAs, err := r.getAdditionalCAs(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	ca := &bytes.Buffer{}
	if _, err = fmt.Fprintf(ca, "%s", r.InitialCA); err != nil {
		return ctrl.Result{}, err
	}
	for _, additionalCA := range additionalCAs {
		ca.Write(additionalCA)
	}

	hash := calculateHash(ca.Bytes())
	controllerLog.Info("Calculated controller manager hash", "hash", hash)

	destinationCM, err := r.Client.CoreV1().ConfigMaps(r.Namespace).Get(ctx, manifests.ServiceServingCA(r.Namespace).Name, metav1.GetOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}
	if destinationCM.Data["service-ca.crt"] != ca.String() {
		destinationCM.Data["service-ca.crt"] = ca.String()
		r.Log.Info("Updating controller manager configmap")
		if _, err = r.Client.CoreV1().ConfigMaps(r.Namespace).Update(ctx, destinationCM, metav1.UpdateOptions{}); err != nil {
			return ctrl.Result{}, err
		}
	}

	for _, deployment := range r.managedDeployments() {
		if err := r.ensureAnnotationOnDeployment(ctx, deployment, hash); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to ensure annotation on %s deployment: %w", deployment, err)
		}
	}

	return ctrl.Result{}, nil

}

func (r *ManagedCAObserver) ensureAnnotationOnDeployment(ctx context.Context, deploymentName string, hash string) error {
	deployment, err := r.Client.AppsV1().Deployments(r.Namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment %s/%s: %w", r.Namespace, deploymentName, err)
	}

	if deployment.Spec.Template.ObjectMeta.Annotations["ca-checksum"] == hash {
		return nil
	}

	r.Log.Info("Updating deployment", "name", deploymentName)
	if deployment.Spec.Template.ObjectMeta.Annotations == nil {
		deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
	}
	deployment.Spec.Template.ObjectMeta.Annotations["ca-checksum"] = hash

	_, err = r.Client.AppsV1().Deployments(r.Namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	return err
}

func (r *ManagedCAObserver) getAdditionalCAs(ctx context.Context) ([][]byte, error) {
	additionalCAs := [][]byte{}
	cm, err := r.TargetClient.CoreV1().ConfigMaps(ManagedConfigNamespace).Get(ctx, RouterCAConfigMap, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to fetch router ca configmap: %v", err)
	}
	if err == nil {
		additionalCAs = append(additionalCAs, []byte(cm.Data["ca-bundle.crt"]))
	}
	cm, err = r.TargetClient.CoreV1().ConfigMaps(ManagedConfigNamespace).Get(ctx, ServiceCAConfigMap, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return nil, fmt.Errorf("failed to fetch service ca configmap: %v", err)
	}
	if err == nil {
		additionalCAs = append(additionalCAs, []byte(cm.Data["ca-bundle.crt"]))
	}
	return additionalCAs, nil
}

func calculateHash(b []byte) string {
	return fmt.Sprintf("%x", md5.Sum(b))
}
