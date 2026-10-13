package labelenforcingclient

import (
	"context"

	"github.com/openshift/hypershift/support/upsert"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	CacheLabelSelectorKey   = "hypershift.openshift.io/managed"
	CacheLabelSelectorValue = "true"
)

func New(upstream client.Client, labels map[string]string) *LabelEnforcingClient {
	return &LabelEnforcingClient{
		Client: upstream,
		labels: labels,
	}
}

// LabelEnforcingClient enforces that its configured labels are set during a Create or Update call
type LabelEnforcingClient struct {
	client.Client
	labels map[string]string
}

func (l *LabelEnforcingClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	l.setLabels(obj)
	return l.Client.Create(ctx, obj, opts...)
}

func (l *LabelEnforcingClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	l.setLabels(obj)
	return l.Client.Update(ctx, obj, opts...)
}

func (l *LabelEnforcingClient) setLabels(obj client.Object) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	for k, v := range l.labels {
		labels[k] = v
	}
	obj.SetLabels(labels)
}

// LabelEnforcingUpsertProvider wraps an existing upsert provider who is called with a cache-backed client
// that has a labelselector configured. If someone unlabels an object, the upserprovider will always get
// a notfound when trying to Get is, because it is not in the cache. Thus it will subsequently try to Create
// it, which will fail with an IsAlreadyExists since it exists in the api. To deal with this, the LabelEnforcingUpsertProvider
// simply re-runs the upsert logic with an api reading client if an IsAlreadyExists happened.
type LabelEnforcingUpsertProvider struct {
	Upstream  upsert.CreateOrUpdateProvider
	APIClient client.Client
}

func (l *LabelEnforcingUpsertProvider) CreateOrUpdate(ctx context.Context, c client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	result, err := l.Upstream.CreateOrUpdate(ctx, c, obj, f)
	if err == nil || !apierrors.IsAlreadyExists(err) {
		return result, err
	}

	return l.Upstream.CreateOrUpdate(ctx, l.APIClient, obj, f)
}
