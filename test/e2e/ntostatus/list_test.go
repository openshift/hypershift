package ntostatus

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/pkg/nodepool"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

type listTestCase struct {
	name             string
	attempts         int
	requestTimeout   time.Duration
	parentTimeout    time.Duration
	cancelParent     bool
	list             func(context.Context, int, *corev1.ConfigMapList) error
	listError        error
	objects          []client.Object
	wantCounts       []int
	wantErrorClasses []string
	wantNames        [][]string
	wantListRV       []string
	wantOmitted      []int
	wantContextError error
}

func TestListPerformanceProfileConfigMaps(t *testing.T) {
	testCases := []listTestCase{
		{
			name:             "When no ConfigMaps match, it should report a successful empty list",
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"none"},
		},
		{
			name:     "When a ConfigMap appears later, it should report the match and resourceVersion",
			attempts: 2,
			list: func(_ context.Context, attempt int, list *corev1.ConfigMapList) error {
				if attempt == 2 {
					list.ResourceVersion = "17"
					list.Items = []corev1.ConfigMap{statusConfigMap("status-config")}
				}
				return nil
			},
			wantCounts:       []int{0, 1},
			wantErrorClasses: []string{"none", "none"},
			wantNames:        [][]string{{}, {"status-config"}},
			wantListRV:       []string{"", "17"},
		},
		{
			name: "When ConfigMaps have different status labels, it should list only matching status objects",
			objects: []client.Object{
				statusConfigMapPointer("status-first"),
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "other", Namespace: "control-plane", Labels: map[string]string{
					nodepool.NodeTuningGeneratedPerformanceProfileStatusLabel: "false",
				}}},
				statusConfigMapPointer("status-second"),
				&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "wrong-namespace", Namespace: "another-namespace", Labels: map[string]string{
					nodepool.NodeTuningGeneratedPerformanceProfileStatusLabel: "true",
				}}},
			},
			attempts:         1,
			wantCounts:       []int{2},
			wantErrorClasses: []string{"none"},
			wantNames:        [][]string{{"status-first", "status-second"}},
		},
		{
			name: "When too many status objects match, it should cap logged names without changing the count",
			list: func(_ context.Context, _ int, list *corev1.ConfigMapList) error {
				for index := range 7 {
					list.Items = append(list.Items, statusConfigMap(fmt.Sprintf("status-%d", index)))
				}
				return nil
			},
			attempts:         1,
			wantCounts:       []int{7},
			wantErrorClasses: []string{"none"},
			wantNames:        [][]string{{"status-0", "status-1", "status-2", "status-3", "status-4"}},
			wantOmitted:      []int{2},
		},
		{
			name:           "When a request stalls, it should expire its own deadline and recover on the next attempt",
			attempts:       2,
			requestTimeout: 20 * time.Millisecond,
			list: func(ctx context.Context, attempt int, list *corev1.ConfigMapList) error {
				if attempt == 1 {
					<-ctx.Done()
					return ctx.Err()
				}
				list.Items = []corev1.ConfigMap{statusConfigMap("status-config")}
				return nil
			},
			wantCounts:       []int{0, 1},
			wantErrorClasses: []string{"request_deadline", "none"},
			wantContextError: context.DeadlineExceeded,
		},
		{
			name:             "When the API returns an error containing a credential, it should classify and redact it",
			listError:        errors.New("Bearer sensitive-token"),
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"api_error"},
		},
		{
			name:             "When the API rejects authentication, it should report unauthorized without its message",
			listError:        apierrors.NewUnauthorized("Bearer sensitive-token"),
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"unauthorized"},
		},
		{
			name:             "When the API forbids access, it should report forbidden without its message",
			listError:        apierrors.NewForbidden(schema.GroupResource{Resource: "configmaps"}, "sensitive-token", errors.New("Bearer sensitive-token")),
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"forbidden"},
		},
		{
			name:             "When the API throttles requests, it should report throttled without its message",
			listError:        apierrors.NewTooManyRequests("Bearer sensitive-token", 1),
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"throttled"},
		},
		{
			name:             "When the API times out, it should report API timeout without its message",
			listError:        apierrors.NewServerTimeout(schema.GroupResource{Resource: "configmaps"}, "Bearer sensitive-token", 1),
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"api_timeout"},
		},
		{
			name:             "When the API fails internally, it should report HTTP 500 without its message",
			listError:        apierrors.NewInternalError(errors.New("Bearer sensitive-token")),
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"http_500"},
		},
		{
			name:             "When the API is unavailable, it should report HTTP 503 without its message",
			listError:        apierrors.NewServiceUnavailable("Bearer sensitive-token"),
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"http_503"},
		},
		{
			name:             "When DNS fails, it should report DNS failure without its message",
			listError:        &net.DNSError{Err: "Bearer sensitive-token", Name: "api.example.test"},
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"dns_error"},
		},
		{
			name:             "When TLS validation fails, it should report TLS failure without its message",
			listError:        fmt.Errorf("Bearer sensitive-token: %w", x509.UnknownAuthorityError{}),
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"tls_error"},
		},
		{
			name:             "When the network fails, it should report network failure without its message",
			listError:        &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("Bearer sensitive-token")},
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"network_error"},
		},
		{
			name:             "When an error wraps cancellation, it should retain safe cancellation identity",
			listError:        fmt.Errorf("Bearer sensitive-token: %w", context.Canceled),
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"canceled"},
			wantContextError: context.Canceled,
		},
		{
			name:             "When an error wraps a deadline, it should retain safe deadline identity",
			listError:        fmt.Errorf("Bearer sensitive-token: %w", context.DeadlineExceeded),
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"deadline"},
			wantContextError: context.DeadlineExceeded,
		},
		{
			name:         "When the caller cancels, it should distinguish cancellation",
			cancelParent: true,
			attempts:     1,
			list: func(ctx context.Context, _ int, _ *corev1.ConfigMapList) error {
				return ctx.Err()
			},
			wantCounts:       []int{0},
			wantErrorClasses: []string{"caller_canceled"},
			wantContextError: context.Canceled,
		},
		{
			name:          "When the overall deadline expires, it should distinguish it from a request deadline",
			parentTimeout: 20 * time.Millisecond,
			attempts:      1,
			list: func(ctx context.Context, _ int, _ *corev1.ConfigMapList) error {
				<-ctx.Done()
				return ctx.Err()
			},
			wantCounts:       []int{0},
			wantErrorClasses: []string{"caller_deadline"},
			wantContextError: context.DeadlineExceeded,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			attempts := 0
			managementClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(testCase.objects...).WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, underlying client.WithWatch, objects client.ObjectList, options ...client.ListOption) error {
					attempts++
					listOptions := &client.ListOptions{}
					listOptions.ApplyOptions(options)
					if listOptions.Namespace != "control-plane" || listOptions.LabelSelector == nil || listOptions.LabelSelector.String() != nodepool.NodeTuningGeneratedPerformanceProfileStatusLabel+"=true" {
						t.Errorf("unexpected effective list options: namespace=%q selector=%v", listOptions.Namespace, listOptions.LabelSelector)
					}
					if testCase.list != nil {
						return testCase.list(ctx, attempts, objects.(*corev1.ConfigMapList))
					}
					if testCase.listError != nil {
						return testCase.listError
					}
					return underlying.List(ctx, objects, options...)
				},
			}).Build()
			ctx := context.Background()
			if testCase.parentTimeout != 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, testCase.parentTimeout)
				defer cancel()
			}
			if testCase.cancelParent {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			requestTimeout := testCase.requestTimeout
			if requestTimeout == 0 {
				requestTimeout = time.Second
			}
			for attempt := 0; attempt < testCase.attempts; attempt++ {
				var logLine string
				objects, err := ListPerformanceProfileConfigMaps(ctx, managementClient, "control-plane", requestTimeout, func(format string, args ...any) {
					logLine = fmt.Sprintf(format, args...)
				})
				assertListAttempt(t, testCase, attempt, objects, err, logLine)
			}
			if attempts != testCase.attempts {
				t.Errorf("got %d requests, want %d", attempts, testCase.attempts)
			}
		})
	}
}

