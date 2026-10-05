package kubevirtexternalinfra

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sync"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/tools/clientcmd"

	cr "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/blang/semver"
	"golang.org/x/sync/errgroup"
)

type KubevirtInfraClientMap interface {
	DiscoverKubevirtClusterClient(context.Context, client.Client, string, *hyperv1.KubevirtPlatformCredentials, string, string) (KubevirtInfraClient, error)
	Delete(string)
}

type KubevirtInfraClient interface {
	GetInfraK8sVersion() (*semver.Version, error)
	GetInfraKubevirtVersion(ctx context.Context) (*semver.Version, error)
	GetInfraClient() client.Client
	GetInfraNamespace() string
}

type kubevirtInfraClientMapImp struct {
	theMap sync.Map
}

type kubevirtInfraClientImp struct {
	Client          client.Client
	DiscoveryClient *discovery.DiscoveryClient
	Namespace       string
	credentialHash  [sha256.Size]byte
}

type mockKubevirtInfraClientMap struct {
	cluster KubevirtInfraClient
}

type mockKubevirtInfraClient struct {
	cnvVersion string
	k8sVersion string
	namespace  string
	client     client.Client
}

func (k *mockKubevirtInfraClient) GetInfraK8sVersion() (*semver.Version, error) {
	v, err := semver.ParseTolerant(k.k8sVersion)
	return &v, err
}
func (k *mockKubevirtInfraClient) GetInfraKubevirtVersion(_ context.Context) (*semver.Version, error) {
	v, err := semver.ParseTolerant(k.cnvVersion)
	return &v, err
}
func (k *mockKubevirtInfraClient) GetInfraClient() client.Client {
	return k.client
}
func (k *mockKubevirtInfraClient) GetInfraNamespace() string {
	return k.namespace
}

func (k *mockKubevirtInfraClientMap) DiscoverKubevirtClusterClient(context.Context, client.Client, string, *hyperv1.KubevirtPlatformCredentials, string, string) (KubevirtInfraClient, error) {
	return k.cluster, nil
}

func (k *mockKubevirtInfraClientMap) Delete(string) {}

func NewMockKubevirtInfraClientMap(client client.Client, cnvVersion, k8sVersion string) KubevirtInfraClientMap {
	return &mockKubevirtInfraClientMap{
		cluster: &mockKubevirtInfraClient{
			client:     client,
			namespace:  "kubevirt-kubevirt",
			cnvVersion: cnvVersion,
			k8sVersion: k8sVersion,
		},
	}
}

func NewKubevirtInfraClientMap() KubevirtInfraClientMap {
	return &kubevirtInfraClientMapImp{
		theMap: sync.Map{},
	}
}

func (k *kubevirtInfraClientMapImp) DiscoverKubevirtClusterClient(ctx context.Context, cl client.Client, key string, credentials *hyperv1.KubevirtPlatformCredentials, localInfraNamespace string, secretNS string) (KubevirtInfraClient, error) {
	if k == nil {
		return nil, nil
	}

	if credentials == nil {
		cfg, err := cr.GetConfig()
		if err != nil {
			return nil, err
		}

		discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
		if err != nil {
			return nil, err
		}

		return &kubevirtInfraClientImp{
			Client:          cl,
			DiscoveryClient: discoveryClient,
			Namespace:       localInfraNamespace,
		}, nil
	}
	if credentials.InfraKubeConfigSecret == nil {
		k.Delete(key)
		return nil, errors.New("infrastructure credential reference is missing")
	}
	ref := credentials.InfraKubeConfigSecret
	kubeConfig, err := GetKubeConfig(ctx, cl, secretNS, ref.Name, ref.Key)
	if err != nil {
		k.Delete(key)
		return nil, err
	}
	credentialHash := sha256.Sum256(kubeConfig)
	loaded, ok := k.theMap.Load(key)
	if ok {
		cached := loaded.(*kubevirtInfraClientImp)
		if cached.credentialHash == credentialHash && cached.Namespace == credentials.InfraNamespace {
			return cached, nil
		}
		k.Delete(key)
	}
	targetClient, targetDiscoveryClient, err := generateKubevirtInfraClusterClient(cl, kubeConfig)
	if err != nil {
		return nil, err
	}

	cluster := &kubevirtInfraClientImp{
		Client:          targetClient,
		DiscoveryClient: targetDiscoveryClient,
		Namespace:       credentials.InfraNamespace,
		credentialHash:  credentialHash,
	}

	k.theMap.Store(key, cluster)
	return cluster, nil
}

func (k *kubevirtInfraClientMapImp) Delete(key string) {
	if k != nil {
		k.theMap.Delete(key)
	}
}

