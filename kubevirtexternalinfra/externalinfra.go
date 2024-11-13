package kubevirtexternalinfra

import (
	"context"
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

	if credentials == nil || credentials.InfraKubeConfigSecret == nil {
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
	loaded, ok := k.theMap.Load(key)
	if ok {
		return loaded.(*kubevirtInfraClientImp), nil
	}
	targetClient, targetDiscoveryClient, err := generateKubevirtInfraClusterClient(ctx, cl, credentials, secretNS)
	if err != nil {
		return nil, err
	}

	cluster := &kubevirtInfraClientImp{
		Client:          targetClient,
		DiscoveryClient: targetDiscoveryClient,
		Namespace:       credentials.InfraNamespace,
	}

	k.theMap.LoadOrStore(key, cluster)
	return cluster, nil
}

func (k *kubevirtInfraClientMapImp) Delete(key string) {
	if k != nil {
		k.theMap.Delete(key)
	}
}

func generateKubevirtInfraClusterClient(ctx context.Context, cpClient client.Client, credentials *hyperv1.KubevirtPlatformCredentials, secretNamespace string) (client.Client, *discovery.DiscoveryClient, error) {
	kubeConfig, err := GetKubeConfig(ctx, cpClient, secretNamespace, credentials.InfraKubeConfigSecret.Name)
	if err != nil {
		return nil, nil, err
	}

	clientConfig, err := clientcmd.NewClientConfigFromBytes(kubeConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create K8s-API client config: %w", err)
	}

	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create REST config: %w", err)
	}
	var infraClusterClient client.Client

	infraClusterClient, err = client.New(restConfig, client.Options{Scheme: cpClient.Scheme()})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create infra cluster client: %w", err)
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create infra cluster discovery client: %w", err)
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
		connErr, isConnectionErr := err.(*url.Error)
		if isConnectionErr {
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
