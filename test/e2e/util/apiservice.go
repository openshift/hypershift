package util

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type TimeSegment struct {
	Start time.Time
	End   time.Time
}

func (t TimeSegment) String() string {
	if t.End.IsZero() {
		return fmt.Sprintf("%s - present", t.Start)
	}
	return fmt.Sprintf("%s - %s (%s)", t.Start, t.End, t.End.Sub(t.Start))
}

type APIServerHealthResult struct {
	// Unavailable keeps track of the time segments that APIServices were unavailable during
	// the upgrade.
	Unavailable map[string][]TimeSegment
}

func (r *APIServerHealthResult) isAvailable(serviceName string) bool {
	if r.Unavailable == nil {
		return true
	}
	segments, ok := r.Unavailable[serviceName]
	if !ok {
		return true
	}
	// If the last segment is still open, the service is unavailable
	lastSegment := segments[len(segments)-1]
	return !lastSegment.End.IsZero()
}

func (r *APIServerHealthResult) ReportAvailability(serviceName string, available bool) {
	// If the service is already in the desired state, do nothing
	if available == r.isAvailable(serviceName) {
		return
	}
	if r.Unavailable == nil {
		r.Unavailable = make(map[string][]TimeSegment)
	}
	if available {
		// If the service is available, close the last segment
		if segments, ok := r.Unavailable[serviceName]; ok {
			lastSegment := segments[len(segments)-1]
			lastSegment.End = time.Now()
			segments[len(segments)-1] = lastSegment
		}
	} else {
		// If the service is unavailable, start a new segment
		r.Unavailable[serviceName] = append(r.Unavailable[serviceName], TimeSegment{
			Start: time.Now(),
		})
	}
}

func (r *APIServerHealthResult) WasUnhealthy() bool {
	return r.Unavailable != nil && len(r.Unavailable) > 0
}

func (r *APIServerHealthResult) Report() string {
	if r.Unavailable == nil {
		return ""
	}
	b := &bytes.Buffer{}
	for serviceName, segments := range r.Unavailable {
		for _, segment := range segments {
			fmt.Fprintf(b, "%s was unavailable: %s\n", serviceName, segment.String())
		}
	}
	return b.String()
}

// WatchOpenShiftAPIServiceHealth watches the OpenShift API services to ensure they are available
func WatchOpenShiftAPIServiceHealth(t *testing.T, ctx context.Context, cfg *rest.Config, result *APIServerHealthResult, done <-chan struct{}) {
	t.Helper()
	g := NewGomegaWithT(t)

	// Watch the OpenShift API services to ensure they are available
	t.Logf("Watching health of OpenShift API services")
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
	})
	g.Expect(err).NotTo(HaveOccurred(), "failed to create manager")
	r := func(rCtx context.Context, req reconcile.Request) (ctrl.Result, error) {
		client := mgr.GetClient()
		apiService := &apiregistrationv1.APIService{}
		err := client.Get(rCtx, req.NamespacedName, apiService)
		if err != nil && !errors.IsNotFound(err) {
			t.Logf("failed to get APIService %s: %v", req.NamespacedName, err)
			return ctrl.Result{}, err
		}
		if apiService.Spec.Service != nil && apiService.Spec.Service.Namespace == "default" &&
			(apiService.Spec.Service.Name == "openshift-apiserver" || apiService.Spec.Service.Name == "openshift-oauth-apiserver") {
			for _, cond := range apiService.Status.Conditions {
				if cond.Type == apiregistrationv1.Available {
					t.Logf("Reporting availability of %s: %v, lastTransitionTime: %v, message: %s", apiService.Name, cond.Status == apiregistrationv1.ConditionTrue, cond.LastTransitionTime, cond.Message)
					result.ReportAvailability(apiService.Name, cond.Status == apiregistrationv1.ConditionTrue)
				}
			}
		}
		return ctrl.Result{}, nil
	}
	err = ctrl.NewControllerManagedBy(mgr).For(&apiregistrationv1.APIService{}).Complete(reconcile.Func(r))
	g.Expect(err).NotTo(HaveOccurred(), "failed to create controller")
	mgrCtx, mgrCancel := context.WithCancel(ctx)
	go func() {
		err := mgr.Start(mgrCtx)
		g.Expect(err).NotTo(HaveOccurred(), "failed to start manager")
	}()
	go func() {
		<-done
		mgrCancel()
	}()
}