func generateKubevirtInfraClusterClient(cpClient client.Client, kubeConfig []byte) (client.Client, *discovery.DiscoveryClient, error) {
	clientConfig, err := clientcmd.NewClientConfigFromBytes(kubeConfig)
	if err != nil {
		return nil, nil, errors.New("failed to create infrastructure client config")
	}

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, nil, errors.New("failed to create infrastructure REST config")
	}
	var infraClusterClient client.Client

	infraClusterClient, err = client.New(restConfig, client.Options{Scheme: cpClient.Scheme()})
	if err != nil {
		return nil, nil, errors.New("failed to create infrastructure client")
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return nil, nil, errors.New("failed to create infrastructure discovery client")
	}

	return infraClusterClient, discoveryClient, nil
}

func (k *kubevirtInfraClientImp) GetInfraK8sVersion() (*semver.Version, error) {
	k8sVersion, err := k.DiscoveryClient.ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to detect infrastructure cluster Kubernetes version for KubeVirt platform: %w", err)
	}

	version, err := semver.ParseTolerant(k8sVersion.GitVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to parse infrastructure cluster Kubernetes version for KubeVirt platform: %w", err)
	}

	return &version, nil
}

func (k *kubevirtInfraClientImp) GetInfraClient() client.Client {
	return k.Client
}

func (k *kubevirtInfraClientImp) GetInfraNamespace() string {
	return k.Namespace
}

func (k *kubevirtInfraClientImp) GetInfraKubevirtVersion(ctx context.Context) (*semver.Version, error) {

	type info struct {
		GitVersion string `json:"gitVersion"`
	}

	restClient := k.DiscoveryClient.RESTClient()

	var group metav1.APIGroup
	// First, find out which version to query
	uri := "/apis/subresources.kubevirt.io"
	result := restClient.Get().AbsPath(uri).Do(ctx)
	if data, err := result.Raw(); err != nil {
		var connErr *url.Error
		isConnectionErr := errors.As(err, &connErr)
		if isConnectionErr {
			err = connErr.Err
		}
		return nil, fmt.Errorf("unable to validate OpenShift Virtualization version due to connection error: %w", err)
	} else if err = json.Unmarshal(data, &group); err != nil {
		return nil, fmt.Errorf("unable to validate OpenShift Virtualization version due malformed group version data: %w", err)
	}

	// Now, query the preferred version
	uri = fmt.Sprintf("/apis/%s/version", group.PreferredVersion.GroupVersion)
	var serverInfo info

	result = restClient.Get().AbsPath(uri).Do(ctx)
	if data, err := result.Raw(); err != nil {
		var connErr *url.Error
		if errors.As(err, &connErr) {
			err = connErr.Err
		}

		return nil, fmt.Errorf("unable to validate OpenShift Virtualization version due to connection error: %w", err)
	} else if err = json.Unmarshal(data, &serverInfo); err != nil {
		return nil, fmt.Errorf("unable to validate OpenShift Virtualization version due malformed version data: %w", err)
	}

	version, err := semver.ParseTolerant(serverInfo.GitVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to parse infrastructure cluster OpenShift Virtualization version [%s] for KubeVirt platform: %w", serverInfo.GitVersion, err)
	}

	return &version, nil

}

// GetKubeConfig retrieves validated infrastructure credentials. The optional key
// selects a configured credential entry; existing callers default to kubeconfig.
func GetKubeConfig(ctx context.Context, cl client.Client, secretNamespace, secretName string, keys ...string) ([]byte, error) {
	if secretName == "" {
		return nil, errors.New("infrastructure credential name is empty")
	}
	key := "kubeconfig"
	if len(keys) > 1 {
		return nil, errors.New("only one infrastructure credential key may be specified")
	}
	if len(keys) == 1 {
		key = keys[0]
	}
	infraKubeconfigSecret := &corev1.Secret{}

	infraKubeconfigSecretKey := client.ObjectKey{Namespace: secretNamespace, Name: secretName}
	if err := cl.Get(ctx, infraKubeconfigSecretKey, infraKubeconfigSecret); err != nil {
		return nil, fmt.Errorf("failed to fetch infra kubeconfig secret %s/%s: %w", secretNamespace, secretName, err)
	}

	data, err := KubeConfigData(infraKubeconfigSecret, key)
	if err != nil {
		return nil, err
	}
	return data[key], nil
}

