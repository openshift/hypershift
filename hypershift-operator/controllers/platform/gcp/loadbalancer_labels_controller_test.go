package gcp

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr/testr"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

type setForwardingRuleLabelsCall struct {
	name   string
	labels *compute.RegionSetLabelsRequest
}

type fakeLoadBalancerLabelsComputeClient struct {
	forwardingRules      []*compute.ForwardingRule
	forwardingRuleFilter string
	listErr              error
	setErr               error
	setCalls             []setForwardingRuleLabelsCall
}

func (f *fakeLoadBalancerLabelsComputeClient) ListForwardingRules(_ context.Context, _, _, filter string) ([]*compute.ForwardingRule, error) {
	f.forwardingRuleFilter = filter
	return f.forwardingRules, f.listErr
}

func (f *fakeLoadBalancerLabelsComputeClient) SetForwardingRuleLabels(_ context.Context, _, _, name string, labels *compute.RegionSetLabelsRequest) (*compute.Operation, error) {
	f.setCalls = append(f.setCalls, setForwardingRuleLabelsCall{name: name, labels: labels})
	return &compute.Operation{Status: "DONE"}, f.setErr
}

func TestGCPLoadBalancerLabelsReconciler(t *testing.T) {
	const (
		namespace          = "clusters-example-example"
		backendServiceName = "k8s2-example-router-abc123"
	)

	tests := []struct {
		name             string
		labels           []hyperv1.GCPResourceLabel
		annotations      map[string]string
		service          *corev1.Service
		forwardingRules  []*compute.ForwardingRule
		wantSetCalls     int
		wantLabels       map[string]string
		wantFilter       string
		checkFilter      bool
		wantRequeueAfter time.Duration
		wantPaused       bool
	}{
		{
			name:       "When HostedControlPlane reconciliation is paused, it should requeue without updating labels",
			wantPaused: true,
		},
		{
			name: "When forwarding rule has unrelated labels, it should preserve them while applying HCP labels",
			labels: []hyperv1.GCPResourceLabel{
				{Key: "goog-partner-solution", Value: ptr.To("isol_psn_0014m00001h31bnqaq_openshift")},
			},
			service: routerService(namespace, backendServiceName),
			forwardingRules: []*compute.ForwardingRule{{
				Name:             "router-forwarding-rule",
				BackendService:   "https://www.googleapis.com/compute/v1/projects/project/regions/us-east1/backendServices/" + backendServiceName,
				LabelFingerprint: "fingerprint",
				Labels:           map[string]string{"existing": "preserved"},
			}},
			wantSetCalls:     1,
			wantLabels:       map[string]string{"existing": "preserved", "goog-partner-solution": "isol_psn_0014m00001h31bnqaq_openshift"},
			wantFilter:       "",
			checkFilter:      true,
			wantRequeueAfter: labelOperationRetry,
		},
		{
			name: "When forwarding rule already has HCP labels, it should not update it",
			labels: []hyperv1.GCPResourceLabel{
				{Key: "goog-partner-solution", Value: ptr.To("isol_psn_0014m00001h31bnqaq_openshift")},
			},
			service: routerService(namespace, backendServiceName),
			forwardingRules: []*compute.ForwardingRule{{
				Name:           "router-forwarding-rule",
				BackendService: "https://www.googleapis.com/compute/v1/projects/project/regions/us-east1/backendServices/" + backendServiceName,
				Labels:         map[string]string{"existing": "preserved", "goog-partner-solution": "isol_psn_0014m00001h31bnqaq_openshift"},
			}},
		},
		{
			name:    "When HCP has no resource labels, it should not update forwarding rules",
			service: routerService(namespace, backendServiceName),
			forwardingRules: []*compute.ForwardingRule{{
				Name:           "router-forwarding-rule",
				BackendService: "https://www.googleapis.com/compute/v1/projects/project/regions/us-east1/backendServices/" + backendServiceName,
			}},
		},
		{
			name:        "When a managed HCP label is removed, it should remove it from the forwarding rule",
			annotations: map[string]string{managedLoadBalancerResourceLabelKeysAnnotation: "managed"},
			service:     routerService(namespace, backendServiceName),
			forwardingRules: []*compute.ForwardingRule{{
				Name:             "router-forwarding-rule",
				BackendService:   "https://www.googleapis.com/compute/v1/projects/project/regions/us-east1/backendServices/" + backendServiceName,
				LabelFingerprint: "fingerprint",
				Labels:           map[string]string{"existing": "preserved", "managed": "old"},
			}},
			wantSetCalls:     1,
			wantLabels:       map[string]string{"existing": "preserved"},
			wantRequeueAfter: labelOperationRetry,
		},
		{
			name: "When router Service has no backend-service annotation, it should requeue",
			labels: []hyperv1.GCPResourceLabel{
				{Key: "goog-partner-solution", Value: ptr.To("isol_psn_0014m00001h31bnqaq_openshift")},
			},
			service:          routerService(namespace, ""),
			wantRequeueAfter: loadBalancerDiscoveryRetry,
		},
		{
			name: "When GCP has not created the forwarding rule, it should requeue",
			labels: []hyperv1.GCPResourceLabel{
				{Key: "goog-partner-solution", Value: ptr.To("isol_psn_0014m00001h31bnqaq_openshift")},
			},
			service:          routerService(namespace, backendServiceName),
			wantRequeueAfter: loadBalancerDiscoveryRetry,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pausedUntil *string
			if tt.wantPaused {
				pausedUntil = ptr.To(time.Now().Add(time.Hour).Format(time.RFC3339))
			}
			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: "example", Annotations: tt.annotations},
				Spec: hyperv1.HostedControlPlaneSpec{Platform: hyperv1.PlatformSpec{
					Type: hyperv1.GCPPlatform,
					GCP:  &hyperv1.GCPPlatformSpec{ResourceLabels: tt.labels},
				}, PausedUntil: pausedUntil},
			}
			objects := []client.Object{hcp}
			if tt.service != nil {
				objects = append(objects, tt.service)
			}
			gcpClient := &fakeLoadBalancerLabelsComputeClient{forwardingRules: tt.forwardingRules}
			reconciler := &GCPLoadBalancerLabelsReconciler{
				Client:    fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(objects...).Build(),
				GcpClient: gcpClient,
				ProjectID: "project",
				Region:    "us-east1",
				Log:       testr.New(t),
			}

			result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hcp)})
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if tt.wantPaused {
				if result.RequeueAfter <= 0 || result.RequeueAfter > time.Hour {
					t.Fatalf("RequeueAfter = %s, want a positive duration no greater than one hour", result.RequeueAfter)
				}
			} else if result.RequeueAfter != tt.wantRequeueAfter {
				t.Fatalf("RequeueAfter = %s, want %s", result.RequeueAfter, tt.wantRequeueAfter)
			}
			if len(gcpClient.setCalls) != tt.wantSetCalls {
				t.Fatalf("SetForwardingRuleLabels calls = %d, want %d", len(gcpClient.setCalls), tt.wantSetCalls)
			}
			if tt.wantSetCalls != 0 && !maps.Equal(gcpClient.setCalls[0].labels.Labels, tt.wantLabels) {
				t.Fatalf("labels = %#v, want %#v", gcpClient.setCalls[0].labels.Labels, tt.wantLabels)
			}
			if tt.wantSetCalls != 0 && gcpClient.setCalls[0].labels.LabelFingerprint != "fingerprint" {
				t.Fatalf("label fingerprint = %q, want fingerprint", gcpClient.setCalls[0].labels.LabelFingerprint)
			}
			if tt.checkFilter && gcpClient.forwardingRuleFilter != tt.wantFilter {
				t.Fatalf("forwarding rule filter = %q, want %q", gcpClient.forwardingRuleFilter, tt.wantFilter)
			}
		})
	}
}

