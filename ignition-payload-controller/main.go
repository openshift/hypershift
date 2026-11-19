package ignitionpayloadcontroller

import (
	"context"
	"fmt"
	"os"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	ignitionpayload "github.com/openshift/hypershift/hypershift-operator/controllers/ignitionpayload"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/ignitionserver"
	ignserver "github.com/openshift/hypershift/ignition-server/controllers"
	hyperapi "github.com/openshift/hypershift/support/api"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"
	"github.com/openshift/hypershift/support/imageregistry"
	"github.com/openshift/hypershift/support/releaseinfo"

	corev1 "k8s.io/api/core/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spf13/cobra"
)

const (
	namespaceEnvVariableName = "MY_NAMESPACE"

	// tokenSigningKeySecretName names the Secret holding the ECDSA private key the generator signs
	// bearer tokens with (PEM under tls.key). Its public half is provisioned to the serving tier.
	tokenSigningKeySecretName = "ignition-payload-token-signing-key"
	tokenSigningKeySecretKey  = "tls.key"
)

var setupLog = ctrl.Log.WithName("setup")

// Options configures the ignition-payload-controller.
type Options struct {
	WorkDir                string
	Platform               string
	FeatureGateManifest    string
	RegistryOverrides      map[string]string
	IgnitionServerEndpoint string
	TokenTTL               time.Duration
}

// NewStartCommand returns the cobra command that runs the leader-elected ignition payload generator
// controller. It is registered as a subcommand of the HyperShift Operator binary.
func NewStartCommand() *cobra.Command {
	opts := Options{
		WorkDir:  "/payloads",
		TokenTTL: 2 * time.Hour,
	}

	cmd := &cobra.Command{
		Use:   "ignition-payload-controller",
		Short: "Leader-elected controller that generates ignition payloads for IgnitionPayload resources",
		Run: func(cmd *cobra.Command, args []string) {
			if err := run(ctrl.SetupSignalHandler(), opts); err != nil {
				setupLog.Error(err, "unable to start manager")
				os.Exit(1)
			}
		},
	}

	cmd.Flags().StringVar(&opts.WorkDir, "work-dir", opts.WorkDir, "Directory for extracted release-image files and rendering scratch space")
	cmd.Flags().StringVar(&opts.Platform, "platform", opts.Platform, "The cloud platform of the hosted cluster")
	cmd.Flags().StringVar(&opts.FeatureGateManifest, "feature-gate-manifest", opts.FeatureGateManifest, "Path to the rendered feature gate manifest")
	cmd.Flags().StringToStringVar(&opts.RegistryOverrides, "registry-overrides", opts.RegistryOverrides, "Registry override mappings applied when resolving release images")
	cmd.Flags().StringVar(&opts.IgnitionServerEndpoint, "ignition-server-endpoint", opts.IgnitionServerEndpoint, "Host[:port] booting nodes fetch ignition from, embedded in authored userData")
	cmd.Flags().DurationVar(&opts.TokenTTL, "token-ttl", opts.TokenTTL, "Validity of a minted ignition bearer token; re-issued at half this interval")

	return cmd
}

