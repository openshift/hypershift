package nodebootstrappertoken

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	v1 "k8s.io/api/core/v1"
	"net/url"
	"strings"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	haproxyTemplateConfigmapName  = "machine-config-server-haproxy-config-template"
	haproxyConfigSecretName       = "machine-config-server-haproxy-config"
	nodeBootstrapperTokenPrefix   = "node-bootstrapper-token"
	machineConfigServerDeployment = "machine-config-server"
	machineConfigServerTLSSecret  = "machine-config-server"
)

// ManagedCAObserver watches 2 CA configmaps in the target cluster:
// - openshift-managed-config/router-ca
// - openshift-managed-config/service-ca
// It populates a configmap on the management cluster with their content.
// A separate controller uses that content to adjust the configmap for
// the Kube controller manager CA.
type NodeBootstrapperTokenObserver struct {

	// Client is a client that allows access to the management cluster
	Client kubeclient.Interface

	// TargetClient is a Kube client for the target cluster
	TargetClient kubeclient.Interface

	// Namespace is the namespace where the control plane of the cluster
	// lives on the management server
	Namespace string

	// Log is the logger for this controller
	Log logr.Logger
}

// Reconcile periodically watches for changes in the CA configmaps and updates
// the kube-controller-manager-ca configmap in the management cluster with their
// content.
func (r *NodeBootstrapperTokenObserver) Reconcile(_ context.Context, req ctrl.Request) (ctrl.Result, error) {
	controllerLog := r.Log.WithValues("node-bootstrapper-token-observer", req.NamespacedName)
	ctx := context.Background()

	if req.Namespace != NodeBootstrapperTokenNamespace {
		return ctrl.Result{}, nil
	}

	controllerLog.Info("syncing node bootstrapper token")
	nodeBootstrapperToken, err := r.fetchBootstrapperToken(ctx, controllerLog)
	if err != nil {
		return ctrl.Result{}, err
	}
	nodeBootstrapperTokenBase64 := base64.StdEncoding.EncodeToString(nodeBootstrapperToken)
	nodeBootstrapperTokenBase64Hash := calculateHash([]byte(nodeBootstrapperTokenBase64))
	controllerLog.Info("Calculated node bootstrapper token hash", "hash", nodeBootstrapperTokenBase64Hash)

	haproxyTemplateConfigMapData, err := r.Client.CoreV1().ConfigMaps(r.Namespace).Get(ctx, haproxyTemplateConfigmapName, metav1.GetOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}
	var haproxyConfigTemplateData string
	var ok bool
	if haproxyConfigTemplateData, ok = haproxyTemplateConfigMapData.Data["haproxy.cfg"]; !ok {
		return ctrl.Result{}, fmt.Errorf("haproxy config not found")
	}

	machineConfigServerSSLCerts, err := r.Client.CoreV1().Secrets(r.Namespace).Get(ctx, machineConfigServerTLSSecret, metav1.GetOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}
	var machineConfigServerTLSCert, machineConfigServerTLSKey []byte
	if machineConfigServerTLSCert, ok = machineConfigServerSSLCerts.Data["tls.crt"]; !ok {
		return ctrl.Result{}, fmt.Errorf("machine config server tls.crt not found")
	}
	if machineConfigServerTLSKey, ok = machineConfigServerSSLCerts.Data["tls.key"]; !ok {
		return ctrl.Result{}, fmt.Errorf("machine config server tls.crt not found")
	}
	machineConfigServerTLSCertHash := calculateHash(machineConfigServerTLSCert)
	machineConfigServerTLSKeyHash := calculateHash(machineConfigServerTLSKey)
	haproxyTLSPem := bytes.Join([][]byte{machineConfigServerTLSCert, machineConfigServerTLSKey}, []byte("\n"))
	haproxyConfigData := bytes.Replace([]byte(haproxyConfigTemplateData), []byte("NODE_BOOTSTRAPPER_TOKEN_REPLACE"), []byte(url.QueryEscape(nodeBootstrapperTokenBase64)), -1)
	haproxyConfigSecret := &v1.Secret{
		Type: v1.SecretTypeOpaque,
		ObjectMeta: metav1.ObjectMeta{
			Name:      haproxyConfigSecretName,
			Namespace: r.Namespace,
		},
		Data: map[string][]byte{
			"node-bootstrapper-token": []byte(nodeBootstrapperToken),
			"haproxy.cfg":             haproxyConfigData,
			"tls.pem":                 haproxyTLSPem,
		},
	}
	_, err = r.Client.CoreV1().Secrets(r.Namespace).Create(ctx, haproxyConfigSecret, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		_, err = r.Client.CoreV1().Secrets(r.Namespace).Update(ctx, haproxyConfigSecret, metav1.UpdateOptions{})
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("could not create/update haproxy config secret: %v", err)
	}
	machineConfigServerDeployment, err := r.Client.AppsV1().Deployments(r.Namespace).Get(ctx, machineConfigServerDeployment, metav1.GetOptions{})
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("could not fetch machine config server deployment: %v", err)
	}
	if machineConfigServerDeployment.Spec.Template.ObjectMeta.Annotations != nil &&
		machineConfigServerDeployment.Spec.Template.ObjectMeta.Annotations["node-bootstrapper-token-checksum"] == nodeBootstrapperTokenBase64 &&
		machineConfigServerDeployment.Spec.Template.ObjectMeta.Annotations["machine-config-server-tls-key-checksum"] == machineConfigServerTLSKeyHash &&
		machineConfigServerDeployment.Spec.Template.ObjectMeta.Annotations["machine-config-server-tls-cert-checksum"] == machineConfigServerTLSCertHash {
		return ctrl.Result{}, nil
	}
	r.Log.Info("Updating machine config server deployment checksum annotation")
	if machineConfigServerDeployment.Spec.Template.ObjectMeta.Annotations == nil {
		machineConfigServerDeployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
	}
	machineConfigServerDeployment.Spec.Template.ObjectMeta.Annotations["node-bootstrapper-token-checksum"] = nodeBootstrapperTokenBase64
	machineConfigServerDeployment.Spec.Template.ObjectMeta.Annotations["machine-config-server-tls-key-checksum"] = machineConfigServerTLSKeyHash
	machineConfigServerDeployment.Spec.Template.ObjectMeta.Annotations["machine-config-server-tls-cert-checksum"] = machineConfigServerTLSCertHash
	if _, err = r.Client.AppsV1().Deployments(r.Namespace).Update(ctx, machineConfigServerDeployment, metav1.UpdateOptions{}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *NodeBootstrapperTokenObserver) fetchBootstrapperToken(ctx context.Context, logger logr.Logger) ([]byte, error) {
	nodeBootstrapperSA, err := r.TargetClient.CoreV1().ServiceAccounts(NodeBootstrapperTokenNamespace).Get(ctx, NodeBootstrapperServiceAccountName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch node bootstrapper token service account: %v", err)
	}
	secretToFetch := ""
	for _, i := range nodeBootstrapperSA.Secrets {
		if strings.HasPrefix(i.Name, nodeBootstrapperTokenPrefix) {
			secretToFetch = i.Name
			break
		}
	}
	if len(secretToFetch) == 0 {
		return nil, fmt.Errorf("service account token secret doesn't exist for node bootstrapper sa")
	}
	secretData, err := r.TargetClient.CoreV1().Secrets(NodeBootstrapperTokenNamespace).Get(ctx, secretToFetch, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to node bootstrapper token secret data: %v", err)
	}
	var tokenData []byte
	var ok bool
	if tokenData, ok = secretData.Data["token"]; !ok {
		return nil, fmt.Errorf("token data could not be found in secret")
	}
	return tokenData, nil
}

func calculateHash(b []byte) string {
	return fmt.Sprintf("%x", md5.Sum(b))
}
