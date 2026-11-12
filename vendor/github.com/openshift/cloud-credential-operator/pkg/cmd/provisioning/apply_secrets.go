package provisioning

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type applySecretsOptions struct {
	TargetDir      string
	KubeConfigFile string
}

var (
	// ApplySecretsOpts captures the options that affect applying generated secret manifests.
	ApplySecretsOpts = applySecretsOptions{}
)

// NewApplySecretsCmd provides the "apply secrets" subcommand
func NewApplySecretsCmd() *cobra.Command {
	applySecretsCmd := &cobra.Command{
		Use:   "secrets",
		Short: "Apply generated secret manifests to a cluster",
		Long: "Apply the Secret manifests previously generated into <output-dir>/manifests " +
			"to the cluster. Manifests of any other kind are ignored.",
		Args: cobra.NoArgs,
		RunE: runApplySecrets,
		// main.go already reports whatever Execute returns, so letting cobra print it too
		// would show every error twice.
		SilenceErrors: true,
	}

	applySecretsCmd.PersistentFlags().StringVar(&ApplySecretsOpts.TargetDir, "output-dir", "", "Directory to place generated files (defaults to current directory)")
	applySecretsCmd.PersistentFlags().StringVar(&ApplySecretsOpts.KubeConfigFile, "kubeconfig", "", "Path to the kubeconfig file (defaults to $KUBECONFIG, then ~/.kube/config)")

	return applySecretsCmd
}

func runApplySecrets(cmd *cobra.Command, args []string) error {
	// Flags parsed successfully, so anything that fails from here on is a runtime problem
	// rather than misuse. Printing the usage block after it would bury the message.
	cmd.SilenceUsage = true

	if ApplySecretsOpts.TargetDir == "" {
		pwd, err := os.Getwd()
		if err != nil {
			log.Fatalf("Failed to get current directory: %s", err)
		}

		ApplySecretsOpts.TargetDir = pwd
	}

	targetDir, err := filepath.Abs(ApplySecretsOpts.TargetDir)
	if err != nil {
		return fmt.Errorf("failed to resolve full path: %w", err)
	}

	// Read and validate the manifests before touching the cluster so that a bad
	// output directory fails without any API calls.
	secrets, err := loadSecretManifests(filepath.Join(targetDir, ManifestsDirName))
	if err != nil {
		return err
	}

	if len(secrets) == 0 {
		log.Printf("WARNING: No Secret manifests found in %s, nothing to apply", filepath.Join(targetDir, ManifestsDirName))
		return nil
	}

	kubeClient, err := newClusterClient(ApplySecretsOpts.KubeConfigFile)
	if err != nil {
		return err
	}

	return applySecrets(cmd.Context(), kubeClient, secrets)
}

// loadSecretManifests reads every YAML document in manifestsDir and returns the ones
// that are core v1 Secrets. Other kinds are ignored: the directory also holds
// install-time manifests such as cluster-authentication-02-config.yaml which must not
// be applied to a running cluster.
func loadSecretManifests(manifestsDir string) ([]*unstructured.Unstructured, error) {
	entries, err := os.ReadDir(manifestsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("manifests directory %s does not exist, run the create command for your cloud first or pass --output-dir", manifestsDir)
		}
		return nil, fmt.Errorf("failed to read manifests directory %s: %w", manifestsDir, err)
	}

	secrets := make([]*unstructured.Unstructured, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !(strings.HasSuffix(entry.Name(), ".yaml") || strings.HasSuffix(entry.Name(), ".yml")) {
			continue
		}

		manifestPath := filepath.Join(manifestsDir, entry.Name())
		fileSecrets, err := decodeSecretsFromFile(manifestPath)
		if err != nil {
			return nil, err
		}
		secrets = append(secrets, fileSecrets...)
	}

	return secrets, nil
}

func decodeSecretsFromFile(manifestPath string) ([]*unstructured.Unstructured, error) {
	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open manifest %s: %w", manifestPath, err)
	}
	defer manifestFile.Close()

	secrets := []*unstructured.Unstructured{}
	decoder := yaml.NewYAMLOrJSONDecoder(manifestFile, 4096)
	for {
		manifest := &unstructured.Unstructured{}
		if err := decoder.Decode(manifest); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to parse manifest %s: %w", manifestPath, err)
		}

		// An empty YAML document decodes without error into an object with no content.
		if len(manifest.Object) == 0 {
			continue
		}

		if manifest.GetKind() != "Secret" {
			log.Printf("Ignoring %s in %s, not a Secret", manifest.GetKind(), manifestPath)
			continue
		}

		if manifest.GetName() == "" {
			return nil, fmt.Errorf("manifest %s contains a Secret without a name", manifestPath)
		}

		if manifest.GetNamespace() == "" {
			return nil, fmt.Errorf("manifest %s contains Secret %s without a namespace", manifestPath, manifest.GetName())
		}

		secrets = append(secrets, manifest)
	}

	return secrets, nil
}

// applySecrets creates or overwrites the given secrets on a best-effort basis: a failure on one
// secret (eg. its target namespace does not exist) does not stop the rest from being applied.
// Every failure is reported together once all secrets have been attempted.
//
// The Secrets these manifests replace are created by the installer at install time via a plain
// create, not "oc apply -f" or any other server-side apply flow, so there is no field manager
// bookkeeping to maintain parity with. Creating or fully overwriting the object here matches
// that behavior without introducing SSA ownership conflicts against objects nobody has ever
// applied before.
func applySecrets(ctx context.Context, kubeClient client.Client, secrets []*unstructured.Unstructured) error {
	var errs []error
	applied := 0

	for _, secret := range secrets {
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(secret.GroupVersionKind())
		err := kubeClient.Get(ctx, types.NamespacedName{Name: secret.GetName(), Namespace: secret.GetNamespace()}, existing)
		switch {
		case apierrors.IsNotFound(err):
			if err := kubeClient.Create(ctx, secret); err != nil {
				errs = append(errs, fmt.Errorf("failed to create Secret %s/%s: %w", secret.GetNamespace(), secret.GetName(), err))
				continue
			}
		case err != nil:
			errs = append(errs, fmt.Errorf("failed to get Secret %s/%s: %w", secret.GetNamespace(), secret.GetName(), err))
			continue
		default:
			secret.SetResourceVersion(existing.GetResourceVersion())
			if err := kubeClient.Update(ctx, secret); err != nil {
				errs = append(errs, fmt.Errorf("failed to update Secret %s/%s: %w", secret.GetNamespace(), secret.GetName(), err))
				continue
			}
		}
		applied++
		log.Printf("Applied Secret %s/%s", secret.GetNamespace(), secret.GetName())
	}

	log.Printf("Applied %d Secret(s)", applied)
	return errors.Join(errs...)
}
