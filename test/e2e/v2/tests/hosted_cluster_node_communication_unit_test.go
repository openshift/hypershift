//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestNodeCommunicationRequired(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		intent   map[string]int32
		node     bool
		listErr  bool
		want     bool
		wantList bool
	}{
		{name: "When worker intent is absent, it should preserve standalone communication coverage", want: true},
		{name: "When a positive configured pool is missing, it should still require communication", intent: map[string]int32{"missing": 1}, want: true},
		{name: "When one configured pool is positive, it should not skip on an empty pool", intent: map[string]int32{"empty": 0, "missing": 2}, want: true},
		{name: "When zero pools are configured and nodes are absent, it should skip the inapplicable probe", intent: map[string]int32{}, wantList: true},
		{name: "When a zero replica pool has no nodes, it should skip the inapplicable probe", intent: map[string]int32{"empty": 0}, wantList: true},
		{name: "When AutoNode has dynamically provisioned a node, it should retain communication coverage", intent: map[string]int32{}, node: true, want: true, wantList: true},
		{name: "When a zero replica fixture has acquired a node, it should retain communication coverage", intent: map[string]int32{"empty": 0}, node: true, want: true, wantList: true},
		{name: "When zero-worker node discovery fails, it should not silently skip", intent: map[string]int32{}, listErr: true, wantList: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			listCalls := 0
			clientset, err := kubernetes.NewForConfig(&rest.Config{Host: "https://konnectivity.invalid", Transport: nodeCommunicationTransport(func(request *http.Request) (*http.Response, error) {
				listCalls++
				if request.URL.Path != "/api/v1/nodes" {
					t.Fatalf("unexpected applicability request %s", request.URL.Path)
				}
				if testCase.listErr {
					return nil, errors.New("node discovery unavailable")
				}
				body := `{"apiVersion":"v1","kind":"NodeList","items":[]}`
				if testCase.node {
					body = `{"apiVersion":"v1","kind":"NodeList","items":[{"metadata":{"name":"dynamic-worker"}}]}`
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			got, err := nodeCommunicationRequired(t.Context(), clientset, testCase.intent)
			if got != testCase.want || (err != nil) != testCase.listErr {
				t.Fatalf("required = %v, error = %v; want required %v, error %v", got, err, testCase.want, testCase.listErr)
			}
			if (listCalls == 1) != testCase.wantList {
				t.Fatalf("node discovery calls = %d, want discovery %v", listCalls, testCase.wantList)
			}
		})
	}
}

type nodeCommunicationTransport func(*http.Request) (*http.Response, error)

func (transport nodeCommunicationTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestNodeCommunicationProbe(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		listError bool
		empty     bool
		logErrors int
		wantError bool
	}{
		{name: "When logs are available, it should pass"},
		{name: "When listing fails, it should return the error", listError: true, wantError: true},
		{name: "When no pods exist, it should reject vacuous communication", empty: true, wantError: true},
		{name: "When all pod logs fail, it should return the error", logErrors: 2, wantError: true},
		{name: "When another pod has available logs, it should preserve the existing fallback", logErrors: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
			defer cancel()
			deadline, _ := ctx.Deadline()
			logCalls := 0
			clientset, err := kubernetes.NewForConfig(&rest.Config{Host: "https://konnectivity.invalid", Transport: nodeCommunicationTransport(func(request *http.Request) (*http.Response, error) {
				actualDeadline, present := request.Context().Deadline()
				if !present || !actualDeadline.Equal(deadline) {
					t.Fatal("every real List/GetLogs request must use the bounded caller context")
				}
				body := `{"apiVersion":"v1","kind":"PodList","items":[{"metadata":{"name":"agent-one"}},{"metadata":{"name":"agent-two"}}]}`
				if request.URL.Path == "/api/v1/namespaces/kube-system/pods" {
					if request.URL.Query().Get("labelSelector") != "app=konnectivity-agent" {
						t.Fatal("pod list must select actual konnectivity-agent pods")
					}
					if testCase.listError {
						return nil, errors.New("list unavailable")
					}
					if testCase.empty {
						body = `{"apiVersion":"v1","kind":"PodList","items":[]}`
					}
				} else {
					logCalls++
					if !strings.HasSuffix(request.URL.Path, "/log") || request.URL.Query().Get("container") != "konnectivity-agent" {
						t.Fatal("log request must target the actual agent container")
					}
					if logCalls <= testCase.logErrors {
						return nil, errors.New("logs unavailable")
					}
					body = "agent connected"
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			if err := nodeCommunicationProbe(ctx, clientset); (err != nil) != testCase.wantError {
				t.Fatalf("probe error = %v, want error %v", err, testCase.wantError)
			}
		})
	}
}

func TestWaitForNodeCommunication(t *testing.T) {
	for _, cancelParent := range []bool{false, true} {
		name := "When communication succeeds, it should enforce the five-minute API deadline"
		if cancelParent {
			name = "When the parent cancels during an API call, it should stop without a retry delay"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			before := time.Now()
			var pollDone <-chan struct{}
			clientset, err := kubernetes.NewForConfig(&rest.Config{Host: "https://konnectivity.invalid", Transport: nodeCommunicationTransport(func(request *http.Request) (*http.Response, error) {
				pollContext := request.Context()
				pollDone = pollContext.Done()
				deadline, present := pollContext.Deadline()
				if !present || deadline.Before(before.Add(5*time.Minute)) || deadline.After(time.Now().Add(5*time.Minute)) {
					t.Fatal("List and GetLogs must share the original bounded five-minute poll context")
				}
				if cancelParent {
					cancel()
					return nil, context.Canceled
				}
				body := `{"apiVersion":"v1","kind":"PodList","items":[{"metadata":{"name":"agent"}}]}`
				if strings.HasSuffix(request.URL.Path, "/log") {
					body = "agent connected"
				}
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			err = waitForNodeCommunication(ctx, clientset)
			if cancelParent && !errors.Is(err, context.Canceled) || !cancelParent && err != nil {
				t.Fatalf("wait error = %v, canceled parent = %v", err, cancelParent)
			}
			select {
			case <-pollDone:
			default:
				t.Fatal("poll context must be released on success and cancellation")
			}
		})
	}
}
