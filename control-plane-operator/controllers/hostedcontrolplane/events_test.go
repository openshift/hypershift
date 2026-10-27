package hostedcontrolplane

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/awsapi"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	"github.com/aws/aws-sdk-go-v2/service/ec2"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"go.uber.org/mock/gomock"
)

type capturedWarning struct {
	record.EventRecorder
	objects  []*hyperv1.HostedControlPlane
	messages []string
}

func (recorder *capturedWarning) Event(object runtime.Object, eventType, reason, message string) {
	if eventType != corev1.EventTypeWarning || reason != reconciliationFailedReason {
		panic("unexpected reconciliation event")
	}
	recorder.objects = append(recorder.objects, object.(*hyperv1.HostedControlPlane).DeepCopy())
	recorder.messages = append(recorder.messages, message)
}

func TestReconcile(t *testing.T) {
	for _, testCase := range []struct {
		name string
		mode string
	}{
		{name: "When ignition creation exceeds quota, it should report and recover without changing healthy workloads", mode: "quota"},
		{name: "When a healthy control plane hits quota, it should retain availability and recover with the normal ready result", mode: "healthy quota"},
		{name: "When a healthy update succeeds, it should retain the normal ready requeue without failure events", mode: "healthy"},
		{name: "When a mixed nested aggregate fails, it should retain the original identity", mode: "aggregate"},
		{name: "When an existing ignition ConfigMap update conflicts, it should retry without a warning and recover", mode: "update"},
		{name: "When a wrapped conflict occurs, it should retry without a warning", mode: "wrapped conflict"},
		{name: "When aggregate errors all conflict, it should retry without a warning", mode: "aggregate conflict"},
		{name: "When joined errors all conflict, it should retry without a warning", mode: "joined conflict"},
		{name: "When nested errors all conflict, it should retry without a warning", mode: "nested conflict"},
		{name: "When nested errors mix conflicts and PKI failures, it should warn and retain diagnostics", mode: "mixed conflict"},
		{name: "When no recorder is configured, it should retain the original error", mode: "nil recorder"},
		{name: "When the recorder is stopped, it should retain the original error", mode: "stopped recorder"},
		{name: "When reconciliation is paused, it should wait without a failure outcome", mode: "paused"},
		{name: "When reconciliation has a timed pause, it should preserve the remaining requeue duration", mode: "timed pause"},
		{name: "When the release mismatches, it should skip without a failure outcome", mode: "release"},
		{name: "When configuration is invalid, it should skip without a failure outcome", mode: "invalid"},
		{name: "When HCP is deleting, it should retain deletion semantics without a failure outcome", mode: "deleting"},
		{name: "When infrastructure is waiting, it should retain a nil-error requeue", mode: "waiting"},
		{name: "When ignition is disabled, it should skip ignition without a failure outcome", mode: "disabled"},
		{name: "When HCP does not exist, it should return without a failure outcome", mode: "missing"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newReconcileEventFixture(t, testCase.mode)
			fixture.configure(t)
			fixture.prepareSnapshots(t)
			result, err := fixture.reconciler.Reconcile(t.Context(), reconcile.Request{NamespacedName: fixture.key})
			switch testCase.mode {
			case "quota", "healthy quota", "aggregate", "update", "wrapped conflict", "aggregate conflict", "joined conflict", "nested conflict", "mixed conflict", "nil recorder", "stopped recorder":
				fixture.assertFailure(t, result, err)
				fixture.assertUnchanged(t)
				fixture.assertRecovery(t)
				fixture.assertUnchanged(t)
			default:
				fixture.assertGatedResult(t, result, err)
			}
			fixture.assertPreservedState(t)
		})
	}
}

type reconcileEventFixture struct {
	mode            string
	hcp             *hyperv1.HostedControlPlane
	workload        *appsv1.Deployment
	originalFailure error
	blocked         bool
	healthyObjects  []client.Object
	baseClient      client.Client
	reconciler      *HostedControlPlaneReconciler
	recorder        *capturedWarning
	key             client.ObjectKey
	snapshots       []client.Object
}

