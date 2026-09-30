package ntostatus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/openshift/hypershift/pkg/nodepool"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestListPerformanceProfileConfigMaps(t *testing.T) {
	testCases := []struct {
		name             string
		attempts         int
		requestTimeout   time.Duration
		parentTimeout    time.Duration
		cancelParent     bool
		list             func(context.Context, int, *corev1.ConfigMapList) error
		wantCounts       []int
		wantErrorClasses []string
	}{
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
					list.Items = []corev1.ConfigMap{{ObjectMeta: metav1.ObjectMeta{Name: "status-config"}}}
				}
				return nil
			},
			wantCounts:       []int{0, 1},
			wantErrorClasses: []string{"none", "none"},
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
				list.Items = []corev1.ConfigMap{{ObjectMeta: metav1.ObjectMeta{Name: "status-config"}}}
				return nil
			},
			wantCounts:       []int{0, 1},
			wantErrorClasses: []string{"request_deadline", "none"},
		},
		{
			name: "When the API returns an error containing a credential, it should classify and redact it",
			list: func(_ context.Context, _ int, _ *corev1.ConfigMapList) error {
				return errors.New("Bearer sensitive-token")
			},
			attempts:         1,
			wantCounts:       []int{0},
			wantErrorClasses: []string{"api_error"},
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
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			attempts := 0
			managementClient := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
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
				if len(objects) != testCase.wantCounts[attempt] || !strings.Contains(logLine, fmt.Sprintf("count=%d", testCase.wantCounts[attempt])) {
					t.Errorf("attempt %d: count=%d log=%q", attempt, len(objects), logLine)
				}
				wantClass := testCase.wantErrorClasses[attempt]
				if !strings.Contains(logLine, "errorClass="+wantClass) || (err != nil) != (wantClass != "none") {
					t.Errorf("attempt %d: error=%v log=%q, want class %s", attempt, err, logLine, wantClass)
				}
				if strings.Contains(logLine, "sensitive-token") || (err != nil && strings.Contains(err.Error(), "sensitive-token")) {
					t.Error("credential appeared in log or returned error")
				}
				if !strings.Contains(logLine, "namespace=\"control-plane\"") || !strings.Contains(logLine, "selector=\""+nodepool.NodeTuningGeneratedPerformanceProfileStatusLabel+"=true\"") {
					t.Errorf("attempt %d: missing namespace or selector: %q", attempt, logLine)
				}
				if testCase.wantCounts[attempt] == 1 && (!strings.Contains(logLine, "names=[\"status-config\"]") || !strings.Contains(logLine, "listRV=\"17\"") && testCase.attempts == 2 && testCase.requestTimeout == 0) {
					t.Errorf("attempt %d: missing name or list resourceVersion: %q", attempt, logLine)
				}
				assertUTCTimestamps(t, logLine)
			}
			if attempts != testCase.attempts {
				t.Errorf("got %d requests, want %d", attempts, testCase.attempts)
			}
		})
	}
}

func assertUTCTimestamps(t *testing.T, logLine string) {
	t.Helper()
	for _, marker := range []string{"start=", "end="} {
		start := strings.Index(logLine, marker)
		if start < 0 {
			t.Errorf("missing %s in %q", marker, logLine)
			continue
		}
		stamp := strings.Fields(logLine[start+len(marker):])[0]
		if parsed, parseErr := time.Parse(time.RFC3339Nano, stamp); parseErr != nil || parsed.Location() != time.UTC {
			t.Errorf("invalid UTC %s timestamp %q: %v", marker, stamp, parseErr)
		}
	}
}
