package etcdbackup

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/support/certs"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/spf13/cobra"
)

type fetchCertsOptions struct {
	hcpNamespace     string
	outputDir        string
	etcdClientSecret string
	etcdCAConfigMap  string
}

// NewFetchCertsCommand returns a cobra command that fetches etcd TLS certificates
// from an HCP namespace and writes them to disk for use by backup Jobs.
func NewFetchCertsCommand() *cobra.Command {
	opts := fetchCertsOptions{
		outputDir:        "/etc/etcd-certs",
		etcdClientSecret: manifests.EtcdClientSecret("").Name,
		etcdCAConfigMap:  manifests.EtcdSignerCAConfigMap("").Name,
	}

	cmd := &cobra.Command{
		Use:          "fetch-etcd-certs",
		Short:        "Fetch etcd TLS certificates from an HCP namespace and write them to disk",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer cancel()

			return runFetchCerts(ctx, opts)
		},
	}

	cmd.Flags().StringVar(&opts.hcpNamespace, "hcp-namespace", "", "namespace of the HostedControlPlane containing etcd secrets")
	cmd.Flags().StringVar(&opts.outputDir, "output-dir", opts.outputDir, "directory to write the TLS certificate files")
	cmd.Flags().StringVar(&opts.etcdClientSecret, "etcd-client-secret", opts.etcdClientSecret, "name of the etcd client TLS Secret")
	cmd.Flags().StringVar(&opts.etcdCAConfigMap, "etcd-ca-configmap", opts.etcdCAConfigMap, "name of the etcd CA ConfigMap")

	_ = cmd.MarkFlagRequired("hcp-namespace")

	return cmd
}

func runFetchCerts(ctx context.Context, opts fetchCertsOptions) error {
	k8sClient, err := newK8sClient()
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	return fetchAndWriteCerts(ctx, k8sClient, opts)
}

func newK8sClient() (client.Client, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	c, err := client.New(cfg, client.Options{})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	return c, nil
}

func fetchAndWriteCerts(ctx context.Context, k8sClient client.Client, opts fetchCertsOptions) error {
	clientSecret := manifests.EtcdClientSecret(opts.hcpNamespace)
	clientSecret.Name = opts.etcdClientSecret
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(clientSecret), clientSecret); err != nil {
		return fmt.Errorf("failed to get etcd client TLS secret %s/%s: %w", opts.hcpNamespace, opts.etcdClientSecret, err)
	}

	caConfigMap := manifests.EtcdSignerCAConfigMap(opts.hcpNamespace)
	caConfigMap.Name = opts.etcdCAConfigMap
	if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(caConfigMap), caConfigMap); err != nil {
		return fmt.Errorf("failed to get etcd CA configmap %s/%s: %w", opts.hcpNamespace, opts.etcdCAConfigMap, err)
	}

	certData, ok := clientSecret.Data[pki.EtcdClientCrtKey]
	if !ok {
		return fmt.Errorf("etcd client secret %s/%s missing key %q", opts.hcpNamespace, opts.etcdClientSecret, pki.EtcdClientCrtKey)
	}

	keyData, ok := clientSecret.Data[pki.EtcdClientKeyKey]
	if !ok {
		return fmt.Errorf("etcd client secret %s/%s missing key %q", opts.hcpNamespace, opts.etcdClientSecret, pki.EtcdClientKeyKey)
	}

	caData, ok := caConfigMap.Data[certs.CASignerCertMapKey]
	if !ok {
		return fmt.Errorf("etcd CA configmap %s/%s missing key %q", opts.hcpNamespace, opts.etcdCAConfigMap, certs.CASignerCertMapKey)
	}

	if err := os.MkdirAll(opts.outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", opts.outputDir, err)
	}

	type certFile struct {
		name string
		data []byte
	}
	files := []certFile{
		{pki.EtcdClientCrtKey, certData},
		{pki.EtcdClientKeyKey, keyData},
		{certs.CASignerCertMapKey, []byte(caData)},
	}

	for _, f := range files {
		name, data := f.name, f.data
		path := filepath.Join(opts.outputDir, name)
		if err := os.WriteFile(path, data, 0600); err != nil {
			return fmt.Errorf("failed to write %s: %w", path, err)
		}
		log.Printf("wrote %s (%d bytes)", path, len(data))
	}

	return nil
}