func assertListAttempt(t *testing.T, testCase listTestCase, attempt int, objects []*corev1.ConfigMap, err error, logLine string) {
	t.Helper()
	if len(objects) != testCase.wantCounts[attempt] || !strings.Contains(logLine, fmt.Sprintf("count=%d", testCase.wantCounts[attempt])) {
		t.Errorf("attempt %d: count=%d log=%q", attempt, len(objects), logLine)
	}
	wantClass := testCase.wantErrorClasses[attempt]
	if !strings.Contains(logLine, "errorClass="+wantClass) || (err != nil) != (wantClass != "none") {
		t.Errorf("attempt %d: error=%v log=%q, want class %s", attempt, err, logLine, wantClass)
	}
	if err != nil && !strings.Contains(err.Error(), wantClass) {
		t.Errorf("attempt %d: returned error omits safe class %s", attempt, wantClass)
	}
	if strings.Contains(logLine, "sensitive-token") || (err != nil && strings.Contains(err.Error(), "sensitive-token")) {
		t.Error("credential appeared in log or returned error")
	}
	for _, object := range objects {
		if object.Namespace != "control-plane" || object.Labels[nodepool.NodeTuningGeneratedPerformanceProfileStatusLabel] != "true" || object.Labels[hyperv1.NodePoolLabel] != "worker-pool" {
			t.Errorf("attempt %d: returned an object without expected status and NodePool metadata", attempt)
		}
	}
	if testCase.wantContextError != nil && (attempt == 0) != errors.Is(err, testCase.wantContextError) {
		t.Errorf("attempt %d: context identity mismatch for class %s", attempt, wantClass)
	}
	if !strings.Contains(logLine, "namespace=\"control-plane\"") || !strings.Contains(logLine, "selector=\""+nodepool.NodeTuningGeneratedPerformanceProfileStatusLabel+"=true\"") {
		t.Errorf("attempt %d: missing namespace or selector: %q", attempt, logLine)
	}
	if testCase.wantNames != nil && !strings.Contains(logLine, fmt.Sprintf("names=%q", testCase.wantNames[attempt])) {
		t.Errorf("attempt %d: incorrect sampled names: %q", attempt, logLine)
	}
	if testCase.wantListRV != nil && !strings.Contains(logLine, fmt.Sprintf("listRV=%q", testCase.wantListRV[attempt])) {
		t.Errorf("attempt %d: incorrect list resourceVersion: %q", attempt, logLine)
	}
	if testCase.wantListRV != nil && testCase.wantListRV[attempt] == "17" && strings.Contains(logLine, "listRV=\"11\"") {
		t.Errorf("attempt %d: logged object resourceVersion as list resourceVersion: %q", attempt, logLine)
	}
	if testCase.wantOmitted != nil && !strings.Contains(logLine, fmt.Sprintf("omittedNames=%d", testCase.wantOmitted[attempt])) {
		t.Errorf("attempt %d: incorrect omitted name count: %q", attempt, logLine)
	}
	if testCase.wantCounts[attempt] == 7 && strings.Contains(logLine, "status-6") {
		t.Errorf("attempt %d: logged an unsampled name: %q", attempt, logLine)
	}
	assertUTCTimestamps(t, logLine)
}

