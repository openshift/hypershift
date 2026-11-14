package integration

import (
	"context"
	"fmt"

	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/oauth"
	hccomanifests "github.com/openshift/hypershift/pkg/manifests/hcco"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reconcileOAuthProjections preserves the original root order, log lines and error wrappers.
// Nothing is projected, and nothing is read from the control plane, when OAuth is disabled.
func reconcileOAuthProjections(ctx context.Context, hosted HostedCluster, controlPlane ControlPlane, params Params) []error {
	if !params.OAuthEnabled {
		return nil
	}
	log := ctrl.LoggerFrom(ctx)
	var errs []error

	log.Info("reconciling oauth serving cert ca bundle")
	if err := reconcileOAuthServingCertCABundle(ctx, hosted, controlPlane, params.Namespace); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile oauth serving cert CA bundle: %w", err))
	}

	log.Info("reconciling oauth browser client")
	oauthBrowserClient := manifests.OAuthServerBrowserClient()
	if _, err := hosted.upsert(ctx, oauthBrowserClient, func() error {
		return oauth.ReconcileBrowserClient(oauthBrowserClient, params.OAuthAddress, params.OAuthPort)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile oauth browser client: %w", err))
	}

	log.Info("reconciling oauth challenging client")
	oauthChallengingClient := hccomanifests.OAuthServerChallengingClient()
	if _, err := hosted.upsert(ctx, oauthChallengingClient, func() error {
		return oauth.ReconcileChallengingClient(oauthChallengingClient, params.OAuthAddress, params.OAuthPort)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile oauth challenging client: %w", err))
	}

	log.Info("reconciling oauth cli client")
	oauthCLIClient := manifests.OAuthServerCLIClient()
	if _, err := hosted.upsert(ctx, oauthCLIClient, func() error {
		return oauth.ReconcileCLIClient(oauthCLIClient, params.OAuthAddress, params.OAuthPort)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile oauth cli client: %w", err))
	}

	log.Info("reconciling oauth serving cert rbac")
	oauthServingCertRole := manifests.OAuthServingCertRole()
	if _, err := hosted.upsert(ctx, oauthServingCertRole, func() error {
		return oauth.ReconcileOauthServingCertRole(oauthServingCertRole)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile oauth serving cert role: %w", err))
	}

	oauthServingCertRoleBinding := manifests.OAuthServingCertRoleBinding()
	if _, err := hosted.upsert(ctx, oauthServingCertRoleBinding, func() error {
		return oauth.ReconcileOauthServingCertRoleBinding(oauthServingCertRoleBinding)
	}); err != nil {
		errs = append(errs, fmt.Errorf("failed to reconcile oauth serving cert rolebinding: %w", err))
	}

	return errs
}

// reconcileOAuthServingCertCABundle projects the control-plane OAuth master CA into the hosted
// oauth-serving-cert ConfigMap so hosted consumers trust the OAuth server.
func reconcileOAuthServingCertCABundle(ctx context.Context, hosted HostedCluster, controlPlane ControlPlane, namespace string) error {
	sourceBundle := cpomanifests.OpenShiftOAuthMasterCABundle(namespace)
	if err := controlPlane.get(ctx, client.ObjectKeyFromObject(sourceBundle), sourceBundle); err != nil {
		return fmt.Errorf("cannot get oauth master ca bundle: %w", err)
	}
	caBundle := manifests.OAuthCABundle()
	if _, err := hosted.upsert(ctx, caBundle, func() error {
		return oauth.ReconcileOAuthServerCertCABundle(caBundle, sourceBundle)
	}); err != nil {
		return fmt.Errorf("failed to reconcile oauth server cert ca bundle: %w", err)
	}
	return nil
}