func newReconcileEventFixture(t *testing.T, mode string) *reconcileEventFixture {
	t.Helper()
	fixture := &reconcileEventFixture{mode: mode, blocked: true}
	hcp := sampleHCP(t)
	hcp.Status.Conditions = append(hcp.Status.Conditions, metav1.Condition{Type: "UnrelatedCondition", Status: metav1.ConditionTrue, Reason: "Unchanged", LastTransitionTime: metav1.Now()})
	workload := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "healthy-workload", Namespace: hcp.Namespace}, Status: appsv1.DeploymentStatus{AvailableReplicas: 1, ReadyReplicas: 1}}
	quotaError := apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, "ignition-config-fips", errors.New("exceeded quota: configmaps, requested: count/configmaps=1, used: count/configmaps=0, limited: count/configmaps=0"))
	var failure error = quotaError
	var originalFailure error = quotaError
	if mode == "update" {
		originalFailure = apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, "ignition-config-fips", errors.New("synthetic-sensitive-update-detail"))
		failure = originalFailure
	}
	if mode == "aggregate" {
		failure = fmt.Errorf("outer: %w", utilerrors.NewAggregate([]error{errors.New("synthetic-sensitive-provider-data"), fmt.Errorf("inner: %w", utilerrors.NewAggregate([]error{quotaError}))}))
	}
	conflict := apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, "ignition-config-fips", errors.New("synthetic concurrent update"))
	switch mode {
	case "wrapped conflict":
		failure = fmt.Errorf("update: %w", conflict)
	case "aggregate conflict":
		failure = utilerrors.NewAggregate([]error{conflict, fmt.Errorf("update: %w", conflict)})
	case "joined conflict":
		failure = errors.Join(conflict, fmt.Errorf("update: %w", conflict))
	case "nested conflict":
		failure = fmt.Errorf("outer: %w", utilerrors.NewAggregate([]error{errors.Join(conflict, fmt.Errorf("inner: %w", conflict))}))
	case "mixed conflict":
		failure = fmt.Errorf("outer: %w", errors.Join(conflict, utilerrors.NewAggregate([]error{errors.New("failed to reconcile PKI synthetic-cert"), conflict})))
	}
	if strings.HasSuffix(mode, " conflict") {
		originalFailure = conflict
	}
	var healthyObjects []client.Object
	if mode == "healthy quota" || mode == "healthy" {
		hcp.Spec.Services[0].Type = hyperv1.NodePort
		hcp.Status.KubeConfig = &hyperv1.KubeconfigSecretRef{Name: manifests.KASAdminKubeconfigSecret(hcp.Namespace, hcp.Spec.KubeConfig).Name, Key: DefaultAdminKubeconfigKey}
		hcp.Status.Conditions = append(hcp.Status.Conditions, metav1.Condition{Type: string(hyperv1.HostedControlPlaneAvailable), Status: metav1.ConditionTrue, Reason: hyperv1.AsExpectedReason, LastTransitionTime: metav1.Now()})
		kasDeployment := manifests.KASDeployment(hcp.Namespace)
		kasDeployment.Status = appsv1.DeploymentStatus{AvailableReplicas: 1, ReadyReplicas: 1, Conditions: []appsv1.DeploymentCondition{{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue}}}
		healthyObjects = []client.Object{kasDeployment, &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "etcd", Namespace: hcp.Namespace}, Spec: appsv1.StatefulSetSpec{Replicas: ptr.To[int32](1)}, Status: appsv1.StatefulSetStatus{ReadyReplicas: 1}}, manifests.KASAdminKubeconfigSecret(hcp.Namespace, hcp.Spec.KubeConfig)}
		if mode == "healthy" {
			fixture.blocked = false
		}
	}
	clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hcp, workload).
		WithObjects(hcp, workload,
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: hcp.Namespace}, Data: map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{}`)}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "etcd-encryption-key", Namespace: hcp.Namespace}, Data: map[string][]byte{"key": []byte("synthetic-sensitive-key")}},
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "node-tuning-operator", Namespace: hcp.Namespace}, Spec: corev1.ServiceSpec{ClusterIP: "None"}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "node-tuning-operator-tls", Namespace: hcp.Namespace}, Data: map[string][]byte{"key": []byte("synthetic-sensitive-tls")}})
	if mode == "update" {
		clientBuilder.WithObjects(manifests.IgnitionFIPSConfig(hcp.Namespace))
	}
	baseClient := clientBuilder.WithObjects(healthyObjects...).Build()
	controlledClient := interceptor.NewClient(baseClient, interceptor.Funcs{Create: func(ctx context.Context, underlying client.WithWatch, object client.Object, options ...client.CreateOption) error {
		if fixture.blocked && object.GetName() == "ignition-config-fips" {
			return failure
		}
		return underlying.Create(ctx, object, options...)
	}, Update: func(ctx context.Context, underlying client.WithWatch, object client.Object, options ...client.UpdateOption) error {
		if fixture.blocked && mode == "update" && object.GetName() == "ignition-config-fips" {
			return failure
		}
		return underlying.Update(ctx, object, options...)
	}})
	mockController := gomock.NewController(t)
	provider := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(mockController)
	provider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).Return(testutils.InitReleaseImageOrDie("4.15.0"), nil).AnyTimes()
	provider.EXPECT().GetRegistryOverrides().Return(map[string]string{}).AnyTimes()
	ec2Client := awsapi.NewMockEC2API(mockController)
	ec2Client.EXPECT().DescribeVpcEndpoints(gomock.Any(), gomock.Any()).Return(&ec2.DescribeVpcEndpointsOutput{}, errors.New("not ready")).AnyTimes()
	recorder := &capturedWarning{}
	reconciler := &HostedControlPlaneReconciler{Client: controlledClient, Recorder: recorder, Log: logr.Discard(), ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{}, ReleaseProvider: provider, UserReleaseProvider: &fakereleaseprovider.FakeReleaseProvider{}, ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProviderHCCO{}, ec2Client: ec2Client,
		reconcileInfrastructureStatus: func(context.Context, *hyperv1.HostedControlPlane) (infra.InfrastructureStatus, error) {
			if mode == "waiting" {
				return infra.InfrastructureStatus{}, nil
			}
			return infra.InfrastructureStatus{APIHost: "foo", APIPort: 1, OAuthHost: "foo", OAuthPort: 1, KonnectivityHost: "foo", KonnectivityPort: 1}, nil
		}}
	reconciler.setup(controllerutil.CreateOrUpdate)
	fixture.hcp = hcp
	fixture.workload = workload
	fixture.originalFailure = originalFailure
	fixture.healthyObjects = healthyObjects
	fixture.baseClient = baseClient
	fixture.reconciler = reconciler
	fixture.recorder = recorder
	fixture.key = client.ObjectKeyFromObject(hcp)
	return fixture
}

func (fixture *reconcileEventFixture) configure(t *testing.T) {
	t.Helper()
	hcp := fixture.hcp
	reconciler := fixture.reconciler
	switch fixture.mode {
	case "nil recorder":
		reconciler.Recorder = nil
	case "stopped recorder":
		broadcaster := record.NewBroadcaster()
		reconciler.Recorder = broadcaster.NewRecorder(api.Scheme, corev1.EventSource{Component: ControllerName})
		broadcaster.Shutdown()
	case "paused":
		paused := "true"
		hcp.Spec.PausedUntil = &paused
	case "timed pause":
		paused := time.Now().Add(5 * time.Minute).UTC().Format(time.RFC3339)
		hcp.Spec.PausedUntil = &paused
	case "release":
		reconciler.OperateOnReleaseImage = "different-release"
	case "invalid":
		reconciler.ManagementClusterCapabilities = &fakecapabilities.FakeSupportNoCapabilities{}
	case "disabled":
		hcp.Annotations[hyperv1.DisableIgnitionServerAnnotation] = "true"
	case "deleting":
		if err := fixture.baseClient.Delete(t.Context(), hcp); err != nil {
			t.Fatal(err)
		}
	case "missing":
		fixture.key.Name = "missing-hcp"
	}
	if fixture.mode != "deleting" && fixture.mode != "missing" {
		if err := fixture.baseClient.Update(t.Context(), hcp); err != nil {
			t.Fatal(err)
		}
	}
}

func (fixture *reconcileEventFixture) prepareSnapshots(t *testing.T) {
	t.Helper()
	if fixture.mode != "healthy quota" {
		return
	}
	fixture.blocked = false
	if _, err := fixture.reconciler.Reconcile(t.Context(), reconcile.Request{NamespacedName: fixture.key}); err != nil {
		t.Fatal(err)
	}
	for _, object := range []client.Object{fixture.healthyObjects[0], fixture.healthyObjects[1], manifests.IgnitionWorkerSSHConfig(fixture.hcp.Namespace)} {
		snapshot := object.DeepCopyObject().(client.Object)
		if err := fixture.baseClient.Get(t.Context(), client.ObjectKeyFromObject(object), snapshot); err != nil {
			t.Fatal(err)
		}
		fixture.snapshots = append(fixture.snapshots, snapshot)
	}
	if err := fixture.baseClient.Delete(t.Context(), manifests.IgnitionFIPSConfig(fixture.hcp.Namespace)); err != nil {
		t.Fatal(err)
	}
	fixture.blocked = true
}

func (fixture *reconcileEventFixture) assertUnchanged(t *testing.T) {
	t.Helper()
	for _, snapshot := range fixture.snapshots {
		actual := snapshot.DeepCopyObject().(client.Object)
		if err := fixture.baseClient.Get(t.Context(), client.ObjectKeyFromObject(snapshot), actual); err != nil {
			t.Fatal(err)
		}
		if !apiequality.Semantic.DeepEqual(snapshot, actual) {
			t.Fatalf("quota reconciliation changed existing %T %s", snapshot, snapshot.GetName())
		}
	}
}

func (fixture *reconcileEventFixture) assertFailure(t *testing.T, result reconcile.Result, err error) {
	t.Helper()
	if !errors.Is(err, fixture.originalFailure) || !result.IsZero() || !strings.HasPrefix(err.Error(), "failed to update control plane: ") {
		t.Fatalf("failure result=%v error=%v", result, err)
	}
	fixture.assertFailureEvent(t)
	if len(fixture.recorder.messages) > 0 && !strings.Contains(err.Error(), fixture.recorder.messages[0]) {
		t.Fatalf("event lost original diagnostic: %q, error: %v", fixture.recorder.messages[0], err)
	}
	if fixture.mode == "healthy quota" {
		failedHCP := &hyperv1.HostedControlPlane{}
		if err := fixture.baseClient.Get(t.Context(), fixture.key, failedHCP); err != nil {
			t.Fatal(err)
		}
		available := meta.FindStatusCondition(failedHCP.Status.Conditions, string(hyperv1.HostedControlPlaneAvailable))
		if !failedHCP.Status.Ready || available == nil || available.Status != metav1.ConditionTrue {
			t.Fatal("quota failure changed healthy availability")
		}
	}
}

func (fixture *reconcileEventFixture) assertFailureEvent(t *testing.T) {
	t.Helper()
	hcp := fixture.hcp
	recorder := fixture.recorder
	if fixture.mode == "update" || (strings.HasSuffix(fixture.mode, " conflict") && fixture.mode != "mixed conflict") {
		if len(recorder.objects) != 0 {
			t.Fatalf("conflict emitted failure events: %v", recorder.messages)
		}
		return
	}
	if fixture.mode != "nil recorder" && fixture.mode != "stopped recorder" {
		expectedDetail := "count/configmaps=1, used: count/configmaps=0, limited: count/configmaps=0"
		if fixture.mode == "mixed conflict" {
			expectedDetail = "failed to reconcile PKI synthetic-cert"
		}
		if len(recorder.objects) != 1 || recorder.objects[0].UID != hcp.UID || recorder.objects[0].Name != hcp.Name || recorder.objects[0].Namespace != hcp.Namespace || !strings.Contains(recorder.messages[0], expectedDetail) {
			t.Fatalf("unexpected failure events: %v", recorder.messages)
		}
	}
}

func (fixture *reconcileEventFixture) assertRecovery(t *testing.T) {
	t.Helper()
	eventCount := len(fixture.recorder.objects)
	fixture.blocked = false
	result, err := fixture.reconciler.Reconcile(t.Context(), reconcile.Request{NamespacedName: fixture.key})
	expectedInterval := hcpNotReadyRequeueInterval
	if fixture.mode == "healthy quota" {
		expectedInterval = hcpReadyRequeueInterval
	}
	if err != nil || result.RequeueAfter != expectedInterval {
		t.Fatalf("recovery result=%v error=%v", result, err)
	}
	if err := fixture.baseClient.Get(t.Context(), client.ObjectKeyFromObject(manifests.IgnitionFIPSConfig(fixture.hcp.Namespace)), &corev1.ConfigMap{}); err != nil {
		t.Fatal(err)
	}
	if len(fixture.recorder.objects) != eventCount {
		t.Fatal("recovery emitted failure event")
	}
}

func (fixture *reconcileEventFixture) assertGatedResult(t *testing.T, result reconcile.Result, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("gated reconcile: %v", err)
	}
	if len(fixture.recorder.objects) != 0 {
		t.Fatalf("gated outcome: %v", fixture.recorder.messages)
	}
	if fixture.mode == "waiting" && result.RequeueAfter != time.Minute {
		t.Fatalf("waiting result=%v", result)
	}
	if fixture.mode == "timed pause" && (result.RequeueAfter < 4*time.Minute || result.RequeueAfter > 5*time.Minute) {
		t.Fatalf("timed pause result=%v", result)
	}
	if fixture.mode == "healthy" && result.RequeueAfter != hcpReadyRequeueInterval {
		t.Fatalf("healthy result=%v", result)
	}
	if (fixture.mode == "release" || fixture.mode == "invalid" || fixture.mode == "missing" || fixture.mode == "paused") && !result.IsZero() {
		t.Fatalf("gated result=%v", result)
	}
}

func (fixture *reconcileEventFixture) assertPreservedState(t *testing.T) {
	t.Helper()
	actualWorkload := &appsv1.Deployment{}
	if err := fixture.baseClient.Get(t.Context(), client.ObjectKeyFromObject(fixture.workload), actualWorkload); err != nil {
		t.Fatal(err)
	}
	if actualWorkload.Status.AvailableReplicas != 1 || actualWorkload.Status.ReadyReplicas != 1 {
		t.Fatal("healthy workload changed")
	}
	actualHCP := &hyperv1.HostedControlPlane{}
	if err := fixture.baseClient.Get(t.Context(), client.ObjectKeyFromObject(fixture.hcp), actualHCP); err == nil {
		condition := meta.FindStatusCondition(actualHCP.Status.Conditions, "UnrelatedCondition")
		if condition == nil || condition.Status != metav1.ConditionTrue || condition.Reason != "Unchanged" {
			t.Fatal("unrelated condition changed")
		}
		if fixture.mode == "healthy quota" || fixture.mode == "healthy" {
			available := meta.FindStatusCondition(actualHCP.Status.Conditions, string(hyperv1.HostedControlPlaneAvailable))
			if !actualHCP.Status.Ready || available == nil || available.Status != metav1.ConditionTrue {
				t.Fatal("healthy control plane availability changed")
			}
		}
	}
}

func TestRequestsForEvent(t *testing.T) {
	hcp := sampleHCP(t)
	if requests := (&HostedControlPlaneReconciler{}).requestsForEvent(t.Context(), nil); len(requests) != 0 {
		t.Fatal("nil Event enqueued a request")
	}
	baseEvent := &corev1.Event{ObjectMeta: metav1.ObjectMeta{Namespace: hcp.Namespace}, Reason: reconciliationFailedReason, Source: corev1.EventSource{Component: ControllerName}, InvolvedObject: corev1.ObjectReference{APIVersion: hyperv1.GroupVersion.String(), Kind: "HostedControlPlane", Namespace: hcp.Namespace, Name: hcp.Name, UID: hcp.UID}}
	for _, testCase := range []struct {
		name     string
		mutate   func(*corev1.Event)
		filtered bool
	}{
		{name: "When own source matches, it should filter", filtered: true},
		{name: "When own reporting controller matches, it should filter", filtered: true, mutate: func(object *corev1.Event) { object.Source.Component = ""; object.ReportingController = ControllerName }},
		{name: "When both own identities match, it should filter", filtered: true, mutate: func(object *corev1.Event) { object.ReportingController = ControllerName }},
		{name: "When another controller reports, it should enqueue", mutate: func(object *corev1.Event) { object.Source.Component = "external" }},
		{name: "When identities conflict, it should enqueue", mutate: func(object *corev1.Event) { object.ReportingController = "external" }},
		{name: "When source is empty, it should enqueue", mutate: func(object *corev1.Event) { object.Source.Component = "" }},
		{name: "When reason differs, it should enqueue", mutate: func(object *corev1.Event) { object.Reason = "LoadBalancerFailed" }},
		{name: "When workload is involved, it should enqueue", mutate: func(object *corev1.Event) { object.InvolvedObject.Kind = "Pod" }},
		{name: "When API group differs, it should enqueue", mutate: func(object *corev1.Event) { object.InvolvedObject.APIVersion = "external.example/v1beta1" }},
		{name: "When UID differs, it should enqueue", mutate: func(object *corev1.Event) { object.InvolvedObject.UID = "other" }},
		{name: "When UID is empty, it should enqueue", mutate: func(object *corev1.Event) { object.InvolvedObject.UID = "" }},
		{name: "When name differs, it should enqueue", mutate: func(object *corev1.Event) { object.InvolvedObject.Name = "other" }},
		{name: "When namespace differs, it should enqueue", mutate: func(object *corev1.Event) { object.InvolvedObject.Namespace = "other" }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			object := baseEvent.DeepCopy()
			if testCase.mutate != nil {
				testCase.mutate(object)
			}
			reconciler := &HostedControlPlaneReconciler{Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcp).Build(), Log: logr.Discard(), ManagementClusterCapabilities: &fakecapabilities.FakeSupportNoCapabilities{}}
			handlers := reconciler.eventHandlers(api.Scheme, meta.NewDefaultRESTMapper(nil))
			for _, operation := range []string{"create", "update", "mixed update", "reverse mixed update", "delete", "generic"} {
				queue := workqueue.NewTypedRateLimitingQueue(workqueue.DefaultTypedControllerRateLimiter[reconcile.Request]())
				switch operation {
				case "create":
					handlers[0].handler.Create(t.Context(), event.CreateEvent{Object: object}, queue)
				case "update":
					handlers[0].handler.Update(t.Context(), event.UpdateEvent{ObjectOld: object, ObjectNew: object.DeepCopy()}, queue)
				case "mixed update":
					handlers[0].handler.Update(t.Context(), event.UpdateEvent{ObjectOld: baseEvent, ObjectNew: object}, queue)
				case "reverse mixed update":
					handlers[0].handler.Update(t.Context(), event.UpdateEvent{ObjectOld: object, ObjectNew: baseEvent}, queue)
				case "delete":
					handlers[0].handler.Delete(t.Context(), event.DeleteEvent{Object: object}, queue)
				case "generic":
					handlers[0].handler.Generic(t.Context(), event.GenericEvent{Object: object}, queue)
				}
				expected := 1
				if testCase.filtered {
					expected = 0
				}
				if queue.Len() != expected {
					t.Fatalf("%s queue length=%d, want %d", operation, queue.Len(), expected)
				}
				queue.ShutDown()
			}
		})
	}
}

func TestReconciliationFailureMessage(t *testing.T) {
	t.Parallel()
	quota := apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, "ignition-config-fips", errors.New("exceeded quota: configmaps, requested: count/configmaps=1, used: count/configmaps=0, limited: count/configmaps=0"))
	for _, testCase := range []struct {
		name    string
		failure error
		want    string
	}{
		{name: "When quota is forbidden, it should retain resource names and quota details", failure: quota, want: quota.Error()},
		{name: "When an API error names a non-core resource, it should retain the group and object name", failure: apierrors.NewNotFound(schema.GroupResource{Group: "synthetic.example", Resource: "widgets"}, "synthetic-widget"), want: `widgets.synthetic.example "synthetic-widget" not found`},
		{name: "When PKI reconciliation fails, it should retain certificate details", failure: errors.New("failed to reconcile PKI: certificate synthetic-serving-cert is expired"), want: "failed to reconcile PKI: certificate synthetic-serving-cert is expired"},
		{name: "When etcd reconciliation fails, it should retain member details", failure: errors.New("etcd member synthetic-member is unhealthy"), want: "etcd member synthetic-member is unhealthy"},
		{name: "When release lookup fails, it should retain lookup details", failure: errors.New("failed to look up release image example.invalid/release:synthetic"), want: "failed to look up release image example.invalid/release:synthetic"},
		{name: "When aggregates and joins are wrapped, it should retain all context", failure: fmt.Errorf("outer: %w", utilerrors.NewAggregate([]error{errors.Join(errors.New("PKI synthetic-cert"), errors.New("etcd synthetic-member")), quota})), want: "outer: [PKI synthetic-cert\netcd synthetic-member, " + quota.Error() + "]"},
		{name: "When the message is exactly the byte limit, it should retain it", failure: errors.New(strings.Repeat("a", 1024)), want: strings.Repeat("a", 1024)},
		{name: "When ASCII exceeds the byte limit, it should truncate by bytes", failure: errors.New(strings.Repeat("a", 1025)), want: strings.Repeat("a", 1024)},
		{name: "When truncation splits a UTF8 character, it should retain only complete characters", failure: errors.New(strings.Repeat("a", 1023) + "界"), want: strings.Repeat("a", 1023)},
		{name: "When multibyte text exceeds the byte limit, it should bound bytes rather than runes", failure: errors.New(strings.Repeat("界", 1024)), want: strings.Repeat("界", 341)},
		{name: "When a UTF8 character ends at the byte limit, it should retain it", failure: errors.New(strings.Repeat("a", 1021) + "界tail"), want: strings.Repeat("a", 1021) + "界"},
		{name: "When the message contains invalid UTF8, it should replace invalid bytes", failure: errors.New("synthetic\xffmessage"), want: "synthetic�message"},
		{name: "When the message is empty, it should retain it", failure: errors.New(""), want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			message := reconciliationFailureMessage(testCase.failure)
			if !utf8.ValidString(message) || len(message) > maxFailureMessageBytes || message != testCase.want {
				t.Fatalf("message=%q (%d bytes), want %q", message, len(message), testCase.want)
			}
		})
	}
}

func TestRecordReconciliationFailure(t *testing.T) {
	t.Parallel()
	conflict := apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, "ignition-config-fips", errors.New("synthetic concurrent update"))
	failure := errors.New("failed to reconcile PKI synthetic-cert")
	for _, testCase := range []struct {
		name        string
		failure     error
		nilRecorder bool
		wantEvent   bool
	}{
		{name: "When no error occurred, it should not emit a warning"},
		{name: "When no recorder is configured, it should not emit a warning", failure: failure, nilRecorder: true},
		{name: "When a non-API failure occurs, it should emit its original diagnostic", failure: failure, wantEvent: true},
		{name: "When a direct conflict occurs, it should not emit a warning", failure: conflict},
		{name: "When a wrapped conflict occurs, it should not emit a warning", failure: fmt.Errorf("update: %w", conflict)},
		{name: "When nested errors all conflict, it should not emit a warning", failure: fmt.Errorf("outer: %w", utilerrors.NewAggregate([]error{errors.Join(conflict, conflict)}))},
		{name: "When nested errors mix conflicts and failures, it should warn with the complete diagnostic", failure: errors.Join(conflict, utilerrors.NewAggregate([]error{failure, conflict})), wantEvent: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Name: "synthetic-hcp", Namespace: "synthetic-namespace", UID: "synthetic-uid"}}
			recorder := &capturedWarning{}
			reconciler := &HostedControlPlaneReconciler{}
			if !testCase.nilRecorder {
				reconciler.Recorder = recorder
			}
			reconciler.recordReconciliationFailure(hcp, testCase.failure)
			if !testCase.wantEvent {
				if len(recorder.objects) != 0 {
					t.Fatalf("unexpected warning events: %v", recorder.messages)
				}
				return
			}
			if len(recorder.objects) != 1 || !apiequality.Semantic.DeepEqual(recorder.objects[0], hcp) || recorder.messages[0] != testCase.failure.Error() {
				t.Fatalf("warning objects=%v messages=%v, want HCP and diagnostic %q", recorder.objects, recorder.messages, testCase.failure.Error())
			}
		})
	}
}

func TestIsConflictOnly(t *testing.T) {
	t.Parallel()
	conflict := apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, "ignition-config-fips", errors.New("synthetic update conflict"))
	other := errors.New("failed to reconcile PKI synthetic-cert")
	for _, testCase := range []struct {
		name    string
		failure error
		want    bool
	}{
		{name: "When error is nil, it should not classify it as a conflict"},
		{name: "When error is a direct conflict, it should suppress the warning", failure: conflict, want: true},
		{name: "When a conflict is wrapped, it should suppress the warning", failure: fmt.Errorf("update: %w", conflict), want: true},
		{name: "When all aggregate errors conflict, it should suppress the warning", failure: utilerrors.NewAggregate([]error{conflict, fmt.Errorf("update: %w", conflict)}), want: true},
		{name: "When all joined errors conflict, it should suppress the warning", failure: errors.Join(conflict, fmt.Errorf("update: %w", conflict)), want: true},
		{name: "When nested aggregate and joined errors all conflict, it should suppress the warning", failure: fmt.Errorf("outer: %w", utilerrors.NewAggregate([]error{errors.Join(conflict, fmt.Errorf("inner: %w", conflict))})), want: true},
		{name: "When an aggregate has mixed failures, it should keep the warning", failure: utilerrors.NewAggregate([]error{conflict, other})},
		{name: "When a join has mixed failures, it should keep the warning", failure: errors.Join(conflict, other)},
		{name: "When nested mixed errors start with a conflict, it should keep the warning", failure: fmt.Errorf("outer: %w", errors.Join(conflict, utilerrors.NewAggregate([]error{other, conflict})))},
		{name: "When nested mixed errors end with a conflict, it should keep the warning", failure: fmt.Errorf("outer: %w", utilerrors.NewAggregate([]error{other, errors.Join(conflict, conflict)}))},
		{name: "When plain text mentions conflict, it should keep the warning", failure: errors.New(conflict.Error())},
		{name: "When a Kubernetes error is not a conflict, it should keep the warning", failure: apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, "synthetic-cert")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := isConflictOnly(testCase.failure); got != testCase.want {
				t.Fatalf("isConflictOnly(%v)=%t, want %t", testCase.failure, got, testCase.want)
			}
		})
	}
}
