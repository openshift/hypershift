// Package testtrace provides shared test support for the HCCO resources controller suites:
// an operation recorder, fake clients whose five client interceptors record every call,
// and a failure-injecting upserter. It deliberately holds no fixtures; each suite keeps its own.
package testtrace

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

// Recorder records "<operation> <cluster> <type> <namespace>/<name>" for every client call made
// through clients built with NewClient. It is safe for concurrent use.
type Recorder struct {
	mu  sync.Mutex
	ops []string
}

func (recorder *Recorder) record(operation, cluster string, object client.Object, key client.ObjectKey) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.ops = append(recorder.ops, fmt.Sprintf("%s %s %T %s/%s", operation, cluster, object, key.Namespace, key.Name))
}

// Operations returns a copy of every recorded operation, in call order.
func (recorder *Recorder) Operations() []string {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return slices.Clone(recorder.ops)
}

// Cluster returns the recorded operations made against the named cluster, in call order.
func (recorder *Recorder) Cluster(cluster string) []string {
	var filtered []string
	for _, operation := range recorder.Operations() {
		if strings.Contains(operation, " "+cluster+" ") {
			filtered = append(filtered, operation)
		}
	}
	return filtered
}

// ObjectKey returns the "<type> <namespace>/<name>" key used for recorded operations,
// get failures and upsert failures.
func ObjectKey(object client.Object) string {
	return fmt.Sprintf("%T %s/%s", object, object.GetNamespace(), object.GetName())
}

// ClientOptions configures a traced fake client.
type ClientOptions struct {
	// Objects seed the fake client.
	Objects []client.Object
	// GetFailures maps ObjectKey-style keys to errors returned by Get instead of reading.
	GetFailures map[string]error
	// StatusSubresources registers status subresources with the fake client.
	StatusSubresources []client.Object
}

// NewClient returns a fake client using the HCCO scheme whose Get, Create, Update, Patch and
// Delete calls are recorded under cluster before being served or failed.
func NewClient(recorder *Recorder, cluster string, options ClientOptions) client.WithWatch {
	return fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(options.Objects...).WithStatusSubresource(options.StatusSubresources...).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, target client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption) error {
			recorder.record("get", cluster, object, key)
			if err := options.GetFailures[fmt.Sprintf("%T %s/%s", object, key.Namespace, key.Name)]; err != nil {
				return err
			}
			return target.Get(ctx, key, object, opts...)
		},
		Create: func(ctx context.Context, target client.WithWatch, object client.Object, opts ...client.CreateOption) error {
			recorder.record("create", cluster, object, client.ObjectKeyFromObject(object))
			return target.Create(ctx, object, opts...)
		},
		Update: func(ctx context.Context, target client.WithWatch, object client.Object, opts ...client.UpdateOption) error {
			recorder.record("update", cluster, object, client.ObjectKeyFromObject(object))
			return target.Update(ctx, object, opts...)
		},
		Patch: func(ctx context.Context, target client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption) error {
			recorder.record("patch", cluster, object, client.ObjectKeyFromObject(object))
			return target.Patch(ctx, object, patch, opts...)
		},
		Delete: func(ctx context.Context, target client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
			recorder.record("delete", cluster, object, client.ObjectKeyFromObject(object))
			return target.Delete(ctx, object, opts...)
		},
	}).Build()
}

// FailingUpserter is an upsert provider that creates or updates objects, except for objects whose
// ObjectKey is in Failures, which fail before any client call.
type FailingUpserter struct {
	Failures map[string]error
}

// CreateOrUpdate implements upsert.CreateOrUpdateProvider; use the method value as an upsert.CreateOrUpdateFN.
func (upserter FailingUpserter) CreateOrUpdate(ctx context.Context, target client.Client, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	if err := upserter.Failures[ObjectKey(object)]; err != nil {
		return controllerutil.OperationResultNone, err
	}
	return controllerutil.CreateOrUpdate(ctx, target, object, mutate)
}
