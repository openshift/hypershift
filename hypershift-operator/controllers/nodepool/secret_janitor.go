package nodepool

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/blang/semver"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportutil "github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// secretJanitor reconciles secrets and determines which secrets should remain in the cluster and which should be cleaned up.
// Any secret annotated with a nodePool name should only be on the cluster if the nodePool continues to exist
// and if our current calculation for the inputs to the name matches what the secret is named.
type secretJanitor struct {
	*NodePoolReconciler

	now func() time.Time
}

func (r *secretJanitor) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx).WithValues("secret", req.String())

	secret := &corev1.Secret{}
	if err := r.Client.Get(ctx, req.NamespacedName, secret); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("not found", "request", req.String())
			return ctrl.Result{}, nil
		}
		log.Error(err, "error getting secret")
		return ctrl.Result{}, err
	}

	// only handle secrets that are associated with a NodePool
	nodePoolName, annotated := secret.Annotations[nodePoolAnnotation]
	if !annotated {
		return ctrl.Result{}, nil
	}
	log = log.WithValues("nodePool", nodePoolName)

	// only handle secret types that we know about explicitly
	shouldHandle := false
	for _, prefix := range []string{TokenSecretPrefix, UserDataSecrePrefix} {
		if strings.HasPrefix(secret.Name, prefix) {
			shouldHandle = true
			break
		}
	}
	if !shouldHandle {
		return ctrl.Result{}, nil
	}

	nodePool := &hyperv1.NodePool{}
	if err := r.Client.Get(ctx, supportutil.ParseNamespacedName(nodePoolName), nodePool); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "error getting nodepool")
		return ctrl.Result{}, err
	} else if apierrors.IsNotFound(err) {
		log.Info("removing secret as nodePool is missing")
		return ctrl.Result{}, r.Client.Delete(ctx, secret)
	}

	hcluster, err := GetHostedClusterByName(ctx, r.Client, nodePool.GetNamespace(), nodePool.Spec.ClusterName)
	if err != nil {
		return ctrl.Result{}, err
	}

	shouldKeepOldUserData, err := r.shouldKeepOldUserData(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, err
	}
	if shouldKeepOldUserData {
		log.V(3).Info("Skipping secretJanitor reconciliation and keeping old user data secret")
		return ctrl.Result{}, nil
	}

	releaseImage, err := r.getReleaseImage(ctx, hcluster, nodePool.Status.Version, nodePool.Spec.Release.Image)
	if err != nil {
		return ctrl.Result{}, err
	}
	haproxyRawConfig, err := r.generateHAProxyRawConfig(ctx, hcluster, releaseImage)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to generate HAProxy raw config: %w", err)
	}

	pullSecret, err := r.getPullSecret(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get pull secret: %w", err)
	}

	configGenerator, err := NewConfigGenerator(ctx, r.Client, hcluster, nodePool, releaseImage, haproxyRawConfig, pullSecret)
	if err != nil {
		return ctrl.Result{}, err
	}
	cpoCapabilities, err := r.detectCPOCapabilities(ctx, hcluster)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to detect CPO capabilities: %w", err)
	}
	token, err := NewToken(ctx, configGenerator, cpoCapabilities)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to create token: %w", err)
	}

	// synchronously deleting the ignition token is unsafe; we need to clean up tokens by annotating them to expire
	synchronousCleanup := func(ctx context.Context, c client.Client, secret *corev1.Secret) error {
		return c.Delete(ctx, secret)
	}
	type nodePoolSecret struct {
		expectedName   string
		matchingPrefix string
		cleanup        func(context.Context, client.Client, *corev1.Secret) error
	}
	valid := false
	options := []nodePoolSecret{
		{
			expectedName:   token.TokenSecret().GetName(),
			matchingPrefix: TokenSecretPrefix,
			cleanup: func(ctx context.Context, c client.Client, secret *corev1.Secret) error {
				return setExpirationTimestampOnToken(ctx, c, secret, r.now)
			},
		},
		{
			expectedName:   token.UserDataSecret().GetName(),
			matchingPrefix: UserDataSecrePrefix,
			cleanup:        synchronousCleanup,
		},
	}
	cleanup := synchronousCleanup
	var names []string
	for _, option := range options {
		names = append(names, option.expectedName)
		if secret.Name == option.expectedName {
			valid = true
		}
		if strings.HasPrefix(secret.Name, option.matchingPrefix) {
			cleanup = option.cleanup
		}
	}

	if valid {
		return ctrl.Result{}, nil
	}

	log.WithValues("options", names, "valid", valid).Info("removing secret as it does not match the expected set of names")
	return ctrl.Result{}, cleanup(ctx, r.Client, secret)
}

// shouldKeepOldUserData determines if the old user data should be kept.
// For AWS < 4.16, we keep the old userdata Secret so old Machines during rolled out can be deleted.
// Otherwise, deletion fails because of https://github.com/kubernetes-sigs/cluster-api-provider-aws/pull/3805.
// TODO (alberto): Drop this check when support for old versions without the fix is not needed anymore.
func (r *NodePoolReconciler) shouldKeepOldUserData(ctx context.Context, hc *hyperv1.HostedCluster) (bool, error) {
	if hc.Spec.Platform.Type != hyperv1.AWSPlatform {
		return false, nil
	}

	// If there's a current version in status, be conservative and assume that one is the one running CAPA.
	releaseImage := hc.Spec.Release.Image
	if hc.Status.Version != nil {
		if len(hc.Status.Version.History) > 0 {
			releaseImage = hc.Status.Version.History[0].Image
		}
	}

	pullSecretBytes, err := r.getPullSecretBytes(ctx, hc)
	if err != nil {
		return true, fmt.Errorf("failed to get pull secret bytes: %w", err)
	}

	releaseInfo, err := r.ReleaseProvider.Lookup(ctx, releaseImage, pullSecretBytes)
	if err != nil {
		return true, fmt.Errorf("failed to lookup release image: %w", err)
	}
	hostedClusterVersion, err := semver.Parse(releaseInfo.Version())
	if err != nil {
		return true, err
	}

	if hostedClusterVersion.LT(semver.MustParse("4.16.0")) {
		return true, nil
	}

	return false, nil
}
