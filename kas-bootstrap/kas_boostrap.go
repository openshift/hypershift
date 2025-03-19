package kasbootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	configv1 "github.com/openshift/api/config/v1"

	equality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/sets"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"go.uber.org/zap/zapcore"
)

func init() {
	utilruntime.Must(configv1.Install(configScheme))
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

	content, err := os.ReadFile(filepath.Join(opts.RenderedFeatureGatePath, "99_feature-gate.yaml"))
	if err != nil {
		return fmt.Errorf("failed to read featureGate file: %w", err)
	}

	renderedFeatureGate, err := parseFeatureGateV1(content)
	if err != nil {
		return fmt.Errorf("failed to parse featureGate file: %w", err)
	}

	if err := reconcileFeatureGate(ctx, c, renderedFeatureGate); err != nil {
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