func run(ctx context.Context, opts Options) error {
	namespace := os.Getenv(namespaceEnvVariableName)
	if namespace == "" {
		return fmt.Errorf("environment variable %s is empty, this is not supported", namespaceEnvVariableName)
	}

	setupLog.Info("Starting ignition-payload-controller", "namespace", namespace, "workDir", opts.WorkDir)

	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = "ignition-payload-controller"

	// A direct (uncached) client for startup reads: the manager cache is not started yet.
	startupClient, err := client.New(restConfig, client.Options{Scheme: hyperapi.Scheme})
	if err != nil {
		return fmt.Errorf("unable to build startup client: %w", err)
	}

	minter, err := loadTokenMinter(ctx, startupClient, namespace)
	if err != nil {
		return err
	}
	caCert, err := loadIgnitionCACert(ctx, startupClient, namespace)
	if err != nil {
		return err
	}

	leaseDuration := time.Second * 60
	renewDeadline := time.Second * 40
	retryPeriod := time.Second * 15

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                        hyperapi.Scheme,
		LeaderElection:                true,
		LeaderElectionID:              "ignition-payload-controller-leader-elect",
		LeaderElectionResourceLock:    "leases",
		LeaderElectionNamespace:       namespace,
		LeaderElectionReleaseOnCancel: true,
		LeaseDuration:                 &leaseDuration,
		RenewDeadline:                 &renewDeadline,
		RetryPeriod:                   &retryPeriod,
		Cache: cache.Options{
			DefaultNamespaces: map[string]cache.Config{namespace: {}},
		},
	})
	if err != nil {
		return fmt.Errorf("unable to start manager: %w", err)
	}

	imageFileCache, err := ignserver.NewImageFileCache(opts.WorkDir)
	if err != nil {
		return fmt.Errorf("unable to create image file cache: %w", err)
	}

	imageMetadataProvider := &imageregistry.RegistryClientImageMetadataProvider{
		OpenShiftImageRegistryOverrides: imageregistry.ConvertImageRegistryOverrideStringToMap(os.Getenv("OPENSHIFT_IMG_OVERRIDES")),
	}

	provider := &ignserver.LocalIgnitionProvider{
		ReleaseProvider: &releaseinfo.ProviderWithOpenShiftImageRegistryOverridesDecorator{
			Delegate: &releaseinfo.RegistryMirrorProviderDecorator{
				Delegate: &releaseinfo.CachedProvider{
					Inner: &releaseinfo.RegistryClientProvider{},
					Cache: map[string]*releaseinfo.ReleaseImage{},
				},
				RegistryOverrides: opts.RegistryOverrides,
			},
			OpenShiftImageRegistryOverrides: imageregistry.ConvertImageRegistryOverrideStringToMap(os.Getenv("OPENSHIFT_IMG_OVERRIDES")),
		},
		Client:                mgr.GetClient(),
		Namespace:             namespace,
		CloudProvider:         hyperv1.PlatformType(opts.Platform),
		WorkDir:               opts.WorkDir,
		ImageFileCache:        imageFileCache,
		FeatureGateManifest:   opts.FeatureGateManifest,
		ImageMetadataProvider: imageMetadataProvider,
	}

	store, err := payloadstore.NewSecretBackedStore(restConfig, namespace)
	if err != nil {
		return fmt.Errorf("unable to build payload store: %w", err)
	}

	r := ignitionpayload.NewReconciler(ignitionpayload.ReconcilerConfig{
		Client:                 mgr.GetClient(),
		Store:                  store,
		Provider:               provider,
		ReleaseProvider:        provider.ReleaseProvider,
		Minter:                 minter,
		Namespace:              namespace,
		IgnitionCACert:         caCert,
		IgnitionServerEndpoint: opts.IgnitionServerEndpoint,
		TokenTTL:               opts.TokenTTL,
	})
	if err := r.SetupWithManager(mgr); err != nil {
		return fmt.Errorf("unable to set up ignition payload controller: %w", err)
	}

	return mgr.Start(ctx)
}

// loadTokenMinter reads the ECDSA token-signing key from its Secret and builds a JWT minter.
func loadTokenMinter(ctx context.Context, c client.Client, namespace string) (ignitionpayload.TokenMinter, error) {
	secret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: tokenSigningKeySecretName}, secret); err != nil {
		return nil, fmt.Errorf("unable to read token-signing key Secret %s/%s: %w", namespace, tokenSigningKeySecretName, err)
	}
	key, err := ignitionpayload.LoadECDSAPrivateKey(secret.Data[tokenSigningKeySecretKey])
	if err != nil {
		return nil, err
	}
	return ignitionpayload.NewJWTMinter(key), nil
}

// loadIgnitionCACert reads the node-facing CA embedded in authored userData.
func loadIgnitionCACert(ctx context.Context, c client.Client, namespace string) ([]byte, error) {
	caSecret := ignitionserver.IgnitionCACertSecret(namespace)
	if err := c.Get(ctx, client.ObjectKeyFromObject(caSecret), caSecret); err != nil {
		return nil, fmt.Errorf("unable to read ignition CA Secret: %w", err)
	}
	caCert, ok := caSecret.Data[corev1.TLSCertKey]
	if !ok {
		return nil, fmt.Errorf("ignition CA Secret %s is missing the %s key", caSecret.Name, corev1.TLSCertKey)
	}
	return caCert, nil
}
