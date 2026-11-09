package integration

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/controllers/resources/manifests"
	hccomanifests "github.com/openshift/hypershift/pkg/manifests/hcco"

	corev1 "k8s.io/api/core/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const testNamespace = "hcp-namespace"

// operationTrace records "<operation> <cluster> <type> <namespace>/<name>" for every client call.
type operationTrace struct {
	mu  sync.Mutex
	ops []string
}

func (trace *operationTrace) record(operation, cluster string, object client.Object, key client.ObjectKey) {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	trace.ops = append(trace.ops, fmt.Sprintf("%s %s %T %s/%s", operation, cluster, object, key.Namespace, key.Name))
}

func (trace *operationTrace) operations() []string {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return slices.Clone(trace.ops)
}

func (trace *operationTrace) cluster(cluster string) []string {
	var filtered []string
	for _, operation := range trace.operations() {
		if strings.Contains(operation, " "+cluster+" ") {
			filtered = append(filtered, operation)
		}
	}
	return filtered
}

func objectKey(object client.Object) string {
	return fmt.Sprintf("%T %s/%s", object, object.GetNamespace(), object.GetName())
}

// newTracedClient returns a fake client that records operations and fails Gets for getFailures keys.
func newTracedClient(trace *operationTrace, cluster string, getFailures map[string]error, objects ...client.Object) client.WithWatch {
	return fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(ctx context.Context, target client.WithWatch, key client.ObjectKey, object client.Object, opts ...client.GetOption) error {
			trace.record("get", cluster, object, key)
			if err := getFailures[fmt.Sprintf("%T %s/%s", object, key.Namespace, key.Name)]; err != nil {
				return err
			}
			return target.Get(ctx, key, object, opts...)
		},
		Create: func(ctx context.Context, target client.WithWatch, object client.Object, opts ...client.CreateOption) error {
			trace.record("create", cluster, object, client.ObjectKeyFromObject(object))
			return target.Create(ctx, object, opts...)
		},
		Update: func(ctx context.Context, target client.WithWatch, object client.Object, opts ...client.UpdateOption) error {
			trace.record("update", cluster, object, client.ObjectKeyFromObject(object))
			return target.Update(ctx, object, opts...)
		},
		Patch: func(ctx context.Context, target client.WithWatch, object client.Object, patch client.Patch, opts ...client.PatchOption) error {
			trace.record("patch", cluster, object, client.ObjectKeyFromObject(object))
			return target.Patch(ctx, object, patch, opts...)
		},
		Delete: func(ctx context.Context, target client.WithWatch, object client.Object, opts ...client.DeleteOption) error {
			trace.record("delete", cluster, object, client.ObjectKeyFromObject(object))
			return target.Delete(ctx, object, opts...)
		},
	}).Build()
}

// failingUpsert creates or updates objects, except for upsertFailures keys which fail before any call.
func failingUpsert(upsertFailures map[string]error) func(context.Context, client.Client, client.Object, controllerutil.MutateFn) (controllerutil.OperationResult, error) {
	return func(ctx context.Context, target client.Client, object client.Object, mutate controllerutil.MutateFn) (controllerutil.OperationResult, error) {
		if err := upsertFailures[objectKey(object)]; err != nil {
			return controllerutil.OperationResultNone, err
		}
		return controllerutil.CreateOrUpdate(ctx, target, object, mutate)
	}
}

type projectionFixture struct {
	controlPlane   []client.Object
	hosted         []client.Object
	cpGetFailures  map[string]error
	upsertFailures map[string]error
}

type projectionRun struct {
	hosted       HostedCluster
	controlPlane ControlPlane
	hostedClient client.WithWatch
	cpClient     client.WithWatch
	trace        *operationTrace
}

func newProjectionRun(fixture projectionFixture) projectionRun {
	trace := &operationTrace{}
	hostedClient := newTracedClient(trace, "hosted", nil, fixture.hosted...)
	cpClient := newTracedClient(trace, "control-plane", fixture.cpGetFailures, fixture.controlPlane...)
	upsert := failingUpsert(fixture.upsertFailures)
	return projectionRun{
		hosted:       NewHostedCluster(hostedClient, upsert),
		controlPlane: NewControlPlane(cpClient, upsert),
		hostedClient: hostedClient,
		cpClient:     cpClient,
		trace:        trace,
	}
}

func rootCASecret() *corev1.Secret {
	secret := cpomanifests.RootCASecret(testNamespace)
	secret.Data = map[string][]byte{"ca.crt": []byte("root-ca-bundle"), "ca.key": []byte("root-ca-key")}
	return secret
}

func controlPlaneService(service *corev1.Service, clusterIP string) *corev1.Service {
	service.Spec.ClusterIP = clusterIP
	return service
}

func openshiftAPIServerControlPlaneService(clusterIP string) *corev1.Service {
	return controlPlaneService(manifests.OpenShiftAPIServerService(testNamespace), clusterIP)
}

func oauthAPIServerControlPlaneService(clusterIP string) *corev1.Service {
	return controlPlaneService(manifests.OpenShiftOAuthAPIServerService(testNamespace), clusterIP)
}

func kubeadminPasswordSecret() *corev1.Secret {
	secret := hccomanifests.KubeadminPasswordSecret(testNamespace)
	secret.Data = map[string][]byte{"password": []byte("adminpass")}
	return secret
}

func oauthMasterCABundle() *corev1.ConfigMap {
	configMap := cpomanifests.OpenShiftOAuthMasterCABundle(testNamespace)
	configMap.Data = map[string]string{"ca.crt": "oauth-master-ca"}
	return configMap
}

// controlPlaneInputs returns every control-plane object the projections read, with distinct ClusterIPs.
func controlPlaneInputs() []client.Object {
	return []client.Object{
		rootCASecret(),
		openshiftAPIServerControlPlaneService("172.30.0.10"),
		oauthAPIServerControlPlaneService("172.30.0.11"),
		kubeadminPasswordSecret(),
		oauthMasterCABundle(),
	}
}

func without(objects []client.Object, removed ...client.Object) []client.Object {
	var kept []client.Object
	for _, object := range objects {
		if !slices.ContainsFunc(removed, func(candidate client.Object) bool { return objectKey(candidate) == objectKey(object) }) {
			kept = append(kept, object)
		}
	}
	return kept
}

func replacing(objects []client.Object, replacements ...client.Object) []client.Object {
	replaced := make([]client.Object, 0, len(objects))
	for _, object := range objects {
		if index := slices.IndexFunc(replacements, func(candidate client.Object) bool { return objectKey(candidate) == objectKey(object) }); index >= 0 {
			object = replacements[index]
		}
		replaced = append(replaced, object)
	}
	return replaced
}

func enabledParams() Params {
	return Params{Namespace: testNamespace, OAuthEnabled: true, OAuthAddress: "oauth.example.com", OAuthPort: 443}
}

func disabledParams() Params {
	params := enabledParams()
	params.OAuthEnabled = false
	return params
}

func errorMessages(errs []error) []string {
	messages := make([]string, 0, len(errs))
	for _, err := range errs {
		messages = append(messages, err.Error())
	}
	return messages
}
