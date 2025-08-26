package kasbootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	configv1 "github.com/openshift/api/config/v1"

	rbacv1 "k8s.io/api/rbac/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	equality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	wait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/clientcmd"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"go.uber.org/zap/zapcore"
)

func init() {
	// Needed for the featureGate.
	utilruntime.Must(configv1.Install(configScheme))
	// Needed for the CRDs.
	utilruntime.Must(apiextensionsv1.AddToScheme(configScheme))
	// Needed for the hcco-rolebinding.
	utilruntime.Must(rbacv1.AddToScheme(configScheme))
}

var (
	configScheme = runtime.NewScheme()
	configCodecs = serializer.NewCodecFactory(configScheme)
)

func run(ctx context.Context, opts Options) error {
	logger := zap.New(zap.JSONEncoder(func(o *zapcore.EncoderConfig) {
		o.EncodeTime = zapcore.RFC3339TimeEncoder
	}))
	ctrl.SetLogger(logger)

	cfg, err := ctrl.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}
	c, err := client.New(cfg, client.Options{Scheme: configScheme})
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	// This binary is meant to run next to the KAS container within the same pod.
	// We briefly poll here to retry on race and transient network issues.
	// 50s is a high margin chosen here as in CI aws kms is observed to take up to 30s to start.
	// This to avoid unnecessary restarts of this container.
	if err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 50*time.Second, true,
		func(ctx context.Context) (done bool, err error) {
			if err := applyBootstrapResources(ctx, c, opts.ResourcesPath); err != nil {
				logger.Error(err, "failed to apply bootstrap resources, retrying")
				return false, nil
			}
			return true, nil
		}); err != nil {
		return fmt.Errorf("failed to apply bootstrap resources: %w", err)
	}

	content, err := os.ReadFile(filepath.Join(opts.ResourcesPath, "99_feature-gate.yaml"))
	if err != nil {
		return fmt.Errorf("failed to read featureGate file: %w", err)
	}

	renderedFeatureGate, err := parseFeatureGateV1(content)
	if err != nil {
		return fmt.Errorf("failed to parse featureGate file: %w", err)
	}

	hccoCFG, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG_HCCO"))
	if err != nil {
		return fmt.Errorf("failed to get HCCO config: %w", err)
	}
	hccoClient, err := client.New(hccoCFG, client.Options{Scheme: configScheme})
	if err != nil {
		return fmt.Errorf("failed to create client with HCCO kubeconfig: %w", err)
	}

	if err := wait.PollUntilContextTimeout(ctx, 500*time.Millisecond, 50*time.Second, true,
		func(ctx context.Context) (done bool, err error) {
			if err := reconcileFeatureGate(ctx, hccoClient, renderedFeatureGate); err != nil {
				logger.Error(err, "failed to reconcile featureGate, retrying")
				return false, nil
			}
			return true, nil
		}); err != nil {
		return fmt.Errorf("failed to reconcile featureGate: %w", err)
	}

	// we want to keep the process running during the lifecycle of the Pod because the Pod runs with restartPolicy=Always
	// and it's not possible for individual containers to have a dedicated restartPolicy like onFailure.

	// start a goroutine that will close the done channel when the context is done.
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(done)
	}()

	logger.Info("kas-bootstrap process completed successfully, waiting for termination signal")
	<-done

	return nil
}

// reconcileFeatureGate reconciles the featureGate CR status appending the renderedFeatureGate status.featureGates to the existing featureGates.
// It will not fail if the clusterVersion is not found as this is expected for a brand new cluster.
// But it will remove any featureGates that are not in the clusterVersion.Status.History if it exists.
func reconcileFeatureGate(ctx context.Context, c client.Client, renderedFeatureGate *configv1.FeatureGate) error {
	logger := ctrl.LoggerFrom(ctx).WithName("kas-bootstrap")

	knownVersions := sets.NewString()
	var clusterVersion configv1.ClusterVersion
	err := c.Get(ctx, client.ObjectKey{Name: "version"}, &clusterVersion)
	if err != nil {
		// we don't fail if we can't get the clusterVersion, we will just not update the featureGate.
		// This is always the case for a brand new cluster as the clusterVersion is not created yet.
		logger.Info("WARNING: failed to get clusterVersion. This is expected for a brand new cluster", "error", err)
	} else {
		knownVersions = sets.NewString(clusterVersion.Status.Desired.Version)
		for _, cvoVersion := range clusterVersion.Status.History {
			knownVersions.Insert(cvoVersion.Version)

			// Once we hit the first Completed entry and insert that into knownVersions
			// we can break, because there shouldn't be anything left on the cluster that cares about those ancient releases anymore.
			if cvoVersion.State == configv1.CompletedUpdate {
				break
			}
		}
	}

	var featureGate configv1.FeatureGate
	if err := c.Get(ctx, client.ObjectKey{Name: "cluster"}, &featureGate); err != nil {
		return fmt.Errorf("failed to get featureGate: %w", err)
	}

	desiredFeatureGates := renderedFeatureGate.Status.FeatureGates
	currentVersion := renderedFeatureGate.Status.FeatureGates[0].Version
	for i := range featureGate.Status.FeatureGates {
		featureGateValues := featureGate.Status.FeatureGates[i]
		if featureGateValues.Version == currentVersion {
			continue
		}
		if len(knownVersions) > 0 && !knownVersions.Has(featureGateValues.Version) {
			continue
		}
		desiredFeatureGates = append(desiredFeatureGates, featureGateValues)
	}

	if equality.Semantic.DeepEqual(desiredFeatureGates, featureGate.Status.FeatureGates) {
		logger.Info("There is no update for featureGate.Status.FeatureGates")
		return nil
	}

	original := featureGate.DeepCopy()
	featureGate.Status.FeatureGates = desiredFeatureGates
	if err := c.Status().Patch(ctx, &featureGate, client.MergeFromWithOptions(original, client.MergeFromWithOptimisticLock{})); err != nil {
		return fmt.Errorf("failed to update featureGate: %w", err)
	}
	return nil
}

func parseFeatureGateV1(objBytes []byte) (*configv1.FeatureGate, error) {
	requiredObj, err := runtime.Decode(configCodecs.UniversalDecoder(configv1.SchemeGroupVersion), objBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to decode featureGate: %w", err)
	}

	return requiredObj.(*configv1.FeatureGate), nil
}

func applyBootstrapResources(ctx context.Context, c client.Client, filesPath string) error {
	logger := ctrl.LoggerFrom(ctx).WithName("kas-bootstrap")

	// Fail early if the specified path does not exist.
	if _, err := os.Stat(filesPath); err != nil {
		return fmt.Errorf("bootstrap resources path %q does not exist: %w", filesPath, err)
	}

	// Walk the filesPath and apply the resources.
	err := filepath.Walk(filesPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return fmt.Errorf("error accessing path %s: %w", path, err)
		}

		if info.IsDir() {
			logger.Info("Skipping dir", "path", path)
			return nil
		}

		if filepath.Ext(path) != ".yaml" {
			logger.Info("Skipping non-yaml file", "path", path)
			return nil
		}

		logger.Info("Processing file", "path", path)

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read file %s: %w", path, err)
		}

		obj, _, err := configCodecs.UniversalDeserializer().Decode(content, nil, nil)
		if err != nil {
			return fmt.Errorf("failed to decode file %s: %w", path, err)
		}

		if _, err = ctrl.CreateOrUpdate(ctx, c, obj.(client.Object), func() error {
			return nil
		}); err != nil {
			return fmt.Errorf("failed to createOrUpdate file %s: %w", path, err)
		}

		return nil
	})

	return err
}