func statusConfigMap(name string) corev1.ConfigMap {
	return corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: "control-plane", ResourceVersion: "11", Annotations: map[string]string{"private": "sensitive-token"},
		Labels: map[string]string{
			nodepool.NodeTuningGeneratedPerformanceProfileStatusLabel: "true",
			hyperv1.NodePoolLabel: "worker-pool",
		},
	}, Data: map[string]string{"private": "sensitive-token"}}
}

func statusConfigMapPointer(name string) *corev1.ConfigMap {
	configMap := statusConfigMap(name)
	return &configMap
}

func assertUTCTimestamps(t *testing.T, logLine string) {
	t.Helper()
	stamps := make([]time.Time, 0, 2)
	for _, marker := range []string{"start=", "end="} {
		start := strings.Index(logLine, marker)
		if start < 0 {
			t.Errorf("missing %s in %q", marker, logLine)
			continue
		}
		stamp := strings.Fields(logLine[start+len(marker):])[0]
		if parsed, parseErr := time.Parse(time.RFC3339Nano, stamp); parseErr != nil || parsed.Location() != time.UTC {
			t.Errorf("invalid UTC %s timestamp %q: %v", marker, stamp, parseErr)
		} else {
			stamps = append(stamps, parsed)
		}
	}
	if len(stamps) != 2 {
		return
	}
	if stamps[1].Before(stamps[0]) {
		t.Errorf("end timestamp precedes start in %q", logLine)
	}
	start := strings.Index(logLine, "duration=")
	if start < 0 {
		t.Errorf("missing duration in %q", logLine)
		return
	}
	durationText := strings.Fields(logLine[start+len("duration="):])[0]
	duration, parseErr := time.ParseDuration(durationText)
	if parseErr != nil || duration < 0 || duration > time.Minute || duration != stamps[1].Sub(stamps[0]) {
		t.Errorf("invalid duration %q for timestamps in %q: %v", durationText, logLine, parseErr)
	}
}
