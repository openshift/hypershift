package kubevirtexternalinfra

import (
	"context"
	"errors"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperv1 "github.com/openshift/hypershift/api/v1beta1"
)

type KubevirtInfraClientMap interface {
	DiscoverKubevirtClusterClient(context.Context, client.Client, string, *hyperv1.KubevirtPlatformCredentials, string, string) (*KubevirtInfraClient, error)
	GetClient(key string) *KubevirtInfraClient
	Delete(string)
}

func NewKubevirtInfraClientMap() KubevirtInfraClientMap {
	return &kubevirtInfraClientMapImp{
		theMap: sync.Map{},
	}
}

type kubevirtInfraClientMapImp struct {
	theMap sync.Map
}

type KubevirtInfraClient struct {
	client.Client
	Namespace string
}

func (k *kubevirtInfraClientMapImp) DiscoverKubevirtClusterClient(ctx context.Context, cl client.Client, key string, credentials *hyperv1.KubevirtPlatformCredentials, localInfraNamespace string, secretNS string) (*KubevirtInfraClient, error) {
	if k == nil {
		return nil, nil
	}

	if credentials == nil || credentials.InfraKubeConfigSecret == nil {
		return &KubevirtInfraClient{
			Client:    cl,
			Namespace: localInfraNamespace,
		}, nil
	}
	loaded, ok := k.theMap.Load(key)
	if ok {
		return loaded.(*KubevirtInfraClient), nil
	}
	targetClient, err := generateKubevirtInfraClusterClient(ctx, cl, credentials, secretNS)
	if err != nil {
		return nil, err
	}

	cluster := &KubevirtInfraClient{
		Client:    targetClient,
		Namespace: credentials.InfraNamespace,
	}

	k.theMap.LoadOrStore(key, cluster)
	return cluster, nil
}

func (k *kubevirtInfraClientMapImp) GetClient(key string) *KubevirtInfraClient {
	if k == nil {
		return nil
	}
	if cl, ok := k.theMap.Load(key); ok {
		if clnt, ok := cl.(*KubevirtInfraClient); ok {
			return clnt
		}
	}
	return nil
}

func (k *kubevirtInfraClientMapImp) Delete(key string) {
	if k != nil {
		k.theMap.Delete(key)
	}
}

func generateKubevirtInfraClusterClient(ctx context.Context, cpClient client.Client, credentials *hyperv1.KubevirtPlatformCredentials, secretNamespace string) (client.Client, error) {
	kubeConfig, err := GetKubeConfig(ctx, cpClient, secretNamespace, credentials.InfraKubeConfigSecret.Name)

	clientConfig, err := clientcmd.NewClientConfigFromBytes(kubeConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create K8s-API client config: %w", err)
	}

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create REST config: %w", err)
	}
	var infraClusterClient client.Client

	infraClusterClient, err = client.New(restConfig, client.Options{Scheme: cpClient.Scheme()})
	if err != nil {
		return nil, fmt.Errorf("failed to create infra cluster client: %w", err)
	}

	return infraClusterClient, nil
}

func GetKubeConfig(ctx context.Context, cl client.Client, secretNamespace, secretName string) ([]byte, error) {
	infraKubeconfigSecret := &corev1.Secret{}

	infraKubeconfigSecretKey := client.ObjectKey{Namespace: secretNamespace, Name: secretName}
	if err := cl.Get(ctx, infraKubeconfigSecretKey, infraKubeconfigSecret); err != nil {
		return nil, fmt.Errorf("failed to fetch infra kubeconfig secret %s/%s: %w", secretNamespace, secretName, err)
	}

	kubeConfig, ok := infraKubeconfigSecret.Data["kubeconfig"]
	if !ok {
		return nil, errors.New("failed to retrieve infra kubeconfig from secret: 'kubeconfig' key is missing")
	}

	return kubeConfig, nil
}
