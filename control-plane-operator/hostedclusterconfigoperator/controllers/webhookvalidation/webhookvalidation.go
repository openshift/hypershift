package webhookvalidation

import (
	"context"
	"errors"
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// webhookType encodes which kind of admission webhook triggered reconciliation.
// It carries the type-specific logic (object construction, URL extraction) so
// callers never need to switch on the kind.
type webhookType struct {
	name     string
	newObj   func() client.Object
	newList  func() client.ObjectList
	getNames func(client.ObjectList) []string
	getURLs  func(client.Object) []*string
}

var (
	validatingType = webhookType{
		name:    "validating",
		newObj:  func() client.Object { return &admissionregistrationv1.ValidatingWebhookConfiguration{} },
		newList: func() client.ObjectList { return &admissionregistrationv1.ValidatingWebhookConfigurationList{} },
		getNames: func(list client.ObjectList) []string {
			items := list.(*admissionregistrationv1.ValidatingWebhookConfigurationList).Items
			names := make([]string, len(items))
			for i := range items {
				names[i] = items[i].Name
			}
			return names
		},
		getURLs: func(obj client.Object) []*string {
			wh := obj.(*admissionregistrationv1.ValidatingWebhookConfiguration)
			urls := make([]*string, 0, len(wh.Webhooks))
			for i := range wh.Webhooks {
				urls = append(urls, wh.Webhooks[i].ClientConfig.URL)
			}
			return urls
		},
	}
	mutatingType = webhookType{
		name:    "mutating",
		newObj:  func() client.Object { return &admissionregistrationv1.MutatingWebhookConfiguration{} },
		newList: func() client.ObjectList { return &admissionregistrationv1.MutatingWebhookConfigurationList{} },
		getNames: func(list client.ObjectList) []string {
			items := list.(*admissionregistrationv1.MutatingWebhookConfigurationList).Items
			names := make([]string, len(items))
			for i := range items {
				names[i] = items[i].Name
			}
			return names
		},
		getURLs: func(obj client.Object) []*string {
			wh := obj.(*admissionregistrationv1.MutatingWebhookConfiguration)
			urls := make([]*string, 0, len(wh.Webhooks))
			for i := range wh.Webhooks {
				urls = append(urls, wh.Webhooks[i].ClientConfig.URL)
			}
			return urls
		},
	}
	webhookTypesByName = map[string]webhookType{
		validatingType.name: validatingType,
		mutatingType.name:   mutatingType,
	}
)

type reconciler struct {
	client       client.Client
	cpClient     client.Reader
	hcpNamespace string
}

func (r *reconciler) Reconcile(ctx context.Context, req reconcile.Request) (ctrl.Result, error) {
	wt, ok := webhookTypesByName[req.Namespace]
	if !ok {
		return ctrl.Result{}, nil
	}

	disallowedURLs, err := r.buildDisallowedURLs(ctx)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to build disallowed URLs: %w", err)
	}

	// Sentinel request from a CP Service event: list all configs of this type
	// so that list errors are surfaced and retried by the work queue.
	if req.Name == serviceEventKey {
		return ctrl.Result{}, r.reconcileAllWebhooks(ctx, disallowedURLs, wt)
	}

	return ctrl.Result{}, r.reconcileWebhook(ctx, req.Name, disallowedURLs, wt)
}

func (r *reconciler) reconcileAllWebhooks(ctx context.Context, disallowedURLs []string, wt webhookType) error {
	list := wt.newList()
	if err := r.client.List(ctx, list); err != nil {
		return fmt.Errorf("failed to list %s webhooks: %w", wt.name, err)
	}
	var errs []error
	for _, name := range wt.getNames(list) {
		if err := r.reconcileWebhook(ctx, name, disallowedURLs, wt); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (r *reconciler) reconcileWebhook(ctx context.Context, name string, disallowedURLs []string, wt webhookType) error {
	log := ctrl.LoggerFrom(ctx)
	obj := wt.newObj()
	if err := r.client.Get(ctx, client.ObjectKey{Name: name}, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get %s webhook: %w", wt.name, err)
	}

	for _, url := range wt.getURLs(obj) {
		if url != nil && !isAllowedWebhookURL(disallowedURLs, *url) {
			log.Info("deleting webhook configuration with a disallowed url", "type", wt.name, "webhook_name", name, "disallowed_url", *url)
			if err := r.client.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
				return fmt.Errorf("failed to delete %s webhook %s: %w", wt.name, name, err)
			}
			return nil
		}
	}
	return nil
}

func (r *reconciler) buildDisallowedURLs(ctx context.Context) ([]string, error) {
	cpServices := &corev1.ServiceList{}
	if err := r.cpClient.List(ctx, cpServices, client.InNamespace(r.hcpNamespace)); err != nil {
		return nil, fmt.Errorf("failed to list control plane services: %w", err)
	}

	var disallowedURLs []string
	for _, svc := range cpServices.Items {
		if _, exist := svc.Labels[hyperv1.AllowGuestWebhooksServiceLabel]; exist {
			continue
		}
		disallowedURLs = append(disallowedURLs, fmt.Sprintf("https://%s", svc.Name))
		disallowedURLs = append(disallowedURLs, fmt.Sprintf("https://%s.%s.svc", svc.Name, svc.Namespace))
		disallowedURLs = append(disallowedURLs, fmt.Sprintf("https://%s.%s.svc.cluster.local", svc.Name, svc.Namespace))
	}

	return disallowedURLs, nil
}

func isAllowedWebhookURL(disallowedURLs []string, url string) bool {
	for i := range disallowedURLs {
		if strings.Contains(url, disallowedURLs[i]) {
			return false
		}
	}
	return true
}
