package nodebootstrappertoken

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/base64"
	"fmt"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	"net/url"
	"strings"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeclient "k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	haproxyTemplateConfigmapName = "machine-config-server-haproxy-config-template"
	haproxyConfigSecretName      = "machine-config-server-haproxy-config"
	nodeBootstrapperTokenPrefix  = "node-bootstrapper-token"
	machineConfigServerTLSSecret = "machine-config-server"
)

// NodeBootstrapperTokenObserver watches the node-bootstrapper service account:
// It populates an haproxy config that the machine config server uses that enables
// authorization on the ignition endpoint with the node-bootstrapper token.
type NodeBootstrapperTokenObserver struct {

	// Client is a client that allows access to the management cluster
	Client client.Client

	// TargetClient is a Kube client for the target cluster
	TargetClient kubeclient.Interface

	// Namespace is the namespace where the control plane of the cluster
	// lives on the management server
	Namespace string

	// Log is the logger for this controller
	Log logr.Logger
}

// Reconcile periodically watches for changes to the node-bootstrapper token and updates
// the cluster machine config servers to use the value for authentication purposes.
func (r *NodeBootstrapperTokenObserver) Reconcile(_ context.Context, req ctrl.Request) (ctrl.Result, error) {
	controllerLog := r.Log.WithValues("node-bootstrapper-token-observer", req.NamespacedName)
	ctx := context.Background()
	if req.Namespace != nodeBootstrapperTokenNamespace {
		return ctrl.Result{}, nil
	}

	controllerLog.Info("syncing node bootstrapper token")
	nodeBootstrapperToken, err := r.fetchBootstrapperToken(ctx, controllerLog)
	if err != nil {
		return ctrl.Result{}, err
	}
	nodeBootstrapperTokenBase64 := base64.StdEncoding.EncodeToString(nodeBootstrapperToken)

	controllerLog.Info("Fetching machine config server haproxy template")
	var haproxyTemplateConfigMapData v1.ConfigMap
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: haproxyTemplateConfigmapName}, &haproxyTemplateConfigMapData)
	if err != nil {
		return ctrl.Result{}, err
	}
	var haproxyConfigTemplateData string
	var ok bool
	if haproxyConfigTemplateData, ok = haproxyTemplateConfigMapData.Data["haproxy.cfg"]; !ok {
		return ctrl.Result{}, fmt.Errorf("haproxy config not found")
	}

	controllerLog.Info("Fetching machine config server tls info")
	var machineConfigServerSSLCerts v1.Secret
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: machineConfigServerTLSSecret}, &machineConfigServerSSLCerts)
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
	haproxyConfigDataHash := calculateHash(haproxyConfigData)
	controllerLog.Info("Creating/Updating machine config server haproxy secret")
	haproxyConfigSecret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      haproxyConfigSecretName,
			Namespace: r.Namespace,
		},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, haproxyConfigSecret, func() error {
		haproxyConfigSecret.Type = v1.SecretTypeOpaque
		haproxyConfigSecret.Data = map[string][]byte{
			"node-bootstrapper-token": []byte(nodeBootstrapperToken),
			"haproxy.cfg":             haproxyConfigData,
			"tls.pem":                 haproxyTLSPem,
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	controllerLog.Info("Annotating MachineConfigServer CRDs in namespace to evaluate restart")
	var machineConfigServerList = &hyperv1.MachineConfigServerList{}
	err = r.Client.List(ctx, client.ObjectList(machineConfigServerList), client.InNamespace(r.Namespace))
	if err != nil {
		return ctrl.Result{}, err
	}
	if machineConfigServerList.Items != nil {
		for _, machineConfigServer := range machineConfigServerList.Items {
			if !(machineConfigServer.ObjectMeta.Annotations != nil &&
				machineConfigServer.ObjectMeta.Annotations["haproxy-config-data-checksum"] == haproxyConfigDataHash &&
				machineConfigServer.ObjectMeta.Annotations["machine-config-server-tls-key-checksum"] == machineConfigServerTLSKeyHash &&
				machineConfigServer.ObjectMeta.Annotations["machine-config-server-tls-cert-checksum"] == machineConfigServerTLSCertHash) {
				controllerLog.Info("Annotating MachineConfigServer CRD to trigger update to machine config servers", "name", machineConfigServer.Name)
				if machineConfigServer.ObjectMeta.Annotations == nil {
					machineConfigServer.ObjectMeta.Annotations = map[string]string{}
				}
				machineConfigServer.ObjectMeta.Annotations["haproxy-config-data-checksum"] = haproxyConfigDataHash
				machineConfigServer.ObjectMeta.Annotations["machine-config-server-tls-key-checksum"] = machineConfigServerTLSKeyHash
				machineConfigServer.ObjectMeta.Annotations["machine-config-server-tls-cert-checksum"] = machineConfigServerTLSCertHash
				if err = r.Client.Update(ctx, &machineConfigServer); err != nil {
					return ctrl.Result{}, err
				}
				controllerLog.Info("Annotated MachineConfigServer CRD", "name", machineConfigServer.Name)
			}
		}
	}
	return ctrl.Result{}, nil
}

func (r *NodeBootstrapperTokenObserver) fetchBootstrapperToken(ctx context.Context, logger logr.Logger) ([]byte, error) {
	logger.Info("Fetching node bootstrapper service account info")
	nodeBootstrapperSA, err := r.TargetClient.CoreV1().ServiceAccounts(nodeBootstrapperTokenNamespace).Get(ctx, nodeBootstrapperServiceAccountName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to fetch node bootstrapper token service account: %v", err)
	}
	logger.Info("Fetched node bootstrapper service account info. Locating token secret in the service account")
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
	logger.Info("Fetching service account token secret")
	secretData, err := r.TargetClient.CoreV1().Secrets(nodeBootstrapperTokenNamespace).Get(ctx, secretToFetch, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to node bootstrapper token secret data: %v", err)
	}
	var tokenData []byte
	var ok bool
	logger.Info("Fetched service account token secret. Ensuring token field is present")
	if tokenData, ok = secretData.Data["token"]; !ok {
		return nil, fmt.Errorf("token data could not be found in secret")
	}
	return tokenData, nil
}

func calculateHash(b []byte) string {
	return fmt.Sprintf("%x", md5.Sum(b))
}
