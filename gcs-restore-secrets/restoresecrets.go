package gcsrestoresecrets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type options struct {
	secretsDir string
	namespace  string
}

func NewStartCommand() *cobra.Command {
	opts := options{}

	cmd := &cobra.Command{
		Use:          "restore-secrets",
		Short:        "Restore PKI secrets from a backup archive into the HCP namespace",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return run(ctx, opts)
		},
	}

	cmd.Flags().StringVar(&opts.secretsDir, "secrets-dir", "", "path to directory containing secret JSON files")
	cmd.Flags().StringVar(&opts.namespace, "namespace", "", "Kubernetes namespace to create/update secrets in")
	_ = cmd.MarkFlagRequired("secrets-dir")
	_ = cmd.MarkFlagRequired("namespace")

	return cmd
}

func run(ctx context.Context, opts options) error {
	k8sClient, err := newK8sClient()
	if err != nil {
		return fmt.Errorf("failed to create Kubernetes client: %w", err)
	}
	return restoreSecrets(ctx, k8sClient, opts)
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

type secretJSON struct {
	Data map[string]string `json:"data"`
}

func restoreSecrets(ctx context.Context, k8sClient client.Client, opts options) error {
	if _, err := os.Stat(opts.secretsDir); os.IsNotExist(err) {
		msg := "no-secrets"
		fmt.Println(msg)
		writeTerminationMessage(msg)
		return nil
	}

	entries, err := os.ReadDir(opts.secretsDir)
	if err != nil {
		return fmt.Errorf("failed to read secrets directory: %w", err)
	}

	var jsonFiles []os.DirEntry
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			jsonFiles = append(jsonFiles, entry)
		}
	}

	if len(jsonFiles) == 0 {
		msg := "no-secrets"
		fmt.Println(msg)
		writeTerminationMessage(msg)
		return nil
	}

	count := 0
	for _, entry := range jsonFiles {
		filePath := filepath.Join(opts.secretsDir, entry.Name())
		secretName := strings.TrimSuffix(entry.Name(), ".json")

		raw, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", filePath, err)
		}

		var sj secretJSON
		if err := json.Unmarshal(raw, &sj); err != nil {
			return fmt.Errorf("failed to parse %s: %w", filePath, err)
		}

		secretData := make(map[string][]byte, len(sj.Data))
		for key, b64Val := range sj.Data {
			decoded, err := base64.StdEncoding.DecodeString(b64Val)
			if err != nil {
				return fmt.Errorf("failed to decode base64 for key %s in %s: %w", key, entry.Name(), err)
			}
			secretData[key] = decoded
		}

		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: opts.namespace,
			},
		}
		result, err := controllerutil.CreateOrUpdate(ctx, k8sClient, secret, func() error {
			secret.Data = secretData
			return nil
		})
		if err != nil {
			return fmt.Errorf("failed to create/update secret %s: %w", secretName, err)
		}
		fmt.Printf("Secret %s/%s: %s\n", opts.namespace, secretName, result)
		count++
	}

	msg := fmt.Sprintf("secrets-restored:%d", count)
	fmt.Println(msg)
	writeTerminationMessage(msg)
	return nil
}

func writeTerminationMessage(msg string) {
	if err := os.WriteFile("/dev/termination-log", []byte(msg), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to write termination log: %v\n", err)
	}
}