func TestLoadBalancerLabelsComputeServiceAdapterListForwardingRules(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		switch requestCount {
		case 1:
			if r.URL.Query().Get("pageToken") != "" {
				http.Error(w, "unexpected page token", http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprint(w, `{"items":[{"name":"first"}],"nextPageToken":"second-page"}`)
		case 2:
			if r.URL.Query().Get("pageToken") != "second-page" {
				http.Error(w, "missing page token", http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprint(w, `{"items":[{"name":"second"}]}`)
		default:
			http.Error(w, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	service, err := compute.NewService(context.Background(), option.WithHTTPClient(server.Client()), option.WithEndpoint(server.URL+"/"))
	if err != nil {
		t.Fatalf("create compute service: %v", err)
	}
	adapter := &loadBalancerLabelsComputeServiceAdapter{svc: service}

	forwardingRules, err := adapter.ListForwardingRules(context.Background(), "project", "us-east1", "")
	if err != nil {
		t.Fatalf("list forwarding rules: %v", err)
	}
	if len(forwardingRules) != 2 || forwardingRules[0].Name != "first" || forwardingRules[1].Name != "second" {
		t.Fatalf("forwarding rules = %#v, want both pages", forwardingRules)
	}
}

func routerService(namespace, backendService string) *corev1.Service {
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: routerServiceName}}
	if backendService != "" {
		service.Annotations = map[string]string{backendServiceAnnotation: backendService}
	}
	return service
}