// KubeConfigData selects and validates the kubeconfig keys consumed by external
// infrastructure clients, preserving only their credentials and namespace metadata.
func KubeConfigData(secret *corev1.Secret, key string) (map[string][]byte, error) {
	if secret == nil {
		return nil, errors.New("infrastructure kubeconfig Secret is missing")
	}
	if key == "" || key == "namespace" {
		return nil, errors.New("infrastructure kubeconfig key must be non-empty and cannot be namespace")
	}
	selected, ok := secret.Data[key]
	if !ok {
		return nil, fmt.Errorf("infrastructure secret %s is missing kubeconfig key %q", client.ObjectKeyFromObject(secret), key)
	}
	if err := ValidateKubeConfig(selected); err != nil {
		return nil, fmt.Errorf("invalid infrastructure secret %s key %q: %w", client.ObjectKeyFromObject(secret), key, err)
	}
	data := map[string][]byte{key: bytes.Clone(selected), "kubeconfig": bytes.Clone(selected)}
	if canonical, exists := secret.Data["kubeconfig"]; key != "kubeconfig" && exists {
		if err := ValidateKubeConfig(canonical); err != nil {
			return nil, fmt.Errorf("invalid infrastructure secret %s key kubeconfig: %w", client.ObjectKeyFromObject(secret), err)
		}
		data["kubeconfig"] = bytes.Clone(canonical)
	}
	if namespace, exists := secret.Data["namespace"]; exists {
		data["namespace"] = bytes.Clone(namespace)
	}
	return data, nil
}

// ValidateKubeConfig checks tenant-provided infrastructure credentials without
// creating a client, executing credential plugins, or opening referenced files.
func ValidateKubeConfig(data []byte) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("kubeconfig is empty")
	}
	config, err := clientcmd.Load(data)
	if err != nil {
		// Decoder errors can contain credentials from the input. Do not expose them.
		return errors.New("kubeconfig cannot be parsed")
	}
	if config.CurrentContext == "" {
		return errors.New("kubeconfig must specify current-context")
	}
	if config.Contexts[config.CurrentContext] == nil {
		return errors.New("kubeconfig current-context does not exist")
	}
	for _, context := range config.Contexts {
		if context == nil {
			return errors.New("kubeconfig contains an invalid context entry")
		}
	}
	for _, authInfo := range config.AuthInfos {
		if authInfo == nil {
			return errors.New("kubeconfig contains an invalid user entry")
		}
		if authInfo.Exec != nil {
			return errors.New("kubeconfig contains prohibited exec credentials")
		}
		if authInfo.AuthProvider != nil {
			return errors.New("kubeconfig contains prohibited auth-provider credentials")
		}
		if authInfo.TokenFile != "" {
			return errors.New("kubeconfig contains prohibited tokenFile credentials")
		}
		if authInfo.ClientCertificate != "" {
			return errors.New("kubeconfig contains prohibited client-certificate file reference")
		}
		if authInfo.ClientKey != "" {
			return errors.New("kubeconfig contains prohibited client-key file reference")
		}
	}
	for _, cluster := range config.Clusters {
		if cluster == nil {
			return errors.New("kubeconfig contains an invalid cluster entry")
		}
		if cluster.InsecureSkipTLSVerify {
			return errors.New("kubeconfig contains prohibited insecure-skip-tls-verify")
		}
		if cluster.CertificateAuthority != "" {
			return errors.New("kubeconfig contains prohibited certificate-authority file reference")
		}
	}
	// Every filesystem-backed field has already been rejected, so upstream
	// structural validation cannot open tenant-selected files.
	if err := clientcmd.Validate(*config); err != nil {
		return errors.New("kubeconfig structure is invalid")
	}
	return nil
}

func ValidateClusterVersions(ctx context.Context, cl KubevirtInfraClient) error {
	var cnvVersion, k8sVersion *semver.Version

	eg, egCtx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		var err error
		cnvVersion, err = cl.GetInfraKubevirtVersion(egCtx)
		return err
	})

	eg.Go(func() error {
		var err error
		k8sVersion, err = cl.GetInfraK8sVersion()
		return err
	})

	err := eg.Wait()
	if err != nil {
		return err
	}

	// ignore "Pre" so this check works accurately with pre-release CNV versions.
	cnvVersion.Pre = []semver.PRVersion{}
	minCNVVersion := semver.MustParse("1.0.0")

	var errs []error
	if cnvVersion.LT(minCNVVersion) {
		errs = append(errs, fmt.Errorf("infrastructure kubevirt version is [%s], hypershift kubevirt platform requires kubevirt version [%s] or greater", cnvVersion.String(), minCNVVersion.String()))
	}

	// ignore "Pre" so this check works accurately with pre-release K8s versions.
	k8sVersion.Pre = []semver.PRVersion{}
	minK8sVersion := semver.MustParse("1.27.0")

	if k8sVersion.LT(minK8sVersion) {
		errs = append(errs, fmt.Errorf("infrastructure Kubernetes version is [%s], hypershift kubevirt platform requires Kubernetes version [%s] or greater", k8sVersion.String(), minK8sVersion.String()))
	}

	return utilerrors.NewAggregate(errs)
}
