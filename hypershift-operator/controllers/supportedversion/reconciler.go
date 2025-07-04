package supportedversion

import (
	"context"
	"encoding/json"
	"fmt"

	manifests "github.com/openshift/hypershift/hypershift-operator/controllers/manifests/supportedversion"
	"github.com/openshift/hypershift/pkg/version"
	"github.com/openshift/hypershift/support/supportedversion"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	ConfigMapVersionsKey      = "supported-versions"
	ConfigMapServerVersionKey = "server-version"
	supportedVersionsLabel    = "hypershift.openshift.io/supported-versions"
)

type Reconciler struct {
	client.Client
	upsert.CreateOrUpdateProvider
	namespace string
}

func New(c client.Client, createOrUpdateProvider upsert.CreateOrUpdateProvider, namespace string) *Reconciler {
	return &Reconciler{
		Client:                 c,
		CreateOrUpdateProvider: createOrUpdateProvider,
		namespace:              namespace,
	}
}
func (r *Reconciler) SetupWithManager(mgr manager.Manager) error {
	// A channel is used to generate an initial sync event.
	// Afterwards, the controller syncs on the ConfigMap.
	initialSync := make(chan event.GenericEvent)
	err := ctrl.NewControllerManagedBy(mgr).
		For(&corev1.ConfigMap{}, builder.WithPredicates(predicate.NewPredicateFuncs(r.selectSupportedVersionsConfigMap))).
		WatchesRawSource(source.Channel(initialSync, &handler.EnqueueRequestForObject{})).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to construct controller: %w", err)
	}
	go func() {
		initialSync <- event.GenericEvent{Object: manifests.ConfigMap(r.namespace)}
	}()
	return nil
}

type SupportedVersions struct {
	Versions []string `json:"versions"`
}

func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	return reconcile.Result{}, r.ensureSupportedVersionConfigMap(ctx)
}

func (r *Reconciler) ensureSupportedVersionConfigMap(ctx context.Context) error {
	cm := manifests.ConfigMap(r.namespace)
	if cm.Labels == nil {
		cm.Labels = map[string]string{}
	}
	cm.Labels[supportedVersionsLabel] = "true"
	if _, err := r.CreateOrUpdate(ctx, r, cm, func() error {
		content := &SupportedVersions{
			Versions: supportedversion.Supported(),
		}
		contentBytes, err := json.Marshal(content)
		if err != nil {
			return fmt.Errorf("cannot marshal content: %w", err)
		}
		if cm.Data == nil {
			cm.Data = map[string]string{}
		}
		cm.Data[ConfigMapVersionsKey] = string(contentBytes)
		cm.Data[ConfigMapServerVersionKey] = version.GetRevision()
		return nil
	}); err != nil {
		return fmt.Errorf("failed to update supported version configmap: %w", err)
	}
	return nil
}

func (r *Reconciler) selectSupportedVersionsConfigMap(obj client.Object) bool {
	return obj.GetNamespace() == r.namespace && obj.GetName() == manifests.ConfigMap(r.namespace).Name
}
