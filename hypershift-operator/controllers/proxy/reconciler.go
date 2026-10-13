package proxy

import (
	"context"
	"fmt"

	proxypkg "github.com/openshift/hypershift/support/proxy"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

func Setup(mgr manager.Manager, deploymentNamespace string, deploymentName string) error {

	// We do not want this controller to require leader election, as that slows things down drastically when there is a proxy
	// and if we have multiple instances running, they should all attempt to do the same change. This means we can not use
	// the builder and have to wrap the controller.
	c, err := controller.NewUnmanaged("proxy", mgr, controller.Options{
		Reconciler: &reconciler{
			client:              mgr.GetClient(),
			deploymentNamespace: deploymentNamespace,
			deploymentName:      deploymentName,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}
	if err := c.Watch(source.Kind(mgr.GetCache(), &configv1.Proxy{}, &handler.TypedEnqueueRequestForObject[*configv1.Proxy]{})); err != nil {
		return fmt.Errorf("failed to set up watch for %T: %w", &configv1.Proxy{}, err)
	}
	if err := mgr.Add(&noLeaderElectionRunnable{c}); err != nil {
		return fmt.Errorf("failed to add controller to mgr: %w", err)
	}
	return nil
}

var _ manager.LeaderElectionRunnable = &noLeaderElectionRunnable{}

type noLeaderElectionRunnable struct {
	manager.Runnable
}

func (*noLeaderElectionRunnable) NeedLeaderElection() bool {
	return false
}

type reconciler struct {
	client              crclient.Client
	deploymentNamespace string
	deploymentName      string
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	return reconcile.Result{}, r.reconcile(ctx, req)
}

const operatorContainerName = "operator"

func (r *reconciler) reconcile(ctx context.Context, req reconcile.Request) error {
	proxy := &configv1.Proxy{}
	if err := r.client.Get(ctx, req.NamespacedName, proxy); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to get proxy %s: %w", req, err)
	}

	// Golangs default proxyFunc caches the env var lookup, because it is apparently slow on some OSes like windows. Hence
	// we have to update the deployment.
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{
		Namespace: r.deploymentNamespace,
		Name:      r.deploymentName,
	}}
	if err := r.client.Get(ctx, crclient.ObjectKeyFromObject(deployment), deployment); err != nil {
		return fmt.Errorf("failed to get deployment %s: %w", crclient.ObjectKeyFromObject(deployment), err)
	}
	for idx := range deployment.Spec.Template.Spec.Containers {
		if deployment.Spec.Template.Spec.Containers[idx].Name == operatorContainerName {
			proxypkg.SetEnvVarsTo(&deployment.Spec.Template.Spec.Containers[idx].Env,
				proxy.Status.HTTPProxy,
				proxy.Status.HTTPSProxy,
				proxy.Status.NoProxy,
			)
			return r.client.Update(ctx, deployment)
		}
	}

	return fmt.Errorf("deployment %s doesn't have a container %s", crclient.ObjectKeyFromObject(deployment), operatorContainerName)
}
